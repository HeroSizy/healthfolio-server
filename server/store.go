package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/herosizy/healthsync-server/model"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database: records, checkup, reports, devices and sync_log, plus the
// daily and v_* views.
type DB struct {
	sql   *sql.DB
	path  string
	mu    sync.Mutex // serialises writes
	onLog func(LogEntry)
}

const schema = `
CREATE TABLE IF NOT EXISTS records (
    type TEXT NOT NULL, start TEXT NOT NULL, "end" TEXT NOT NULL,
    value TEXT NOT NULL, num REAL, unit TEXT, source TEXT NOT NULL DEFAULT '',
    received TEXT NOT NULL,
    UNIQUE (type, start, "end", source, value));
CREATE INDEX IF NOT EXISTS records_type_start ON records(type, start);
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
`

func OpenDB(path string) (*DB, error) {
	// WAL + busy timeout so other tools (sqlite3, Datasette, scripts) can read while we write.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)")
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
	return &DB{sql: db, path: path}, nil
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
	rows, err := db.Query(`SELECT name FROM pragma_table_info('records')`)
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
	for _, c := range recordColumns {
		if !have[c[0]] {
			if _, err := db.Exec(`ALTER TABLE records ADD COLUMN ` + c[0] + ` ` + c[1]); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS records_uuid ON records(uuid) WHERE uuid IS NOT NULL`); err != nil {
		return err
	}
	_, err = db.Exec(views)
	return err
}

func (d *DB) Close() error { return d.sql.Close() }

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

type Report struct {
	ID          string       `json:"id"`
	Date        string       `json:"date"`
	Institution string       `json:"institution"`
	Items       []ReportItem `json:"items"`
}

type ReportItem struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Value any    `json:"value"`
	Unit  string `json:"unit"`
	Range string `json:"range"`
	Flag  string `json:"flag"`
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
	for _, it := range rep.Items {
		v := str(it.Value)
		var num any
		if f, ok := asNumber(v); ok {
			num = f
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO checkup VALUES (?,?,?,?,?,?,?,?,?,?)`,
			id, date, rep.Institution, it.Name, it.Key, v, num, it.Unit, it.Range, it.Flag); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO reports VALUES (?,?,?,?,?,?)`,
		id, date, rep.Institution, len(rep.Items), folder, nowISO()); err != nil {
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
	types, _ := json.Marshal(e.Types)
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = d.sql.Exec(`INSERT INTO sync_log (time, kind, device, ip, received, added, bytes, duration_ms, ok, message, types)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, e.Time, e.Kind, e.Device, e.IP, e.Received, e.Added, e.Bytes, e.DurationMS, e.OK, e.Message, string(types))
	// keep the log bounded
	_, _ = d.sql.Exec(`DELETE FROM sync_log WHERE id <= (SELECT MAX(id) FROM sync_log) - 20000`)
	if d.onLog != nil {
		go d.onLog(e)
	}
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
			return nil, err
		}
		by[c.Day] = c
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

func (d *DB) AddDevice(name, token, ip string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := nowISO()
	_, err := d.sql.Exec(`INSERT INTO devices (name, token_hash, created, last_seen, last_ip) VALUES (?,?,?,?,?)`,
		name, hashToken(token), now, now, ip)
	return err
}

// DeviceForToken returns the device that owns token, if any and not revoked.
func (d *DB) DeviceForToken(token string) (Device, bool) {
	var dev Device
	var seen, ip sql.NullString
	err := d.sql.QueryRow(`SELECT id, name, created, last_seen, last_ip, revoked FROM devices WHERE token_hash = ?`, hashToken(token)).
		Scan(&dev.ID, &dev.Name, &dev.Created, &seen, &ip, &dev.Revoked)
	if err != nil || dev.Revoked {
		return dev, false
	}
	dev.LastSeen, dev.LastIP = seen.String, ip.String
	return dev, true
}

func (d *DB) TouchDevice(id int64, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = d.sql.Exec(`UPDATE devices SET last_seen = ?, last_ip = ? WHERE id = ?`, nowISO(), ip, id)
}

func (d *DB) Devices() ([]Device, error) {
	rows, err := d.sql.Query(`SELECT id, name, created, COALESCE(last_seen,''), COALESCE(last_ip,''), revoked FROM devices ORDER BY revoked, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var dev Device
		if err := rows.Scan(&dev.ID, &dev.Name, &dev.Created, &dev.LastSeen, &dev.LastIP, &dev.Revoked); err != nil {
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
	        SUM(CASE WHEN c.flag IN ('high','low','H','L','abnormal') THEN 1 ELSE 0 END), COALESCE(MAX(r.received),'')
	    FROM checkup c LEFT JOIN reports r ON r.id = c.report_id
	    GROUP BY c.report_id ORDER BY MAX(c.date) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportRow{}
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ID, &r.Date, &r.Institution, &r.Items, &r.Flagged, &r.Received); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
