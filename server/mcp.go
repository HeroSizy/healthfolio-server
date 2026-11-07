package server

// A small Model Context Protocol server so AI assistants (Claude, etc.) can read your
// health data. Two transports:
//
//   - Streamable HTTP at POST /mcp on the running server (Bearer token, same tokens as
//     the app; stateless JSON responses)
//   - stdio: `healthsync-server mcp`, reading health.db directly, for desktop clients
//
// Everything is read-only.

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/herosizy/healthsync-server/model"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const mcpProtocol = "2025-06-18"

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCP struct {
	db *sql.DB // read-only connection
}

func OpenMCP(dbPath string) (*MCP, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	return &MCP{db: db}, db.Ping()
}

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

var readOnly = map[string]any{"readOnlyHint": true, "openWorldHint": false}

var mcpTools = []tool{
	{Name: "list_data_types", Title: "List health data types",
		Description: "Lists every Apple Health data type stored on this server with its record count, unit and the date range covered. Call this first to learn the exact type names.",
		InputSchema: obj(map[string]any{}), Annotations: readOnly},
	{Name: "daily_summary", Title: "Daily summary",
		Description: "Per-day aggregates (total, mean, min, max, count) for one data type. Use totals for cumulative types (steps, distance, active energy) and mean/min/max for samples (heart rate, weight, blood oxygen). Dates are the local day of the sample start.",
		InputSchema: obj(map[string]any{
			"type": prop("string", "Data type, e.g. HKQuantityTypeIdentifierStepCount or just StepCount"),
			"from": prop("string", "First day, YYYY-MM-DD (default: 30 days ago)"),
			"to":   prop("string", "Last day, YYYY-MM-DD (default: today)"),
		}, "type"), Annotations: readOnly},
	{Name: "get_records", Title: "Raw records",
		Description: "Raw samples for one data type in a time range, newest first. Sleep stages, workouts and other category types are best read this way.",
		InputSchema: obj(map[string]any{
			"type":  prop("string", "Data type (full identifier or short name)"),
			"from":  prop("string", "Start, YYYY-MM-DD or ISO 8601 (default: 7 days ago)"),
			"to":    prop("string", "End, YYYY-MM-DD or ISO 8601 (default: now)"),
			"limit": prop("integer", "Max rows, default 200, max 2000"),
		}, "type"), Annotations: readOnly},
	{Name: "get_checkups", Title: "Checkup results",
		Description: "Lab / medical checkup values imported in the app (from PDFs or photos), with reference ranges and high/low flags. Optionally filter by item name to see a trend across reports.",
		InputSchema: obj(map[string]any{
			"name": prop("string", "Filter by item name or key, substring match (e.g. LDL, 血糖)"),
			"from": prop("string", "Earliest report date, YYYY-MM-DD"),
		}), Annotations: readOnly},
	{Name: "query_sql", Title: "Read-only SQL",
		Description: "Run a read-only SQLite query. Tables: records(type, start, end, value, num, unit, source, received, uuid, source_bundle, source_version, device_model, device_software, user_entered, tz, metadata, deleted_at), daily view(type, day, unit, total, mean, min, max, n), checkup(report_id, date, institution, name, key, value, num, unit, range, flag). Views of live samples: v_heart_rate, v_resting_heart_rate, v_hrv, v_steps, v_weight, v_spo2 (uuid, start_at, end_at, <value>, source, device_model), v_sleep (stage), v_workouts (activity, duration_s, distance_km, active_energy_kcal, avg_heart_rate). `start`/`end` are ISO 8601 strings with time zone; `num` is the numeric value; `metadata` is JSON text (use json_extract). Rows with `deleted_at` set were deleted in Apple Health: add `WHERE deleted_at IS NULL` when querying records. At most 1000 rows are returned.",
		InputSchema: obj(map[string]any{"sql": prop("string", "A single SELECT statement")}, "sql"), Annotations: readOnly},
}

// Handle processes one JSON-RPC message. It returns nil for notifications.
func (m *MCP) Handle(raw []byte) *rpcResp {
	var req rpcReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}}
	}
	if len(req.ID) == 0 { // notification
		return nil
	}
	resp := &rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := mcpProtocol
		if p.ProtocolVersion != "" && p.ProtocolVersion < mcpProtocol {
			v = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "healthsync", "title": "HealthSync", "version": version},
			"instructions": "Personal Apple Health data synced from the user's iPhone, plus medical checkup results. " +
				"Start with list_data_types. Times are ISO 8601 with the phone's time zone. This is the user's private health data: " +
				"answer their questions, don't speculate beyond what the data shows, and suggest a doctor for medical decisions.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcErr{-32602, "invalid params"}
			break
		}
		out, err := m.call(p.Name, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
			break
		}
		b, _ := json.Marshal(out)
		resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + req.Method}
	}
	return resp
}

func argStr(a map[string]any, k string) string {
	if v, ok := a[k]; ok {
		return strings.TrimSpace(str(v))
	}
	return ""
}

// resolveType accepts "StepCount", "stepcount" or the full HK identifier.
func (m *MCP) resolveType(t string) (string, error) {
	if t == "" {
		return "", fmt.Errorf("type is required")
	}
	var full string
	err := m.db.QueryRow(`SELECT type FROM records WHERE deleted_at IS NULL AND (type = ? OR lower(type) LIKE '%' || lower(?)) GROUP BY type ORDER BY type = ? DESC, COUNT(*) DESC LIMIT 1`, t, t, t).Scan(&full)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no data type matching %q; call list_data_types", t)
	}
	return full, err
}

func (m *MCP) call(name string, a map[string]any) (any, error) {
	switch name {
	case "list_data_types":
		return m.rows(`SELECT type, COUNT(*) AS count, MAX(unit) AS unit, MIN(substr(start,1,10)) AS first_day, MAX(substr(start,1,10)) AS last_day
			FROM records WHERE deleted_at IS NULL GROUP BY type ORDER BY count DESC`)
	case "daily_summary":
		t, err := m.resolveType(argStr(a, "type"))
		if err != nil {
			return nil, err
		}
		from := firstNonEmpty(argStr(a, "from"), time.Now().AddDate(0, 0, -29).Format("2006-01-02"))
		to := firstNonEmpty(argStr(a, "to"), time.Now().Format("2006-01-02"))
		rows, err := m.rows(`SELECT day, unit, ROUND(total, 3) AS total, ROUND(mean, 3) AS mean, min, max, n
			FROM daily WHERE type = ? AND day BETWEEN ? AND ? ORDER BY day`, t, from[:min(10, len(from))], to[:min(10, len(to))])
		return map[string]any{"type": t, "days": rows}, err
	case "get_records":
		t, err := m.resolveType(argStr(a, "type"))
		if err != nil {
			return nil, err
		}
		from := firstNonEmpty(argStr(a, "from"), time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
		to := argStr(a, "to")
		if to == "" {
			to = "9999"
		} else if len(to) == 10 {
			to += "T23:59:59~" // include the whole last day
		}
		limit := 200
		if v, ok := a["limit"].(float64); ok && v > 0 {
			limit = min(int(v), 2000)
		}
		rows, err := m.rows(`SELECT start, "end", value, unit, source, device_model, metadata FROM records WHERE type = ? AND deleted_at IS NULL AND start >= ? AND start <= ? ORDER BY start DESC LIMIT ?`,
			t, from, to, limit)
		return map[string]any{"type": t, "records": rows}, err
	case "get_checkups":
		q := `SELECT date, institution, name, key, value, unit, range, flag FROM checkup WHERE 1=1`
		var args []any
		if n := argStr(a, "name"); n != "" {
			q += ` AND (lower(name) LIKE '%' || lower(?) || '%' OR lower(key) LIKE '%' || lower(?) || '%')`
			args = append(args, n, n)
		}
		if f := argStr(a, "from"); f != "" {
			q += ` AND date >= ?`
			args = append(args, f)
		}
		return m.rows(q+` ORDER BY date DESC, name LIMIT 2000`, args...)
	case "query_sql":
		s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(argStr(a, "sql")), ";"))
		low := strings.ToLower(s)
		if !(strings.HasPrefix(low, "select") || strings.HasPrefix(low, "with")) || strings.Contains(s, ";") {
			return nil, fmt.Errorf("only a single SELECT statement is allowed")
		}
		return m.rows(`SELECT * FROM (` + s + `) LIMIT 1000`)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func (m *MCP) rows(q string, args ...any) ([]map[string]any, error) {
	rs, err := m.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	out := []map[string]any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// ServeHTTP implements the Streamable HTTP transport (stateless, JSON responses only).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding protection: browsers send Origin; it must match the host.
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			errJSON(w, 403, "origin not allowed")
			return
		}
	}
	if _, ok := s.authDevice(r); !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="HealthSync"`)
		errJSON(w, 401, "bad token")
		return
	}
	if r.Method != http.MethodPost { // stateless: no server-initiated stream, no sessions
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "[") { // batch
		var msgs []json.RawMessage
		if json.Unmarshal(body, &msgs) != nil {
			errJSON(w, 400, "bad batch")
			return
		}
		var out []*rpcResp
		for _, m := range msgs {
			if r := s.mcp.Handle(m); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	resp := s.mcp.Handle(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, 200, resp)
}

// ServeStdio runs the stdio transport: newline-delimited JSON-RPC on stdin/stdout.
func (m *MCP) ServeStdio(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if resp := m.Handle([]byte(line)); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// RunMCPStdio serves MCP on stdin/stdout from the database in cfg's data directory.
func RunMCPStdio(cfg *Config) error {
	if err := cfg.Prepare(); err != nil {
		return err
	}
	p := cfg.DataDir + string(os.PathSeparator) + "health.db"
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("no database at %s (start the server and sync once first)", p)
	}
	// bring a database from an older version up to date before reading it
	db, err := OpenDB(p)
	if err != nil {
		return err
	}
	db.Close()
	m, err := OpenMCP(p)
	if err != nil {
		return err
	}
	return m.ServeStdio(os.Stdin, os.Stdout)
}

// MCPConnect is everything a client needs to show "connect your AI agent" with one-tap copy.
// Used by the dashboard and by the iPhone app (POST /mcp/tokens).
type MCPConnect = model.MCPConnect

type MCPConfig = model.MCPConfig

func mcpConnect(serverName, url, token string) MCPConnect {
	auth := "Bearer " + token
	jsonCfg, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"healthsync": map[string]any{
		"type": "http", "url": url, "headers": map[string]string{"Authorization": auth}}}}, "", "  ")
	remote, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"healthsync": map[string]any{
		"command": "npx", "args": []string{"-y", "mcp-remote", url, "--allow-http", "--header", "Authorization:" + auth}}}}, "", "  ")
	list := []MCPConfig{
		{ID: "claude_code", Title: "Claude Code", Text: fmt.Sprintf("claude mcp add --transport http healthsync %s --header \"Authorization: %s\"", url, auth)},
		{ID: "json", Title: "JSON (Cursor, VS Code, Windsurf, …)", Text: string(jsonCfg)},
		{ID: "stdio_bridge", Title: "Claude Desktop / stdio-only clients", Text: string(remote)},
		{ID: "agent_prompt", Title: "Instructions for any AI agent", Text: fmt.Sprintf(`Connect to my personal health data through MCP (read-only).

- Server: HealthSync on %s
- Transport: Streamable HTTP, JSON-RPC 2.0, POST %s
- Auth header: Authorization: %s
- Tools: list_data_types, daily_summary, get_records, get_checkups, query_sql

Start with list_data_types to see which Apple Health types exist, then use daily_summary for trends.
The server is on my home network (plain HTTP); you must run on a machine in the same network or over my VPN.
This is private health data: only use it to answer my questions.`, serverName, url, auth)},
	}
	m := map[string]string{}
	for _, c := range list {
		m[c.ID] = c.Text
	}
	return MCPConnect{Name: serverName, Token: token, URL: url, Transport: "streamable-http", Configs: m, ConfigList: list}
}

// handleMCPToken lets a paired device (the iPhone app) mint a separate, revocable token for
// an AI agent and get ready-to-copy connection configs. POST /mcp/tokens {"name": "..."}
func (s *Server) handleMCPToken(w http.ResponseWriter, r *http.Request) {
	device, ok := s.authDevice(r)
	if !ok {
		errJSON(w, 401, "bad token")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "AI assistant"
	}
	if device != "" {
		name += " (via " + device + ")"
	}
	if rr := []rune(name); len(rr) > 80 {
		name = string(rr[:80])
	}
	tok := newToken()
	if err := s.db.AddDevice(name, tok, clientIP(r)); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: name, IP: clientIP(r), OK: true, Message: "MCP token created"})
	writeJSON(w, 200, mcpConnect(s.cfg.Name, s.baseURL(r)+"/mcp", tok))
}
