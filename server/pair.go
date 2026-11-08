package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	PairTTL      = 10 * time.Minute
	pairMaxTries = 5
)

// PairCode is a one-time 6-digit code. It lives in a file so that `healthsync pair`
// (a separate process) and the running server see the same code.
type PairCode struct {
	Code    string  `json:"code"`
	Expires float64 `json:"expires"` // unix seconds
	Tries   int     `json:"tries"`
}

type Pairing struct {
	file string
	mu   sync.Mutex

	// requests to pair without a code, waiting for someone to click Allow (see pair_requests.go)
	rq requests
}

func NewPairing(dataDir string) *Pairing {
	return &Pairing{file: filepath.Join(dataDir, "pair-code.json"), rq: newRequests()}
}

func (p *Pairing) New() (PairCode, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := PairCode{Code: randomDigits(6), Expires: float64(time.Now().Add(PairTTL).Unix())}
	b, _ := json.Marshal(c)
	return c, os.WriteFile(p.file, b, 0o600)
}

// randomDigits is n random decimal digits.
func randomDigits(n int) string {
	max := big.NewInt(1)
	for i := 0; i < n; i++ {
		max.Mul(max, big.NewInt(10))
	}
	v, _ := rand.Int(rand.Reader, max)
	return fmt.Sprintf("%0*d", n, v.Int64())
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

// PairLink is what the pairing QR code holds; the iPhone app opens it directly. alt, when
// not "", is a second address of the server, its Tailscale one, which the app falls back
// to away from home.
func PairLink(base, alt, code, name string) string {
	q := url.Values{"url": {base}, "code": {code}, "name": {name}}
	if alt != "" && alt != base {
		q.Set("alt", alt)
	}
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
