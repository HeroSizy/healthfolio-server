package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMCP(t *testing.T) {
	s, ts := newTestServer(t)
	body := "HKQuantityTypeIdentifierStepCount|2026-11-01T08:00:00+08:00|2026-11-01T08:10:00+08:00|500|count|phone\n" +
		"HKQuantityTypeIdentifierStepCount|2026-11-01T09:00:00+08:00|2026-11-01T09:10:00+08:00|700|count|phone\n"
	do(t, "POST", ts.URL+"/ingest", "master", body)

	if code, _ := do(t, "POST", ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); code != 401 {
		t.Fatalf("mcp without token: %d", code)
	}
	code, r := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if code != 200 || r["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "healthsync" {
		t.Fatalf("initialize: %d %v", code, r)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "http://evil.example"); code != 403 {
		t.Fatalf("foreign origin allowed: %d", code)
	}

	call := func(name, args string) (string, bool) {
		_, r := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		res := r["result"].(map[string]any)
		isErr, _ := res["isError"].(bool)
		return res["content"].([]any)[0].(map[string]any)["text"].(string), isErr
	}
	out, isErr := call("daily_summary", `{"type":"stepcount","from":"2026-11-01","to":"2026-11-01"}`)
	if isErr || !strings.Contains(out, `"total":1200`) {
		t.Fatalf("daily_summary: %s", out)
	}
	out, isErr = call("get_records", `{"type":"StepCount","from":"2026-11-01","to":"2026-11-01"}`)
	if isErr || strings.Count(out, `"value"`) != 2 {
		t.Fatalf("get_records: %s", out)
	}
	if out, isErr = call("query_sql", `{"sql":"DELETE FROM records"}`); !isErr {
		t.Fatalf("delete allowed: %s", out)
	}
	if out, isErr = call("query_sql", `{"sql":"WITH x AS (SELECT 1) DELETE FROM records"}`); !isErr {
		t.Fatalf("CTE delete allowed: %s", out)
	}
	if out, isErr = call("query_sql", `{"sql":"SELECT COUNT(*) AS n FROM records"}`); isErr || !strings.Contains(out, `"n":2`) {
		t.Fatalf("select: %s", out)
	}

	// the app mints a separate MCP token and gets copyable configs
	code, c := do(t, "POST", ts.URL+"/mcp/tokens", "master", `{"name":"Claude"}`)
	if code != 200 || c["url"] != "http://192.168.1.2:8787/mcp" || !strings.Contains(c["configs"].(map[string]any)["claude_code"].(string), "claude mcp add") {
		t.Fatalf("mcp token: %d %v", code, c)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp", c["token"].(string), `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 200 {
		t.Fatalf("new mcp token rejected: %d", code)
	}

	// stdio transport
	var buf bytes.Buffer
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	if err := s.mcp.ServeStdio(strings.NewReader(in), &buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var tl struct {
		Result struct{ Tools []tool } `json:"result"`
	}
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &tl) != nil || len(tl.Result.Tools) != len(allTools()) {
		t.Fatalf("stdio: %q", buf.String())
	}
}

func TestMCPPromptsResourcesAndSwitch(t *testing.T) {
	s, ts := newTestServer(t)
	do(t, "POST", ts.URL+"/ingest", "master", "StepCount|"+time.Now().Format("2006-01-02")+"T08:00:00+08:00|"+time.Now().Format("2006-01-02")+"T08:10:00+08:00|500|count|phone\n")
	rpc := func(method, params string) map[string]any {
		_, r := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`)
		return r
	}
	if p := rpc("prompts/list", `{}`)["result"].(map[string]any)["prompts"].([]any); len(p) != len(mcpPrompts) {
		t.Fatalf("prompts: %v", p)
	}
	r := rpc("prompts/get", `{"name":"compare_periods","arguments":{"period_a":"last month","period_b":"this month"}}`)
	if msg, _ := json.Marshal(r["result"]); !strings.Contains(string(msg), "between last month and this month") {
		t.Fatalf("prompt: %s", msg)
	}
	if r := rpc("prompts/get", `{"name":"compare_periods","arguments":{}}`); r["error"] == nil {
		t.Fatalf("missing argument accepted")
	}
	for _, uri := range []string{"healthsync://schema", "healthsync://skill", "healthsync://data-types", "healthsync://summary/7d", "healthsync://checkups/latest", "healthsync://daily/StepCount"} {
		r := rpc("resources/read", `{"uri":"`+uri+`"}`)
		if r["error"] != nil {
			t.Fatalf("%s: %v", uri, r["error"])
		}
	}
	if b, _ := json.Marshal(rpc("resources/read", `{"uri":"healthsync://summary/7d"}`)); !strings.Contains(string(b), `StepCount`) {
		t.Errorf("summary: %s", b)
	}

	// turned off in the dashboard: /mcp, /mcp/tokens and stdio refuse, /health hides mcp
	if code, _ := do(t, "POST", ts.URL+"/api/mcp", "", `{"enabled":false}`, "X-HealthSync", "1"); code != 200 {
		t.Fatalf("switch off: %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 403 {
		t.Fatalf("mcp still on: %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/mcp/tokens", "master", `{}`); code != 403 {
		t.Fatalf("tokens still issued: %d", code)
	}
	if _, h := do(t, "GET", ts.URL+"/health", "", ""); h["mcp"] != nil || h["mcp_enabled"] != false {
		t.Fatalf("health: %v", h)
	}
	var buf bytes.Buffer
	_ = s.mcp.ServeStdio(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &buf)
	if !strings.Contains(buf.String(), "turned off") {
		t.Fatalf("stdio still on: %s", buf.String())
	}
	_ = s.db.SetMCPEnabled(true)
	if code, _ := do(t, "POST", ts.URL+"/mcp", "master", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 200 {
		t.Fatalf("mcp back on: %d", code)
	}
}
