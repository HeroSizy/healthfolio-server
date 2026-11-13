package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/herosizy/healthsync-server/model"
)

// Remote is a Backend that talks to a running server over its dashboard API: the same
// window, compiled to WebAssembly, is the web dashboard.
//
// In a browser the view must never wait for the network: MyGo runs it on Go's main
// goroutine while JavaScript waits for it, and a fetch could then never finish. So the
// calls the view makes (revoke, rename, allow, …) send their request in the background,
// answer at once, and call Changed when the server has answered; the reads are made by
// App.load, off the main goroutine.
type Remote struct {
	Base    string // the server, e.g. "http://192.168.1.10:8787"; "" for the page's own
	Changed func() // called when a background request is done, to load again
	Failed  func(error)

	client http.Client
	mu     sync.Mutex
	skill  string
	mcpOn  *bool // what SetMCPEnabled asked for, until the server says so too
}

func NewRemote(base string) *Remote {
	return &Remote{Base: strings.TrimSuffix(base, "/"), client: http.Client{Timeout: 30 * time.Second}}
}

func (r *Remote) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.Base+path, rd)
	if err != nil {
		return err
	}
	// the dashboard refuses state changes without it (a cross-site form can't send it)
	req.Header.Set("X-HealthSync", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if out == nil {
		return nil
	}
	if s, ok := out.(*string); ok {
		*s = string(data)
		return nil
	}
	return json.Unmarshal(data, out)
}

// background sends a request without waiting, then reports how it went.
func (r *Remote) background(method, path string, body any) {
	go func() {
		err := r.do(method, path, body, nil)
		if err != nil && r.Failed != nil {
			r.Failed(err)
		}
		if r.Changed != nil {
			r.Changed()
		}
	}()
}

func (r *Remote) Overview() model.Overview {
	var o model.Overview
	if err := r.do("GET", "/api/overview", nil, &o); err != nil && r.Failed != nil {
		r.Failed(err)
	}
	r.mu.Lock()
	if r.mcpOn != nil {
		if *r.mcpOn == o.MCPEnabled {
			r.mcpOn = nil
		} else {
			o.MCPEnabled = *r.mcpOn
		}
	}
	r.mu.Unlock()
	return o
}

func (r *Remote) Logs(limit int, before int64, kind string) ([]model.LogEntry, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "kind": {kind}}
	if before > 0 {
		q.Set("before", strconv.FormatInt(before, 10))
	}
	var out []model.LogEntry
	return out, r.do("GET", "/api/logs?"+q.Encode(), nil, &out)
}

func (r *Remote) Devices() ([]model.Device, error) {
	var out []model.Device
	return out, r.do("GET", "/api/devices", nil, &out)
}

func (r *Remote) RevokeDevice(id int64) error {
	r.background("POST", "/api/devices/"+strconv.FormatInt(id, 10)+"/revoke", nil)
	return nil
}

func (r *Remote) DeleteDevice(id int64) error {
	r.background("DELETE", "/api/devices/"+strconv.FormatInt(id, 10), nil)
	return nil
}

func (r *Remote) RenameDevice(id int64, name string) error {
	if n := len([]rune(strings.TrimSpace(name))); n == 0 || n > 64 {
		return fmt.Errorf("a device name needs 1 to 64 characters")
	}
	r.background("PATCH", "/api/devices/"+strconv.FormatInt(id, 10), map[string]string{"name": name})
	return nil
}

func (r *Remote) SetMCPEnabled(on bool) error {
	r.mu.Lock()
	r.mcpOn = &on
	r.mu.Unlock()
	r.background("POST", "/api/mcp", map[string]bool{"enabled": on})
	return nil
}

// SkillMD is fetched once, by LoadSkill, before anyone can ask for it.
func (r *Remote) SkillMD() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skill
}

// LoadSkill fetches SKILL.md in the background.
func (r *Remote) LoadSkill() {
	go func() {
		var s string
		if r.do("GET", "/skill.md", nil, &s) == nil {
			r.mu.Lock()
			r.skill = s
			r.mu.Unlock()
		}
	}()
}

func (r *Remote) Reports() ([]model.ReportRow, error) {
	var out []model.ReportRow
	return out, r.do("GET", "/api/reports", nil, &out)
}

func (r *Remote) Daily(typ string, days int) ([]model.DailyPoint, error) {
	var out []model.DailyPoint
	q := url.Values{"type": {typ}, "days": {strconv.Itoa(days)}}
	return out, r.do("GET", "/api/daily?"+q.Encode(), nil, &out)
}

type pairState struct {
	Active  *model.PairInfo     `json:"active"`
	Pending []model.PendingPair `json:"pending"`
}

func (r *Remote) NewPairCode() (model.PairInfo, error) {
	var p pairState
	if err := r.do("POST", "/api/pair", nil, &p); err != nil {
		return model.PairInfo{}, err
	}
	if p.Active == nil {
		return model.PairInfo{}, fmt.Errorf("no pairing code")
	}
	return *p.Active, nil
}

func (r *Remote) pair() pairState {
	var p pairState
	_ = r.do("GET", "/api/pair", nil, &p)
	return p
}

func (r *Remote) CurrentPair() (model.PairInfo, bool) {
	p := r.pair()
	if p.Active == nil {
		return model.PairInfo{}, false
	}
	return *p.Active, true
}

func (r *Remote) CancelPair() { r.background("DELETE", "/api/pair", nil) }

func (r *Remote) Pending() []model.PendingPair {
	p := r.pair().Pending
	if p == nil {
		p = []model.PendingPair{}
	}
	return p
}

func (r *Remote) AnswerPending(id string, allow bool) bool {
	r.background("POST", "/api/pair/pending/"+url.PathEscape(id), map[string]bool{"allow": allow})
	return true
}

func (r *Remote) NewMCPToken(name string) (model.MCPConnect, error) {
	var m model.MCPConnect
	return m, r.do("POST", "/api/devices", map[string]string{"name": name}, &m)
}

func (r *Remote) DashboardURL() string { return r.Base + "/" }
