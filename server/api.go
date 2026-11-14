package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/herosizy/healthsync-server/model"
)

func logf(format string, a ...any) { log.Printf(format, a...) }

// Version is the build's version, set with -ldflags "-X github.com/herosizy/healthsync-server/server.version=…".
func Version() string { return version }

// Instance is a running server: the HTTP listener, the database and Bonjour.
// The desktop app drives it through these methods; they are safe from any goroutine.
type Instance struct {
	Cfg   *Config
	srv   *Server
	db    *DB
	hs    *http.Server
	stop  func()
	done  chan error
	once  sync.Once
	Token string
}

// Start opens the database and starts listening. onEvent, if not nil, is called (from a
// background goroutine) after anything that changes what the dashboard shows.
func Start(cfg *Config, onEvent func(model.LogEntry)) (*Instance, error) {
	if err := cfg.Prepare(); err != nil {
		return nil, err
	}
	token, err := LoadToken(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	db, err := OpenDB(filepath.Join(cfg.DataDir, "health.db"))
	if err != nil {
		return nil, err
	}
	if cfg.DataDir == expandHome(defaultDataDir()) {
		tightenPermissions(cfg.DataDir)
	}
	db.onLog = onEvent
	srv, err := NewServer(cfg, db, token)
	if err != nil {
		db.Close()
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		srv.mcp.db.Close()
		db.Close()
		return nil, err
	}
	in := &Instance{Cfg: cfg, srv: srv, db: db, Token: token, stop: func() {}, done: make(chan error, 1),
		hs: &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}}
	logf("healthsync-server %s listening on %s, data in %s", version, addr, cfg.DataDir)
	go TailscaleAddr() // look it up now rather than in the first request
	if !cfg.NoBonjour {
		in.stop = Advertise(cfg.Name, cfg.Port)
	}
	go func() {
		err := in.hs.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		in.done <- err
	}()
	return in, nil
}

// Done reports the listener's end (nil after Stop).
func (in *Instance) Done() <-chan error { return in.done }

// Stop shuts the server down and closes the database.
func (in *Instance) Stop() {
	in.once.Do(func() {
		in.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = in.hs.Shutdown(ctx)
		// the read-only connection of MCP too: the desktop app starts a new server each
		// time the settings are saved, and each one left its connection open
		in.srv.mcp.db.Close()
		in.db.Close()
	})
}

func (in *Instance) Overview() model.Overview { return in.srv.overview(in.Cfg.BaseURL()) }

func (in *Instance) Logs(limit int, before int64, kind string) ([]model.LogEntry, error) {
	return in.db.Logs(limit, before, kind)
}
func (in *Instance) Devices() ([]model.Device, error) { return in.db.Devices() }
func (in *Instance) RevokeDevice(id int64) error      { return in.db.RevokeDevice(id) }
func (in *Instance) DeleteDevice(id int64) error      { return in.db.DeleteDevice(id) }
func (in *Instance) SetMCPEnabled(on bool) error      { return in.srv.setMCP(on, "app") }
func (in *Instance) SkillMD() string                  { return skillMD }
func (in *Instance) RenameDevice(id int64, name string) error {
	return in.db.RenameDevice(id, name)
}
func (in *Instance) Reports() ([]model.ReportRow, error) { return in.db.Reports() }

// ReportDetail is a report's items and the originals kept with it.
func (in *Instance) ReportDetail(id string) (model.ReportDetail, error) { return in.srv.reportDetail(id) }

// ReportPage is page n of an original as an image (PDF pages drawn), at most width wide.
func (in *Instance) ReportPage(id, file string, n, width int) (model.PageImage, error) {
	return in.srv.reportPage(id, file, n, width)
}
func (in *Instance) Daily(typ string, days int) ([]model.DailyPoint, error) {
	return in.db.Daily(typ, days)
}

func (in *Instance) pairInfo(c PairCode) model.PairInfo {
	base := in.Cfg.BaseURL()
	alt := TailscaleURL(in.Cfg.Port)
	return model.PairInfo{Code: c.Code, Expires: int64(c.Expires), Link: PairLink(base, alt, c.Code, in.Cfg.Name), URL: base, AltURL: alt}
}

// NewPairCode makes a new one-time code, replacing any earlier one.
func (in *Instance) NewPairCode() (model.PairInfo, error) {
	c, err := in.srv.pair.New()
	return in.pairInfo(c), err
}

// CurrentPair is the active code, if any.
func (in *Instance) CurrentPair() (model.PairInfo, bool) {
	c, ok := in.srv.pair.Current()
	return in.pairInfo(c), ok
}

func (in *Instance) CancelPair() { in.srv.pair.Cancel() }

func (in *Instance) Pending() []model.PendingPair { return in.srv.pair.PendingInfo() }

func (in *Instance) AnswerPending(id string, allow bool) bool { return in.srv.pair.Answer(id, allow) }

// NewMCPToken issues a revocable token for an AI agent, with copyable configs.
func (in *Instance) NewMCPToken(name string) (model.MCPConnect, error) {
	if name == "" {
		name = "AI assistant"
	}
	tok := newToken()
	if _, _, err := in.db.AddDevice(name, tok, "127.0.0.1", "", scopeMCP); err != nil {
		return model.MCPConnect{}, err
	}
	in.db.Log(LogEntry{Kind: "pair", Device: name, OK: true, Message: "MCP token created in the app"})
	return mcpConnect(in.Cfg.Name, in.Cfg.BaseURL()+"/mcp", tok), nil
}

// DashboardURL is the web dashboard's address on this machine.
func (in *Instance) DashboardURL() string { return fmt.Sprintf("http://127.0.0.1:%d/", in.Cfg.Port) }

// ConfigFromSettings makes a server configuration from the desktop app's settings.
func ConfigFromSettings(s model.Settings) *Config {
	c := &Config{Port: 8787, Bind: "0.0.0.0", DataDir: defaultDataDir(), DashboardLAN: true}
	if s.Port > 0 {
		c.Port = s.Port
	}
	if s.DataDir != "" {
		c.DataDir = s.DataDir
	}
	c.Name, c.PublicURL, c.AdminPassword, c.NoBonjour = s.Name, s.PublicURL, s.AdminPassword, s.NoBonjour
	if s.CSVEnabled {
		c.CSVDir = s.CSVDir
		if c.CSVDir == "" {
			c.CSVDir = filepath.Join(c.DataDir, "csv")
		}
	}
	if s.HookEnabled {
		c.Hook = s.Hook
	}
	return c
}

// DefaultDataDir is where data goes when the settings don't say.
func DefaultDataDir() string { return defaultDataDir() }
