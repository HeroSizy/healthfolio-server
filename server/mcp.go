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
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/herosizy/healthsync-server/model"
	"io"
	"modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// MCP serves the tools over the data of every person on the server (people.go). Each call
// reads one person's database; whom a caller may see is its mcpScope.
type MCP struct {
	main *sql.DB // read-only connection to health.db: the people, the prefs and the owner's data
	dir  string  // the data folder
	// ensure makes a person's database when it is not there yet (the server sets it; the
	// stdio transport only reads what exists)
	ensure func(n int64) (*DB, error)

	mu  sync.Mutex
	dbs map[int64]*sql.DB // read-only connections to the other people's databases
}

// mcpDB is one person's data, as the tools read it.
type mcpDB struct {
	db  *sql.DB
	dir string // the person's folder: chats/, settings/ (reports know their own folders)
	who Person
}

// mcpScope is whom a caller may see: everyone, or one person.
type mcpScope struct {
	all    bool
	person int64
}

var everyone = mcpScope{all: true}

func openRO(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
}

func OpenMCP(dbPath string) (*MCP, error) {
	db, err := openRO(dbPath)
	if err != nil {
		return nil, err
	}
	return &MCP{main: db, dir: filepath.Dir(dbPath), dbs: map[int64]*sql.DB{}}, db.Ping()
}

// Close closes every connection.
func (m *MCP) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for n, db := range m.dbs {
		db.Close()
		delete(m.dbs, n)
	}
	m.main.Close()
}

// closePerson closes the connection to person n's database (deleted or merged).
func (m *MCP) closePerson(n int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if db := m.dbs[n]; db != nil {
		db.Close()
		delete(m.dbs, n)
	}
}

// open is person p's data.
func (m *MCP) open(p Person) (*mcpDB, error) {
	if p.Num <= ownerNum {
		return &mcpDB{db: m.main, dir: m.dir, who: p}, nil
	}
	dir := personDir(m.dir, p.Num)
	m.mu.Lock()
	defer m.mu.Unlock()
	if db := m.dbs[p.Num]; db != nil {
		return &mcpDB{db: db, dir: dir, who: p}, nil
	}
	path := filepath.Join(dir, "health.db")
	if _, err := os.Stat(path); err != nil {
		if m.ensure == nil {
			return nil, fmt.Errorf("%s has no data on this server yet", personLabel(p))
		}
		if _, err := m.ensure(p.Num); err != nil {
			return nil, err
		}
	}
	db, err := openRO(path)
	if err != nil {
		return nil, err
	}
	m.dbs[p.Num] = db
	return &mcpDB{db: db, dir: dir, who: p}, nil
}

// visible is the people sc may see, the owner first.
func (m *MCP) visible(sc mcpScope) ([]Person, error) {
	rows, err := m.main.Query(`SELECT ` + personCols + ` FROM people ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		if sc.all || p.Num == sc.person {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// pick is the person a call names, among those sc may see. Without a name it is the only
// one there is; when there are several it asks for one, rather than guess (lenient: the
// owner, for resources, which take no arguments).
func (m *MCP) pick(sc mcpScope, key string, lenient bool) (*mcpDB, error) {
	vis, err := m.visible(sc)
	if err != nil {
		return nil, err
	}
	if len(vis) == 0 {
		return nil, fmt.Errorf("this token may not see anyone's data")
	}
	if key == "" {
		if len(vis) == 1 {
			return m.open(vis[0])
		}
		if lenient && vis[0].Owner {
			return m.open(vis[0])
		}
		names := []string{}
		for _, p := range vis {
			names = append(names, fmt.Sprintf("%s (%s)", personLabel(p), p.ID))
		}
		return nil, fmt.Errorf("this server keeps the health data of %d people: %s. Pass person with one of these ids (see list_people)",
			len(vis), strings.Join(names, ", "))
	}
	var n int64
	if err := m.main.QueryRow(`SELECT person FROM person_keys WHERE key = ?`, key).Scan(&n); err != nil {
		// a name works too, when it is unambiguous
		var hits []Person
		for _, p := range vis {
			if strings.EqualFold(p.Name, key) {
				hits = append(hits, p)
			}
		}
		if len(hits) == 1 {
			return m.open(hits[0])
		}
		return nil, fmt.Errorf("no person %q; call list_people", key)
	}
	for _, p := range vis {
		if p.Num == n {
			return m.open(p)
		}
	}
	return nil, fmt.Errorf("no person %q; call list_people", key)
}

// listPeople is the list_people tool.
func (m *MCP) listPeople(sc mcpScope) (any, error) {
	vis, err := m.visible(sc)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range vis {
		row := map[string]any{"person": p.ID, "name": p.Name, "relation": p.Relation, "birth": p.Birth, "sex": p.Sex, "owner": p.Owner}
		if d, err := m.open(p); err == nil {
			var n int
			var last, rep sql.NullString
			_ = d.db.QueryRow(`SELECT COUNT(*), MAX(start) FROM records WHERE deleted_at IS NULL`).Scan(&n, &last)
			row["records"], row["latest_record"] = n, last.String
			_ = d.db.QueryRow(`SELECT COUNT(*), MAX(date) FROM reports`).Scan(&n, &rep)
			row["reports"], row["latest_report"] = n, rep.String
		}
		out = append(out, row)
	}
	return out, nil
}

// personProp is the argument every tool but list_people takes.
var personProp = prop("string", "Whose data: a person id from list_people (or a name). Required when this server keeps the data of more than one person.")

// allTools is every tool the server offers.
func allTools() []tool {
	out := []tool{listPeopleTool}
	for _, t := range append(append([]tool{}, mcpTools...), mcpFileTools...) {
		schema := map[string]any{}
		for k, v := range t.InputSchema {
			schema[k] = v
		}
		props := map[string]any{"person": personProp}
		for k, v := range t.InputSchema["properties"].(map[string]any) {
			props[k] = v
		}
		schema["properties"] = props
		t.InputSchema = schema
		out = append(out, t)
	}
	return out
}

var listPeopleTool = tool{Name: "list_people", Title: "People",
	Description: "Lists the people whose health data this server keeps (the user and their family): id, name, relation, birth, sex, " +
		"how many records and checkup reports each has. Every other tool reads one person: pass their id as person. Call this first.",
	InputSchema: obj(map[string]any{}), Annotations: readOnly}

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
		Description: "Run a read-only SQLite query. Tables: records(type, start, end, value, num, unit, source, received, uuid, source_bundle, source_version, device_model, device_software, user_entered, tz, metadata, deleted_at), daily view(type, day, unit, total, mean, min, max, n), checkup(report_id, date, institution, name, key, value, num, unit, range, flag, original_name, loc_file, loc_page, loc_x, loc_y, loc_w, loc_h), reports(id, date, institution, title, items, folder, received, originals JSON), sync_log(time, kind, device, received, added, ok, message). Views of live samples: v_heart_rate, v_resting_heart_rate, v_hrv, v_steps, v_weight, v_spo2 (uuid, start_at, end_at, <value>, source, device_model), v_sleep (stage), v_workouts (activity, duration_s, distance_km, active_energy_kcal, avg_heart_rate). `start`/`end` are ISO 8601 strings with time zone; `num` is the numeric value; `metadata` is JSON text (use json_extract). Rows with `deleted_at` set were deleted in Apple Health: add `WHERE deleted_at IS NULL` when querying records. At most 1000 rows are returned.",
		InputSchema: obj(map[string]any{"sql": prop("string", "A single SELECT statement")}, "sql"), Annotations: readOnly},
}

// Handle processes one JSON-RPC message for a caller that sees everyone. It returns nil
// for notifications.
func (m *MCP) Handle(raw []byte) *rpcResp { return m.HandleFor(everyone, raw) }

// HandleFor is Handle for a caller that may see only whom sc says.
func (m *MCP) HandleFor(sc mcpScope, raw []byte) *rpcResp {
	var req rpcReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}}
	}
	if len(req.ID) == 0 { // notification
		return nil
	}
	resp := &rpcResp{JSONRPC: "2.0", ID: req.ID}
	if req.Method != "ping" && !mcpEnabled(m.main) {
		resp.Error = &rpcErr{-32001, mcpOffMessage}
		return resp
	}
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
			"capabilities":    map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":      map[string]any{"name": "healthsync", "title": "Healthfolio", "version": version},
			"instructions": "Personal Apple Health data synced from the user's iPhone, plus medical checkup results with their original PDFs and photos, " +
				"and the conversations and settings of the Healthfolio iPhone app. The server may keep the data of several people (a family), each apart: " +
				"call list_people first, and pass person to the other tools; never mix one person's data with another's. " +
				"Then start with list_data_types (or list_checkup_reports for checkups). Times are ISO 8601 with the phone's time zone. This is private health data: " +
				"answer the user's questions, don't speculate beyond what the data shows, and suggest a doctor for medical decisions.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": allTools()}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcErr{-32602, "invalid params"}
			break
		}
		var out any
		var err error
		if p.Name == "list_people" {
			out, err = m.listPeople(sc)
		} else {
			var d *mcpDB
			if d, err = m.pick(sc, argStr(p.Arguments, "person"), false); err == nil {
				out, err = d.call(p.Name, p.Arguments)
			}
		}
		if err != nil {
			resp.Result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
			break
		}
		if c, ok := out.(mcpContent); ok {
			resp.Result = map[string]any{"content": []any(c)}
			break
		}
		b, _ := json.Marshal(out)
		resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": mcpPrompts}
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		out, err := getPrompt(p.Name, p.Arguments)
		if err != nil {
			resp.Error = &rpcErr{-32602, err.Error()}
			break
		}
		resp.Result = out
	case "resources/list":
		resp.Result = map[string]any{"resources": mcpResources}
	case "resources/templates/list":
		resp.Result = map[string]any{"resourceTemplates": mcpResourceTemplates}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		// a resource is the owner's, or ?person=<id> at the end of its address
		uri, key, _ := strings.Cut(p.URI, "?person=")
		d, err := m.pick(sc, key, true)
		var out map[string]any
		if err == nil {
			out, err = d.readResource(uri)
		}
		if err == nil {
			out["uri"] = p.URI
		}
		if err != nil {
			resp.Error = &rpcErr{-32002, err.Error()}
			break
		}
		resp.Result = map[string]any{"contents": []any{out}}
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
func (m *mcpDB) resolveType(t string) (string, error) {
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

func (m *mcpDB) call(name string, a map[string]any) (any, error) {
	if out, ok, err := m.callFiles(name, a); ok {
		return out, err
	}
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
		q := `SELECT report_id, date, institution, name, original_name, key, value, unit, range, flag,
			loc_file, loc_page, loc_x, loc_y, loc_w, loc_h FROM checkup WHERE 1=1`
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
		return m.userQuery(`SELECT * FROM (` + s + `) LIMIT 1000`)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

// queryTimeout bounds every query an AI assistant starts: a cross join over the records
// table would otherwise keep a CPU core busy for hours.
const queryTimeout = 15 * time.Second

func (m *mcpDB) rows(q string, args ...any) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rs, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, timeoutErr(ctx, err)
	}
	defer rs.Close()
	out, err := scanRows(rs)
	return out, timeoutErr(ctx, err)
}

func timeoutErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("the query took longer than %d seconds and was stopped; narrow it down (a time range, a type, fewer joins)", int(queryTimeout.Seconds()))
	}
	return err
}

// hiddenTables hold the server's own bookkeeping, not health data: the paired devices (with
// their token hashes and addresses) and the server's preferences. query_sql may not read them.
var hiddenTables = map[string]bool{"devices": true, "prefs": true, "people": true, "person_keys": true}

// sqlPolicy is the SQLite authorizer for query_sql: reading and computing only, nothing
// from hiddenTables, no client addresses from sync_log, and no functions that touch
// anything outside the database.
func sqlPolicy(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
	switch action {
	case sqlite.AuthSelect, sqlite.AuthRecursive:
		return sqlite.AuthorizerOK
	case sqlite.AuthRead:
		t := strings.ToLower(arg1)
		if hiddenTables[t] {
			return sqlite.AuthorizerDeny
		}
		if t == "sync_log" && strings.EqualFold(arg2, "ip") {
			return sqlite.AuthorizerIgnore // reads as NULL
		}
		return sqlite.AuthorizerOK
	case sqlite.AuthFunction:
		switch strings.ToLower(arg2) {
		case "load_extension", "readfile", "writefile", "edit", "fts3_tokenizer", "sqlite_offset":
			return sqlite.AuthorizerDeny
		}
		return sqlite.AuthorizerOK
	case sqlite.AuthPragma:
		switch strings.ToLower(arg1) {
		case "table_info", "table_xinfo", "table_list", "index_list", "index_info", "index_xinfo", "foreign_key_list":
			if hiddenTables[strings.ToLower(arg2)] {
				return sqlite.AuthorizerDeny
			}
			return sqlite.AuthorizerOK
		}
	}
	return sqlite.AuthorizerDeny
}

// userQuery runs SQL written by an AI assistant on its own connection, under sqlPolicy and
// queryTimeout.
func (m *mcpDB) userQuery(q string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	setAuth := func(fn sqlite.AuthorizerFn) error {
		return conn.Raw(func(dc any) error {
			r, ok := dc.(sqlite.AuthorizerRegisterer)
			if !ok {
				return fmt.Errorf("the SQLite driver cannot restrict queries")
			}
			return r.RegisterAuthorizer(fn)
		})
	}
	if err := setAuth(sqlPolicy); err != nil {
		return nil, err
	}
	// the connection goes back to the pool afterwards: take the policy off again, or drop
	// the connection if that fails
	defer func() {
		if setAuth(nil) != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	rs, err := conn.QueryContext(ctx, q)
	if err != nil {
		if strings.Contains(err.Error(), "not authorized") {
			return nil, fmt.Errorf("not allowed: query_sql reads the health tables and views only (records, daily, checkup, reports, sync_log and the v_* views)")
		}
		return nil, timeoutErr(ctx, err)
	}
	defer rs.Close()
	out, err := scanRows(rs)
	return out, timeoutErr(ctx, err)
}

func scanRows(rs *sql.Rows) ([]map[string]any, error) {
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
	dev, ok := s.authDevice(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Healthfolio"`)
		errJSON(w, 401, "bad token")
		return
	}
	sc := mcpScope{all: seesEveryone(dev), person: dev.PersonNum}
	if !sc.all && sc.person == 0 {
		errJSON(w, 403, "this token belongs to no person")
		return
	}
	if !s.db.MCPEnabled() {
		errJSON(w, 403, mcpOffMessage)
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
			if r := s.mcp.HandleFor(sc, m); r != nil {
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
	resp := s.mcp.HandleFor(sc, body)
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
	on := db.MCPEnabled()
	db.Close()
	if !on {
		return fmt.Errorf("%s", mcpOffMessage)
	}
	m, err := OpenMCP(p)
	if err != nil {
		return err
	}
	defer m.Close()
	return m.ServeStdio(os.Stdin, os.Stdout)
}

// MCPConnect is everything a client needs to show "connect your AI agent" with one-tap copy.
// Used by the dashboard and by the iPhone app (POST /mcp/tokens).
type MCPConnect = model.MCPConnect

type MCPConfig = model.MCPConfig

func mcpConnect(serverName, url, token string) MCPConnect {
	auth := "Bearer " + token
	httpServer := map[string]any{"healthsync": map[string]any{
		"type": "http", "url": url, "headers": map[string]string{"Authorization": auth}}}
	jsonCfg, _ := json.MarshalIndent(map[string]any{"mcpServers": httpServer}, "", "  ")
	// VS Code's mcp.json keeps servers under "servers", not "mcpServers"
	vscodeCfg, _ := json.MarshalIndent(map[string]any{"servers": httpServer}, "", "  ")
	// mcp-remote reads the header from the environment: a space inside an argument can be
	// split apart by some clients, so "Bearer <token>" goes in env, as its docs advise.
	remote, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"healthsync": map[string]any{
		"command": "npx", "args": []string{"-y", "mcp-remote", url, "--allow-http", "--header", "Authorization:${AUTH_HEADER}"},
		"env": map[string]string{"AUTH_HEADER": auth}}}}, "", "  ")
	list := []MCPConfig{
		{ID: "claude_code", Title: "Claude Code", Text: fmt.Sprintf("claude mcp add --transport http healthsync %s --header \"Authorization: %s\"", url, auth)},
		{ID: "json", Title: "JSON (Cursor and other mcpServers clients)", Text: string(jsonCfg)},
		{ID: "vscode", Title: "VS Code (.vscode/mcp.json)", Text: string(vscodeCfg)},
		{ID: "stdio_bridge", Title: "Claude Desktop / stdio-only clients", Text: string(remote)},
		{ID: "agent_prompt", Title: "Instructions for any AI agent", Text: fmt.Sprintf(`Connect to my personal health data through MCP (read-only).

- Server: Healthfolio on %s
- Transport: Streamable HTTP, JSON-RPC 2.0, POST %s
- Auth header: Authorization: %s
- Tools: list_people, list_data_types, daily_summary, get_records, get_checkups, list_checkup_reports, get_report_file, list_ai_chats, get_ai_chat, get_app_settings, query_sql

Start with list_people: the server may keep the data of several people in my family, and each tool reads one of them (the person argument).
Then use list_data_types to see which Apple Health types exist, and daily_summary for trends.
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
	dev, ok := s.authPhone(w, r, "/mcp/tokens")
	if !ok {
		return
	}
	device := dev.Name
	if device == "shared token" {
		device = ""
	}
	if !s.db.MCPEnabled() {
		errJSON(w, 403, mcpOffMessage)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Person string `json:"person"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	name := cleanName(req.Name)
	if name == "" {
		name = "AI assistant"
	}
	if device != "" {
		name += " (via " + device + ")"
	}
	if rr := []rune(name); len(rr) > 80 {
		name = string(rr[:80])
	}
	// whom the token sees: the person asked for; without one, everyone for a phone that may
	// manage the family, else the phone's own person
	person := dev.PersonNum
	if dev.Family {
		person = 0
	}
	if key := firstNonEmpty(strings.TrimSpace(req.Person), personParam(r)); key != "" {
		p, found := s.db.personByKey(key)
		if !found {
			writeJSON(w, 404, map[string]any{"error": "no such person", "reason": "unknown_person"})
			return
		}
		if !canAccess(dev, p.Num) {
			writeJSON(w, 403, map[string]any{"error": "this phone may only make AI tokens for its own person", "reason": "person_not_allowed"})
			return
		}
		person = p.Num
	}
	tok := newToken()
	if _, _, err := s.db.AddDevice(name, tok, clientIP(r), "", scopeMCP, person, false); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: name, IP: clientIP(r), OK: true, Message: "MCP token created"})
	writeJSON(w, 200, mcpConnect(s.cfg.Name, s.baseURL(r)+"/mcp", tok))
}
