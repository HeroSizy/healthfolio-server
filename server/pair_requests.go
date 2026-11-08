package server

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/herosizy/healthsync-server/model"
)

// Pairing without a code: the phone asks, somebody allows it on the computer (a native
// dialog on macOS, the desktop app or the web dashboard). Both sides show the same short
// confirmation code, so the person can tell their phone's request from someone else's.
//
//	POST   /pair/requests        → 202 {id, confirm, expires_in}
//	GET    /pair/requests/{id}   → pending | approved (with the token, once) | denied | expired
//	DELETE /pair/requests/{id}   → the phone gave up
//
// The older POST /pair without a code does the same in one long request.

const (
	requestTTL      = 120 * time.Second // how long a request waits for an answer
	requestKeep     = 60 * time.Second  // how long an answer stays readable after that
	maxOpenRequests = 3                 // across all addresses
	rateWindow      = 10 * time.Minute
	rateMax         = 6 // requests per address per window
)

var (
	errBusy        = errors.New("too many pairing requests are waiting")
	errRateLimited = errors.New("too many pairing requests from this address")
)

const (
	reqPending  = "pending"
	reqApproved = "approved"
	reqDenied   = "denied"
	reqExpired  = "expired"
)

type PendingPair struct {
	ID       string    `json:"id"`
	Device   string    `json:"device"`
	DeviceID string    `json:"-"`
	IP       string    `json:"ip"`
	Confirm  string    `json:"confirm"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`

	status string
	done   chan struct{} // closed once answered
	cancel context.CancelFunc
}

type requests struct {
	mu    sync.Mutex
	byID  map[string]*PendingPair
	hits  map[string][]time.Time // per address, for rate limiting
	now   func() time.Time
	popup func(ctx context.Context, pp *PendingPair) (allowed, answered bool)
}

func newRequests() requests {
	return requests{byID: map[string]*PendingPair{}, hits: map[string][]time.Time{}, now: time.Now, popup: macConfirm}
}

// statusLocked is the request's state, counting the clock.
func (q *requests) statusLocked(pp *PendingPair) string {
	if pp.status == reqPending && !q.now().Before(pp.Expires) {
		return reqExpired
	}
	return pp.status
}

func (q *requests) purgeLocked() {
	now := q.now()
	for id, pp := range q.byID {
		if now.After(pp.Expires.Add(requestKeep)) {
			pp.cancel()
			delete(q.byID, id)
		}
	}
	for ip, ts := range q.hits {
		keep := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < rateWindow {
				keep = append(keep, t)
			}
		}
		if len(keep) == 0 {
			delete(q.hits, ip)
		} else {
			q.hits[ip] = keep
		}
	}
}

// Request opens a request to pair, or returns the open one from the same phone (same
// device id and address), so that retrying does not pop up a second dialog. retry is how
// long to wait when the request is refused for being too frequent.
func (p *Pairing) Request(device, deviceID, ip string, macDialog bool) (pp *PendingPair, retry time.Duration, err error) {
	q := &p.rq
	q.mu.Lock()
	defer q.mu.Unlock()
	q.purgeLocked()
	open := 0
	for _, o := range q.byID {
		if q.statusLocked(o) != reqPending {
			continue
		}
		if deviceID != "" && o.DeviceID == deviceID && o.IP == ip {
			return o, 0, nil
		}
		open++
	}
	if hits := q.hits[ip]; len(hits) >= rateMax {
		return nil, rateWindow - q.now().Sub(hits[0]), errRateLimited
	}
	if open >= maxOpenRequests {
		return nil, 30 * time.Second, errBusy
	}
	now := q.now()
	q.hits[ip] = append(q.hits[ip], now)
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(requestTTL))
	pp = &PendingPair{ID: newToken()[:16], Device: device, DeviceID: deviceID, IP: ip, Confirm: randomDigits(4),
		Created: now, Expires: now.Add(requestTTL), status: reqPending, done: make(chan struct{}), cancel: cancel}
	q.byID[pp.ID] = pp
	if macDialog && runtime.GOOS == "darwin" && q.popup != nil {
		go func() {
			// Only a real click counts. If the dialog could not be shown (no display, a
			// headless Mac mini, the screen locked), the request still waits in the
			// desktop app and the dashboard.
			if ok, answered := q.popup(ctx, pp); answered {
				p.Answer(pp.ID, ok)
			}
		}()
	}
	return pp, 0, nil
}

// Answer allows or denies a waiting request. It is false when there is no such request
// or it was already answered or expired.
func (p *Pairing) Answer(id string, allow bool) bool {
	q := &p.rq
	q.mu.Lock()
	defer q.mu.Unlock()
	pp, ok := q.byID[id]
	if !ok || q.statusLocked(pp) != reqPending {
		return false
	}
	pp.status = reqDenied
	if allow {
		pp.status = reqApproved
	}
	close(pp.done)
	pp.cancel() // closes the dialog if it is still up
	return true
}

// Withdraw is the phone giving up: the request goes away and its dialog closes.
func (p *Pairing) Withdraw(id string) bool {
	q := &p.rq
	q.mu.Lock()
	defer q.mu.Unlock()
	pp, ok := q.byID[id]
	if ok {
		pp.cancel()
		delete(q.byID, id)
	}
	return ok
}

// Wait waits up to d (or until ctx ends) for the request to be answered and returns its
// state. found is false for an unknown request.
func (p *Pairing) Wait(ctx context.Context, id string, d time.Duration) (pp *PendingPair, status string, found bool) {
	q := &p.rq
	q.mu.Lock()
	pp, found = q.byID[id]
	if !found {
		q.mu.Unlock()
		return nil, "", false
	}
	status = q.statusLocked(pp)
	left := pp.Expires.Sub(q.now())
	q.mu.Unlock()
	if status != reqPending {
		return pp, status, true
	}
	t := time.NewTimer(min(d, left))
	defer t.Stop()
	select {
	case <-pp.done:
	case <-t.C:
	case <-ctx.Done():
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return pp, q.statusLocked(pp), true
}

// Claim hands out an approved request once: it is true the first time and removes the
// request, so its token can't be fetched twice.
func (p *Pairing) Claim(id string) bool {
	q := &p.rq
	q.mu.Lock()
	defer q.mu.Unlock()
	pp, ok := q.byID[id]
	if !ok || pp.status != reqApproved {
		return false
	}
	delete(q.byID, id)
	return true
}

// Pending lists the requests still waiting for an answer, oldest first.
func (p *Pairing) Pending() []*PendingPair {
	q := &p.rq
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []*PendingPair{}
	for _, pp := range q.byID {
		if q.statusLocked(pp) == reqPending {
			out = append(out, pp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (p *Pairing) PendingInfo() []model.PendingPair {
	out := []model.PendingPair{}
	for _, pp := range p.Pending() {
		out = append(out, model.PendingPair{ID: pp.ID, Device: pp.Device, IP: pp.IP, Confirm: pp.Confirm,
			Created: pp.Created.Format(time.RFC3339), Expires: pp.Expires.Format(time.RFC3339)})
	}
	return out
}

// macConfirm shows the native dialog. answered is false when nobody clicked: it gave up,
// was closed because the request was answered elsewhere, or could not be shown at all.
func macConfirm(ctx context.Context, pp *PendingPair) (allowed, answered bool) {
	device := strings.NewReplacer(`"`, "'", `\`, "").Replace(pp.Device)
	wait := int(time.Until(pp.Expires).Seconds())
	if wait < 1 {
		return false, false
	}
	script := fmt.Sprintf(`display dialog "“%[1]s” wants to sync health data to this computer.\n`+
		`Allow only if your phone shows the code %[2]s.\n\n`+
		`「%[1]s」想把健康数据同步到这台电脑。\n手机上显示的确认码是 %[2]s 时再点允许。" `+
		`with title "HealthSync · %[2]s" buttons {"Deny 拒绝", "Allow 允许"} default button "Deny 拒绝" with icon note giving up after %[3]d`,
		device, pp.Confirm, wait)
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		if ctx.Err() == nil {
			logf("pairing dialog could not be shown: %v", err)
		}
		return false, false
	}
	s := string(out)
	if strings.Contains(s, "gave up:true") {
		return false, false
	}
	return strings.Contains(s, "Allow"), true
}
