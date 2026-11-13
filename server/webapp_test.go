package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func rawGet(t *testing.T, url string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	// no transparent gunzip: the test looks at what the server sent
	res, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

// The plain HTML dashboard is always at /lite/; / is the WebAssembly one when the build
// has it (scripts/build-web.sh), else the plain one too.
func TestDashboardRoutes(t *testing.T) {
	_, ts := newTestServer(t)

	res, body := rawGet(t, ts.URL+"/lite/")
	if res.StatusCode != 200 || !strings.Contains(string(body), `<script src="app.js">`) {
		t.Fatalf("/lite/: %d %.200s", res.StatusCode, body)
	}
	if res, _ := rawGet(t, ts.URL+"/lite/style.css"); res.StatusCode != 200 {
		t.Fatalf("/lite/style.css: %d", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "'wasm-unsafe-eval'") || strings.Contains(csp, "'unsafe-eval'") {
		t.Fatalf("CSP: %s", csp)
	}
	// other computers still need the password, for the app as for the rest
	req, _ := http.NewRequest("GET", ts.URL+"/app.wasm", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.50")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 403 {
		t.Fatalf("app.wasm through a proxy without a password: %v %v", r.StatusCode, err)
	}

	res, body = rawGet(t, ts.URL+"/")
	if !hasWebApp() {
		if !strings.Contains(string(body), `<script src="app.js">`) {
			t.Fatalf("/ without the web app should be the lite dashboard: %.200s", body)
		}
		t.Log("no server/webapp build: only the fallback was checked (run scripts/build-web.sh)")
		return
	}
	if !strings.Contains(string(body), `src="boot.js"`) || res.Header.Get("ETag") == "" {
		t.Fatalf("/: %.300s", body)
	}

	// the app goes gzipped to browsers, is 304 once they have it, and plain to the rest
	res, gz := rawGet(t, ts.URL+"/app.wasm", "Accept-Encoding", "gzip, deflate")
	if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Content-Type") != "application/wasm" {
		t.Fatalf("app.wasm: %d %v", res.StatusCode, res.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	if !bytes.HasPrefix(raw, []byte("\x00asm")) {
		t.Fatalf("app.wasm is not WebAssembly: % x", raw[:8])
	}
	if r2, _ := rawGet(t, ts.URL+"/app.wasm", "Accept-Encoding", "gzip", "If-None-Match", res.Header.Get("ETag")); r2.StatusCode != 304 {
		t.Fatalf("app.wasm again: %d", r2.StatusCode)
	}
	if r3, plain := rawGet(t, ts.URL+"/app.wasm"); r3.Header.Get("Content-Encoding") != "" || !bytes.Equal(plain, raw) {
		t.Fatalf("app.wasm without gzip: %v, %d bytes", r3.Header, len(plain))
	}
	if r4, js := rawGet(t, ts.URL+"/wasm_exec.js"); r4.StatusCode != 200 || !strings.Contains(string(js), "Go") {
		t.Fatalf("wasm_exec.js: %d", r4.StatusCode)
	}
	// the lite dashboard's files still load from / (the app page uses its icon)
	if r5, _ := rawGet(t, ts.URL+"/icon.svg"); r5.StatusCode != 200 {
		t.Fatalf("icon.svg: %d", r5.StatusCode)
	}
}
