package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CSVOutput appends every received record to <dir>/<YYYY-MM-DD>.csv (by sample start day).
type CSVOutput struct {
	dir string
	mu  sync.Mutex
}

func NewCSVOutput(dir string) (*CSVOutput, error) {
	return &CSVOutput{dir: dir}, os.MkdirAll(dir, 0o755)
}

func (c *CSVOutput) Write(rows []Record) (int, error) {
	byDay := map[string][]Record{}
	for _, r := range rows {
		day := "unknown"
		if len(r.Start) >= 10 {
			day = r.Start[:10]
		}
		byDay[day] = append(byDay[day], r)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for day, items := range byDay {
		p := filepath.Join(c.dir, filepath.Base(day)+".csv")
		_, statErr := os.Stat(p)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		w := csv.NewWriter(f)
		if os.IsNotExist(statErr) {
			_ = w.Write(recordFields)
		}
		for _, r := range items {
			_ = w.Write([]string{r.Type, r.Start, r.End, r.Value, r.Unit, r.Source})
		}
		w.Flush()
		f.Close()
		if err := w.Error(); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

// Hook runs a user command after data arrives, so you can commit to git, push to
// InfluxDB, notify someone, etc. Received records are also written to <data>/inbox/*.txt
// (pipe format); the hook decides what to do with them (and should delete or move them).
//
// Several POSTs from one sync are batched: the hook runs once things have been quiet for
// `debounce`. Environment passed to the command:
//
//	HEALTHSYNC_EVENT       ingest | report
//	HEALTHSYNC_INBOX       directory with app-*.txt files (ingest)
//	HEALTHSYNC_REPORT_DIR  folder of the report just received (report)
//	HEALTHSYNC_DATA        the data directory
//	HEALTHSYNC_DB          path to health.db
type Hook struct {
	cmd      string
	inbox    string
	data     string
	db       string
	debounce time.Duration
	mu       sync.Mutex
	timer    *time.Timer
	run      sync.Mutex
	onResult func(kind string, ok bool, output string, d time.Duration)
}

func NewHook(cmd, dataDir, dbPath string) (*Hook, error) {
	h := &Hook{cmd: cmd, inbox: filepath.Join(dataDir, "inbox"), data: dataDir, db: dbPath, debounce: 20 * time.Second}
	if err := os.MkdirAll(h.inbox, 0o700); err != nil {
		return nil, err
	}
	if m, _ := filepath.Glob(filepath.Join(h.inbox, "*.txt")); len(m) > 0 {
		h.schedule() // leftovers from last time
	}
	return h, nil
}

func (h *Hook) Write(rows []Record) error {
	if len(rows) == 0 {
		return nil
	}
	name := fmt.Sprintf("app-%s-%06d.txt", time.Now().Format("20060102-150405"), time.Now().Nanosecond()/1000)
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.Pipe())
		b.WriteByte('\n')
	}
	tmp := filepath.Join(h.inbox, "."+name)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(h.inbox, name)); err != nil {
		return err
	}
	h.schedule()
	return nil
}

func (h *Hook) schedule() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.timer != nil {
		h.timer.Stop()
	}
	h.timer = time.AfterFunc(h.debounce, func() { h.exec("ingest", "") })
}

func (h *Hook) Report(folder string) {
	go h.exec("report", folder)
}

func (h *Hook) exec(event, reportDir string) {
	h.run.Lock()
	defer h.run.Unlock()
	start := time.Now()
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", h.cmd)
	} else {
		c = exec.Command("/bin/sh", "-c", h.cmd)
	}
	c.Env = append(os.Environ(),
		"HEALTHSYNC_EVENT="+event,
		"HEALTHSYNC_INBOX="+h.inbox,
		"HEALTHSYNC_REPORT_DIR="+reportDir,
		"HEALTHSYNC_DATA="+h.data,
		"HEALTHSYNC_DB="+h.db,
	)
	out, err := c.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if len(text) > 4000 {
		text = "…" + text[len(text)-4000:]
	}
	if err != nil {
		text = strings.TrimSpace(text + "\n" + err.Error())
	}
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			logf("  hook: %s", line)
		}
	}
	if h.onResult != nil {
		h.onResult(event, err == nil, text, time.Since(start))
	}
}
