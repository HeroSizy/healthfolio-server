package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// A report with two originals keeps both, keeps every field of report.json, and the
// dashboard can find each item in its original.
func TestReportKeepsOriginalsAndLocations(t *testing.T) {
	s, ts := newTestServer(t)
	pdf, png := []byte("%PDF-1.4 fake"), []byte("\x89PNG fake")
	body := `{"folder":"2026-11-01-Clinic","report":{"id":"R1","date":"2026-11-01","institution":"Clinic","title":"Annual",
		"originals":[{"name":"report.pdf","type":"application/pdf","size":13,"sha256":"` + sha(pdf) + `"},
		             {"name":"page2.png","type":"image/png","size":9,"sha256":"` + sha(png) + `"}],
		"futureField":{"kept":true},
		"items":[{"name":"Glucose","originalName":"空腹血糖","key":"glucose","value":"5.1","unit":"mmol/L","range":"3.9-6.1","flag":"",
		          "location":{"file":"report.pdf","page":0,"x":0.1,"y":0.4,"w":0.8,"h":0.03}},
		         {"name":"LDL","value":"3.9","unit":"mmol/L","flag":"high",
		          "location":{"file":"page2.png","page":0,"x":0.1,"y":0.2,"w":0.8,"h":0.04}},
		         {"name":"Old item","value":"1"}]},
		"files":[{"name":"report.pdf","data":"` + base64.StdEncoding.EncodeToString(pdf) + `"},
		         {"name":"page2.png","data":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`
	code, r := do(t, "POST", ts.URL+"/report", "master", body)
	if code != 200 || len(r["files"].([]any)) != 2 {
		t.Fatalf("report: %d %v", code, r)
	}
	folder := filepath.Join(s.cfg.DataDir, "reports", "2026-11-01-Clinic")
	for _, f := range []string{"report.pdf", "page2.png", "report.json"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Fatalf("%s not kept: %v", f, err)
		}
	}
	js, _ := os.ReadFile(filepath.Join(folder, "report.json"))
	for _, want := range []string{"futureField", "originalName", "location", "originals", "Annual"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("report.json lost %s", want)
		}
	}

	// the dashboard's view (from this computer, so no password)
	code, d := do(t, "GET", ts.URL+"/api/reports/R1", "", "")
	if code != 200 {
		t.Fatalf("detail: %d %v", code, d)
	}
	rep := d["report"].(map[string]any)
	items := rep["items"].([]any)
	first := items[0].(map[string]any)
	loc := first["location"].(map[string]any)
	if first["originalName"] != "空腹血糖" || loc["file"] != "report.pdf" || loc["y"].(float64) != 0.4 || items[2].(map[string]any)["location"] != nil {
		t.Fatalf("items: %v", items)
	}
	if len(rep["originals"].([]any)) != 2 || len(d["files"].([]any)) != 2 || rep["title"] != "Annual" {
		t.Fatalf("originals/files: %v", d)
	}
	resp, err := http.Get(ts.URL + "/api/reports/R1/files/page2.png")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != string(png) || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("file: %d %q %s", resp.StatusCode, got, resp.Header.Get("Content-Type"))
	}
	for _, bad := range []string{"report.json", "..%2Fhealth.db", "nope.pdf"} {
		if resp, _ := http.Get(ts.URL + "/api/reports/R1/files/" + bad); resp.StatusCode != 404 {
			t.Errorf("%s: %d", bad, resp.StatusCode)
		}
	}

	// a file that does not match its hash is refused
	badBody := strings.Replace(body, base64.StdEncoding.EncodeToString(png), base64.StdEncoding.EncodeToString([]byte("other")), 1)
	if code, _ := do(t, "POST", ts.URL+"/report", "master", badBody); code != 400 {
		t.Fatalf("mismatched file accepted: %d", code)
	}
}
