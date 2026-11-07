// healthsync-server receives Apple Health data from the HealthSync iPhone app and keeps it
// in a local SQLite database, with a web dashboard for sync history and QR-code pairing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func logf(format string, a ...any) { log.Printf(format, a...) }

const usage = `healthsync-server — local receiver for the HealthSync iPhone app

Usage:
  healthsync-server [serve] [flags]   run the server (default)
  healthsync-server pair [flags]      show a one-time pairing code + QR code in the terminal
  healthsync-server token [flags]     print the shared token
  healthsync-server stats [flags]     summary of what's in the database
  healthsync-server mcp [flags]       MCP server on stdio, for AI assistants (read-only)
  healthsync-server version

Flags (every flag also has an environment variable, see README):
`

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfg := bindFlags(fs)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	_ = fs.Parse(args)

	var err error
	switch cmd {
	case "serve":
		err = serve(cfg)
	case "pair":
		err = cmdPair(cfg)
	case "token":
		err = cmdToken(cfg)
	case "stats":
		err = cmdStats(cfg)
	case "mcp":
		err = cmdMCP(cfg)
	case "version", "--version":
		fmt.Println("healthsync-server", version)
	case "help", "-h", "--help":
		fs.Usage()
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve(cfg *Config) error {
	if err := cfg.prepare(); err != nil {
		return err
	}
	token, err := LoadToken(cfg.DataDir)
	if err != nil {
		return err
	}
	db, err := OpenDB(filepath.Join(cfg.DataDir, "health.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	srv, err := NewServer(cfg, db, token)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}

	logf("healthsync-server %s listening on %s", version, addr)
	logf("dashboard:  %s/", cfg.BaseURL())
	logf("data:       %s", cfg.DataDir)
	if cfg.AdminPassword == "" {
		logf("dashboard has no password (LAN only: %v); set HEALTHSYNC_ADMIN_PASSWORD to add one", cfg.DashboardLAN)
	}
	stop := func() {}
	if !cfg.NoBonjour {
		stop = Advertise(cfg.Name, cfg.Port)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		stop()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = hs.Shutdown(sctx)
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logf("bye")
	return nil
}

func cmdPair(cfg *Config) error {
	if err := cfg.prepare(); err != nil {
		return err
	}
	if _, err := LoadToken(cfg.DataDir); err != nil {
		return err
	}
	c, err := NewPairing(cfg.DataDir).New()
	if err != nil {
		return err
	}
	link := PairLink(cfg.BaseURL(), c.Code, cfg.Name)
	fmt.Printf("\nPairing code / 配对码:  %s %s   (valid %d min, one use)\n\n", c.Code[:3], c.Code[3:], int(pairTTL.Minutes()))
	fmt.Print(QRTerminal(link))
	fmt.Printf("\nScan with the HealthSync app (or the iPhone camera), or open:\n%s\n\n", link)
	fmt.Printf("The server must be running. Dashboard: %s/\n", cfg.BaseURL())
	return nil
}

func cmdToken(cfg *Config) error {
	if err := cfg.prepare(); err != nil {
		return err
	}
	t, err := LoadToken(cfg.DataDir)
	if err == nil {
		fmt.Println(t)
	}
	return err
}

func cmdStats(cfg *Config) error {
	if err := cfg.prepare(); err != nil {
		return err
	}
	p := filepath.Join(cfg.DataDir, "health.db")
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("no database yet at %s", p)
	}
	db, err := OpenDB(p)
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := db.Stats()
	if err != nil {
		return err
	}
	last := "-"
	if s.LastReceived != nil {
		last = *s.LastReceived
	}
	fmt.Printf("%d records, last received %s\n", s.Total, last)
	for i, t := range s.Types {
		if i == 40 {
			fmt.Printf("  … %d more types\n", len(s.Types)-40)
			break
		}
		fmt.Printf("  %-40s %9d  latest %s\n", t.Type, t.Count, t.Latest)
	}
	return nil
}
