package server

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Port          int
	Bind          string
	DataDir       string
	Name          string
	PublicURL     string // how phones reach this server, e.g. http://192.168.1.10:8787
	CSVDir        string
	Hook          string
	AdminPassword string
	NoBonjour     bool
	NoDialog      bool
	DashboardLAN  bool // only serve the dashboard to private / loopback addresses
}

// env returns the first non-empty variable among names.
func env(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func envInt(def int, names ...string) int {
	if v := env(names...); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(names ...string) bool {
	switch strings.ToLower(env(names...)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func defaultDataDir() string {
	if v := env("HEALTHSYNC_DATA"); v != "" {
		return expandHome(v)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library/Application Support/HealthSync")
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "HealthSync")
		}
		return filepath.Join(home, "HealthSync")
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "healthsync")
	}
	return filepath.Join(home, ".local/share/healthsync")
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// BindFlags registers the server's flags (each with an environment variable) on fs.
func BindFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.IntVar(&c.Port, "port", envInt(8787, "HEALTHSYNC_PORT"), "HTTP port")
	fs.StringVar(&c.Bind, "bind", firstNonEmpty(env("HEALTHSYNC_BIND"), "0.0.0.0"), "address to listen on")
	fs.StringVar(&c.DataDir, "data", defaultDataDir(), "data directory (health.db, reports/, token)")
	fs.StringVar(&c.Name, "name", env("HEALTHSYNC_NAME"), "server name shown in the app (default: computer name)")
	fs.StringVar(&c.PublicURL, "public-url", env("HEALTHSYNC_PUBLIC_URL"), "URL phones use to reach this server (default: detected LAN address)")
	fs.StringVar(&c.CSVDir, "csv", env("HEALTHSYNC_CSV"), "also append records to one CSV file per day in this directory")
	fs.StringVar(&c.Hook, "hook", env("HEALTHSYNC_HOOK"), "shell command to run after data arrives (see README)")
	fs.StringVar(&c.AdminPassword, "admin-password", env("HEALTHSYNC_ADMIN_PASSWORD"), "password for the web dashboard (user name: admin)")
	fs.BoolVar(&c.NoBonjour, "no-bonjour", envBool("HEALTHSYNC_NO_BONJOUR"), "don't advertise _healthsync._tcp on the LAN")
	fs.BoolVar(&c.NoDialog, "no-dialog", envBool("HEALTHSYNC_NO_DIALOG"), "macOS: don't show the Allow dialog for pairing without a code")
	lanOnly := !envBool("HEALTHSYNC_DASHBOARD_ANYWHERE")
	fs.BoolVar(&c.DashboardLAN, "dashboard-lan-only", lanOnly, "only serve the dashboard to LAN / loopback clients")
	return c
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Prepare fills in defaults and creates the data directory.
func (c *Config) Prepare() error {
	c.DataDir = expandHome(c.DataDir)
	if c.CSVDir != "" {
		c.CSVDir = expandHome(c.CSVDir)
	}
	if c.Name == "" {
		c.Name = machineName()
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	return os.MkdirAll(c.DataDir, 0o700)
}

func (c *Config) BaseURL() string {
	if c.PublicURL != "" {
		return c.PublicURL
	}
	return fmt.Sprintf("http://%s:%d", lanIP(), c.Port)
}

// ---------------------------------------------------------------- token

// LoadToken returns the server's shared token, creating one if needed.
// Order: HEALTHSYNC_TOKEN env → <data>/token.
// Paired devices get their own tokens (stored hashed in the database); this one is the
// "master" token you can paste into the app manually.
func LoadToken(dataDir string) (string, error) {
	if t := env("HEALTHSYNC_TOKEN"); t != "" {
		return t, nil
	}
	p := filepath.Join(dataDir, "token")
	if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	t := newToken()
	if err := os.WriteFile(p, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	logf("generated a new token in %s", p)
	return t, nil
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------- machine info

func machineName() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	if h == "" {
		h = "HealthSync"
	}
	return h
}

func osTag() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "windows"
	}
	return "linux"
}

func lanIP() string {
	// No packet is sent: this just asks the OS which interface it would route through.
	conn, err := net.Dial("udp4", "10.255.255.255:1")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func isLAN(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || isTailscale(ip))
}

// isLANAddr is isLAN for an address as it comes from RemoteAddr. It understands IPv6 zones
// ("fe80::1%en0": a .local name often resolves to the link-local IPv6 address, and
// net.ParseIP returns nil for it) and IPv4-mapped IPv6 ("::ffff:192.168.1.5").
func isLANAddr(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	a = a.WithZone("").Unmap()
	return isLAN(net.IP(a.AsSlice()))
}
