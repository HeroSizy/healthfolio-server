package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// dialogServer is a test server that pairs without a code (as on macOS), with the native
// dialog replaced by nothing: requests are answered through the dashboard API.
func dialogServer(t *testing.T) (*Server, string) {
	s, ts := newTestServer(t)
	s.cfg.NoDialog = false
	t.Setenv("HEALTHSYNC_DASHBOARD_APPROVAL", "1")
	s.pair.rq.popup = nil
	return s, ts.URL
}

func TestPairRequestFlow(t *testing.T) {
	s, url := dialogServer(t)

	code, h := do(t, "GET", url+"/health", "", "")
	if code != 200 || !strings.Contains(strings.Join(anyStrings(h["features"]), ","), "pair_requests") {
		t.Fatalf("health features: %v", h["features"])
	}

	body := `{"device":"Zen's iPhone","device_id":"PHONE-1"}`
	code, r := do(t, "POST", url+"/pair/requests", "", body)
	if code != 202 || len(r["confirm"].(string)) != 4 {
		t.Fatalf("request: %d %v", code, r)
	}
	id := r["id"].(string)

	// the same phone asking again gets the same request, not a second dialog
	_, again := do(t, "POST", url+"/pair/requests", "", body)
	if again["id"] != id {
		t.Fatalf("retry opened a new request: %v vs %v", again["id"], id)
	}
	if p := s.pair.PendingInfo(); len(p) != 1 || p[0].Confirm != r["confirm"] {
		t.Fatalf("pending: %+v", p)
	}

	code, st := do(t, "GET", url+"/pair/requests/"+id, "", "")
	if code != 200 || st["status"] != "pending" {
		t.Fatalf("poll: %d %v", code, st)
	}

	// allow it while the phone is waiting
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.pair.Answer(id, true)
	}()
	code, ok := do(t, "GET", url+"/pair/requests/"+id+"?wait=5", "", "")
	if code != 200 || ok["status"] != "approved" || ok["token"] == "" || ok["repaired"] != false {
		t.Fatalf("approved: %d %v", code, ok)
	}
	tok1 := ok["token"].(string)

	// the token is handed out once
	if code, _ := do(t, "GET", url+"/pair/requests/"+id, "", ""); code != 404 {
		t.Fatalf("second fetch of the token: %d", code)
	}

	code, a := do(t, "GET", url+"/auth", tok1, "")
	if code != 200 || a["scope"] != "device" {
		t.Fatalf("auth: %d %v", code, a)
	}

	// pairing again with the same device id keeps the device and replaces its token
	_, r2 := do(t, "POST", url+"/pair/requests", "", body)
	s.pair.Answer(r2["id"].(string), true)
	_, ok2 := do(t, "GET", url+"/pair/requests/"+r2["id"].(string), "", "")
	if ok2["repaired"] != true || ok2["device"].(map[string]any)["id"] != ok["device"].(map[string]any)["id"] {
		t.Fatalf("pair again: %v", ok2)
	}
	if code, a := do(t, "GET", url+"/auth", tok1, ""); code != 401 || a["reason"] != "unknown" {
		t.Fatalf("old token after pairing again: %d %v", code, a)
	}
	devs, _ := s.db.Devices()
	if len(devs) != 1 {
		t.Fatalf("pairing again added a device: %+v", devs)
	}

	// revoked
	tok2 := ok2["token"].(string)
	_ = s.db.RevokeDevice(devs[0].ID)
	if code, a := do(t, "GET", url+"/auth", tok2, ""); code != 401 || a["reason"] != "revoked" {
		t.Fatalf("revoked: %d %v", code, a)
	}
	if code, a := do(t, "GET", url+"/auth", "", ""); code != 401 || a["reason"] != "missing" {
		t.Fatalf("missing: %d %v", code, a)
	}
}

func TestPairRequestDeniedAndExpired(t *testing.T) {
	s, url := dialogServer(t)
	_, r := do(t, "POST", url+"/pair/requests", "", `{"device":"a","device_id":"A"}`)
	s.pair.Answer(r["id"].(string), false)
	if code, st := do(t, "GET", url+"/pair/requests/"+r["id"].(string), "", ""); code != 403 || st["reason"] != "not_approved" {
		t.Fatalf("denied: %d %v", code, st)
	}

	_, r = do(t, "POST", url+"/pair/requests", "", `{"device":"b","device_id":"B"}`)
	later := time.Now().Add(requestTTL + time.Second)
	s.pair.rq.mu.Lock()
	s.pair.rq.now = func() time.Time { return later }
	s.pair.rq.mu.Unlock()
	if code, st := do(t, "GET", url+"/pair/requests/"+r["id"].(string), "", ""); code != 410 || st["reason"] != "expired" {
		t.Fatalf("expired: %d %v", code, st)
	}
	if s.pair.Answer(r["id"].(string), true) {
		t.Fatal("an expired request was allowed")
	}
}

func TestPairRequestLimits(t *testing.T) {
	s, url := dialogServer(t)
	for i := 0; i < maxOpenRequests; i++ {
		if code, r := do(t, "POST", url+"/pair/requests", "", `{"device":"x"}`); code != 202 {
			t.Fatalf("request %d: %d %v", i, code, r)
		}
	}
	code, r := do(t, "POST", url+"/pair/requests", "", `{"device":"x"}`)
	if code != 429 || r["reason"] != "rate_limited" || r["retry_after"] == nil {
		t.Fatalf("too many open requests: %d %v", code, r)
	}
	for _, p := range s.pair.Pending() {
		s.pair.Withdraw(p.ID)
	}
	for i := maxOpenRequests; i < rateMax; i++ {
		code, _ := do(t, "POST", url+"/pair/requests", "", `{"device":"x"}`)
		if code != 202 {
			t.Fatalf("request %d: %d", i, code)
		}
		for _, p := range s.pair.Pending() {
			s.pair.Withdraw(p.ID)
		}
	}
	if code, _ := do(t, "POST", url+"/pair/requests", "", `{"device":"x"}`); code != 429 {
		t.Fatalf("rate limit: %d", code)
	}
}

func TestLegacyPairWaits(t *testing.T) {
	s, url := dialogServer(t)
	go func() {
		for i := 0; i < 50; i++ {
			if p := s.pair.Pending(); len(p) > 0 {
				s.pair.Answer(p[0].ID, true)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	code, r := do(t, "POST", url+"/pair", "", `{"device":"old app"}`)
	if code != 200 || r["token"] == "" {
		t.Fatalf("legacy pair: %d %v", code, r)
	}
}

func TestAITokenIsReadOnly(t *testing.T) {
	s, ts := newTestServer(t)
	url := ts.URL
	_, conn := do(t, "POST", url+"/mcp/tokens", "master", `{"name":"Claude"}`)
	tok, _ := conn["token"].(string)
	if tok == "" {
		t.Fatalf("no AI token: %v", conn)
	}
	if code, r := do(t, "POST", url+"/ingest", tok, "A|2026-11-01T08:00:00+08:00|2026-11-01T08:00:00+08:00|1|count|p\n"); code != 403 || r["reason"] != "read_only" {
		t.Fatalf("AI token sent data: %d %v", code, r)
	}
	if code, _ := do(t, "POST", url+"/mcp/tokens", tok, `{}`); code != 403 {
		t.Fatalf("AI token made another token: %d", code)
	}
	if code, a := do(t, "GET", url+"/auth", tok, ""); code != 200 || a["scope"] != "mcp" {
		t.Fatalf("auth: %d %v", code, a)
	}
	if code, _ := do(t, "POST", url+"/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 200 {
		t.Fatalf("AI token can't use MCP: %d", code)
	}
	_ = s
}

func anyStrings(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestDashboardNeedsPasswordFromOtherComputers(t *testing.T) {
	s, _ := newTestServer(t)
	req := func(remote, host, path, pw string, hdr ...string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr, r.Host = remote, host
		if pw != "" {
			r.SetBasicAuth("admin", pw)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	get := func(remote, path, pw string) int { return req(remote, "192.168.1.2:8787", path, pw) }
	for _, host := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787", "localhost"} {
		if c := req("127.0.0.1:5000", host, "/api/overview", ""); c != 200 {
			t.Fatalf("this computer, at %s: %d", host, c)
		}
		if c := req("127.0.0.1:5000", host, "/api/token", ""); c != 200 {
			t.Fatalf("shared token on this computer, at %s: %d", host, c)
		}
	}
	// A page elsewhere that points its own name at 127.0.0.1 (DNS rebinding) is not this
	// computer's user, and neither is whoever comes through a reverse proxy running here.
	if c := req("127.0.0.1:5000", "evil.example:8787", "/api/overview", ""); c != 403 {
		t.Fatalf("DNS rebinding read the dashboard: %d", c)
	}
	if c := req("127.0.0.1:5000", "evil.example:8787", "/api/token", ""); c != 403 {
		t.Fatalf("DNS rebinding read the shared token: %d", c)
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "Via"} {
		if c := req("127.0.0.1:5000", "127.0.0.1:8787", "/api/overview", "", h, "203.0.113.9"); c != 403 {
			t.Fatalf("through a reverse proxy (%s), no password set: %d", h, c)
		}
	}
	if c := get("192.168.1.9:5000", "/api/overview", ""); c != 403 {
		t.Fatalf("LAN without a password set: %d", c)
	}
	s.cfg.AdminPassword = "pw"
	if c := get("192.168.1.9:5000", "/api/overview", ""); c != 401 {
		t.Fatalf("LAN, no password given: %d", c)
	}
	if c := get("192.168.1.9:5000", "/api/overview", "pw"); c != 200 {
		t.Fatalf("LAN with the password: %d", c)
	}
	if c := get("192.168.1.9:5000", "/api/token", "pw"); c != 403 {
		t.Fatalf("shared token shown to another computer: %d", c)
	}
	// with a password set, the proxy's visitors need it like everyone else
	if c := req("127.0.0.1:5000", "health.example", "/api/overview", "", "X-Forwarded-For", "203.0.113.9"); c != 401 {
		t.Fatalf("through a reverse proxy, no password given: %d", c)
	}
	if c := req("127.0.0.1:5000", "health.example", "/api/overview", "pw", "X-Forwarded-For", "203.0.113.9"); c != 200 {
		t.Fatalf("through a reverse proxy with the password: %d", c)
	}
	if c := req("127.0.0.1:5000", "health.example", "/api/token", "pw", "X-Forwarded-For", "203.0.113.9"); c != 403 {
		t.Fatalf("shared token shown through a reverse proxy: %d", c)
	}
	if c := get("8.8.8.8:5000", "/api/overview", "pw"); c != 403 {
		t.Fatalf("internet: %d", c)
	}
}
