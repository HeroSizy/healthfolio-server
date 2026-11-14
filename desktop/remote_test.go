package desktop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/herosizy/healthsync-server/server"
)

// remoteFor starts a real server on a fresh data folder and a Remote reading it, as the
// web dashboard does from a browser on the same computer.
func remoteFor(t *testing.T) (*Remote, *httptest.Server, *server.DB) {
	t.Helper()
	dir := t.TempDir()
	cfg := &server.Config{DataDir: dir, Name: "test box", PublicURL: "http://192.168.1.2:8787", DashboardLAN: true, NoDialog: true}
	db, err := server.OpenDB(filepath.Join(dir, "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := server.NewServer(cfg, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return NewRemote(ts.URL), ts, db
}

// changed waits for a background request to finish.
func changed(r *Remote) func(t *testing.T) {
	var mu sync.Mutex
	n := 0
	r.Changed = func() { mu.Lock(); n++; mu.Unlock() }
	return func(t *testing.T) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			done := n > 0
			n = 0
			mu.Unlock()
			if done {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the background request never finished")
	}
}

func TestRemoteReadsAndChanges(t *testing.T) {
	r, _, db := remoteFor(t)
	var failed []error
	r.Failed = func(err error) { failed = append(failed, err) }
	wait := changed(r)

	if _, _, err := db.InsertRecords([]server.Record{{Type: "HKQuantityTypeIdentifierStepCount", Start: "2026-11-01T08:00:00+08:00",
		End: "2026-11-01T08:10:00+08:00", Value: "500", Unit: "count", Source: "phone"}}); err != nil {
		t.Fatal(err)
	}
	o := r.Overview()
	if o.Name != "test box" || o.Stats.Total != 1 || !o.MCPEnabled {
		t.Fatalf("overview: %+v", o)
	}
	if d, err := r.Daily("HKQuantityTypeIdentifierStepCount", 3650); err != nil || len(d) != 1 || d[0].Total != 500 {
		t.Fatalf("daily: %+v %v", d, err)
	}

	// pairing: a code, then cancelled in the background
	p, err := r.NewPairCode()
	if err != nil || len(p.Code) != 6 || !strings.HasPrefix(p.Link, "healthsync://pair?") {
		t.Fatalf("pair code: %+v %v", p, err)
	}
	if cur, ok := r.CurrentPair(); !ok || cur.Code != p.Code {
		t.Fatalf("current pair: %+v %v", cur, ok)
	}
	if pend := r.Pending(); pend == nil || len(pend) != 0 {
		t.Fatalf("pending: %v", pend)
	}
	r.CancelPair()
	wait(t)
	if _, ok := r.CurrentPair(); ok {
		t.Fatal("the code is still there after CancelPair")
	}

	// an AI token, renamed, revoked and deleted, each in the background
	m, err := r.NewMCPToken("Claude")
	if err != nil || m.Token == "" || !strings.HasSuffix(m.URL, "/mcp") {
		t.Fatalf("mcp token: %+v %v", m, err)
	}
	devs, err := r.Devices()
	if err != nil || len(devs) != 1 || devs[0].Scope != "mcp" {
		t.Fatalf("devices: %+v %v", devs, err)
	}
	id := devs[0].ID
	if err := r.RenameDevice(id, strings.Repeat("x", 65)); err == nil {
		t.Fatal("a 65-character name was accepted")
	}
	if err := r.RenameDevice(id, "Claude Desktop"); err != nil {
		t.Fatal(err)
	}
	wait(t)
	r.RevokeDevice(id)
	wait(t)
	if devs, _ = r.Devices(); devs[0].Name != "Claude Desktop" || !devs[0].Revoked {
		t.Fatalf("after rename and revoke: %+v", devs[0])
	}
	r.DeleteDevice(id)
	wait(t)
	if devs, _ = r.Devices(); len(devs) != 0 {
		t.Fatalf("after delete: %+v", devs)
	}

	// AI access off: shown off at once, and the server agrees
	r.SetMCPEnabled(false)
	if r.Overview().MCPEnabled {
		t.Fatal("MCP still shown on right after turning it off")
	}
	wait(t)
	if r.Overview().MCPEnabled || db.MCPEnabled() {
		t.Fatal("MCP still on")
	}

	if logs, err := r.Logs(50, 0, "pair"); err != nil || len(logs) == 0 {
		t.Fatalf("logs: %v %v", logs, err)
	}
	if reps, err := r.Reports(); err != nil || reps == nil && err != nil {
		t.Fatalf("reports: %v %v", reps, err)
	}
	r.LoadSkill()
	for i := 0; i < 100 && r.SkillMD() == ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(r.SkillMD(), "name:") {
		t.Fatalf("skill: %.80q", r.SkillMD())
	}
	if len(failed) > 0 {
		t.Fatalf("failures: %v", failed)
	}
}

// A server that can't be reached is reported, and the view gets empty data, not a hang.
func TestRemoteUnreachable(t *testing.T) {
	r, ts, _ := remoteFor(t)
	ts.Close()
	var failed error
	r.Failed = func(err error) { failed = err }
	if o := r.Overview(); o.Name != "" || failed == nil {
		t.Fatalf("overview of a stopped server: %+v %v", o, failed)
	}
	if _, err := r.Devices(); err == nil {
		t.Fatal("devices of a stopped server")
	}
}

// A report and a page of its original, as the window reads them in a browser.
func TestRemoteReportPage(t *testing.T) {
	r, ts, _ := remoteFor(t)
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil)
	body := `{"folder":"f","report":{"id":"R9","date":"2026-11-03","institution":"Lab","items":[{"name":"LDL","value":3.9,"flag":"high",
		"location":{"file":"photo.jpg","page":0,"x":0.1,"y":0.2,"w":0.5,"h":0.1}}]},
		"files":[{"name":"photo.jpg","data":"` + base64.StdEncoding.EncodeToString(jb.Bytes()) + `"}]}`
	req, _ := http.NewRequest("POST", ts.URL+"/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer master")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("report: %v %v", res, err)
	}
	d, err := r.ReportDetail("R9")
	if err != nil || len(d.Report.Items) != 1 || d.Report.Items[0].Location.File != "photo.jpg" || len(d.Files) != 1 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	if valueText(d.Report.Items[0].Value) != "3.9" || !flagged.MatchString(d.Report.Items[0].Flag) {
		t.Fatalf("item: %+v", d.Report.Items[0])
	}
	img, err := r.ReportPage("R9", "photo.jpg", 0, 800)
	if err != nil || img.Pages != 1 || img.Type != "image/jpeg" || !bytes.Equal(img.Data, jb.Bytes()) {
		t.Fatalf("page: %v %v", img.Type, err)
	}
	if _, err := r.ReportPage("R9", "photo.jpg", 3, 800); err == nil {
		t.Fatal("page 4 of a photo")
	}
	if _, err := r.ReportDetail("nope"); err == nil {
		t.Fatal("unknown report")
	}
}
