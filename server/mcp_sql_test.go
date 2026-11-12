package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// query_sql may read the health tables, never the server's own bookkeeping, and stops
// queries that run too long.
func TestQuerySQLPolicy(t *testing.T) {
	s, ts := newTestServer(t)
	do(t, "POST", ts.URL+"/ingest", "master",
		"HKQuantityTypeIdentifierStepCount|2026-11-01T08:00:00+08:00|2026-11-01T08:10:00+08:00|500|count|phone\n")
	if _, _, err := s.db.AddDevice("Zen's iPhone", "secret-token-1234567890", "192.168.1.9", "", scopeDevice); err != nil {
		t.Fatal(err)
	}
	q := func(sql string) (any, error) { return s.mcp.call("query_sql", map[string]any{"sql": sql}) }

	for _, bad := range []string{
		"SELECT * FROM devices",
		"SELECT token_hash FROM devices",
		"SELECT name FROM records JOIN devices ON 1=1",
		"WITH d AS (SELECT * FROM devices) SELECT * FROM d",
		"SELECT * FROM prefs",
		"SELECT (SELECT token_hash FROM devices LIMIT 1) AS x",
		"SELECT * FROM pragma_table_info('devices')",
		"SELECT load_extension('x')",
	} {
		if out, err := q(bad); err == nil {
			t.Errorf("%s: allowed, got %v", bad, out)
		}
	}
	for _, good := range []string{
		"SELECT COUNT(*) AS n FROM records",
		"SELECT * FROM daily",
		"SELECT * FROM v_steps",
		"SELECT name FROM sqlite_master WHERE type = 'table'",
		"SELECT * FROM pragma_table_info('records')",
		"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c LIMIT 5) SELECT x FROM c",
		"SELECT json_extract('{\"a\":1}', '$.a') AS a",
	} {
		if out, err := q(good); err != nil {
			t.Errorf("%s: %v %v", good, err, out)
		}
	}
	s.db.Log(LogEntry{Kind: "ingest", IP: "192.168.1.9", OK: true})
	out, err := q("SELECT ip, kind FROM sync_log")
	if err != nil || strings.Contains(strings.Join(strings.Fields(fmtAny(out)), ""), "192.168.1.9") {
		t.Errorf("sync_log ip: %v %v", out, err)
	}
	// the policy stays on query_sql's connection only: the other tools still work, and
	// the server itself still reads devices
	if _, err := s.mcp.call("list_data_types", nil); err != nil {
		t.Fatal(err)
	}
	if d, err := s.db.Devices(); err != nil || len(d) == 0 {
		t.Fatalf("devices: %v %v", d, err)
	}
	for i := 0; i < 20; i++ { // every pooled connection
		if _, err := s.mcp.rows(`SELECT COUNT(*) FROM devices`); err != nil {
			t.Fatalf("policy leaked to the pool: %v", err)
		}
		_, _ = q("SELECT 1")
	}

	start := time.Now()
	_, err = q("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT COUNT(*) FROM c")
	if err == nil || !strings.Contains(err.Error(), "longer than") || time.Since(start) > queryTimeout+5*time.Second {
		t.Fatalf("endless query: %v after %v", err, time.Since(start))
	}
}

func fmtAny(v any) string { return strings.TrimSpace(strings.ReplaceAll(fmtJSON(v), "\n", "")) }

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
