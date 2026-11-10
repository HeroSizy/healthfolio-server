package server

import (
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countItems(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM checkup WHERE report_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A report can print the same name twice (white blood cells in the blood count and in the
// urine test): both items are kept, in the order they were sent.
func TestReportKeepsItemsWithTheSameName(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rep := Report{ID: "R1", Date: "2026-11-01", Institution: "Clinic", Items: []ReportItem{
		{Name: "白细胞", Key: "wbc", Value: "6.1", Unit: "10^9/L"}, {Name: "Glucose", Value: "5.1"}, {Name: "白细胞", Key: "other", Value: "阴性"}}}
	for i := 0; i < 2; i++ { // sent twice: still three, not six
		if n, err := db.InsertReport(rep, "2026-11-01-Clinic", "/tmp/a"); err != nil || n != 3 {
			t.Fatalf("insert: %d %v", n, err)
		}
	}
	got, _, err := db.ReportDetail("R1")
	if err != nil || len(got.Items) != 3 || got.Items[0].Value != "6.1" || got.Items[2].Value != "阴性" {
		t.Fatalf("items: %+v %v", got.Items, err)
	}
	reps, _ := db.Reports()
	if len(reps) != 1 || reps[0].Items != 3 {
		t.Fatalf("reports: %+v", reps)
	}
}

// oldSchema is what versions up to 0.0.12 created, with the items of two reports as they
// stored them: the second item with a repeated name had replaced the first.
const oldSchema = `
CREATE TABLE records (type TEXT NOT NULL, start TEXT NOT NULL, "end" TEXT NOT NULL, value TEXT NOT NULL, num REAL, unit TEXT,
    source TEXT NOT NULL DEFAULT '', received TEXT NOT NULL, UNIQUE (type, start, "end", source, value));
CREATE TABLE checkup (
    report_id TEXT NOT NULL, date TEXT NOT NULL, institution TEXT, name TEXT NOT NULL,
    key TEXT, value TEXT, num REAL, unit TEXT, range TEXT, flag TEXT, original_name TEXT, loc_file TEXT, loc_page INTEGER,
    loc_x REAL, loc_y REAL, loc_w REAL, loc_h REAL,
    UNIQUE (report_id, name));
CREATE TABLE reports (id TEXT PRIMARY KEY, date TEXT, institution TEXT, items INTEGER, folder TEXT, received TEXT NOT NULL, title TEXT, originals TEXT);
CREATE VIEW my_flagged AS SELECT report_id, name, value FROM checkup WHERE flag = 'high';
INSERT INTO checkup (report_id, date, institution, name, key, value, flag, loc_file, loc_page) VALUES
    ('R1', '2026-11-01', 'Clinic', 'Glucose', 'glucose', '7.9', 'high', 'report.pdf', 2),
    ('R1', '2026-11-01', 'Clinic', '白细胞', 'other', '阴性', '', NULL, NULL),
    ('R2', '2025-11-01', 'Clinic', 'Glucose', 'glucose', '5.0', '', NULL, NULL);
`

// A database from an older version loses the rule that dropped items, keeps what it has
// (and a view of the user's own that reads checkup), and gets the lost items back from
// report.json.
func TestOldCheckupTableIsRebuiltAndItemsRestored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.db")
	folder := filepath.Join(dir, "reports", "2026-11-01-Clinic")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	reportJSON := `{"id":"R1","date":"2026-11-01","institution":"Clinic","items":[
		{"name":"白细胞","key":"wbc","value":"6.1","unit":"10^9/L"},
		{"name":"Glucose","key":"glucose","value":7.9,"flag":"high","location":{"file":"report.pdf","page":2,"x":0.1,"y":0.2,"w":0.3,"h":0.04}},
		{"name":"白细胞","key":"other","value":"阴性"}]}`
	if err := os.WriteFile(filepath.Join(folder, "report.json"), []byte(reportJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	// R1 arrived with 3 items and its folder is known; R2's folder is gone
	if _, err := old.Exec(`INSERT INTO reports (id, date, institution, items, folder, received) VALUES
		('R1', '2026-11-01', 'Clinic', 3, ?, '2026-11-02T08:00:00+08:00'), ('R2', '2025-11-01', 'Clinic', 2, '/nowhere/2025', '2025-11-02T08:00:00+08:00')`, folder); err != nil {
		t.Fatal(err)
	}
	old.Close()

	for i := 0; i < 2; i++ { // opening it again changes nothing more
		db, err := OpenDB(path)
		if err != nil {
			t.Fatal(err)
		}
		var def string
		db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'checkup'`).Scan(&def)
		if def == "" || strings.Contains(strings.ToLower(def), "unique") {
			t.Fatalf("checkup still has the rule: %s", def)
		}
		if n := countItems(t, db.sql, "R1"); n != 3 {
			t.Fatalf("R1 has %d items, want 3", n)
		}
		if n := countItems(t, db.sql, "R2"); n != 1 {
			t.Fatalf("R2 (no report.json) has %d items, want the 1 it had", n)
		}
		rep, _, err := db.ReportDetail("R1")
		if err != nil || rep.Items[0].Key != "wbc" || rep.Items[1].Location == nil || rep.Items[1].Location.Page != 2 || rep.Items[2].Value != "阴性" {
			t.Fatalf("R1: %+v %v", rep.Items, err)
		}
		var received string
		db.sql.QueryRow(`SELECT received FROM reports WHERE id = 'R1'`).Scan(&received)
		if received != "2026-11-02T08:00:00+08:00" {
			t.Fatalf("received changed: %s", received)
		}
		var flagged int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM my_flagged`).Scan(&flagged); err != nil || flagged != 1 {
			t.Fatalf("the user's view: %d %v", flagged, err)
		}
		if _, err := db.InsertReport(Report{ID: "R3", Date: "2026-11-03", Items: []ReportItem{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}}}, "r3", ""); err != nil {
			t.Fatal(err)
		}
		if n := countItems(t, db.sql, "R3"); n != 2 {
			t.Fatalf("R3 has %d items, want 2", n)
		}
		db.Close()
	}
}

// After the data folder is moved (the folder kept in the database no longer exists), the
// originals are found in reports/ next to the database.
func TestReportsFoundAfterTheDataFolderMoved(t *testing.T) {
	s, ts := newTestServer(t)
	pdf := []byte("%PDF-1.4 fake report")
	body := `{"folder":"2026-11-01-Clinic","report":{"id":"R1","date":"2026-11-01","institution":"Clinic","items":[{"name":"Glucose","value":"5.1"}]},
		"files":[{"name":"report.pdf","data":"` + base64.StdEncoding.EncodeToString(pdf) + `"},
		{"name":"..","data":"eA=="},{"name":".hidden","data":"eA=="},{"name":"a/../../escape.txt","data":"eA=="}]}`
	if code, r := do(t, "POST", ts.URL+"/report", "master", body); code != 200 {
		t.Fatalf("report: %d %v", code, r)
	}
	// names that are not plain file names are not written anywhere they should not be
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "escape.txt")); err == nil {
		t.Fatal("a file escaped the report's folder")
	}
	if left, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, ".tmp-*")); len(left) > 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	if names := originalFiles(filepath.Join(s.cfg.DataDir, "reports", "2026-11-01-Clinic")); len(names) != 2 {
		t.Fatalf("originals: %v, want report.pdf and escape.txt", names)
	}
	// as if the data folder had been somewhere else when the report arrived
	if _, err := s.db.sql.Exec(`UPDATE reports SET folder = '/Volumes/Old Disk/HealthSync/reports/2026-11-01-Clinic'`); err != nil {
		t.Fatal(err)
	}
	if code, b := get(t, ts.URL+"/backup/reports/R1/files/report.pdf", "master"); code != 200 || string(b) != string(pdf) {
		t.Fatalf("file after the move: %d %q", code, b)
	}
	code, r := do(t, "GET", ts.URL+"/backup/reports", "master", "")
	reps, _ := r["reports"].([]any)
	if code != 200 || len(reps) != 1 || len(reps[0].(map[string]any)["files"].([]any)) != 2 {
		t.Fatalf("list after the move: %d %v", code, r)
	}
	rows, _ := s.db.Reports()
	if len(rows) != 1 || rows[0].Files != 2 {
		t.Fatalf("dashboard list after the move: %+v", rows)
	}
	out, err := s.mcp.call("get_report_file", map[string]any{"report_id": "R1", "name": "report.pdf"})
	if c, ok := out.(mcpContent); err != nil || !ok || len(c) != 1 {
		t.Fatalf("MCP file after the move: %v %v", out, err)
	}
}
