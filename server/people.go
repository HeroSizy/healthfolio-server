package server

// People: the server keeps the health data of several people (a family), each in their own
// database. Person 1 is the server's owner: their data is in health.db in the data folder,
// where it always was, so nothing moves when a server is upgraded. Everyone else has
// people/<n>/ with its own health.db, reports/, chats/ and settings/.
//
// One database per person, not a person column in every table: query_sql lets an AI
// assistant write its own SQL, and a WHERE it forgets would read the whole family; the
// uniqueness of a sample would need the person in it; and deleting a person would mean
// deleting from every table. With a file per person all three are simply true.
//
// The app names a person with its own id (a UUID; for the phone's owner the "person id" it
// keeps in iCloud). person_keys maps every id a person was ever sent with to them, and
// "me" always to the owner.
//
// Every paired phone belongs to one person and may read and write only that person, unless
// it may manage the family (devices.family). AI tokens see one person or (person NULL)
// everyone.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/herosizy/healthsync-server/model"
)

type Person = model.Person

// ownerNum is the server owner's person number.
const ownerNum int64 = 1

// ownerKey is the owner's id until a phone takes it, and stays another id of theirs.
const ownerKey = "me"

var (
	errUnknownPerson = errors.New("no such person")
	personKeyChars   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	birthFormat      = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)
)

// validPersonKey is an id the app may send: letters, digits and . _ : -, up to 128.
func validPersonKey(k string) bool { return personKeyChars.MatchString(k) }

// personDir is where a person's files are.
func personDir(dataDir string, n int64) string {
	if n <= ownerNum {
		return dataDir
	}
	return filepath.Join(dataDir, "people", strconv.FormatInt(n, 10))
}

// personDBs are the open databases of the people other than the owner.
type personDBs struct {
	mu sync.Mutex
	m  map[int64]*DB
}

// personDB is person n's database, opened (and made) the first time it is needed.
func (s *Server) personDB(n int64) (*DB, error) {
	if n <= ownerNum {
		return s.db, nil
	}
	s.pdbs.mu.Lock()
	defer s.pdbs.mu.Unlock()
	if d := s.pdbs.m[n]; d != nil {
		return d, nil
	}
	if _, ok := s.db.personByNum(n); !ok {
		return nil, errUnknownPerson
	}
	dir := personDir(s.cfg.DataDir, n)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d, err := OpenDB(filepath.Join(dir, "health.db"))
	if err != nil {
		return nil, err
	}
	if s.pdbs.m == nil {
		s.pdbs.m = map[int64]*DB{}
	}
	s.pdbs.m[n] = d
	return d, nil
}

// closePersonDB closes person n's database, if open.
func (s *Server) closePersonDB(n int64) {
	s.pdbs.mu.Lock()
	d := s.pdbs.m[n]
	delete(s.pdbs.m, n)
	s.pdbs.mu.Unlock()
	if d != nil {
		d.Close()
	}
	s.mcp.closePerson(n)
}

func (s *Server) closePeople() {
	s.pdbs.mu.Lock()
	defer s.pdbs.mu.Unlock()
	for n, d := range s.pdbs.m {
		d.Close()
		delete(s.pdbs.m, n)
	}
}

// ---------------------------------------------------------------- the people table

const personCols = `id, key, name, relation, birth, sex`

func scanPerson(sc interface{ Scan(...any) error }) (Person, error) {
	var p Person
	err := sc.Scan(&p.Num, &p.ID, &p.Name, &p.Relation, &p.Birth, &p.Sex)
	p.Owner = p.Num == ownerNum
	return p, err
}

// PeopleList is everyone, the owner first.
func (d *DB) PeopleList() ([]Person, error) {
	rows, err := d.sql.Query(`SELECT ` + personCols + ` FROM people ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) personByNum(n int64) (Person, bool) {
	p, err := scanPerson(d.sql.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, n))
	return p, err == nil
}

// personByKey finds a person by any id they were sent with.
func (d *DB) personByKey(key string) (Person, bool) {
	p, err := scanPerson(d.sql.QueryRow(`SELECT `+strings.ReplaceAll(personCols, "id, key", "p.id, p.key")+
		` FROM person_keys k JOIN people p ON p.id = k.person WHERE k.key = ?`, key))
	return p, err == nil
}

// ownerClaimed reports whether a phone has given the owner an id of its own.
func (d *DB) ownerClaimed() bool {
	p, ok := d.personByNum(ownerNum)
	return ok && p.ID != ownerKey
}

// addPersonKey makes key another id of person n. The owner's first id from a phone becomes
// their id ("me" stays one too).
func (d *DB) addPersonKey(key string, n int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.sql.Exec(`INSERT OR IGNORE INTO person_keys (key, person) VALUES (?, ?)`, key, n); err != nil {
		return err
	}
	_, err := d.sql.Exec(`UPDATE people SET key = ? WHERE id = ? AND key = ? AND NOT EXISTS (SELECT 1 FROM people WHERE key = ?)`,
		key, n, ownerKey, key)
	return err
}

// personFields are what the app and the dashboard may set on a person.
type personFields struct {
	Name     *string `json:"name"`
	Relation *string `json:"relation"`
	Birth    *string `json:"birth"`
	Sex      *string `json:"sex"`
}

// check cleans the fields and says what is wrong with them.
func (f *personFields) check() error {
	if f.Name != nil {
		v := cleanName(*f.Name)
		if utf8.RuneCountInString(v) > 40 {
			return fmt.Errorf("a name has at most 40 characters")
		}
		f.Name = &v
	}
	if f.Relation != nil {
		v := cleanName(*f.Relation)
		if utf8.RuneCountInString(v) > 32 {
			return fmt.Errorf("a relation has at most 32 characters")
		}
		f.Relation = &v
	}
	if f.Birth != nil {
		v := strings.TrimSpace(*f.Birth)
		if v != "" && !birthFormat.MatchString(v) {
			return fmt.Errorf("birth is YYYY-MM-DD, YYYY-MM or YYYY")
		}
		f.Birth = &v
	}
	if f.Sex != nil {
		v := strings.ToLower(strings.TrimSpace(*f.Sex))
		switch v {
		case "", "female", "male", "other":
		default:
			return fmt.Errorf("sex is female, male, other or empty")
		}
		f.Sex = &v
	}
	return nil
}

// addPerson stores a new person with the id key.
func (d *DB) addPerson(key string, f personFields) (Person, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.sql.Begin()
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	val := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	res, err := tx.Exec(`INSERT INTO people (key, name, relation, birth, sex, created) VALUES (?,?,?,?,?,?)`,
		key, val(f.Name), val(f.Relation), val(f.Birth), val(f.Sex), nowISO())
	if err != nil {
		return Person{}, err
	}
	n, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO person_keys (key, person) VALUES (?, ?)`, key, n); err != nil {
		return Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return Person{}, err
	}
	p, _ := scanPerson(d.sql.QueryRow(`SELECT `+personCols+` FROM people WHERE id = ?`, n))
	return p, nil
}

func (d *DB) updatePerson(n int64, f personFields) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	set := map[string]*string{"name": f.Name, "relation": f.Relation, "birth": f.Birth, "sex": f.Sex}
	for col, v := range set {
		if v == nil {
			continue
		}
		if _, err := d.sql.Exec(`UPDATE people SET `+col+` = ? WHERE id = ?`, *v, n); err != nil {
			return err
		}
	}
	return nil
}

// phonesOf counts the phones (not revoked) that belong to person n.
func (d *DB) phonesOf(n int64) int {
	var c int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM devices WHERE person = ? AND scope = 'device' AND revoked = 0`, n).Scan(&c)
	return c
}

// SetDevicePerson moves a device to person n.
func (d *DB) SetDevicePerson(id, n int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.sql.Exec(`UPDATE devices SET person = ? WHERE id = ?`, n, id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return fmt.Errorf("no device %d", id)
	}
	return nil
}

// SetDeviceEveryone lets an AI token see every person.
func (d *DB) SetDeviceEveryone(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.sql.Exec(`UPDATE devices SET person = NULL WHERE id = ? AND scope = 'mcp'`, id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return fmt.Errorf("only an AI token can see everyone")
	}
	return nil
}

// SetDeviceFamily lets a phone manage every person, or only its own.
func (d *DB) SetDeviceFamily(id int64, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.sql.Exec(`UPDATE devices SET family = ? WHERE id = ? AND scope = 'device'`, on, id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return fmt.Errorf("no phone %d", id)
	}
	return nil
}

// ---------------------------------------------------------------- who may see whom

// canAccess reports whether dev may read (and, for a phone, write) person n.
func canAccess(dev Device, n int64) bool {
	if dev.Scope == scopeMCP {
		return dev.PersonNum == 0 || dev.PersonNum == n
	}
	return dev.Family || (dev.PersonNum != 0 && dev.PersonNum == n)
}

// seesEveryone: an AI token without a person, or a phone that may manage the family.
func seesEveryone(dev Device) bool {
	if dev.Scope == scopeMCP {
		return dev.PersonNum == 0
	}
	return dev.Family
}

// personParam is the person a request names: ?person= or X-Healthfolio-Person.
func personParam(r *http.Request) string {
	if v := strings.TrimSpace(r.URL.Query().Get("person")); v != "" {
		return v
	}
	return strings.TrimSpace(r.Header.Get("X-Healthfolio-Person"))
}

// target is the person a call from dev is for, and their database. Without a person in
// the request, it is the device's own. It answers 404 unknown_person or 403
// person_not_allowed itself.
func (s *Server) target(w http.ResponseWriter, r *http.Request, dev Device) (Person, *DB, bool) {
	key := personParam(r)
	var p Person
	var ok bool
	if key == "" {
		n := dev.PersonNum
		if n == 0 {
			n = ownerNum
		}
		p, ok = s.db.personByNum(n)
	} else {
		p, ok = s.db.personByKey(key)
	}
	if !ok {
		writeJSON(w, 404, map[string]any{"error": "no person " + strconv.Quote(key) + " on this server; add it with PUT /people/{id}", "reason": "unknown_person"})
		return p, nil, false
	}
	if !canAccess(dev, p.Num) {
		s.db.Log(LogEntry{Kind: "auth", Device: dev.Name, IP: clientIP(r), Message: "refused: may not manage " + personLabel(p)})
		writeJSON(w, 403, map[string]any{"error": "this phone may only manage its own person; turn on \"Can manage the family\" for it in the dashboard", "reason": "person_not_allowed"})
		return p, nil, false
	}
	db, err := s.personDB(p.Num)
	if err != nil {
		errJSON(w, 500, err.Error())
		return p, nil, false
	}
	return p, db, true
}

// personLabel names a person in the history.
func personLabel(p Person) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Owner {
		return "the owner"
	}
	return p.ID
}

// logPerson fills in whose data a history entry is about.
func logPerson(e LogEntry, p Person) LogEntry {
	e.PersonNum = p.Num
	if !p.Owner {
		e.Person = personLabel(p)
	}
	return e
}

// summary adds the counts the dashboard and the app show for each person.
func (s *Server) summary(p Person) Person {
	db, err := s.personDB(p.Num)
	if err != nil {
		return p
	}
	if st, err := db.Stats(); err == nil {
		p.Records, p.LastReceived = st.Total, st.LastReceived
	}
	if reps, err := db.reportCounts(); err == nil {
		p.Reports, p.LatestReport, p.Flagged = reps.n, reps.latest, reps.flagged
	}
	p.Devices = s.db.phonesOf(p.Num)
	return p
}

type reportCount struct {
	n       int
	latest  string
	flagged int
}

// reportCounts is the number of reports, the date of the latest and its flagged items.
func (d *DB) reportCounts() (reportCount, error) {
	var c reportCount
	err := d.sql.QueryRow(`SELECT COUNT(DISTINCT report_id), COALESCE(MAX(date),'') FROM checkup`).Scan(&c.n, &c.latest)
	if err != nil || c.n == 0 {
		return c, err
	}
	err = d.sql.QueryRow(`SELECT COUNT(*) FROM checkup WHERE flag IN ('high','low','H','L','abnormal')
		AND report_id = (SELECT report_id FROM checkup ORDER BY date DESC LIMIT 1)`).Scan(&c.flagged)
	return c, err
}

// people is everyone with their counts.
func (s *Server) people() []Person {
	list, _ := s.db.PeopleList()
	for i := range list {
		list[i] = s.summary(list[i])
	}
	return list
}

// ---------------------------------------------------------------- pairing

// pairPerson decides whom a phone that pairs belongs to, and whether it may manage the
// family, from the person id and name it sent (see docs/people-api.md):
//  1. a known person id: that person;
//  2. a phone paired before: its person (the id becomes theirs too);
//  3. nobody has taken the owner yet and no other phone belongs to them: the owner;
//  4. else a new person.
//
// The owner's phone may manage the family; other phones may not. A phone paired again keeps
// what it had.
func (s *Server) pairPerson(req pairRequest) (n int64, family bool, err error) {
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	old, known := s.db.DeviceByDeviceID(req.DeviceID)
	if known && old.Scope == scopeDevice && old.PersonNum == 0 {
		known = false // its person was deleted
	}
	keep := func(n int64) (int64, bool, error) {
		if known && old.PersonNum == n {
			return n, old.Family, nil
		}
		return n, n == ownerNum, nil
	}
	ownerFree := !s.db.ownerClaimed() && s.db.phonesOf(ownerNum) == 0
	key := req.PersonID
	if key == "" {
		// an app that knows nothing of people: it keeps the person it had, or takes the
		// owner while no phone has. Any other phone is someone new, without the family:
		// a relative's phone on an old app must not see everyone.
		if known {
			return keep(old.PersonNum)
		}
		if ownerFree {
			return ownerNum, true, nil
		}
		name := req.Device
		p, err := s.db.addPerson("srv-"+randomHex(8), personFields{Name: &name})
		if err != nil {
			return 0, false, err
		}
		return p.Num, false, nil
	}
	if p, ok := s.db.personByKey(key); ok {
		return keep(p.Num)
	}
	if known {
		return old.PersonNum, old.Family, s.db.addPersonKey(key, old.PersonNum)
	}
	if ownerFree {
		if err := s.db.addPersonKey(key, ownerNum); err != nil {
			return 0, false, err
		}
		if req.PersonName != "" {
			name := req.PersonName
			_ = s.db.updatePerson(ownerNum, personFields{Name: &name})
		}
		return ownerNum, true, nil
	}
	name := firstNonEmpty(req.PersonName, req.Device)
	p, err := s.db.addPerson(key, personFields{Name: &name})
	if err != nil {
		return 0, false, err
	}
	return p.Num, false, nil
}

// pairPersonName is who a pairing request says it is for, for the "allow?" dialog.
func (s *Server) pairPersonName(req pairRequest) string {
	if req.PersonID != "" {
		if p, ok := s.db.personByKey(req.PersonID); ok {
			return personLabel(p)
		}
	}
	return req.PersonName
}

// personJSON is a person as the app's API shows it.
func personJSON(p Person) map[string]any {
	return map[string]any{"id": p.ID, "name": p.Name, "owner": p.Owner}
}

// ---------------------------------------------------------------- the app's API

// GET /people: the people this token may see.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authDevice(r)
	if !ok {
		writeJSON(w, 401, map[string]any{"error": "bad token", "reason": "bad_token"})
		return
	}
	out := []Person{}
	for _, p := range s.people() {
		if !canAccess(dev, p.Num) {
			continue
		}
		p.Me = p.Num == dev.PersonNum
		out = append(out, p)
	}
	writeJSON(w, 200, map[string]any{"people": out})
}

// PUT /people/{id}: add a person or change one.
func (s *Server) handlePutPerson(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/people")
	if !ok {
		return
	}
	key := r.PathValue("id")
	var f personFields
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&f); err != nil && err != io.EOF {
		errJSON(w, 400, "bad JSON: "+err.Error())
		return
	}
	if err := f.check(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error(), "reason": "bad_field"})
		return
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	p, found := s.db.personByKey(key)
	if found {
		if !canAccess(dev, p.Num) {
			writeJSON(w, 403, map[string]any{"error": "this phone may only change its own person", "reason": "person_not_allowed"})
			return
		}
		if err := s.db.updatePerson(p.Num, f); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		p, _ = s.db.personByNum(p.Num)
		writeJSON(w, 200, map[string]any{"person": s.summary(p), "created": false})
		return
	}
	if !dev.Family {
		writeJSON(w, 403, map[string]any{"error": "this phone may not add people; turn on \"Can manage the family\" for it in the dashboard", "reason": "person_not_allowed"})
		return
	}
	if !validPersonKey(key) || key == ownerKey {
		writeJSON(w, 400, map[string]any{"error": "a person id has 1 to 128 letters, digits and . _ : -", "reason": "bad_id"})
		return
	}
	p, err := s.db.addPerson(key, f)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if _, err := s.personDB(p.Num); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	s.db.Log(logPerson(LogEntry{Kind: "pair", Device: dev.Name, IP: clientIP(r), OK: true, Message: "added " + personLabel(p)}, p))
	writeJSON(w, 200, map[string]any{"person": s.summary(p), "created": true})
}

// DELETE /people/{id}: a family phone deletes a person and all their data.
func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/people")
	if !ok {
		return
	}
	if !dev.Family {
		writeJSON(w, 403, map[string]any{"error": "only a phone that may manage the family can delete people", "reason": "person_not_allowed"})
		return
	}
	p, found := s.db.personByKey(r.PathValue("id"))
	if !found {
		writeJSON(w, 404, map[string]any{"error": "no such person", "reason": "unknown_person"})
		return
	}
	if err := s.deletePerson(p); err != nil {
		s.peopleErr(w, err)
		return
	}
	s.db.Log(LogEntry{Kind: "pair", Device: dev.Name, IP: clientIP(r), OK: true, Message: "deleted " + personLabel(p) + " and their data"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /people/me {"id","name"}: a phone paired before the app knew about people tells
// which person id is its own.
func (s *Server) handleClaimPerson(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/people/me")
	if !ok {
		return
	}
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	req.ID = strings.TrimSpace(req.ID)
	if !validPersonKey(req.ID) || req.ID == ownerKey {
		writeJSON(w, 400, map[string]any{"error": "a person id has 1 to 128 letters, digits and . _ : -", "reason": "bad_id"})
		return
	}
	n := dev.PersonNum
	if dev.ID == 0 || n == 0 {
		writeJSON(w, 409, map[string]any{"error": "this token belongs to no person", "reason": "no_person"})
		return
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	if p, found := s.db.personByKey(req.ID); found && p.Num != n {
		writeJSON(w, 409, map[string]any{"error": "this person id belongs to someone else on this server; the server owner can move this phone in the dashboard", "reason": "person_taken"})
		return
	}
	if err := s.db.addPersonKey(req.ID, n); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if name := cleanName(req.Name); name != "" {
		if p, _ := s.db.personByNum(n); p.Name == "" && utf8.RuneCountInString(name) <= 40 {
			_ = s.db.updatePerson(n, personFields{Name: &name})
		}
	}
	p, _ := s.db.personByNum(n)
	p.Me = true
	writeJSON(w, 200, map[string]any{"person": s.summary(p)})
}

// ---------------------------------------------------------------- deleting and merging

var (
	errOwner      = errors.New("the server owner cannot be deleted or merged into someone else")
	errHasPhones  = errors.New("a phone still belongs to this person; move or revoke it first")
	errSamePerson = errors.New("a person cannot be merged into themselves")
)

func (s *Server) peopleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOwner):
		writeJSON(w, 409, map[string]any{"error": err.Error(), "reason": "owner"})
	case errors.Is(err, errHasPhones):
		writeJSON(w, 409, map[string]any{"error": err.Error(), "reason": "person_has_devices"})
	case errors.Is(err, errSamePerson), errors.Is(err, errUnknownPerson):
		writeJSON(w, 400, map[string]any{"error": err.Error(), "reason": "bad_request"})
	default:
		errJSON(w, 500, err.Error())
	}
}

// deletePerson deletes a person and their folder. Their AI tokens are revoked.
func (s *Server) deletePerson(p Person) error {
	if p.Owner {
		return errOwner
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	if s.db.phonesOf(p.Num) > 0 {
		return errHasPhones
	}
	s.closePersonDB(p.Num)
	if err := os.RemoveAll(personDir(s.cfg.DataDir, p.Num)); err != nil {
		return err
	}
	return s.db.forgetPerson(p.Num)
}

// forgetPerson removes person n from the list; their devices belong to nobody.
func (d *DB) forgetPerson(n int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, q := range []string{
		`UPDATE devices SET revoked = 1 WHERE person = ? AND scope = 'mcp'`,
		`UPDATE devices SET person = NULL, family = 0 WHERE person = ?`,
		`DELETE FROM person_keys WHERE person = ?`,
		`DELETE FROM people WHERE id = ?`,
	} {
		if _, err := d.sql.Exec(q, n); err != nil {
			return err
		}
	}
	return nil
}

// mergePeople moves everything of person from into person into, and from's ids and
// devices with it: for the same human paired twice, or added on the phone and paired too.
func (s *Server) mergePeople(from, into Person) error {
	if from.Num == into.Num {
		return errSamePerson
	}
	if from.Owner {
		return errOwner
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	dst, err := s.personDB(into.Num)
	if err != nil {
		return err
	}
	src, err := s.personDB(from.Num)
	if err != nil {
		return err
	}
	srcDir, dstDir := personDir(s.cfg.DataDir, from.Num), personDir(s.cfg.DataDir, into.Num)
	// the report folders first, so the database can point at where they are now
	moved := map[string]string{} // old folder path → new
	reps, err := src.ReportFolders()
	if err != nil {
		return err
	}
	for _, rf := range reps {
		name := filepath.Base(rf.Folder)
		to := filepath.Join(dstDir, "reports", name)
		if _, err := os.Stat(to); err == nil {
			to = filepath.Join(dstDir, "reports", name+"-"+strconv.FormatInt(from.Num, 10))
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := os.Rename(rf.Folder, to); err != nil && !os.IsNotExist(err) {
			return err
		}
		moved[rf.Folder] = to
	}
	for _, sub := range []string{"chats", "settings"} {
		if err := moveFiles(filepath.Join(srcDir, sub), filepath.Join(dstDir, sub)); err != nil {
			return err
		}
	}
	srcPath := src.path
	s.closePersonDB(from.Num)
	if err := dst.absorb(srcPath, moved); err != nil {
		return err
	}
	if err := s.db.movePerson(from.Num, into.Num, from.ID); err != nil {
		return err
	}
	return os.RemoveAll(srcDir)
}

// moveFiles moves the files of dir into to, keeping both when a name is taken.
func moveFiles(dir, to string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(to, e.Name())
		if _, err := os.Stat(dst); err == nil {
			ext := filepath.Ext(e.Name())
			dst = filepath.Join(to, strings.TrimSuffix(e.Name(), ext)+"-merged-"+randomHex(3)+ext)
		}
		if err := os.Rename(filepath.Join(dir, e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// absorb copies the samples and reports of the database at path into d. Samples already in
// d stay as they are; a report in both keeps d's. folders maps the report folders that
// moved to where they are now.
func (d *DB) absorb(path string, folders map[string]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx := context.Background()
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS src`, path); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE src`)
	const recCols = `type, start, "end", value, num, unit, source, received, uuid, source_bundle, source_version,
		device_model, device_software, user_entered, tz, metadata, deleted_at`
	const repCols = `id, date, institution, items, folder, received, title, originals`
	steps := []string{
		`BEGIN IMMEDIATE`,
		`INSERT OR IGNORE INTO records (` + recCols + `) SELECT ` + recCols + ` FROM src.records`,
		`INSERT INTO checkup (` + checkupCols + `) SELECT ` + checkupCols + ` FROM src.checkup
			WHERE report_id NOT IN (SELECT report_id FROM main.checkup) AND report_id NOT IN (SELECT id FROM main.reports)`,
		`INSERT OR IGNORE INTO reports (` + repCols + `) SELECT ` + repCols + ` FROM src.reports`,
	}
	for _, q := range steps {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return fmt.Errorf("merging: %w", err)
		}
	}
	for old, now := range folders {
		if _, err := conn.ExecContext(ctx, `UPDATE reports SET folder = ? WHERE folder = ?`, now, old); err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	d.recordsChanged()
	return nil
}

// movePerson gives person from's ids, devices and history to person into, and removes from.
func (d *DB) movePerson(from, into int64, fromKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, q := range []string{
		`UPDATE person_keys SET person = ? WHERE person = ?`,
		`UPDATE devices SET person = ? WHERE person = ?`,
		`UPDATE sync_log SET person_num = ? WHERE person_num = ?`,
	} {
		if _, err := d.sql.Exec(q, into, from); err != nil {
			return err
		}
	}
	if _, err := d.sql.Exec(`DELETE FROM people WHERE id = ?`, from); err != nil {
		return err
	}
	// an owner no phone had taken yet takes the id of the person merged into them
	_, err := d.sql.Exec(`UPDATE people SET key = ? WHERE id = ? AND key = ?`, fromKey, into, ownerKey)
	return err
}

// ---------------------------------------------------------------- the dashboard's API

// dashPerson is the person a dashboard call names (?person=), the owner without one.
func (s *Server) dashPerson(w http.ResponseWriter, r *http.Request) (Person, *DB, bool) {
	key := firstNonEmpty(r.URL.Query().Get("person"), ownerKey)
	p, ok := s.db.personByKey(key)
	if !ok {
		writeJSON(w, 404, map[string]any{"error": "no such person", "reason": "unknown_person"})
		return p, nil, false
	}
	db, err := s.personDB(p.Num)
	if err != nil {
		errJSON(w, 500, err.Error())
		return p, nil, false
	}
	return p, db, true
}

func (s *Server) apiPeople(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.people())
}

// POST /api/people {name, relation, birth, sex}: add a person on the computer.
func (s *Server) apiNewPerson(w http.ResponseWriter, r *http.Request) {
	var f personFields
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&f)
	p, err := s.NewPerson(f)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

// NewPerson adds a person made on the computer, with an id of the server's own.
func (s *Server) NewPerson(f personFields) (Person, error) {
	if err := f.check(); err != nil {
		return Person{}, err
	}
	if f.Name == nil || *f.Name == "" {
		return Person{}, fmt.Errorf("a person needs a name")
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	p, err := s.db.addPerson("srv-"+randomHex(8), f)
	if err != nil {
		return p, err
	}
	_, err = s.personDB(p.Num)
	return s.summary(p), err
}

// PATCH /api/people/{id} {name, relation, birth, sex}
func (s *Server) apiUpdatePerson(w http.ResponseWriter, r *http.Request) {
	p, ok := s.db.personByKey(r.PathValue("id"))
	if !ok {
		errJSON(w, 404, "no such person")
		return
	}
	var f personFields
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&f); err != nil {
		errJSON(w, 400, "bad JSON")
		return
	}
	if err := f.check(); err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	if err := s.db.updatePerson(p.Num, f); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/people/{id}/merge {"into": "<id>"}
func (s *Server) apiMergePerson(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Into string `json:"into"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	if err := s.MergePeople(r.PathValue("id"), req.Into); err != nil {
		s.peopleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// MergePeople merges the person with id from into the one with id into.
func (s *Server) MergePeople(from, into string) error {
	a, ok := s.db.personByKey(from)
	b, ok2 := s.db.personByKey(into)
	if !ok || !ok2 {
		return errUnknownPerson
	}
	if err := s.mergePeople(a, b); err != nil {
		return err
	}
	s.db.Log(LogEntry{Kind: "pair", OK: true, Message: "merged " + personLabel(a) + " into " + personLabel(b)})
	return nil
}

// DELETE /api/people/{id}
func (s *Server) apiDeletePerson(w http.ResponseWriter, r *http.Request) {
	if err := s.DeletePerson(r.PathValue("id")); err != nil {
		s.peopleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// DeletePerson deletes the person with this id and all their data.
func (s *Server) DeletePerson(id string) error {
	p, ok := s.db.personByKey(id)
	if !ok {
		return errUnknownPerson
	}
	if err := s.deletePerson(p); err != nil {
		return err
	}
	s.db.Log(LogEntry{Kind: "pair", OK: true, Message: "deleted " + personLabel(p) + " and their data in the dashboard"})
	return nil
}

// SetDevicePerson moves a device to the person with this id. "" lets an AI token see
// everyone again; a phone always belongs to someone.
func (s *Server) SetDevicePerson(id int64, person string) error {
	if person == "" {
		return s.db.SetDeviceEveryone(id)
	}
	p, ok := s.db.personByKey(person)
	if !ok {
		return errUnknownPerson
	}
	return s.db.SetDevicePerson(id, p.Num)
}
