package server

import (
	"path/filepath"
	"testing"
)

// The same report sent twice (two devices import the same file, so the app gives it the
// same id) is stored once, and items dropped in the second copy don't linger.
func TestReportSentTwiceIsStoredOnce(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := Report{ID: "5d41402a-bc4b-5a76-8000-000000000001", Date: "2022-12-13", Institution: "Clinic",
		Items: []ReportItem{{Name: "Glucose", Value: "5.1"}, {Name: "Typo name", Value: "1"}}}
	second := Report{ID: first.ID, Date: "2022-12-13", Institution: "Clinic",
		Items: []ReportItem{{Name: "Glucose", Value: "5.1"}, {Name: "Fixed name", Value: "1"}}}
	if _, err := db.InsertReport(first, "2022-12-13-Clinic", "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertReport(second, "2022-12-13-Clinic", "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	var reports, items, stale int
	db.sql.QueryRow(`SELECT COUNT(*) FROM reports`).Scan(&reports)
	db.sql.QueryRow(`SELECT COUNT(*) FROM checkup WHERE report_id = ?`, first.ID).Scan(&items)
	db.sql.QueryRow(`SELECT COUNT(*) FROM checkup WHERE name = 'Typo name'`).Scan(&stale)
	if reports != 1 || items != 2 || stale != 0 {
		t.Fatalf("reports=%d items=%d stale=%d, want 1, 2, 0", reports, items, stale)
	}
}
