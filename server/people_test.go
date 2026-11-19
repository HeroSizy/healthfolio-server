package server

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pairAs pairs a phone with a fresh code and returns the answer.
func pairAs(t *testing.T, base, body string) map[string]any {
	t.Helper()
	_, p := do(t, "POST", base+"/api/pair", "", "", "X-HealthSync", "1")
	code := p["active"].(map[string]any)["code"].(string)
	var req map[string]any
	_ = json.Unmarshal([]byte(body), &req)
	req["code"] = code
	b, _ := json.Marshal(req)
	st, r := do(t, "POST", base+"/pair", "", string(b))
	if st != 200 {
		t.Fatalf("pair %s: %d %v", body, st, r)
	}
	return r
}

func steps(day string, n string) string {
	return "HKQuantityTypeIdentifierStepCount|" + day + "T08:00:00+08:00|" + day + "T08:10:00+08:00|" + n + "|count|phone\n"
}

func mcpCall(t *testing.T, base, tok, name, args string) (string, bool) {
	t.Helper()
	_, r := do(t, "POST", base+"/mcp", tok, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: %v", name, r)
	}
	isErr, _ := res["isError"].(bool)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), isErr
}

func TestPeople(t *testing.T) {
	s, ts := newTestServer(t)
	u := ts.URL

	// The first phone takes the owner and may manage the family.
	a := pairAs(t, u, `{"device":"Zen's iPhone","device_id":"dev-a","person_id":"PID-ZEN","person_name":"Zen"}`)
	if a["family"] != true || a["person"].(map[string]any)["owner"] != true || a["person"].(map[string]any)["id"] != "PID-ZEN" {
		t.Fatalf("first phone: %v", a)
	}
	tokA := a["token"].(string)
	// A second phone with another person id is a new person, and may not manage the family.
	b := pairAs(t, u, `{"device":"Wife's iPhone","device_id":"dev-b","person_id":"PID-WIFE","person_name":"Lin"}`)
	if b["family"] != false || b["person"].(map[string]any)["owner"] != false || b["person"].(map[string]any)["name"] != "Lin" {
		t.Fatalf("second phone: %v", b)
	}
	tokB := b["token"].(string)

	// Each one's HealthKit goes to their own database.
	do(t, "POST", u+"/ingest", tokA, steps("2026-11-01", "500"))
	if code, r := do(t, "POST", u+"/ingest", tokB, steps("2026-11-01", "800")+steps("2026-11-02", "900")); code != 200 {
		t.Fatalf("ingest B: %d %v", code, r)
	}
	if _, st := do(t, "GET", u+"/stats", tokA, ""); st["total"].(float64) != 1 {
		t.Fatalf("owner's stats mix in the wife's: %v", st)
	}
	if _, st := do(t, "GET", u+"/stats", tokB, ""); st["total"].(float64) != 2 {
		t.Fatalf("wife's stats: %v", st)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "people", "2", "health.db")); err != nil {
		t.Fatalf("no database of her own: %v", err)
	}
	// The CSV output keeps the owner's data only.
	csvs, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, "csv", "*.csv"))
	for _, f := range csvs {
		if b, _ := os.ReadFile(f); strings.Contains(string(b), "|800|") || strings.Contains(string(b), ",800,") {
			t.Fatalf("someone else's data in the CSV: %s", b)
		}
	}

	// Least privilege: the wife's phone cannot touch the owner, nor add people.
	if code, r := do(t, "POST", u+"/ingest?person=PID-ZEN", tokB, steps("2026-11-03", "1")); code != 403 || r["reason"] != "person_not_allowed" {
		t.Fatalf("B wrote to the owner: %d %v", code, r)
	}
	if code, _ := do(t, "GET", u+"/stats", tokB, "", "X-Healthfolio-Person", "me"); code != 403 {
		t.Fatalf("B read the owner: %d", code)
	}
	if code, _ := do(t, "PUT", u+"/people/PID-MUM", tokB, `{"name":"Mum"}`); code != 403 {
		t.Fatalf("B added a person: %d", code)
	}
	if code, r := do(t, "POST", u+"/ingest?person=NOBODY", tokA, steps("2026-11-03", "1")); code != 404 || r["reason"] != "unknown_person" {
		t.Fatalf("unknown person: %d %v", code, r)
	}

	// The owner's phone adds Mum and sends her report.
	if code, r := do(t, "PUT", u+"/people/PID-MUM", tokA, `{"name":"Mum","relation":"mother","birth":"1962-03-14","sex":"female"}`); code != 200 || r["created"] != true {
		t.Fatalf("add Mum: %d %v", code, r)
	}
	if code, _ := do(t, "PUT", u+"/people/PID-X", tokA, `{"birth":"14.03.1962"}`); code != 400 {
		t.Fatalf("bad birth accepted: %d", code)
	}
	rep := `{"report":{"id":"m1","date":"2026-09-01","items":[{"name":"Glucose","value":7.1,"flag":"high"}]},"files":[{"name":"a.jpg","data":"aGk="}]}`
	if code, r := do(t, "POST", u+"/report?person=PID-MUM", tokA, rep); code != 200 || !strings.Contains(r["folder"].(string), filepath.Join("people", "3", "reports")) {
		t.Fatalf("Mum's report: %d %v", code, r)
	}
	if _, r := do(t, "GET", u+"/backup/reports", tokA, ""); len(r["reports"].([]any)) != 0 {
		t.Fatalf("Mum's report among the owner's: %v", r)
	}
	if _, r := do(t, "GET", u+"/backup/reports?person=PID-MUM", tokA, ""); len(r["reports"].([]any)) != 1 {
		t.Fatalf("Mum's reports: %v", r)
	}

	// GET /people: everyone for A, B only for B.
	_, pa := do(t, "GET", u+"/people", tokA, "")
	if n := len(pa["people"].([]any)); n != 3 {
		t.Fatalf("A sees %d people: %v", n, pa)
	}
	_, pb := do(t, "GET", u+"/people", tokB, "")
	if l := pb["people"].([]any); len(l) != 1 || l[0].(map[string]any)["me"] != true || l[0].(map[string]any)["records"].(float64) != 2 {
		t.Fatalf("B sees: %v", pb)
	}
	_, au := do(t, "GET", u+"/auth", tokB, "")
	if au["family"] != false || au["person"].(map[string]any)["id"] != "PID-WIFE" {
		t.Fatalf("auth: %v", au)
	}

	// MCP: the owner's AI sees everyone and must say whom; the wife's sees only her.
	_, ma := do(t, "POST", u+"/mcp/tokens", tokA, `{"name":"Claude"}`)
	_, mb := do(t, "POST", u+"/mcp/tokens", tokB, `{"name":"Claude"}`)
	aiA, aiB := ma["token"].(string), mb["token"].(string)
	if code, _ := do(t, "POST", u+"/mcp/tokens", tokB, `{"name":"x","person":"PID-ZEN"}`); code != 403 {
		t.Fatalf("B made a token for the owner: %d", code)
	}
	out, isErr := mcpCall(t, u, aiA, "list_people", `{}`)
	if isErr || strings.Count(out, `"person":`) != 3 {
		t.Fatalf("list_people: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiA, "list_data_types", `{}`); !isErr || !strings.Contains(out, "list_people") {
		t.Fatalf("no person with several: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiA, "daily_summary", `{"person":"PID-WIFE","type":"StepCount","from":"2026-11-01","to":"2026-11-02"}`); isErr || !strings.Contains(out, `"total":900`) {
		t.Fatalf("wife's steps for A: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiA, "get_checkups", `{"person":"Mum"}`); isErr || !strings.Contains(out, "Glucose") {
		t.Fatalf("Mum's checkups by name: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiB, "list_people", `{}`); isErr || strings.Count(out, `"person":`) != 1 {
		t.Fatalf("B's list_people: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiB, "query_sql", `{"sql":"SELECT SUM(num) AS n FROM records"}`); isErr || !strings.Contains(out, `"n":1700`) {
		t.Fatalf("B's query_sql: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiB, "query_sql", `{"person":"PID-ZEN","sql":"SELECT 1"}`); !isErr {
		t.Fatalf("B's AI read the owner: %s", out)
	}
	if out, isErr = mcpCall(t, u, aiA, "query_sql", `{"person":"me","sql":"SELECT * FROM people"}`); !isErr {
		t.Fatalf("query_sql read the people table: %s", out)
	}

	// Dashboard: per person, and moving a phone.
	if _, r := do(t, "GET", u+"/api/overview?person=PID-WIFE", "", ""); r["stats"].(map[string]any)["total"].(float64) != 2 || len(r["people"].([]any)) != 3 {
		t.Fatalf("overview for the wife: %v", r)
	}
	devs, _ := s.db.Devices()
	var idB int64
	for _, d := range devs {
		if d.DeviceID == "dev-b" {
			idB = d.ID
		}
	}
	if code, _ := do(t, "PATCH", u+"/api/devices/"+itoa64(idB), "", `{"family":true}`, "X-HealthSync", "1"); code != 200 {
		t.Fatalf("family on: %d", code)
	}
	if code, _ := do(t, "GET", u+"/stats?person=me", tokB, ""); code != 200 {
		t.Fatalf("B with family: %d", code)
	}

	// Deleting needs the person's phones gone; Mum has none.
	if code, r := do(t, "DELETE", u+"/people/PID-WIFE", tokA, ""); code != 409 || r["reason"] != "person_has_devices" {
		t.Fatalf("deleted a person with a phone: %d %v", code, r)
	}
	if code, r := do(t, "DELETE", u+"/people/me", tokA, ""); code != 409 || r["reason"] != "owner" {
		t.Fatalf("deleted the owner: %d %v", code, r)
	}
	if code, _ := do(t, "DELETE", u+"/people/PID-MUM", tokA, ""); code != 200 {
		t.Fatalf("delete Mum: %d", code)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "people", "3")); !os.IsNotExist(err) {
		t.Fatalf("Mum's folder is still there: %v", err)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestPeopleClaimAndMerge(t *testing.T) {
	s, ts := newTestServer(t)
	u := ts.URL

	// An app from before people: everything stays the owner's, as before.
	old := pairAs(t, u, `{"device":"Zen's iPhone","device_id":"dev-a"}`)
	tok := old["token"].(string)
	if old["person"].(map[string]any)["id"] != "me" || old["family"] != true {
		t.Fatalf("old app: %v", old)
	}
	do(t, "POST", u+"/ingest", tok, steps("2026-11-01", "500"))
	// The updated app says which person id is its own.
	if code, r := do(t, "POST", u+"/people/me", tok, `{"id":"PID-ZEN","name":"Zen"}`); code != 200 || r["person"].(map[string]any)["id"] != "PID-ZEN" {
		t.Fatalf("claim: %d %v", code, r)
	}
	// Pairing again with the same id finds the owner.
	again := pairAs(t, u, `{"device":"Zen's iPad","device_id":"dev-ipad","person_id":"PID-ZEN"}`)
	if again["person"].(map[string]any)["owner"] != true {
		t.Fatalf("iPad: %v", again)
	}
	// A phone that paired before keeps its person when its id changed (iCloud turned off).
	re := pairAs(t, u, `{"device":"Zen's iPhone","device_id":"dev-a","person_id":"DEVICE-ID-A"}`)
	if re["person"].(map[string]any)["owner"] != true || re["repaired"] != true {
		t.Fatalf("paired again: %v", re)
	}
	// A second phone of someone else, then the same person added on the computer by
	// mistake, merged into her.
	w := pairAs(t, u, `{"device":"Lin's iPhone","device_id":"dev-b","person_id":"PID-WIFE","person_name":"Lin"}`)
	do(t, "POST", u+"/ingest", w["token"].(string), steps("2026-11-01", "800"))
	if code, r := do(t, "POST", u+"/people/me", w["token"].(string), `{"id":"PID-ZEN"}`); code != 409 || r["reason"] != "person_taken" {
		t.Fatalf("took someone else's id: %d %v", code, r)
	}
	_, dup := do(t, "POST", u+"/api/people", "", `{"name":"Lin (computer)"}`, "X-HealthSync", "1")
	dupID := dup["id"].(string)
	tokNew := s.token // the shared token may manage the family
	do(t, "POST", u+"/ingest?person="+dupID, tokNew, steps("2026-11-01", "800")+steps("2026-11-05", "300"))
	rep := `{"report":{"id":"w1","date":"2026-08-01","items":[{"name":"TSH","value":2.1}]},"files":[{"name":"w.pdf","data":"aGk="}]}`
	do(t, "POST", u+"/report?person="+dupID, tokNew, rep)
	if code, r := do(t, "POST", u+"/api/people/"+dupID+"/merge", "", `{"into":"PID-WIFE"}`, "X-HealthSync", "1"); code != 200 {
		t.Fatalf("merge: %d %v", code, r)
	}
	if _, st := do(t, "GET", u+"/stats", w["token"].(string), ""); st["total"].(float64) != 2 {
		t.Fatalf("after merge: %v", st)
	}
	if _, r := do(t, "GET", u+"/backup/reports", w["token"].(string), ""); len(r["reports"].([]any)) != 1 {
		t.Fatalf("merged report: %v", r)
	} else if f := r["reports"].([]any)[0].(map[string]any)["files"].([]any); len(f) != 1 {
		t.Fatalf("merged report lost its file: %v", r)
	}
	if _, ok := s.db.personByKey(dupID); !ok {
		t.Fatalf("the merged id no longer finds her")
	}
	people, _ := s.db.PeopleList()
	if len(people) != 2 {
		t.Fatalf("people after merge: %v", people)
	}
	if code, _ := do(t, "POST", u+"/api/people/me/merge", "", `{"into":"PID-WIFE"}`, "X-HealthSync", "1"); code != 409 {
		t.Fatalf("merged the owner away: %d", code)
	}
}

// An app that sends no person id takes the owner only while no phone has it; any other
// such phone is someone new, without the family.
func TestOldAppPairing(t *testing.T) {
	_, ts := newTestServer(t)
	u := ts.URL
	first := pairAs(t, u, `{"device":"old1","device_id":"OLD1"}`)
	if first["family"] != true || first["person"].(map[string]any)["owner"] != true {
		t.Fatalf("first old phone: %v", first)
	}
	second := pairAs(t, u, `{"device":"old2","device_id":"OLD2"}`)
	if second["family"] != false || second["person"].(map[string]any)["owner"] != false || second["person"].(map[string]any)["name"] != "old2" {
		t.Fatalf("second old phone: %v", second)
	}
	// pairing again keeps its person
	again := pairAs(t, u, `{"device":"old2","device_id":"OLD2"}`)
	if again["person"].(map[string]any)["id"] != second["person"].(map[string]any)["id"] || again["family"] != false {
		t.Fatalf("old phone paired again: %v", again)
	}
	if _, r := do(t, "GET", u+"/people", again["token"].(string), ""); len(r["people"].([]any)) != 1 {
		t.Fatalf("second old phone sees: %v", r)
	}
}

func TestDevicesMigrateToOwner(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "health.db")
	raw, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE devices (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
			created TEXT NOT NULL, last_seen TEXT, last_ip TEXT, revoked INTEGER NOT NULL DEFAULT 0, device_id TEXT, scope TEXT NOT NULL DEFAULT 'device')`,
		`INSERT INTO devices (name, token_hash, created, scope) VALUES ('phone', 'h1', 'x', 'device'), ('ai', 'h2', 'x', 'mcp')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	db, err := OpenDB(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	devs, _ := db.Devices()
	for _, d := range devs {
		switch d.Scope {
		case scopeDevice:
			if d.PersonNum != ownerNum || !d.Family || d.Person != "me" {
				t.Fatalf("phone after upgrade: %+v", d)
			}
		case scopeMCP:
			if d.PersonNum != 0 {
				t.Fatalf("AI token after upgrade sees one person: %+v", d)
			}
		}
	}
}
