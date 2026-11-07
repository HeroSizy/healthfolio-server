package server

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/grandcat/zeroconf"
)

// Advertise publishes _healthsync._tcp so the iPhone app finds the server without typing
// an address. On macOS the system's mDNSResponder (dns-sd) is used; elsewhere a built-in
// responder. In Docker this needs `network_mode: host`.
func Advertise(name string, port int) (stop func()) {
	txt := []string{"path=/ingest", fmt.Sprintf("v=%d", protocolVersion), "os=" + osTag()}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/usr/bin/dns-sd"); err == nil {
			host := ""
			if out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output(); err == nil {
				host = strings.TrimSpace(string(out))
			}
			args := []string{"-R", name, "_healthsync._tcp", "local", fmt.Sprint(port)}
			if host != "" {
				args = append(args, "host="+host+".local")
			}
			cmd := exec.Command("/usr/bin/dns-sd", append(args, txt...)...)
			if err := cmd.Start(); err == nil {
				logf("advertising %q on the LAN (dns-sd, port %d)", name, port)
				return func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
			}
		}
	}
	srv, err := zeroconf.Register(name, "_healthsync._tcp", "local.", port, txt, nil)
	if err != nil {
		logf("Bonjour advertising failed (%v); the app can still pair by QR code or address", err)
		return func() {}
	}
	logf("advertising %q on the LAN (mDNS, port %d)", name, port)
	return srv.Shutdown
}
