package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &tl) != nil || len(tl.Result.Tools) != len(mcpTools) {
		t.Fatalf("stdio: %q", buf.String())
	}
}
