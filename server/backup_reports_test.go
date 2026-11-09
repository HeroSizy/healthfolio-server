package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func get(t *testing.T, url, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// A paired phone can read back every report it sent, to restore it from the backup.
func TestBackupReportsReadBack(t *testing.T) {
	_, ts := newTestServer(t)
	url := ts.URL
	pdf := []byte("%PDF-1.4 fake report")
	body := `{"folder":"2026-11-01-Clinic","report":{"id":"R1","date":"2026-11-01","institution":"Clinic",
		"originals":[{"name":"report.pdf","sha256":"` + sha(pdf) + `"}],"futureField":42,
		"items":[{"name":"Glucose","value":"5.1"}]},
		"files":[{"name":"report.pdf","data":"` + base64.StdEncoding.EncodeToString(pdf) + `"}]}`
	if code, r := do(t, "POST", url+"/report", "master", body); code != 200 {
		t.Fatalf("report: %d %v", code, r)
	}

	if code, _ := get(t, url+"/backup/reports", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	code, r := do(t, "GET", url+"/backup/reports", "master", "")
	reps, _ := r["reports"].([]any)
	if code != 200 || len(reps) != 1 {
		t.Fatalf("list: %d %v", code, r)
	}
	rep := reps[0].(map[string]any)
	files := rep["files"].([]any)
	f := files[0].(map[string]any)
	if rep["id"] != "R1" || rep["folder"] != "2026-11-01-Clinic" || rep["date"] != "2026-11-01" ||
		len(files) != 1 || f["name"] != "report.pdf" || f["size"].(float64) != float64(len(pdf)) || f["sha256"] != sha(pdf) {
		t.Fatalf("list entry: %v", rep)
	}

	// an AI assistant's read-only token may read too
	_, conn := do(t, "POST", url+"/mcp/tokens", "master", `{"name":"Claude"}`)
	ai, _ := conn["token"].(string)
	if code, b := get(t, url+"/backup/reports/R1", ai); code != 200 || !strings.Contains(string(b), "futureField") {
		t.Fatalf("report.json: %d %s", code, b)
	}
	if code, b := get(t, url+"/backup/reports/R1/files/report.pdf", "master"); code != 200 || string(b) != string(pdf) {
		t.Fatalf("file: %d %q", code, b)
	}
	for _, bad := range []string{"/backup/reports/nope", "/backup/reports/R1/files/report.json",
		"/backup/reports/R1/files/..%2Fhealth.db", "/backup/reports/R1/files/missing.pdf"} {
		if code, _ := get(t, url+bad, "master"); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code, _ := get(t, url+"/backup/reports/R1/files/report.pdf", "wrong"); code != 401 {
		t.Errorf("bad token read a file: %d", code)
	}
}
