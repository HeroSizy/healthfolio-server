package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	pairTTL      = 10 * time.Minute
	pairMaxTries = 5
	dialogWait   = 120 * time.Second
)

// PairCode is a one-time 6-digit code. It lives in a file so that `healthsync pair`
// (a separate process) and the running server see the same code.
type PairCode struct {
	Code    string  `json:"code"`
	Expires float64 `json:"expires"` // unix seconds, same as receiver.py
	Tries   int     `json:"tries"`
}

type Pairing struct {
	file string
	mu   sync.Mutex

	// approvals waiting for someone to click Allow (macOS dialog or the dashboard)
	pmu     sync.Mutex
	pending map[string]*PendingPair
}

type PendingPair struct {
	ID      string    `json:"id"`
	Device  string    `json:"device"`
	IP      string    `json:"ip"`
	Created time.Time `json:"created"`
	answer  chan bool
}

func NewPairing(dataDir string) *Pairing {
	return &Pairing{file: filepath.Join(dataDir, "pair-code.json"), pending: map[string]*PendingPair{}}
}

func (p *Pairing) New() (PairCode, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	c := PairCode{Code: fmt.Sprintf("%06d", n.Int64()), Expires: float64(time.Now().Add(pairTTL).Unix())}
	b, _ := json.Marshal(c)
	return c, os.WriteFile(p.file, b, 0o600)
}

// Current returns the active code, if any.
func (p *Pairing) Current() (PairCode, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.read()
	return c, ok
}

func (p *Pairing) read() (PairCode, bool) {
	var c PairCode
	b, err := os.ReadFile(p.file)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	if float64(time.Now().Unix()) > c.Expires || c.Tries >= pairMaxTries {
		os.Remove(p.file)
		return c, false
	}
	return c, true
}

func (p *Pairing) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	os.Remove(p.file)
}

// Check consumes the code if it matches; wrong guesses count towards pairMaxTries.
func (p *Pairing) Check(code string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.read()
	if !ok {
		return false
	}
	code = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	if subtle.ConstantTimeCompare([]byte(code), []byte(c.Code)) == 1 {
		os.Remove(p.file)
		return true
	}
	c.Tries++
	b, _ := json.Marshal(c)
	_ = os.WriteFile(p.file, b, 0o600)
	return false
}

// PairLink is what the QR code encodes; the iPhone app opens it directly.
func PairLink(base, code, name string) string {
	q := url.Values{"url": {base}, "code": {code}, "name": {name}}
	return "healthsync://pair?" + q.Encode()
}

func QRTerminal(text string) string {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return ""
	}
	return q.ToSmallString(false)
}

// QRSVG renders a QR code as a compact SVG (one path, scales to any size).
func QRSVG(text string) string {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return ""
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	n := len(bm)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-2 -2 %d %d" shape-rendering="crispEdges"><rect x="-2" y="-2" width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n+4, n+4, n+4, n+4)
	for y, row := range bm {
		for x := 0; x < len(row); x++ {
			if !row[x] {
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start, y, x-start, x-start)
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// ---------------------------------------------------------------- approval without a code

// DialogSupported: pairing without a code needs somebody to approve it. On macOS we pop up a
// native dialog; everywhere the dashboard can approve it too.
func (p *Pairing) Approve(ctx context.Context, device, ip string, macDialog bool) bool {
	pp := &PendingPair{ID: newToken()[:12], Device: device, IP: ip, Created: time.Now(), answer: make(chan bool, 2)}
	p.pmu.Lock()
	p.pending[pp.ID] = pp
	p.pmu.Unlock()
	defer func() {
		p.pmu.Lock()
		delete(p.pending, pp.ID)
		p.pmu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, dialogWait)
	defer cancel()
	if macDialog && runtime.GOOS == "darwin" {
		go func() { pp.answer <- macConfirm(ctx, device) }()
	}
	select {
	case ok := <-pp.answer:
		return ok
	case <-ctx.Done():
		return false
	}
}

func (p *Pairing) Pending() []*PendingPair {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	out := make([]*PendingPair, 0, len(p.pending))
	for _, pp := range p.pending {
		out = append(out, pp)
	}
	return out
}

func (p *Pairing) Answer(id string, ok bool) bool {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	pp, found := p.pending[id]
	if found {
		select {
		case pp.answer <- ok:
		default:
		}
	}
	return found
}

func macConfirm(ctx context.Context, device string) bool {
	device = strings.NewReplacer(`"`, "'", `\`, "").Replace(device)
	script := fmt.Sprintf(`display dialog "“%s” wants to sync health data to this computer.\n\n「%s」想把健康数据同步到这台电脑。" `+
		`with title "HealthSync" buttons {"Deny 拒绝", "Allow 允许"} default button "Allow 允许" with icon note giving up after %d`,
		device, device, int(dialogWait.Seconds()))
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "Allow") && !strings.Contains(s, "gave up:true")
}
