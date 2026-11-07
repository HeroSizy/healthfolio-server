package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The app's JSON Lines rows (HealthRecord.jsonLine), and a deletion.
const (
	jsonlHR      = `{"device_model":"Watch7,5","device_software":"11.1","end":"2026-11-01T08:00:00+08:00","metadata":{"HKMetadataKeyHeartRateMotionContext":"1"},"source":"Zen's Apple Watch","source_bundle":"com.apple.health.ABC","source_version":"11.1","start":"2026-11-01T08:00:00+08:00","type":"HeartRate","tz":"Asia/Shanghai","unit":"count/min","uuid":"U-HR","value":61}`
	jsonlSteps   = `{"end":"2026-11-01T08:10:00+08:00","source":"iPhone","start":"2026-11-01T08:00:00+08:00","type":"StepCount","unit":"count","user_entered":true,"uuid":"U-STEPS","value":512}`
	jsonlWorkout = `{"end":"2026-11-01T19:30:00+08:00","metadata":{"active_energy_kcal":"320.5","distance_km":"5.2","duration_s":"1800"},"source":"Watch","start":"2026-11-01T19:00:00+08:00","type":"Workout","unit":"","uuid":"U-RUN","value":"Running"}`
)

func TestParseJSONLinesFull(t *testing.T) {
	rows, err := ParseBody([]byte(jsonlHR + "\n" + jsonlSteps + "\n" + `{"uuid":"U-OLD","deleted":true}` + "\n"))
	if err != nil || len(rows) != 3 {
		t.Fatalf("%v %v", rows, err)
	}
	hr := rows[0]
	if hr.UUID != "U-HR" || hr.DeviceModel != "Watch7,5" || hr.SourceBundle != "com.apple.health.ABC" || hr.TZ != "Asia/Shanghai" ||
		hr.Value != "61" || hr.Metadata != `{"HKMetadataKeyHeartRateMotionContext":"1"}` {
		t.Errorf("heart rate: %+v", hr)
	}
	if !rows[1].UserEntered {
		t.Errorf("user_entered lost: %+v", rows[1])
	}
	if d := rows[2]; !d.Deleted || d.UUID != "U-OLD" {
		t.Errorf("deletion: %+v", d)
	}

	rows, _ = ParseBody([]byte(`{"records":[` + jsonlSteps + `],"deleted":["U-A","U-B"]}`))
	if len(rows) != 3 || rows[0].UUID != "U-STEPS" || !rows[2].Deleted || rows[2].UUID != "U-B" {
		t.Errorf("records + deleted: %+v", rows)
	}

	// the app's CSV export
	rows, _ = ParseBody([]byte("uuid,type,start,end,value,unit,source,device,user_entered,timezone\nU1,StepCount,s,e,5,count,iPhone,iPhone15,2,Asia/Shanghai\n"))
	if len(rows) != 1 || rows[0].UUID != "U1" || rows[0].Type != "StepCount" || rows[0].DeviceModel != "iPhone15" || rows[0].TZ != "Asia/Shanghai" {
		t.Errorf("csv: %+v", rows)
	}
}

func TestRecordsMigrationUpgradeAndDelete(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "health.db")

	// a database as receiver.py left it, with one pipe-format row
	old, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE records (type TEXT NOT NULL, start TEXT NOT NULL, "end" TEXT NOT NULL, value TEXT NOT NULL, num REAL, unit TEXT, source TEXT NOT NULL DEFAULT '', received TEXT NOT NULL, UNIQUE (type, start, "end", source, value))`,
		`CREATE VIEW daily AS SELECT type, substr(start, 1, 10) AS day, unit, SUM(num) AS total, AVG(num) AS mean, MIN(num) AS min, MAX(num) AS max, COUNT(*) AS n FROM records GROUP BY type, day, unit`,
		`INSERT INTO records VALUES ('StepCount', '2026-11-01T08:00:00+08:00', '2026-11-01T08:10:00+08:00', '512', 512, 'count', 'iPhone', 'x')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	db, err := OpenDB(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// the same steps again, now with a uuid: no new row, the old one gets the uuid
	rows, _ := ParseBody([]byte(jsonlSteps + "\n" + jsonlHR + "\n" + jsonlWorkout))
	added, deleted, err := db.InsertRecords(rows)
	if err != nil || added != 2 || deleted != 0 {
		t.Fatalf("insert: added %d deleted %d %v", added, deleted, err)
	}
	var n int
	var uuid, tz sql.NullString
	var userEntered sql.NullBool
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM records WHERE type = 'StepCount'`).Scan(&n)
	_ = db.sql.QueryRow(`SELECT uuid, user_entered FROM records WHERE type = 'StepCount'`).Scan(&uuid, &userEntered)
	if n != 1 || uuid.String != "U-STEPS" || !userEntered.Bool {
		t.Fatalf("pipe row not upgraded: n=%d uuid=%v user_entered=%v", n, uuid, userEntered)
	}
	_ = db.sql.QueryRow(`SELECT tz FROM records WHERE uuid = 'U-HR'`).Scan(&tz)
	if tz.String != "Asia/Shanghai" {
		t.Errorf("tz = %v", tz)
	}
	var dist float64
	if err := db.sql.QueryRow(`SELECT distance_km FROM v_workouts WHERE uuid = 'U-RUN'`).Scan(&dist); err != nil || dist != 5.2 {
		t.Errorf("v_workouts: %v %v", dist, err)
	}

	// sending them again changes nothing
	if added, _, _ := db.InsertRecords(rows); added != 0 {
		t.Errorf("duplicates added: %d", added)
	}

	// deleting the heart rate sample hides it from stats, the daily view and v_*
	rows, _ = ParseBody([]byte(`{"uuid":"U-HR","deleted":true}` + "\n" + `{"uuid":"U-NOPE","deleted":true}`))
	if _, deleted, err := db.InsertRecords(rows); err != nil || deleted != 1 {
		t.Fatalf("delete: %d %v", deleted, err)
	}
	st, _ := db.Stats()
	for _, ts := range st.Types {
		if ts.Type == "HeartRate" {
			t.Errorf("deleted sample still in stats: %+v", st)
		}
	}
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM v_heart_rate`).Scan(&n)
	if n != 0 {
		t.Errorf("deleted sample still in v_heart_rate")
	}
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM daily WHERE type = 'HeartRate'`).Scan(&n)
	if n != 0 {
		t.Errorf("deleted sample still in daily")
	}
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM records WHERE uuid = 'U-HR' AND deleted_at IS NOT NULL`).Scan(&n)
	if n != 1 {
		t.Errorf("deleted row should stay, marked")
	}
}

func TestIngestJSONLines(t *testing.T) {
	s, ts := newTestServer(t)
	if _, h := do(t, "GET", ts.URL+"/health", "", ""); h["records"] != float64(recordsVersion) {
		t.Fatalf("/health records: %v", h)
	}
	csv, err := NewCSVOutput(s.cfg.CSVDir)
	if err != nil {
		t.Fatal(err)
	}
	s.csv = csv

	code, r := do(t, "POST", ts.URL+"/ingest", "master", jsonlHR+"\n"+jsonlSteps+"\n")
	if code != 200 || r["added"].(map[string]any)["sqlite"] != float64(2) {
		t.Fatalf("ingest: %d %v", code, r)
	}
	code, r = do(t, "POST", ts.URL+"/ingest", "master", `{"uuid":"U-HR","deleted":true}`+"\n")
	if code != 200 || r["deleted"] != float64(1) || r["received"] != float64(1) {
		t.Fatalf("delete: %d %v", code, r)
	}
	b, _ := os.ReadFile(filepath.Join(s.cfg.CSVDir, "2026-11-01.csv"))
	if lines := strings.Count(string(b), "\n"); lines != 3 { // header + 2 samples, no deletion row
		t.Errorf("csv:\n%s", b)
	}
	logs, _ := s.db.Logs(1, 0, "ingest")
	if len(logs) != 1 || logs[0].Message != "1 deleted" {
		t.Errorf("log: %+v", logs)
	}
	if code, st := do(t, "GET", ts.URL+"/stats", "master", ""); code != 200 || st["total"] != float64(1) {
		t.Errorf("stats: %v", st)
	}
}
