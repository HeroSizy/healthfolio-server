package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/herosizy/healthsync-server/internal/telemetry"
	"github.com/herosizy/healthsync-server/model"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
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
	stor    storageCache
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
	m.HandleFunc("POST /pair/requests", s.handlePairRequest)
	m.HandleFunc("GET /pair/requests/{id}", s.handlePairPoll)
	m.HandleFunc("DELETE /pair/requests/{id}", s.handlePairWithdraw)
	m.HandleFunc("GET /auth", s.handleAuth)
	m.HandleFunc("POST /backup/chats", s.handleBackupChats)
	m.HandleFunc("GET /backup/chats", s.handleListChats)
	m.HandleFunc("GET /backup/chats/{id}", s.handleGetChat)
	m.HandleFunc("DELETE /backup/chats/{id}", s.handleDeleteChat)
	m.HandleFunc("POST /backup/chats/delete", s.handleDeleteChats)
	m.HandleFunc("POST /backup/settings", s.handleBackupSettings)
	m.HandleFunc("GET /backup/settings", s.handleGetSettings)
	m.HandleFunc("GET /backup/reports", s.handleListReports)
	m.HandleFunc("GET /backup/reports/{id}", s.handleGetReport)
	m.HandleFunc("GET /backup/reports/{id}/files/{name}", s.handleGetReportFile)
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
	m.HandleFunc("GET /api/reports/{id}", s.admin(s.apiReport))
	m.HandleFunc("GET /api/reports/{id}/files/{name}", s.admin(s.apiReportFile))
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

// pairErr answers a failed /pair with a stable reason code. The app shows its own,
// translated text for the code; "error" stays for older apps and for people using curl.
func pairErr(w http.ResponseWriter, status int, reason, msg string, extra map[string]any) {
	body := map[string]any{"error": msg, "reason": reason}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) dialogModes() []string {
	// The app shows "click Allow on the computer" for dialog mode. On macOS a native
	// dialog pops up (unless -no-dialog); anywhere, HEALTHSYNC_DASHBOARD_APPROVAL lets the
	// desktop app or the dashboard approve it.
	if (osTag() == "mac" && !s.cfg.NoDialog) || envBool("HEALTHSYNC_DASHBOARD_APPROVAL") {
		return []string{"dialog", "code"}
	}
	return []string{"code"}
}

func bearer(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(tok)
}

// authDevice checks the bearer token and returns its owner. The shared token counts as a
// device named "shared token".
func (s *Server) authDevice(r *http.Request) (Device, bool) {
	tok := bearer(r)
	if tok == "" {
		return Device{}, false
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1 {
		return Device{Name: "shared token", Scope: scopeDevice}, true
	}
	if d, ok := s.db.DeviceForToken(tok); ok {
		s.db.TouchDevice(d.ID, clientIP(r))
		return d, true
	}
	return Device{}, false
}

// authPhone is authDevice for calls only a phone may make (sending data, making AI
// tokens): an AI assistant's read-only token is refused with 403.
func (s *Server) authPhone(w http.ResponseWriter, r *http.Request, what string) (Device, bool) {
	d, ok := s.authDevice(r)
	if !ok {
		s.db.Log(LogEntry{Kind: "auth", IP: clientIP(r), Message: "bad token on " + what})
		logf("rejected: bad token on %s (from %s)", what, clientIP(r))
		writeJSON(w, 401, map[string]any{"error": "bad token", "reason": "bad_token"})
		return d, false
	}
	if d.Scope != scopeDevice {
		s.db.Log(LogEntry{Kind: "auth", Device: d.Name, IP: clientIP(r), Message: "read-only AI token used on " + what})
		writeJSON(w, 403, map[string]any{"error": "this token is read-only (AI assistant)", "reason": "read_only"})
		return d, false
	}
	return d, true
}

// handleAuth tells the app whether its token still works: GET /auth.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	if tok == "" {
		writeJSON(w, 401, map[string]any{"ok": false, "reason": "missing"})
		return
	}
	server := map[string]any{"name": s.cfg.Name, "version": version, "protocol": protocolVersion}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1 {
		writeJSON(w, 200, map[string]any{"ok": true, "scope": scopeDevice, "device": map[string]any{"name": "shared token"}, "server": server})
		return
	}
	d, found := s.db.LookupToken(tok)
	switch {
	case !found:
		writeJSON(w, 401, map[string]any{"ok": false, "reason": "unknown", "server": server})
	case d.Revoked:
		writeJSON(w, 401, map[string]any{"ok": false, "reason": "revoked", "server": server})
	default:
		s.db.TouchDevice(d.ID, clientIP(r))
		writeJSON(w, 200, map[string]any{"ok": true, "scope": d.Scope, "device": map[string]any{"id": d.ID, "name": d.Name}, "server": server})
	}
}

// admin guards the dashboard: LAN only by default, plus an optional password.
// State-changing calls must carry X-HealthSync: 1, which a cross-site form can't send.
func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if s.cfg.DashboardLAN && !isLANAddr(ip) {
			http.Error(w, "The dashboard is only available on the local network.", http.StatusForbidden)
			return
		}
		local := thisComputer(r)
		if !local && s.cfg.AdminPassword == "" && !s.cfg.DashboardNoPassword {
			// Everyone on the Wi-Fi (guests too) or the tailnet could otherwise read all
			// the health data and the shared token. This computer itself needs no password.
			s.needPassword(w, r)
			return
		}
		if !local && s.cfg.AdminPassword != "" {
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

func isLoopback(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && a.WithZone("").Unmap().IsLoopback()
}

// proxyHeaders are what reverse proxies and tunnels (nginx, Caddy, Tailscale Serve,
// cloudflared, ngrok…) add to the requests they pass on.
var proxyHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Forwarded", "Via"}

// thisComputer reports whether the request comes from a browser on this computer, straight
// to the server: only that may skip the dashboard password and see the shared token.
// A connection from 127.0.0.1 alone does not say so:
//   - a reverse proxy on this computer connects from 127.0.0.1 for everyone it serves;
//   - a web page elsewhere can point its own domain name at 127.0.0.1 (DNS rebinding) and
//     then read the dashboard from the browser on this computer. Its requests carry that
//     name in Host, so the address typed must be an IP address or localhost.
func thisComputer(r *http.Request) bool {
	if !isLoopback(clientIP(r)) {
		return false
	}
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

// needPassword explains that the dashboard needs a password before other computers may
// open it.
func (s *Server) needPassword(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		writeJSON(w, 403, map[string]any{"error": "set a dashboard password to open the dashboard from another computer", "reason": "password_not_set"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, needPasswordPage)
}

const needPasswordPage = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Healthfolio</title><style>
:root{color-scheme:light dark}body{font:15px/1.6 -apple-system,system-ui,sans-serif;max-width:560px;margin:12vh auto;padding:0 20px}
h1{font-size:20px}code{background:rgba(127,127,127,.15);padding:1px 5px;border-radius:4px}p{opacity:.85}</style></head><body>
<h1>先给面板设个密码 · Set a dashboard password first</h1>
<p>面板里有全部健康数据。为了不让同一 Wi‑Fi 或 Tailscale 里的其他人看到，从别的电脑打开面板前需要先设密码：</p>
<ul><li>桌面版 Healthfolio：在运行它的电脑上打开「设置 › 网页面板密码」。</li>
<li>服务器版：设置环境变量 <code>HEALTHSYNC_ADMIN_PASSWORD</code>（或 <code>-admin-password</code>）后重启。</li></ul>
<p>在运行 Healthfolio 的这台电脑上用 <code>http://127.0.0.1</code> 打开不需要密码。</p>
<hr><p>The dashboard shows all of your health data, so opening it from another computer needs a password.
Desktop app: Settings › Dashboard password, on the computer running it. Server: set <code>HEALTHSYNC_ADMIN_PASSWORD</code> and restart.
On the computer running Healthfolio, <code>http://127.0.0.1</code> works without one.</p></body></html>`

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
		// what this server can do beyond protocol 2: two-step pairing with a confirmation
		// code, GET /auth, pairing again by device_id, backups of AI chats and settings, and reading checkup reports back
		"features": []string{"pair_requests", "auth", "device_id", "chats", "chats_delete", "settings", "reports_read"},
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
	dev, ok := s.authPhone(w, r, "/ingest")
	if !ok {
		return
	}
	device := dev.Name
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

// folderReportID is the id in folder/report.json, "" when there is none.
func folderReportID(folder string) string {
	b, err := os.ReadFile(filepath.Join(folder, "report.json"))
	if err != nil {
		return ""
	}
	var rep struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &rep)
	return rep.ID
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	dev, ok := s.authPhone(w, r, "/report")
	if !ok {
		return
	}
	device := dev.Name
	var req struct {
		// kept as sent, so report.json has every field the app knows (locations of the
		// items, the list of originals, …), not just those this server reads
		Raw    json.RawMessage `json:"report"`
		Report *Report         `json:"-"`
		Folder string          `json:"folder"`
		Files  []struct {
			Name string `json:"name"`
			Data string `json:"data"`
		} `json:"files"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportBody)).Decode(&req); err != nil || len(req.Raw) == 0 {
		errJSON(w, 400, "could not parse report")
		return
	}
	if err := json.Unmarshal(req.Raw, &req.Report); err != nil || req.Report == nil {
		errJSON(w, 400, "could not parse report: "+fmt.Sprint(err))
		return
	}
	name := safeName(firstNonEmpty(req.Folder, req.Report.ID))
	if name == "" {
		name = "report-" + time.Now().Format("20060102-150405")
	}
	folder := filepath.Join(s.cfg.DataDir, "reports", name)
	if other := folderReportID(folder); other != "" && req.Report.ID != "" && other != req.Report.ID {
		// another report already has this folder name: don't overwrite its files
		suffix := safeName(req.Report.ID)
		if r := []rune(suffix); len(r) > 8 {
			suffix = string(r[:8])
		}
		name += "-" + suffix
		folder = filepath.Join(s.cfg.DataDir, "reports", name)
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	// Every original file is kept under its own name; each is checked against the hash in
	// the report's list of originals, when there is one.
	want := map[string]string{}
	for _, o := range req.Report.Originals {
		want[o.Name] = strings.ToLower(o.SHA256)
	}
	saved := []map[string]any{}
	for _, f := range req.Files {
		// The name alone, whichever slash the sender used. Names starting with a dot are
		// not kept: "..", the temporary files of writeFileAtomic, and hidden files, which
		// nothing here lists or serves.
		fn := f.Name[strings.LastIndexAny(f.Name, `/\`)+1:]
		if fn == "" || strings.HasPrefix(fn, ".") || fn == "report.json" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			errJSON(w, 400, "bad base64 in "+fn)
			return
		}
		sum := sha256.Sum256(data)
		hexSum := hex.EncodeToString(sum[:])
		if h, ok := want[fn]; ok && h != "" && h != hexSum {
			errJSON(w, 400, fn+": its SHA-256 does not match the list of originals")
			return
		}
		if err := writeFileAtomic(filepath.Join(folder, fn), data); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		saved = append(saved, map[string]any{"name": fn, "size": len(data), "sha256": hexSum})
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, req.Raw, "", "  ") != nil {
		pretty.Write(req.Raw)
	}
	if err := writeFileAtomic(filepath.Join(folder, "report.json"), pretty.Bytes()); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	n, err := s.db.InsertReport(*req.Report, name, folder)
	if err != nil {
		// The files are saved, but the report is not in the database: fail, so the app
		// sends it again (sending a report again replaces it).
		s.db.Log(LogEntry{Kind: "report", Device: device, IP: clientIP(r), Received: len(req.Report.Items),
			Message: name + ": sqlite: " + err.Error(), DurationMS: int(time.Since(start).Milliseconds())})
		telemetry.Errorf("report", err.Error())
		errJSON(w, 500, "sqlite: "+err.Error())
		return
	}
	stores := map[string]any{"sqlite": n}
	if s.hook != nil {
		s.hook.Report(folder)
		stores["hook"] = "queued"
	}
	s.db.Log(LogEntry{Kind: "report", Device: device, IP: clientIP(r), Received: len(req.Report.Items), Added: n,
		OK: err == nil, Message: name, DurationMS: int(time.Since(start).Milliseconds())})
	logf("received checkup report %s (%d items) from %s", name, len(req.Report.Items), device)
	writeJSON(w, 200, map[string]any{"ok": true, "folder": folder, "stores": stores, "files": saved})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.pairAllowed(w, r) {
		return
	}
	req := readPairRequest(r)
	if strings.TrimSpace(req.Code) != "" {
		if s.pair.Check(req.Code) {
			s.issue(w, r, req.Device, req.DeviceID, "code", nil)
			return
		}
		s.db.Log(LogEntry{Kind: "pair", Device: req.Device, IP: ip, Message: "wrong or expired code"})
		pairErr(w, 403, "wrong_code", "配对码不对或已过期 / wrong or expired code", nil)
		return
	}
	// Without a code, in one long request (older apps; newer ones use /pair/requests).
	pp, ok := s.openRequest(w, r, req)
	if !ok {
		return
	}
	_, status, _ := s.pair.Wait(r.Context(), pp.ID, requestTTL)
	if status == reqApproved && s.pair.Claim(pp.ID) {
		s.issue(w, r, req.Device, req.DeviceID, "approved", nil)
		return
	}
	if status == reqPending { // the phone hung up
		s.pair.Withdraw(pp.ID)
	}
	s.db.Log(LogEntry{Kind: "pair", Device: req.Device, IP: ip, Message: status})
	pairErr(w, 403, "not_approved", "电脑上没有点「允许」 / not approved", map[string]any{"status": status})
}

type pairRequest struct {
	Device   string `json:"device"`
	DeviceID string `json:"device_id"`
	Code     string `json:"code"`
}

func readPairRequest(r *http.Request) pairRequest {
	var req pairRequest
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	req.Device = strings.TrimSpace(req.Device)
	if req.Device == "" {
		req.Device = "iPhone"
	}
	if rr := []rune(req.Device); len(rr) > 60 {
		req.Device = string(rr[:60])
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if len(req.DeviceID) > 128 {
		req.DeviceID = req.DeviceID[:128]
	}
	return req
}

// issue makes a token for a phone that may pair and answers with it.
func (s *Server) issue(w http.ResponseWriter, r *http.Request, device, deviceID, how string, extra map[string]any) {
	ip := clientIP(r)
	tok := newToken()
	dev, repaired, err := s.db.AddDevice(device, tok, ip, deviceID, scopeDevice)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	msg := how
	if repaired {
		msg += ", paired again (earlier token replaced)"
	}
	s.db.Log(LogEntry{Kind: "pair", Device: device, IP: ip, OK: true, Message: msg})
	logf("paired %s (%s) by %s", device, ip, msg)
	out := map[string]any{"token": tok, "name": s.cfg.Name, "version": protocolVersion, "urls": s.urls(r),
		"device": map[string]any{"id": dev.ID, "name": dev.Name}, "repaired": repaired}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

// openRequest starts a request to pair without a code, or answers why it can't.
func (s *Server) openRequest(w http.ResponseWriter, r *http.Request, req pairRequest) (*PendingPair, bool) {
	ip := clientIP(r)
	if s.dialogModes()[0] != "dialog" {
		writeJSON(w, 428, map[string]any{"error": "需要配对码 / pairing code required", "reason": "needs_code", "needs_code": true})
		return nil, false
	}
	pp, retry, err := s.pair.Request(req.Device, req.DeviceID, ip, !s.cfg.NoDialog)
	if err != nil {
		secs := int(retry.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		s.db.Log(LogEntry{Kind: "pair", Device: req.Device, IP: ip, Message: "refused: " + err.Error()})
		pairErr(w, 429, "rate_limited", err.Error(), map[string]any{"retry_after": secs})
		return nil, false
	}
	logf("pair request from %s (%s), confirmation code %s, waiting for approval", req.Device, ip, pp.Confirm)
	s.notify(LogEntry{Kind: "pair_request", Device: req.Device, IP: ip, Message: pp.Confirm})
	return pp, true
}

// notify tells the desktop app something changed without writing to the history.
func (s *Server) notify(e LogEntry) {
	if s.db.onLog != nil {
		go s.db.onLog(e)
	}
}

// POST /pair/requests: ask to pair without a code. 202 {id, confirm, expires_in}.
func (s *Server) handlePairRequest(w http.ResponseWriter, r *http.Request) {
	if !s.pairAllowed(w, r) {
		return
	}
	pp, ok := s.openRequest(w, r, readPairRequest(r))
	if !ok {
		return
	}
	writeJSON(w, 202, map[string]any{"id": pp.ID, "confirm": pp.Confirm, "status": reqPending,
		"expires_in": int(time.Until(pp.Expires).Seconds())})
}

// GET /pair/requests/{id}?wait=25: the state of a request, waiting for a change up to
// wait seconds. An approved request answers with the token once.
func (s *Server) handlePairPoll(w http.ResponseWriter, r *http.Request) {
	if !s.pairAllowed(w, r) {
		return
	}
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	wait = max(0, min(wait, 30))
	id := r.PathValue("id")
	pp, status, found := s.pair.Wait(r.Context(), id, time.Duration(wait)*time.Second)
	if !found {
		pairErr(w, 404, "unknown_request", "no such pairing request (expired, cancelled or already used)", nil)
		return
	}
	switch status {
	case reqApproved:
		if !s.pair.Claim(id) {
			pairErr(w, 404, "unknown_request", "this request was already used", nil)
			return
		}
		s.issue(w, r, pp.Device, pp.DeviceID, "approved", map[string]any{"status": reqApproved})
	case reqDenied:
		s.db.Log(LogEntry{Kind: "pair", Device: pp.Device, IP: pp.IP, Message: "denied on the computer"})
		pairErr(w, 403, "not_approved", "电脑上点了「拒绝」 / denied", map[string]any{"status": reqDenied})
	case reqExpired:
		pairErr(w, 410, "expired", "没有人在电脑上点「允许」，请求已过期 / expired", map[string]any{"status": reqExpired})
	default:
		writeJSON(w, 200, map[string]any{"status": reqPending, "confirm": pp.Confirm,
			"expires_in": max(0, int(time.Until(pp.Expires).Seconds()))})
	}
}

// DELETE /pair/requests/{id}: the phone cancelled.
func (s *Server) handlePairWithdraw(w http.ResponseWriter, r *http.Request) {
	if !s.pairAllowed(w, r) {
		return
	}
	if !s.pair.Withdraw(r.PathValue("id")) {
		pairErr(w, 404, "unknown_request", "no such pairing request", nil)
		return
	}
	s.notify(LogEntry{Kind: "pair_request"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// pairAllowed refuses pairing from outside the local network and Tailscale.
func (s *Server) pairAllowed(w http.ResponseWriter, r *http.Request) bool {
	ip := clientIP(r)
	if isLANAddr(ip) {
		return true
	}
	// Log it with the address, so "why was I refused" can be answered from the history.
	s.db.Log(LogEntry{Kind: "pair", IP: ip, Message: "refused: not from the local network or Tailscale"})
	logf("pair refused: %s is not a local network or Tailscale address", ip)
	pairErr(w, 403, "not_local", "pairing is only allowed from the local network", map[string]any{"ip": ip})
	return false
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
		Storage: s.storage(),
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
	if _, _, err := s.db.AddDevice(name, tok, clientIP(r), "", scopeMCP); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: name, IP: clientIP(r), OK: true, Message: "AI token created in dashboard"})
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

// apiReport is one report with its items, their places in the originals, and the
// original files kept: GET /api/reports/{id}.
func (s *Server) apiReport(w http.ResponseWriter, r *http.Request) {
	rep, folder, err := s.db.ReportDetail(r.PathValue("id"))
	if err != nil {
		errJSON(w, 404, "no such report")
		return
	}
	files := []map[string]any{}
	for _, name := range originalFiles(folder) {
		fi, err := os.Stat(filepath.Join(folder, name))
		if err != nil {
			continue
		}
		files = append(files, map[string]any{"name": name, "size": fi.Size(), "type": contentType(name)})
	}
	writeJSON(w, 200, map[string]any{"report": rep, "files": files, "folder": folder})
}

// apiReportFile serves one original file of a report: GET /api/reports/{id}/files/{name}.
func (s *Server) apiReportFile(w http.ResponseWriter, r *http.Request) {
	_, folder, err := s.db.ReportDetail(r.PathValue("id"))
	name := r.PathValue("name")
	if err != nil || folder == "" || name != filepath.Base(name) || name == "report.json" || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(folder, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".heic":
		return "image/heic"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
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

// apiToken shows the shared token, which can do anything a paired phone can: only on the
// computer running the server.
func (s *Server) apiToken(w http.ResponseWriter, r *http.Request) {
	if !thisComputer(r) {
		writeJSON(w, 403, map[string]any{"error": "the shared token is only shown on the computer running Healthfolio", "reason": "local_only"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": s.token})
}
