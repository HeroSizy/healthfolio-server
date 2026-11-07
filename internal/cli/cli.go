// Package cli is the headless server's command line, shared by healthsync-server and the
// desktop app's --headless mode.
package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/herosizy/healthsync-server/internal/telemetry"
	"github.com/herosizy/healthsync-server/server"
)

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

// Main runs the command line (serve, pair, token, stats, mcp, version) with args.
func Main(args []string) {
	log.SetFlags(log.Ldate | log.Ltime)
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfg := server.BindFlags(fs)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	_ = fs.Parse(args)

	var err error
	switch cmd {
	case "serve":
		flush := telemetry.Init("server", server.Version(), false)
		func() {
			defer telemetry.Recover()
			err = serve(cfg)
		}()
		if err != nil {
			telemetry.Error("serve", err)
		}
		flush()
	case "pair":
		err = cmdPair(cfg)
	case "token":
		err = cmdToken(cfg)
	case "stats":
		err = cmdStats(cfg)
	case "mcp":
		err = server.RunMCPStdio(cfg)
	case "version", "--version":
		fmt.Println("healthsync-server", server.Version())
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

func serve(cfg *server.Config) error {
	in, err := server.Start(cfg, nil)
	if err != nil {
		return err
	}
	log.Printf("dashboard:  %s/", cfg.BaseURL())
	if cfg.AdminPassword == "" {
		log.Printf("dashboard has no password (LAN only: %v); set HEALTHSYNC_ADMIN_PASSWORD to add one", cfg.DashboardLAN)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-ctx.Done():
		in.Stop()
		log.Printf("bye")
		return nil
	case err := <-in.Done():
		in.Stop()
		return err
	}
}

func cmdPair(cfg *server.Config) error {
	if err := cfg.Prepare(); err != nil {
		return err
	}
	if _, err := server.LoadToken(cfg.DataDir); err != nil {
		return err
	}
	c, err := server.NewPairing(cfg.DataDir).New()
	if err != nil {
		return err
	}
	link := server.PairLink(cfg.BaseURL(), server.TailscaleURL(cfg.Port), c.Code, cfg.Name)
	fmt.Printf("\nPairing code / 配对码:  %s %s   (valid %d min, one use)\n\n", c.Code[:3], c.Code[3:], int(server.PairTTL.Minutes()))
	fmt.Print(server.QRTerminal(link))
	fmt.Printf("\nScan with the HealthSync app (or the iPhone camera), or open:\n%s\n\n", link)
	fmt.Printf("The server must be running. Dashboard: %s/\n", cfg.BaseURL())
	return nil
}

func cmdToken(cfg *server.Config) error {
	if err := cfg.Prepare(); err != nil {
		return err
	}
	t, err := server.LoadToken(cfg.DataDir)
	if err == nil {
		fmt.Println(t)
	}
	return err
}

func cmdStats(cfg *server.Config) error {
	if err := cfg.Prepare(); err != nil {
		return err
	}
	p := filepath.Join(cfg.DataDir, "health.db")
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("no database yet at %s", p)
	}
	db, err := server.OpenDB(p)
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
