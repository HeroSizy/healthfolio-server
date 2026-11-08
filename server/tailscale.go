package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"
)

// Tailscale: with this computer on a tailnet, the phone reaches the server through the
// Tailscale app away from home too. The server finds its tailnet address, puts it in
// /health, pairing replies and QR codes next to the LAN address, and treats tailnet peers
// like the local network for pairing and the dashboard: only devices signed in to the
// same tailnet can reach those addresses.

var (
	// tailscaleV4 is Tailscale's IPv4 range (CGNAT), tailscaleV6 its IPv6 prefix.
	_, tailscaleV4, _ = net.ParseCIDR("100.64.0.0/10")
	_, tailscaleV6, _ = net.ParseCIDR("fd7a:115c:a1e0::/48")
)

func isTailscale(ip net.IP) bool {
	return ip != nil && (tailscaleV4.Contains(ip) || tailscaleV6.Contains(ip))
}

var tsCache struct {
	sync.Mutex
	at      time.Time
	ip      string
	name    string
	looking bool
}

// tailscaleCLIs are where the tailscale command lives: on PATH, inside the Mac app, Homebrew.
var tailscaleCLIs = []string{"tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"}

// TailscaleAddr returns this computer's tailnet IPv4 address and MagicDNS name ("" when
// not on a tailnet). The first call looks it up; after that it answers at once from what
// was found and looks again in the background once a minute, so a slow or hung tailscale
// command never holds up a request.
func TailscaleAddr() (ip, name string) {
	tsCache.Lock()
	if tsCache.at.IsZero() {
		tsCache.Unlock()
		ip, name := lookupTailscale()
		tsCache.Lock()
		tsCache.at, tsCache.ip, tsCache.name = time.Now(), ip, name
	} else if time.Since(tsCache.at) > time.Minute && !tsCache.looking {
		tsCache.looking = true
		go func() {
			ip, name := lookupTailscale()
			tsCache.Lock()
			tsCache.at, tsCache.ip, tsCache.name, tsCache.looking = time.Now(), ip, name, false
			tsCache.Unlock()
		}()
	}
	defer tsCache.Unlock()
	return tsCache.ip, tsCache.name
}

// TailscaleURL is the server's address on the tailnet, e.g. http://100.101.102.103:8787.
// The IP works whether or not the phone uses MagicDNS.
func TailscaleURL(port int) string {
	if ip, _ := TailscaleAddr(); ip != "" {
		return fmt.Sprintf("http://%s:%d", ip, port)
	}
	return ""
}

func lookupTailscale() (ip, name string) {
	for _, cli := range tailscaleCLIs {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
		cancel()
		if err != nil {
			continue
		}
		var st struct {
			BackendState string
			Self         struct {
				DNSName      string
				TailscaleIPs []string
			}
		}
		if json.Unmarshal(out, &st) != nil || st.BackendState != "Running" {
			return "", ""
		}
		for _, a := range st.Self.TailscaleIPs {
			if p := net.ParseIP(a); p != nil && p.To4() != nil {
				ip = a
				break
			}
		}
		name = trimDot(st.Self.DNSName)
		if ip != "" {
			return ip, name
		}
	}
	// No CLI (e.g. in Docker with the host's network): look at the interfaces.
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && tailscaleV4.Contains(n.IP) {
			return n.IP.String(), ""
		}
	}
	return "", ""
}

func trimDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}
