package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupChatsAndSettings(t *testing.T) {
	s, ts := newTestServer(t)
	url := ts.URL
	if code, _ := do(t, "POST", url+"/backup/chats", "", `{"id":"a"}`); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	_, conn := do(t, "POST", url+"/mcp/tokens", "master", `{"name":"Claude"}`)
	ai, _ := conn["token"].(string)
	if code, _ := do(t, "POST", url+"/backup/chats", ai, `{"id":"a"}`); code != 403 {
		t.Fatalf("AI token wrote a backup: %d", code)
	}

	// one conversation, then a batch that replaces it and adds another
	if code, r := do(t, "POST", url+"/backup/chats", "master", `{"id":"c1","title":"Sleep","messages":[{"role":"user","content":"hi"}]}`); code != 200 || r["saved"].(float64) != 1 {
		t.Fatalf("save one: %d %v", code, r)
	}
	if code, r := do(t, "POST", url+"/backup/chats", "master", `{"chats":[{"id":"c1","title":"Sleep v2"},{"id":"../evil","title":"x"}]}`); code != 200 || r["saved"].(float64) != 2 {
		t.Fatalf("save batch: %d %v", code, r)
	}
	if code, _ := do(t, "POST", url+"/backup/chats", "master", `{"chats":[{"title":"no id"}]}`); code != 400 {
		t.Fatalf("chat without id accepted: %d", code)
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "chats", "c1.json"))
	if err != nil || !strings.Contains(string(b), "Sleep v2") {
		t.Fatalf("c1 not replaced: %s %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.cfg.DataDir, "chats"))
	if len(entries) != 2 {
		t.Fatalf("want 2 chat files, got %d", len(entries))
	}
	if code, r := do(t, "GET", url+"/backup/chats", "master", ""); code != 200 || len(r["chats"].([]any)) != 2 {
		t.Fatalf("list: %d %v", code, r)
	}
	if code, r := do(t, "GET", url+"/backup/chats/c1", "master", ""); code != 200 || r["title"] != "Sleep v2" {
		t.Fatalf("get: %d %v", code, r)
	}

	// settings: secret-looking strings are dropped
	code, r := do(t, "POST", url+"/backup/settings", "master", `{"theme":"dark","maxTokens":4000,"provider":{"name":"x","apiKey":"sk-1"}}`)
	if code != 200 || len(anyStrings(r["dropped"])) != 1 || anyStrings(r["dropped"])[0] != "provider.apiKey" {
		t.Fatalf("settings: %d %v", code, r)
	}
	code, r = do(t, "GET", url+"/backup/settings", "master", "")
	set, _ := r["settings"].(map[string]any)
	if code != 200 || set["theme"] != "dark" || set["maxTokens"].(float64) != 4000 {
		t.Fatalf("get settings: %d %v", code, r)
	}
	if strings.Contains(r["settings"].(map[string]any)["provider"].(map[string]any)["name"].(string), "sk-") {
		t.Fatal("secret kept")
	}
	files, _ := os.ReadDir(filepath.Join(s.cfg.DataDir, "settings"))
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(s.cfg.DataDir, "settings", f.Name()))
		if strings.Contains(string(b), "sk-1") {
			t.Fatal("secret written to disk")
		}
	}
}

func TestChatFile(t *testing.T) {
	if chatFile("6F9619FF-8B86-D011-B42D-00C04FC964FF") != "6F9619FF-8B86-D011-B42D-00C04FC964FF.json" {
		t.Error("uuid changed")
	}
	for _, id := range []string{"../x", "a/b", "..", "."} {
		if f := chatFile(id); strings.ContainsAny(f, `/\`) || strings.HasPrefix(f, ".") {
			t.Errorf("%q → %q", id, f)
		}
	}
	if chatFile("a/b") == chatFile("a_b") {
		t.Error("collision")
	}
}
