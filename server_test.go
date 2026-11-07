package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBody(t *testing.T) {
	cases := map[string]string{
		"pipe":  "HKQuantityTypeIdentifierStepCount|2026-11-01T08:00:00+08:00|2026-11-01T08:10:00+08:00|512|count|My iPhone|x\n",
		"json":  `[{"type":"HKQuantityTypeIdentifierStepCount","start":"2026-11-01T08:00:00+08:00","end":"e","value":512,"unit":"count","source":"My iPhone|x"}]`,
		"obj":   `{"records":[{"type":"HKQuantityTypeIdentifierStepCount","start":"2026-11-01T08:00:00+08:00","end":"e","value":"512","unit":"count","source":"My iPhone|x"}]}`,
		"jsonl": "{\"type\":\"HKQuantityTypeIdentifierStepCount\",\"start\":\"2026-11-01T08:00:00+08:00\",\"value\":512,\"source\":\"My iPhone|x\"}\n{\"type\":\"B\",\"start\":\"s\"}",
		"csv":   "type,start,end,value,unit,source\nHKQuantityTypeIdentifierStepCount,2026-11-01T08:00:00+08:00,e,512,count,My iPhone|x\n",
	}
	for name, body := range cases {
		rows, err := ParseBody([]byte(body))
		if err != nil || len(rows) == 0 {
			t.Fatalf("%s: %v %v", name, rows, err)
		}
		r := rows[0]
		if r.Type != "HKQuantityTypeIdentifierStepCount" || r.Value != "512" || r.Source != "My iPhone|x" {
			t.Errorf("%s: got %+v", name, r)
		}
	}
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	dir := t.TempDir()
	cfg := &Config{DataDir: dir, Name: "test", PublicURL: "http://192.168.1.2:8787", DashboardLAN: true, NoDialog: true, CSVDir: filepath.Join(dir, "csv")}
	db, err := OpenDB(filepath.Join(dir, "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewServer(cfg, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, method, url, token, body string, hdr ...string) (int, map[string]any) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestFlow(t *testing.T) {
	s, ts := newTestServer(t)

	code, h := do(t, "GET", ts.URL+"/health", "", "")
	if code != 200 || h["version"].(float64) != 2 {
		t.Fatalf("health: %d %v", code, h)
	}

	body := "A|2026-11-01T08:00:00+08:00|2026-11-01T08:10:00+08:00|5|count|phone\nB|2026-11-01T09:00:00+08:00|2026-11-01T09:00:00+08:00|60|count/min|watch\n"
	if code, _ := do(t, "POST", ts.URL+"/ingest", "wrong", body); code != 401 {
		t.Fatalf("bad token accepted: %d", code)
	}
	code, r := do(t, "POST", ts.URL+"/ingest", "master", body)
	if code != 200 || r["received"].(float64) != 2 || r["added"].(map[string]any)["sqlite"].(float64) != 2 {
		t.Fatalf("ingest: %d %v", code, r)
	}
	_, r = do(t, "POST", ts.URL+"/ingest", "master", body)
	if r["added"].(map[string]any)["sqlite"].(float64) != 0 {
		t.Fatalf("duplicates not ignored: %v", r)
	}

	// pairing with a code issues a per-device token
	if code, r := do(t, "POST", ts.URL+"/pair", "", `{"device":"iPhone"}`); code != 428 || r["needs_code"] != true {
		t.Fatalf("pair without code: %d %v", code, r)
	}
	if code, _ := do(t, "POST", ts.URL+"/api/pair", "", ""); code != 403 {
		t.Fatalf("dashboard POST without header should be refused, got %d", code)
	}
	code, p := do(t, "POST", ts.URL+"/api/pair", "", "", "X-HealthSync", "1")
	active := p["active"].(map[string]any)
	if code != 200 || !strings.HasPrefix(active["link"].(string), "healthsync://pair?") || !strings.Contains(active["qr"].(string), "<svg") {
		t.Fatalf("new pair code: %d %v", code, p)
	}
	if code, _ := do(t, "POST", ts.URL+"/pair", "", `{"device":"iPhone","code":"000000x"}`); code != 403 {
		t.Fatalf("wrong code accepted")
	}
	code, pr := do(t, "POST", ts.URL+"/pair", "", `{"device":"My iPhone","code":"`+active["code"].(string)+`"}`)
	if code != 200 || pr["token"] == nil {
		t.Fatalf("pair: %d %v", code, pr)
	}
	devTok := pr["token"].(string)
	if code, _ := do(t, "POST", ts.URL+"/pair", "", `{"code":"`+active["code"].(string)+`"}`); code != 403 {
		t.Fatalf("code reusable")
	}
	if code, st := do(t, "GET", ts.URL+"/stats", devTok, ""); code != 200 || st["total"].(float64) != 2 {
		t.Fatalf("stats with device token: %d %v", code, st)
	}

	// report
	rep := `{"report":{"id":"r1","date":"2026-10-01","institution":"Clinic","items":[{"name":"LDL","value":3.1,"unit":"mmol/L","flag":"high"}]},"files":[{"name":"../evil.pdf","data":"aGk="}]}`
	if code, r := do(t, "POST", ts.URL+"/report", devTok, rep); code != 200 {
		t.Fatalf("report: %d %v", code, r)
	}
	if _, err := filepathGlob(s.cfg.DataDir, "reports/r1/evil.pdf"); err != nil {
		t.Fatal(err)
	}

	// revoke
	devs, _ := s.db.Devices()
	if len(devs) != 1 || devs[0].Name != "My iPhone" {
		t.Fatalf("devices: %v", devs)
	}
	_ = s.db.RevokeDevice(devs[0].ID)
	if code, _ := do(t, "GET", ts.URL+"/stats", devTok, ""); code != 401 {
		t.Fatalf("revoked token still works")
	}

	logs, _ := s.db.Logs(100, 0, "")
	kinds := map[string]int{}
	for _, l := range logs {
		kinds[l.Kind]++
	}
	if kinds["ingest"] != 2 || kinds["auth"] < 1 || kinds["pair"] < 2 || kinds["report"] != 1 {
		t.Fatalf("log kinds: %v", kinds)
	}
	if code, o := do(t, "GET", ts.URL+"/api/overview", "", ""); code != 200 || o["stats"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("overview: %d %v", code, o)
	}
}

func filepathGlob(dir, rel string) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(dir, rel))
	if err == nil && len(m) == 0 {
		err = &globErr{rel}
	}
	return m, err
}

type globErr struct{ p string }

func (e *globErr) Error() string { return "missing " + e.p }
