package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/herosizy/healthsync-server/model"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database: records, checkup, reports, devices and sync_log, plus the
// daily and v_* views.
type DB struct {
	sql   *sql.DB
	path  string
	mu    sync.Mutex // serialises writes
	onLog func(LogEntry)
	// compacted is closed when the background compaction after opening is done
	compacted chan struct{}
}

const schema = `
CREATE TABLE IF NOT EXISTS records (
    type TEXT NOT NULL, start TEXT NOT NULL, "end" TEXT NOT NULL,
    value TEXT NOT NULL, num REAL, unit TEXT, source TEXT NOT NULL DEFAULT '',
    received TEXT NOT NULL,
    UNIQUE (type, start, "end", source, value));
CREATE TABLE IF NOT EXISTS checkup (
    report_id TEXT NOT NULL, date TEXT NOT NULL, institution TEXT, name TEXT NOT NULL,
    key TEXT, value TEXT, num REAL, unit TEXT, range TEXT, flag TEXT,
    UNIQUE (report_id, name));

-- added by healthsync-server
CREATE TABLE IF NOT EXISTS sync_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    time TEXT NOT NULL, kind TEXT NOT NULL, device TEXT, ip TEXT,
    received INTEGER, added INTEGER, bytes INTEGER, duration_ms INTEGER,
    ok INTEGER NOT NULL, message TEXT, types TEXT);
CREATE INDEX IF NOT EXISTS sync_log_time ON sync_log(time);
CREATE TABLE IF NOT EXISTS devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
    created TEXT NOT NULL, last_seen TEXT, last_ip TEXT, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS reports (
    id TEXT PRIMARY KEY, date TEXT, institution TEXT, items INTEGER, folder TEXT, received TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS prefs (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// MCPEnabled reports whether AI assistants may connect. It is on unless turned off in the
// app or the dashboard.
func (d *DB) MCPEnabled() bool { return mcpEnabled(d.sql) }

func mcpEnabled(db *sql.DB) bool {
	var v string
	_ = db.QueryRow(`SELECT value FROM prefs WHERE key = 'mcp_disabled'`).Scan(&v)
	return v != "1"
}

// SetMCPEnabled turns MCP on or off at once, for HTTP and stdio.
func (d *DB) SetMCPEnabled(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := "1"
	if on {
		v = "0"
	}
	_, err := d.sql.Exec(`INSERT INTO prefs (key, value) VALUES ('mcp_disabled', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v)
	return err
}

func OpenDB(path string) (*DB, error) {
	// WAL + busy timeout so other tools (sqlite3, Datasette, scripts) can read while we write.
	// journal_size_limit: after a checkpoint the WAL file shrinks back to 16 MB instead of
	// staying as big as the largest sync ever made it.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)&_pragma=journal_size_limit(16777216)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	d := &DB{sql: db, path: path, compacted: make(chan struct{})}
	go func() {
		defer close(d.compacted)
		d.compact()
	}()
	return d, nil
}

// compact gives back the room of deleted rows and dropped indexes when there is a lot of
// it, and trims the WAL. It runs in the background once after opening: VACUUM rewrites the
// whole file, and syncs meanwhile wait for it (busy_timeout).
func (d *DB) compact() {
	_, _ = d.sql.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	var free, size int64
	if d.sql.QueryRow(`PRAGMA freelist_count`).Scan(&free) != nil || d.sql.QueryRow(`PRAGMA page_size`).Scan(&size) != nil {
		return
	}
	if free*size < 32<<20 {
		return
	}
	start := time.Now()
	d.mu.Lock()
	_, err := d.sql.Exec(`VACUUM`)
	d.mu.Unlock()
	if err != nil {
		logf("compacting the database: %v", err)
		return
	}
	_, _ = d.sql.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	logf("compacted the database: gave back %d MB in %s", free*size>>20, time.Since(start).Round(time.Millisecond))
}

// recordColumns are the columns added to the base records table for the app's full
// records (the same fields as the app's own database destinations).
var recordColumns = [][2]string{
	{"uuid", "TEXT"}, {"source_bundle", "TEXT"}, {"source_version", "TEXT"},
	{"device_model", "TEXT"}, {"device_software", "TEXT"}, {"user_entered", "INTEGER"},
	{"tz", "TEXT"}, {"metadata", "TEXT"}, {"deleted_at", "TEXT"},
}

// views skip deleted samples. v_* match the views the app creates in D1 / Supabase.
const views = `
DROP VIEW IF EXISTS daily;
CREATE VIEW daily AS
    SELECT type, substr(start, 1, 10) AS day, unit,
           SUM(num) AS total, AVG(num) AS mean, MIN(num) AS min, MAX(num) AS max, COUNT(*) AS n
    FROM records WHERE deleted_at IS NULL GROUP BY type, day, unit;
CREATE VIEW IF NOT EXISTS v_heart_rate AS SELECT uuid, start AS start_at, "end" AS end_at, num AS bpm, source, device_model FROM records WHERE type = 'HeartRate' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_resting_heart_rate AS SELECT uuid, start AS start_at, "end" AS end_at, num AS bpm, source, device_model FROM records WHERE type = 'RestingHeartRate' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_hrv AS SELECT uuid, start AS start_at, "end" AS end_at, num AS ms, source, device_model FROM records WHERE type = 'HeartRateVariabilitySDNN' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_steps AS SELECT uuid, start AS start_at, "end" AS end_at, num AS steps, source, device_model FROM records WHERE type = 'StepCount' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_weight AS SELECT uuid, start AS start_at, "end" AS end_at, num AS kg, source, device_model FROM records WHERE type = 'BodyMass' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_spo2 AS SELECT uuid, start AS start_at, "end" AS end_at, num AS percent, source, device_model FROM records WHERE type = 'OxygenSaturation' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_sleep AS SELECT uuid, start AS start_at, "end" AS end_at, value AS stage, source FROM records WHERE type = 'SleepAnalysis' AND deleted_at IS NULL;
CREATE VIEW IF NOT EXISTS v_workouts AS
    SELECT uuid, value AS activity, start AS start_at, "end" AS end_at,
           CAST(json_extract(metadata, '$.duration_s') AS REAL) AS duration_s,
           CAST(json_extract(metadata, '$.distance_km') AS REAL) AS distance_km,
           CAST(json_extract(metadata, '$.active_energy_kcal') AS REAL) AS active_energy_kcal,
           CAST(json_extract(metadata, '$.avg_heart_rate') AS REAL) AS avg_heart_rate,
           source, metadata
    FROM records WHERE type = 'Workout' AND deleted_at IS NULL;
`

// migrate adds the newer records columns to a database an older version created, then
// (re)creates the views.
func migrate(db *sql.DB) error {
	if err := addColumns(db, "records", recordColumns); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS records_uuid ON records(uuid) WHERE uuid IS NOT NULL`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS records_start ON records(start)`); err != nil {
		return err
	}
	// checkups: the name as printed and where each item is in the original files; the
	// report's title and its list of original files (JSON)
	if err := addColumns(db, "checkup", [][2]string{{"original_name", "TEXT"}, {"loc_file", "TEXT"}, {"loc_page", "INTEGER"},
		{"loc_x", "REAL"}, {"loc_y", "REAL"}, {"loc_w", "REAL"}, {"loc_h", "REAL"}}); err != nil {
		return err
	}
	if err := addColumns(db, "reports", [][2]string{{"title", "TEXT"}, {"originals", "TEXT"}}); err != nil {
		return err
	}
	// records_type_start repeated the start of the UNIQUE (type, start, …) index, which
	// answers the same queries: it took about a sixth of the file for nothing.
	if _, err := db.Exec(`DROP INDEX IF EXISTS records_type_start`); err != nil {
		return err
	}
	if err := addColumns(db, "devices", [][2]string{{"device_id", "TEXT"}, {"scope", "TEXT NOT NULL DEFAULT 'device'"}}); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS devices_device_id ON devices(device_id) WHERE device_id IS NOT NULL`); err != nil {
		return err
	}
	_, err := db.Exec(views)
	return err
}

// addColumns adds the columns table is missing.
func addColumns(db *sql.DB, table string, cols [][2]string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	for _, c := range cols {
		if !have[c[0]] {
			if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + c[0] + ` ` + c[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *DB) Close() error {
	if d.compacted != nil {
		<-d.compacted
	}
	return d.sql.Close()
}

func nowISO() string { return time.Now().Format(time.RFC3339) }

// InsertRecords stores rows and applies deletions. It returns how many samples were new
// and how many were marked deleted.
//
// A sample that is already stored is not added again: the same uuid, or the same type, start, end, source and value. When a sample with a uuid matches a row
// stored without one (sent earlier in the pipe format), that row gets the uuid and the
// other new fields. A deletion sets deleted_at on the sample with its uuid; the row stays,
// so other tools reading health.db can see what was deleted.
func (d *DB) InsertRecords(rows []Record) (added, deleted int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	ins, err := tx.Prepare(`INSERT OR IGNORE INTO records (type, start, "end", value, num, unit, source, received,
		uuid, source_bundle, source_version, device_model, device_software, user_entered, tz, metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, 0, err
	}
	defer ins.Close()
	// fills in the new fields on a row stored earlier, by uuid or, without one, by content
	const extras = `uuid = ?, source_bundle = ?, source_version = ?, device_model = ?, device_software = ?,
		user_entered = ?, tz = ?, metadata = ?`
	byUUID, err := tx.Prepare(`UPDATE records SET ` + extras + ` WHERE uuid = ?`)
	if err != nil {
		return 0, 0, err
	}
	defer byUUID.Close()
	byContent, err := tx.Prepare(`UPDATE records SET ` + extras + `
		WHERE uuid IS NULL AND type = ? AND start = ? AND "end" = ? AND source = ? AND value = ?`)
	if err != nil {
		return 0, 0, err
	}
	defer byContent.Close()
	del, err := tx.Prepare(`UPDATE records SET deleted_at = ? WHERE uuid = ? AND deleted_at IS NULL`)
	if err != nil {
		return 0, 0, err
	}
	defer del.Close()

	now := nowISO()
	for _, r := range rows {
		if r.Deleted {
			res, err := del.Exec(firstNonEmpty(r.DeletedAt, now), r.UUID)
			if err != nil {
				return 0, 0, err
			}
			n, _ := res.RowsAffected()
			deleted += int(n)
			continue
		}
		var num any
		if f, ok := asNumber(r.Value); ok {
			num = f
		}
		res, err := ins.Exec(r.Type, r.Start, r.End, r.Value, num, r.Unit, r.Source, now,
			nullable(r.UUID), nullable(r.SourceBundle), nullable(r.SourceVersion), nullable(r.DeviceModel),
			nullable(r.DeviceSoftware), r.UserEntered, nullable(r.TZ), nullable(r.Metadata))
		if err != nil {
			return 0, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
			continue
		}
		if r.UUID == "" {
			continue
		}
		set := []any{r.UUID, nullable(r.SourceBundle), nullable(r.SourceVersion), nullable(r.DeviceModel),
			nullable(r.DeviceSoftware), r.UserEntered, nullable(r.TZ), nullable(r.Metadata)}
		res, err = byUUID.Exec(append(set, r.UUID)...)
		if err != nil {
			return 0, 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := byContent.Exec(append(set, r.Type, r.Start, r.End, r.Source, r.Value)...); err != nil {
				return 0, 0, err
			}
		}
	}
	return added, deleted, tx.Commit()
}

// nullable stores "" as NULL, so that "no uuid" never collides in the uuid index.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Report is what the database keeps of a checkup report. report.json in its folder keeps
// everything the app sent, including fields this server does not know.
type Report struct {
	ID          string       `json:"id"`
	Date        string       `json:"date"`
	Institution string       `json:"institution"`
	Title       string       `json:"title,omitempty"`
	Items       []ReportItem `json:"items"`
	// Originals lists the report's original files (PDFs, photos), when the app sent it.
	Originals []OriginalFile `json:"originals,omitempty"`
}

type ReportItem struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Value any    `json:"value"`
	Unit  string `json:"unit"`
	Range string `json:"range"`
	Flag  string `json:"flag"`
	// OriginalName is the name as printed in the report, when it differs from Name.
	OriginalName string `json:"originalName,omitempty"`
	// Location is where the item is in the original files.
	Location *ItemLocation `json:"location,omitempty"`
}

// ItemLocation is a box on a page of one of the original files: File is its name, Page
// counts from 0, and X, Y, W, H are fractions of the page, from its top left corner.
type ItemLocation struct {
	File string  `json:"file"`
	Page int     `json:"page"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

type OriginalFile struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (d *DB) InsertReport(rep Report, folderName, folder string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := rep.ID
	if id == "" {
		id = folderName
	}
	date := rep.Date
	if len(date) > 10 {
		date = date[:10]
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// The same report sent again (from another device, or after editing) replaces the old
	// one: the app derives the id from the original file's hash, so drop the old items first
	// and items that were renamed or removed don't linger.
	if _, err := tx.Exec(`DELETE FROM checkup WHERE report_id = ?`, id); err != nil {
		return 0, err
	}
	for _, it := range rep.Items {
		v := str(it.Value)
		var num any
		if f, ok := asNumber(v); ok {
			num = f
		}
		var lf, lp, lx, ly, lw, lh any
		if l := it.Location; l != nil {
			lf, lp, lx, ly, lw, lh = l.File, l.Page, l.X, l.Y, l.W, l.H
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO checkup (report_id, date, institution, name, key, value, num, unit, range, flag,
				original_name, loc_file, loc_page, loc_x, loc_y, loc_w, loc_h) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, date, rep.Institution, it.Name, it.Key, v, num, it.Unit, it.Range, it.Flag,
			nullable(it.OriginalName), lf, lp, lx, ly, lw, lh); err != nil {
			return 0, err
		}
	}
	var originals any
	if len(rep.Originals) > 0 {
		b, _ := json.Marshal(rep.Originals)
		originals = string(b)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO reports (id, date, institution, items, folder, received, title, originals) VALUES (?,?,?,?,?,?,?,?)`,
		id, date, rep.Institution, len(rep.Items), folder, nowISO(), nullable(rep.Title), originals); err != nil {
		return 0, err
	}
	return len(rep.Items), tx.Commit()
}

type TypeStat = model.TypeStat

type Stats = model.Stats

// Stats counts the samples that are not deleted, per type.
func (d *DB) Stats() (Stats, error) {
	s := Stats{Types: []TypeStat{}}
	rows, err := d.sql.Query(`SELECT type, COUNT(*), MAX(start) FROM records WHERE deleted_at IS NULL GROUP BY type ORDER BY 2 DESC`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var t TypeStat
		var latest sql.NullString
		if err := rows.Scan(&t.Type, &t.Count, &latest); err != nil {
			return s, err
		}
		t.Latest = latest.String
		s.Total += t.Count
		s.Types = append(s.Types, t)
	}
	var last sql.NullString
	_ = d.sql.QueryRow(`SELECT MAX(received) FROM records`).Scan(&last)
	if last.Valid {
		s.LastReceived = &last.String
	}
	return s, rows.Err()
}

// ---------------------------------------------------------------- sync log

type LogEntry = model.LogEntry

func (d *DB) Log(e LogEntry) {
	if e.Time == "" {
		e.Time = nowISO()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if e.Kind == "ingest" && e.OK && d.mergeIngest(e) {
		if d.onLog != nil {
			go d.onLog(e)
		}
		return
	}
	types, _ := json.Marshal(e.Types)
	_, _ = d.sql.Exec(`INSERT INTO sync_log (time, kind, device, ip, received, added, bytes, duration_ms, ok, message, types)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, e.Time, e.Kind, e.Device, e.IP, e.Received, e.Added, e.Bytes, e.DurationMS, e.OK, e.Message, string(types))
	// keep the log bounded
	_, _ = d.sql.Exec(`DELETE FROM sync_log WHERE id <= (SELECT MAX(id) FROM sync_log) - 20000`)
	if d.onLog != nil {
		go d.onLog(e)
	}
}

// syncGap: batches from one device closer together than this are one sync in the history.
const syncGap = 2 * time.Minute

// mergeIngest adds a successful batch to the previous history row when that row is the
// same device's sync from moments ago: the app sends a big sync in many batches, which
// would otherwise fill the history with dozens of rows. Called with d.mu held.
func (d *DB) mergeIngest(e LogEntry) bool {
	var (
		id                            int64
		t, device, typesJSON, msg     string
		received, added, bytes, durMS int
	)
	var kind string
	var ok bool
	err := d.sql.QueryRow(`SELECT id, time, kind, ok, COALESCE(device,''), COALESCE(types,'[]'), COALESCE(message,''),
			COALESCE(received,0), COALESCE(added,0), COALESCE(bytes,0), COALESCE(duration_ms,0)
		FROM sync_log ORDER BY id DESC LIMIT 1`).Scan(&id, &t, &kind, &ok, &device, &typesJSON, &msg, &received, &added, &bytes, &durMS)
	if err != nil || kind != "ingest" || !ok || device != e.Device {
		return false
	}
	last, err := time.Parse(time.RFC3339, t)
	now, err2 := time.Parse(time.RFC3339, e.Time)
	if err != nil || err2 != nil || now.Sub(last) > syncGap || now.Before(last) {
		return false
	}
	var types []string
	_ = json.Unmarshal([]byte(typesJSON), &types)
	seen := map[string]bool{}
	for _, x := range types {
		seen[x] = true
	}
	for _, x := range e.Types {
		if !seen[x] && len(types) < 40 {
			seen[x] = true
			types = append(types, x)
		}
	}
	sort.Strings(types)
	tj, _ := json.Marshal(types)
	if e.Message != "" {
		msg = e.Message
	}
	_, err = d.sql.Exec(`UPDATE sync_log SET time = ?, received = ?, added = ?, bytes = ?, duration_ms = ?, types = ?, message = ? WHERE id = ?`,
		e.Time, received+e.Received, added+e.Added, bytes+e.Bytes, durMS+e.DurationMS, string(tj), msg, id)
	return err == nil
}

func (d *DB) Logs(limit int, before int64, kind string) ([]LogEntry, error) {
	q := `SELECT id, time, kind, COALESCE(device,''), COALESCE(ip,''), COALESCE(received,0), COALESCE(added,0),
	             COALESCE(bytes,0), COALESCE(duration_ms,0), ok, COALESCE(message,''), COALESCE(types,'[]')
	      FROM sync_log WHERE 1=1`
	var args []any
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		var types string
		if err := rows.Scan(&e.ID, &e.Time, &e.Kind, &e.Device, &e.IP, &e.Received, &e.Added, &e.Bytes, &e.DurationMS, &e.OK, &e.Message, &types); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(types), &e.Types)
		out = append(out, e)
	}
	return out, rows.Err()
}

type DayCount = model.DayCount

// Activity returns per-day ingest activity for the last n days (local time).
func (d *DB) Activity(days int) ([]DayCount, error) {
	since := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
	rows, err := d.sql.Query(`SELECT substr(time,1,10) AS day, SUM(COALESCE(added,0)), SUM(ok), SUM(1-ok)
		FROM sync_log WHERE kind='ingest' AND substr(time,1,10) >= ? GROUP BY day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]DayCount{}
	for rows.Next() {
		var c DayCount
		if err := rows.Scan(&c.Day, &c.Added, &c.Syncs, &c.Fails); err != nil {
			rows.Close()
			return nil, err
		}
		by[c.Day] = c
	}
	rows.Close()
	// Records counts the samples by the day they were taken, not the day they arrived: the
	// first sync brings years of history in one day, which made a chart by arrival one
	// tall bar and nothing else.
	rec, err := d.sql.Query(`SELECT substr(start,1,10) AS day, COUNT(*) FROM records
		WHERE start >= ? AND deleted_at IS NULL GROUP BY day`, since)
	if err != nil {
		return nil, err
	}
	defer rec.Close()
	for rec.Next() {
		var day string
		var n int
		if err := rec.Scan(&day, &n); err != nil {
			return nil, err
		}
		c := by[day]
		c.Records = n
		by[day] = c
	}
	out := make([]DayCount, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		c := by[day]
		c.Day = day
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- devices

type Device = model.Device

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

const (
	scopeDevice = "device" // a paired phone: sends data, reads, makes AI tokens
	scopeMCP    = "mcp"    // an AI assistant: reads through /mcp only
)

// AddDevice stores a new token. With a deviceID that is already known, it replaces that
// device's token instead (its old token stops working at once) and repaired is true.
func (d *DB) AddDevice(name, token, ip, deviceID, scope string) (dev Device, repaired bool, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := nowISO()
	if scope == "" {
		scope = scopeDevice
	}
	if deviceID != "" {
		res, err := d.sql.Exec(`UPDATE devices SET name = ?, token_hash = ?, last_seen = ?, last_ip = ?, revoked = 0, scope = ?
			WHERE device_id = ?`, name, hashToken(token), now, ip, scope, deviceID)
		if err != nil {
			return dev, false, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			dev, err = d.deviceWhere(`device_id = ?`, deviceID)
			return dev, true, err
		}
	}
	res, err := d.sql.Exec(`INSERT INTO devices (name, token_hash, created, last_seen, last_ip, device_id, scope) VALUES (?,?,?,?,?,?,?)`,
		name, hashToken(token), now, now, ip, nullable(deviceID), scope)
	if err != nil {
		return dev, false, err
	}
	id, _ := res.LastInsertId()
	dev, err = d.deviceWhere(`id = ?`, id)
	return dev, false, err
}

const deviceCols = `id, name, created, COALESCE(last_seen,''), COALESCE(last_ip,''), revoked, scope, COALESCE(device_id,'')`

func scanDevice(sc interface{ Scan(...any) error }) (Device, error) {
	var dev Device
	err := sc.Scan(&dev.ID, &dev.Name, &dev.Created, &dev.LastSeen, &dev.LastIP, &dev.Revoked, &dev.Scope, &dev.DeviceID)
	return dev, err
}

func (d *DB) deviceWhere(where string, args ...any) (Device, error) {
	return scanDevice(d.sql.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE `+where, args...))
}

// LookupToken returns the device that owns token, revoked or not. ok is false when no
// device has this token.
func (d *DB) LookupToken(token string) (Device, bool) {
	dev, err := d.deviceWhere(`token_hash = ?`, hashToken(token))
	return dev, err == nil
}

// DeviceForToken returns the device that owns token, if any and not revoked.
func (d *DB) DeviceForToken(token string) (Device, bool) {
	dev, ok := d.LookupToken(token)
	return dev, ok && !dev.Revoked
}

func (d *DB) TouchDevice(id int64, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = d.sql.Exec(`UPDATE devices SET last_seen = ?, last_ip = ? WHERE id = ?`, nowISO(), ip, id)
}

func (d *DB) Devices() ([]Device, error) {
	rows, err := d.sql.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY revoked, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dev)
	}
	return out, rows.Err()
}

func (d *DB) RevokeDevice(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`UPDATE devices SET revoked = 1 WHERE id = ?`, id)
	return err
}

// RenameDevice changes the name shown for a paired device. Its token stays the same.
func (d *DB) RenameDevice(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return fmt.Errorf("a device name needs 1 to 64 characters")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.sql.Exec(`UPDATE devices SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no device %d", id)
	}
	return nil
}

func (d *DB) DeleteDevice(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.sql.Exec(`DELETE FROM devices WHERE id = ? AND revoked = 1`, id)
	return err
}

// ---------------------------------------------------------------- reports & data browsing

type ReportRow = model.ReportRow

func (d *DB) Reports() ([]ReportRow, error) {
	rows, err := d.sql.Query(`SELECT c.report_id, MAX(c.date), MAX(COALESCE(c.institution,'')), COUNT(*),
	        SUM(CASE WHEN c.flag IN ('high','low','H','L','abnormal') THEN 1 ELSE 0 END), COALESCE(MAX(r.received),''),
	        COALESCE(MAX(r.title),''), COALESCE(MAX(r.folder),'')
	    FROM checkup c LEFT JOIN reports r ON r.id = c.report_id
	    GROUP BY c.report_id ORDER BY MAX(c.date) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportRow{}
	for rows.Next() {
		var r ReportRow
		var folder string
		if err := rows.Scan(&r.ID, &r.Date, &r.Institution, &r.Items, &r.Flagged, &r.Received, &r.Title, &folder); err != nil {
			return nil, err
		}
		r.Files = len(originalFiles(folder))
		out = append(out, r)
	}
	return out, rows.Err()
}

// originalFiles are the files in a report's folder other than report.json.
func originalFiles(folder string) []string {
	if folder == "" {
		return nil
	}
	entries, _ := os.ReadDir(folder)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "report.json" && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// ReportDetail is one report with its items, where each is in the original files, and
// the original files kept in its folder. folder is "" for an unknown report.
func (d *DB) ReportDetail(id string) (rep Report, folder string, err error) {
	var originals sql.NullString
	var title sql.NullString
	err = d.sql.QueryRow(`SELECT id, COALESCE(date,''), COALESCE(institution,''), title, COALESCE(folder,''), originals FROM reports WHERE id = ?`, id).
		Scan(&rep.ID, &rep.Date, &rep.Institution, &title, &folder, &originals)
	if err != nil {
		return rep, "", err
	}
	rep.Title = title.String
	if originals.Valid {
		_ = json.Unmarshal([]byte(originals.String), &rep.Originals)
	}
	rows, err := d.sql.Query(`SELECT name, COALESCE(key,''), COALESCE(value,''), COALESCE(unit,''), COALESCE(range,''), COALESCE(flag,''),
			COALESCE(original_name,''), loc_file, loc_page, loc_x, loc_y, loc_w, loc_h
		FROM checkup WHERE report_id = ? ORDER BY rowid`, id)
	if err != nil {
		return rep, folder, err
	}
	defer rows.Close()
	rep.Items = []ReportItem{}
	for rows.Next() {
		var it ReportItem
		var v string
		var lf sql.NullString
		var lp sql.NullInt64
		var lx, ly, lw, lh sql.NullFloat64
		if err := rows.Scan(&it.Name, &it.Key, &v, &it.Unit, &it.Range, &it.Flag, &it.OriginalName, &lf, &lp, &lx, &ly, &lw, &lh); err != nil {
			return rep, folder, err
		}
		it.Value = v
		if lf.Valid {
			it.Location = &ItemLocation{File: lf.String, Page: int(lp.Int64), X: lx.Float64, Y: ly.Float64, W: lw.Float64, H: lh.Float64}
		}
		rep.Items = append(rep.Items, it)
	}
	return rep, folder, rows.Err()
}

type DailyPoint = model.DailyPoint

func (d *DB) Daily(typ string, days int) ([]DailyPoint, error) {
	since := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
	rows, err := d.sql.Query(`SELECT day, COALESCE(total,0), COALESCE(mean,0), COALESCE(min,0), COALESCE(max,0), n, COALESCE(unit,'')
		FROM daily WHERE type = ? AND day >= ? ORDER BY day`, typ, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyPoint{}
	for rows.Next() {
		var p DailyPoint
		if err := rows.Scan(&p.Day, &p.Total, &p.Mean, &p.Min, &p.Max, &p.N, &p.Unit); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
