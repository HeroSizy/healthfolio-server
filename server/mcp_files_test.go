package server

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// An AI assistant reads everything the server keeps: checkups with their places in the
// originals, the original files, the app's conversations and its settings.
func TestMCPReadsFilesChatsAndSettings(t *testing.T) {
	_, ts := newTestServer(t)
	png := []byte("\x89PNG fake")
	do(t, "POST", ts.URL+"/report", "master", `{"folder":"r","report":{"id":"R1","date":"2026-11-01","institution":"Clinic","title":"Annual",
		"items":[{"name":"LDL","value":"3.9","flag":"high","location":{"file":"p.png","page":0,"x":0.1,"y":0.2,"w":0.5,"h":0.05}}]},
		"files":[{"name":"p.png","data":"`+base64.StdEncoding.EncodeToString(png)+`"}]}`)
	do(t, "POST", ts.URL+"/backup/chats", "master", `{"id":"c1","title":"Sleep","messages":[{"role":"user","content":"hi"}]}`)
	do(t, "POST", ts.URL+"/backup/settings", "master", `{"theme":"dark"}`)
	_, conn := do(t, "POST", ts.URL+"/mcp/tokens", "master", `{"name":"Claude"}`)
	ai := conn["token"].(string)

	call := func(name, args string) map[string]any {
		t.Helper()
		code, r := do(t, "POST", ts.URL+"/mcp", ai, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		res, _ := r["result"].(map[string]any)
		if code != 200 || res == nil || res["isError"] == true {
			t.Fatalf("%s: %d %v", name, code, r)
		}
		return res["content"].([]any)[0].(map[string]any)
	}
	text := func(c map[string]any) string { return c["text"].(string) }

	if s := text(call("list_checkup_reports", `{}`)); !strings.Contains(s, `"p.png"`) || !strings.Contains(s, "Annual") || !strings.Contains(s, `"flagged":1`) {
		t.Errorf("reports: %s", s)
	}
	if s := text(call("get_checkups", `{"name":"LDL"}`)); !strings.Contains(s, `"loc_file":"p.png"`) || !strings.Contains(s, `"report_id":"R1"`) {
		t.Errorf("checkups: %s", s)
	}
	if c := call("get_report_file", `{"report_id":"R1","name":"p.png"}`); c["type"] != "image" || c["data"] != base64.StdEncoding.EncodeToString(png) {
		t.Errorf("file: %v", c)
	}
	if s := text(call("list_ai_chats", `{}`)); !strings.Contains(s, `"messages":1`) {
		t.Errorf("chats: %s", s)
	}
	var chat map[string]any
	_ = json.Unmarshal([]byte(text(call("get_ai_chat", `{"id":"c1"}`))), &chat)
	if chat["title"] != "Sleep" {
		t.Errorf("chat: %v", chat)
	}
	if s := text(call("get_app_settings", `{}`)); !strings.Contains(s, "dark") {
		t.Errorf("settings: %s", s)
	}
	// nothing outside the report's folder
	code, r := do(t, "POST", ts.URL+"/mcp", ai, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_report_file","arguments":{"report_id":"R1","name":"../health.db"}}}`)
	if res := r["result"].(map[string]any); code != 200 || res["isError"] != true {
		t.Errorf("escaped the folder: %v", r)
	}
}
