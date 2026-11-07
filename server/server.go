package server

import (
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/herosizy/healthsync-server/internal/telemetry"
	"github.com/herosizy/healthsync-server/model"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	protocolVersion = 2 // what /health reports; the app checks it
	maxIngestBody   = 50 << 20
	maxReportBody   = 100 << 20
)

var version = "dev" // set with -ldflags "-X main.version=..."

//go:embed web
var webFS embed.FS

type Server struct {
	cfg     *Config
	db      *DB
	token   string
	pair    *Pairing
	csv     *CSVOutput
	hook    *Hook
	mcp     *MCP
	started time.Time
	mux     *http.ServeMux
}

func NewServer(cfg *Config, db *DB, token string) (*Server, error) {
	s := &Server{cfg: cfg, db: db, token: token, pair: NewPairing(cfg.DataDir), started: time.Now()}
	if cfg.CSVDir != "" {
		c, err := NewCSVOutput(cfg.CSVDir)
		if err != nil {
			return nil, err
		}
		s.csv = c
	}
	if cfg.Hook != "" {
		h, err := NewHook(cfg.Hook, cfg.DataDir, db.path)
		if err != nil {
			return nil, err
		}
		h.onResult = func(kind string, ok bool, out string, d time.Duration) {
			db.Log(LogEntry{Kind: "hook", Device: kind, OK: ok, Message: out, DurationMS: int(d.Milliseconds())})
		}
		s.hook = h
	}
	m, err := OpenMCP(db.path)
	if err != nil {
		return nil, err
	}
	s.mcp = m
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				telemetry.Errorf(r.Method+" "+r.Pattern, fmt.Sprintf("panic: %v", p))
			}
			panic(p) // net/http logs it and closes the connection
		}
	}()
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := http.NewServeMux()
	// API used by the iPhone app
	m.HandleFunc("GET /health", s.handleHealth)
	m.HandleFunc("GET /stats", s.handleStats)
	m.HandleFunc("POST /ingest", s.handleIngest)
	m.HandleFunc("POST /report", s.handleReport)
	m.HandleFunc("POST /pair", s.handlePair)
	m.HandleFunc("POST /mcp", s.handleMCP)
	m.HandleFunc("GET /mcp", s.handleMCP)
	m.HandleFunc("DELETE /mcp", s.handleMCP)
	m.HandleFunc("POST /mcp/tokens", s.handleMCPToken)
	m.HandleFunc("GET /skill.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="SKILL.md"`)
		_, _ = io.WriteString(w, skillMD)
	})
	m.HandleFunc("POST /api/mcp", s.admin(s.apiSetMCP))

	// dashboard
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	m.Handle("GET /", s.admin(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	m.HandleFunc("GET /api/overview", s.admin(s.apiOverview))
	m.HandleFunc("GET /api/logs", s.admin(s.apiLogs))
	m.HandleFunc("GET /api/devices", s.admin(s.apiDevices))
	m.HandleFunc("POST /api/devices", s.admin(s.apiNewDevice))
	m.HandleFunc("POST /api/devices/{id}/revoke", s.admin(s.apiRevoke))
	m.HandleFunc("DELETE /api/devices/{id}", s.admin(s.apiDeleteDevice))
	m.HandleFunc("PATCH /api/devices/{id}", s.admin(s.apiRenameDevice))
	m.HandleFunc("GET /api/pair", s.admin(s.apiPairGet))
	m.HandleFunc("POST /api/pair", s.admin(s.apiPairNew))
	m.HandleFunc("DELETE /api/pair", s.admin(s.apiPairCancel))
	m.HandleFunc("POST /api/pair/pending/{id}", s.admin(s.apiPairAnswer))
	m.HandleFunc("GET /api/reports", s.admin(s.apiReports))
	m.HandleFunc("GET /api/daily", s.admin(s.apiDaily))
	m.HandleFunc("GET /api/token", s.admin(s.apiToken))
	s.mux = m
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) dialogModes() []string {
	if s.cfg.NoDialog {
		return []string{"code"}
	}
	// The app shows "click Allow on the computer" for dialog mode. On macOS a native
	// dialog pops up; on other systems approve it in the dashboard.
	if osTag() == "mac" || envBool("HEALTHSYNC_DASHBOARD_APPROVAL") {
		return []string{"dialog", "code"}
	}
	return []string{"code"}
}

// authDevice checks the bearer token. It returns the device name to show in the log.
func (s *Server) authDevice(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1 {
		return "", true
	}
	if d, ok := s.db.DeviceForToken(tok); ok {
		s.db.TouchDevice(d.ID, clientIP(r))
		return d.Name, true
	}
	return "", false
}

// admin guards the dashboard: LAN only by default, plus an optional password.
// State-changing calls must carry X-HealthSync: 1, which a cross-site form can't send.
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.DashboardLAN && !isLAN(net.ParseIP(clientIP(r))) {
			http.Error(w, "The dashboard is only available on the local network.", http.StatusForbidden)
			return
		}
		if s.cfg.AdminPassword != "" {
			_, pw, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(s.cfg.AdminPassword)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="HealthSync"`)
				http.Error(w, "password required", http.StatusUnauthorized)
				return
			}
		}
		if r.Method != http.MethodGet && r.Header.Get("X-HealthSync") != "1" {
			errJSON(w, http.StatusForbidden, "missing X-HealthSync header")
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next(w, r)
	}
}

// ---------------------------------------------------------------- app API

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	stores := []string{"sqlite"}
	if s.csv != nil {
		stores = append(stores, "csv")
	}
	if s.hook != nil {
		stores = append(stores, "hook")
	}
	out := map[string]any{
		"ok": true, "name": s.cfg.Name, "version": protocolVersion, "os": osTag(),
		"pair_modes": s.dialogModes(), "stores": stores, "server": "healthsync-server/" + version,
		"records": recordsVersion, "urls": s.urls(r), "mcp_enabled": s.db.MCPEnabled(),
	}
	if s.db.MCPEnabled() {
		out["mcp"] = "/mcp"
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authDevice(r); !ok {
		errJSON(w, 401, "bad token")
		return
	}
	st, err := s.db.Stats()
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ip := clientIP(r)
	device, ok := s.authDevice(r)
	if !ok {
		logf("rejected: bad token (from %s)", ip)
		s.db.Log(LogEntry{Kind: "auth", IP: ip, OK: false, Message: "bad token on /ingest"})
		errJSON(w, 401, "bad token")
		return
	}
	if device == "" {
		device = "shared token"
	}
	if r.ContentLength > maxIngestBody {
		errJSON(w, 400, "bad length")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIngestBody))
	if err != nil || len(body) == 0 {
		errJSON(w, 400, "bad length")
		return
	}
	entry := LogEntry{Kind: "ingest", Device: device, IP: ip, Bytes: len(body)}
	fail := func(code int, msg string) {
		entry.Message, entry.DurationMS = msg, int(time.Since(start).Milliseconds())
		s.db.Log(entry)
		logf("ingest from %s failed: %s", device, msg)
		if code >= 500 {
			telemetry.Errorf("ingest", msg)
		}
		errJSON(w, code, msg)
	}
	rows, err := ParseBody(body)
	if err != nil {
		fail(400, "could not parse body: "+err.Error())
		return
	}
	entry.Received = len(rows)
	entry.Types = distinctTypes(rows, 30)
	added := map[string]int{}
	n, deleted, err := s.db.InsertRecords(rows)
	if err != nil {
		fail(500, "sqlite: "+err.Error())
		return
	}
	added["sqlite"] = n
	entry.Added = n
	if deleted > 0 {
		entry.Message = fmt.Sprintf("%d deleted", deleted)
	}
	// CSV files and the hook inbox keep the six-column format and get no deletions.
	rows = samples(rows)
	if s.csv != nil {
		n, err := s.csv.Write(rows)
		if err != nil {
			fail(500, "csv: "+err.Error())
			return
		}
		added["csv"] = n
	}
	if s.hook != nil {
		if err := s.hook.Write(rows); err != nil {
			fail(500, "hook inbox: "+err.Error())
			return
		}
		added["hook"] = len(rows)
	}
	entry.OK = true
	entry.DurationMS = int(time.Since(start).Milliseconds())
	s.db.Log(entry)
	logf("received %d records from %s (%s), %d new, %d deleted", entry.Received, device, ip, added["sqlite"], deleted)
	writeJSON(w, 200, map[string]any{"ok": true, "received": entry.Received, "added": added, "deleted": deleted})
}

func distinctTypes(rows []Record, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.Type != "" && !seen[r.Type] {
			seen[r.Type] = true
			out = append(out, r.Type)
		}
	}
	sort.Strings(out)
	if len(out) > max {
		out = append(out[:max], "…")
	}
	return out
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(s, ". ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	device, ok := s.authDevice(r)
	if !ok {
		s.db.Log(LogEntry{Kind: "auth", IP: clientIP(r), Message: "bad token on /report"})
		errJSON(w, 401, "bad token")
		return
	}
	if device == "" {
		device = "shared token"
	}
	var req struct {
		Report *Report `json:"report"`
		Folder string  `json:"folder"`
		Files  []struct {
			Name string `json:"name"`
			Data string `json:"data"`
		} `json:"files"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBody)).Decode(&req); err != nil || req.Report == nil {
		errJSON(w, 400, "could not parse report")
		return
	}
	name := safeName(firstNonEmpty(req.Folder, req.Report.ID))
	if name == "" {
		name = "report-" + time.Now().Format("20060102-150405")
	}
	folder := filepath.Join(s.cfg.DataDir, "reports", name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	for _, f := range req.Files {
		fn := filepath.Base(f.Name)
		if fn == "." || fn == "/" || fn == "" || fn == "report.json" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			errJSON(w, 400, "bad base64 in "+fn)
			return
		}
		if err := os.WriteFile(filepath.Join(folder, fn), data, 0o600); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
	}
	pretty, _ := json.MarshalIndent(req.Report, "", "  ")
	if err := os.WriteFile(filepath.Join(folder, "report.json"), pretty, 0o600); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	n, err := s.db.InsertReport(*req.Report, name, folder)
	stores := map[string]any{"sqlite": n}
	if err != nil {
		stores["sqlite"] = "error: " + err.Error()
	}
	if s.hook != nil {
		s.hook.Report(folder)
		stores["hook"] = "queued"
	}
	s.db.Log(LogEntry{Kind: "report", Device: device, IP: clientIP(r), Received: len(req.Report.Items), Added: n,
		OK: err == nil, Message: name, DurationMS: int(time.Since(start).Milliseconds())})
	logf("received checkup report %s (%d items) from %s", name, len(req.Report.Items), device)
	writeJSON(w, 200, map[string]any{"ok": true, "folder": folder, "stores": stores})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !isLAN(net.ParseIP(ip)) {
		errJSON(w, 403, "pairing is only allowed from the local network")
		return
	}
	var req struct {
		Device string `json:"device"`
		Code   string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	device := strings.TrimSpace(req.Device)
	if device == "" {
		device = "iPhone"
	}
	if r := []rune(device); len(r) > 60 {
		device = string(r[:60])
	}
	issue := func(how string) {
		tok := newToken()
		if err := s.db.AddDevice(device, tok, ip); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		s.db.Log(LogEntry{Kind: "pair", Device: device, IP: ip, OK: true, Message: how})
		logf("paired %s (%s) by %s", device, ip, how)
		writeJSON(w, 200, map[string]any{"token": tok, "name": s.cfg.Name, "version": protocolVersion, "urls": s.urls(r)})
	}
	if strings.TrimSpace(req.Code) != "" {
		if s.pair.Check(req.Code) {
			issue("code")
			return
		}
		s.db.Log(LogEntry{Kind: "pair", Device: device, IP: ip, Message: "wrong or expired code"})
		errJSON(w, 403, "配对码不对或已过期 / wrong or expired code")
		return
	}
	modes := s.dialogModes()
	if modes[0] != "dialog" {
		writeJSON(w, 428, map[string]any{"error": "需要配对码 / pairing code required", "needs_code": true})
		return
	}
	logf("pair request from %s (%s), waiting for approval", device, ip)
	if s.pair.Approve(r.Context(), device, ip, !s.cfg.NoDialog) {
		issue("approved")
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: device, IP: ip, Message: "denied or timed out"})
	errJSON(w, 403, "电脑上没有点「允许」 / not approved")
}

// ---------------------------------------------------------------- dashboard API

func (s *Server) apiOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.overview(s.baseURL(r)))
}

func (s *Server) overview(base string) model.Overview {
	st, _ := s.db.Stats()
	act, _ := s.db.Activity(30)
	devs, _ := s.db.Devices()
	reps, _ := s.db.Reports()
	last, _ := s.db.Logs(1, 0, "ingest")
	active := 0
	for _, d := range devs {
		if !d.Revoked {
			active++
		}
	}
	outputs := []model.Output{{Kind: "sqlite", Where: s.db.path}}
	if s.csv != nil {
		outputs = append(outputs, model.Output{Kind: "csv", Where: s.csv.dir})
	}
	if s.hook != nil {
		outputs = append(outputs, model.Output{Kind: "hook", Where: s.hook.cmd})
	}
	o := model.Overview{
		Name: s.cfg.Name, Version: version, Protocol: protocolVersion, OS: osTag(),
		Started: s.started.Format(time.RFC3339), BaseURL: base, TailscaleURL: TailscaleURL(s.cfg.Port), MCPURL: base + "/mcp", DataDir: s.cfg.DataDir,
		Outputs: outputs, Stats: st, Activity: act, Devices: active, Reports: len(reps),
		PairModes: s.dialogModes(), Password: s.cfg.AdminPassword != "", MCPEnabled: s.db.MCPEnabled(),
	}
	if len(last) > 0 {
		o.LastSync = &last[0]
	}
	return o
}

// urls are the addresses the app can reach this server at: "lan" at home, "tailscale"
// through the Tailscale app anywhere. The app tries them in that order.
func (s *Server) urls(r *http.Request) map[string]string {
	out := map[string]string{"lan": s.baseURL(r)}
	if ts := TailscaleURL(s.cfg.Port); ts != "" {
		out["tailscale"] = ts
		if ip, name := TailscaleAddr(); name != "" && ip != "" {
			out["tailscale_name"] = strings.Replace(ts, ip, name, 1)
		}
	}
	return out
}

// baseURL guesses the LAN address a phone should use. If you opened the dashboard through
// a LAN address, that's the best guess; otherwise the configured / detected one.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	host := r.Host
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if ip := net.ParseIP(h); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !isTailscale(ip) {
		return "http://" + host
	}
	if strings.HasSuffix(h, ".local") {
		return "http://" + host
	}
	return s.cfg.BaseURL()
}

func (s *Server) apiLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	logs, err := s.db.Logs(limit, before, r.URL.Query().Get("kind"))
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, logs)
}

func (s *Server) apiDevices(w http.ResponseWriter, r *http.Request) {
	d, err := s.db.Devices()
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, d)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, errors.New("bad id")
	}
	return id, nil
}

// apiNewDevice issues a token without pairing, e.g. for an AI assistant using /mcp.
func (s *Server) apiNewDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "AI assistant"
	}
	tok := newToken()
	if err := s.db.AddDevice(name, tok, clientIP(r)); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: name, IP: clientIP(r), OK: true, Message: "token created in dashboard"})
	writeJSON(w, 200, mcpConnect(s.cfg.Name, s.baseURL(r)+"/mcp", tok))
}

func (s *Server) apiRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.db.RevokeDevice(id)
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// apiSetMCP turns AI assistant access on or off: {"enabled": true|false}.
func (s *Server) apiSetMCP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil || req.Enabled == nil {
		errJSON(w, 400, `send {"enabled": true} or {"enabled": false}`)
		return
	}
	if err := s.setMCP(*req.Enabled, "dashboard"); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "enabled": *req.Enabled})
}

func (s *Server) setMCP(on bool, where string) error {
	if err := s.db.SetMCPEnabled(on); err != nil {
		return err
	}
	msg := "AI assistant access turned off in the " + where
	if on {
		msg = "AI assistant access turned on in the " + where
	}
	s.db.Log(LogEntry{Kind: "pair", OK: true, Message: msg})
	return nil
}

// apiRenameDevice takes {"name":"…"}.
func (s *Server) apiRenameDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	id, err := pathID(r)
	if err == nil {
		err = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	}
	if err == nil {
		err = s.db.RenameDevice(id, req.Name)
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = s.db.DeleteDevice(id)
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) pairPayload(r *http.Request, c PairCode) map[string]any {
	alt := TailscaleURL(s.cfg.Port)
	link := PairLink(s.baseURL(r), alt, c.Code, s.cfg.Name)
	return map[string]any{"code": c.Code, "expires": int64(c.Expires), "link": link, "qr": QRSVG(link), "url": s.baseURL(r), "alt_url": alt}
}

func (s *Server) apiPairGet(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"pending": s.pair.Pending()}
	if c, ok := s.pair.Current(); ok {
		out["active"] = s.pairPayload(r, c)
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiPairNew(w http.ResponseWriter, r *http.Request) {
	c, err := s.pair.New()
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"active": s.pairPayload(r, c), "pending": s.pair.Pending()})
}

func (s *Server) apiPairCancel(w http.ResponseWriter, r *http.Request) {
	s.pair.Cancel()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiPairAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Allow bool `json:"allow"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	if !s.pair.Answer(r.PathValue("id"), req.Allow) {
		errJSON(w, 404, "request no longer pending")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiReports(w http.ResponseWriter, r *http.Request) {
	reps, err := s.db.Reports()
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, reps)
}

func (s *Server) apiDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 3650 {
		days = 30
	}
	pts, err := s.db.Daily(r.URL.Query().Get("type"), days)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, pts)
}

func (s *Server) apiToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"token": s.token})
}
