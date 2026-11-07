// HealthSync is the desktop app: the HealthSync server with a menu bar icon and a native
// window (drawn by MyGo, no webview). It receives Apple Health data from the iPhone app and
// keeps it in SQLite, like the headless healthsync-server.
//
//	go run ./cmd/healthsync              the app
//	go run ./cmd/healthsync --headless   just the server, as healthsync-server does
//	healthsync pair|token|stats|mcp      the same commands as healthsync-server
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/desktop"
	"github.com/herosizy/healthsync-server/internal/cli"
	"github.com/herosizy/healthsync-server/model"
	"github.com/herosizy/healthsync-server/server"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch strings.TrimLeft(args[0], "-") {
		case "headless":
			cli.Main(args[1:])
			return
		case "serve", "pair", "token", "stats", "mcp", "version", "help", "h":
			cli.Main(args)
			return
		}
	}
	h := &host{}
	h.settings = loadSettings()
	h.app = desktop.New(h, isChinese())
	mygo.App.SetName("HealthSync")
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { h.quitting = true })
	mygo.App.WhenReady(h.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	h.stopServer()
}

type host struct {
	app      *desktop.App
	win      *mygo.Window
	tray     *mygo.Tray
	quitting bool

	mu       sync.Mutex
	settings model.Settings
	inst     *server.Instance
	err      string
	synced   time.Time
}

// ---------------------------------------------------------------- desktop.Host

func (h *host) Settings() model.Settings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

func (h *host) SaveSettings(s model.Settings) error {
	if err := saveSettings(s); err != nil {
		return err
	}
	h.mu.Lock()
	h.settings = s
	h.mu.Unlock()
	h.stopServer()
	h.startServer()
	if _, why := h.Backend(); why != "" {
		return fmt.Errorf("%s", why)
	}
	return nil
}

func (h *host) Backend() (desktop.Backend, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inst == nil {
		if h.err == "" {
			return nil, "starting…"
		}
		return nil, h.err
	}
	return h.inst, ""
}

func (h *host) DefaultDataDir() string { return server.DefaultDataDir() }
func (h *host) Copy(text string)       { mygo.Clipboard.WriteText(text) }
func (h *host) OpenURL(url string)     { _ = mygo.Shell.OpenExternal(url) }

func (h *host) ChooseFolder(title, start string, done func(string)) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: h.win, Title: title, DefaultPath: start,
			Directory: true, CreateDirectories: true})
		if err != nil || len(paths) == 0 {
			done("")
			return
		}
		done(paths[0])
	}()
}

func (h *host) Update(fn func()) {
	if h.win == nil {
		mygo.RunOnMain(fn)
		return
	}
	h.win.Update(fn)
}

// ---------------------------------------------------------------- server

func (h *host) startServer() {
	cfg := server.ConfigFromSettings(h.Settings())
	in, err := server.Start(cfg, h.onEvent)
	h.mu.Lock()
	h.inst, h.err = in, ""
	if err != nil {
		h.err = err.Error()
		log.Printf("server: %v", err)
	}
	h.mu.Unlock()
	h.app.Refresh()
	h.updateTray()
}

func (h *host) stopServer() {
	h.mu.Lock()
	in := h.inst
	h.inst = nil
	h.mu.Unlock()
	if in != nil {
		in.Stop()
	}
}

// onEvent runs after every sync, pairing or rejected request.
func (h *host) onEvent(e model.LogEntry) {
	h.app.Refresh()
	if e.Kind == "ingest" && e.OK {
		h.mu.Lock()
		h.synced = time.Now()
		notify := h.settings.Notify
		h.mu.Unlock()
		h.updateTray()
		if notify && e.Added > 0 {
			zh := isChinese()
			title := "HealthSync"
			body := fmt.Sprintf("Received %d new records from %s", e.Added, e.Device)
			if zh {
				body = fmt.Sprintf("收到 %s 的 %d 条新记录", e.Device, e.Added)
			}
			_ = mygo.NewNotification(mygo.NotificationOptions{ID: "sync", Title: title, Body: body, Silent: true}).Show()
		}
	}
}

// ---------------------------------------------------------------- window and tray

func (h *host) ready() {
	h.win = mygo.NewWindow(mygo.WindowOptions{
		Title: "HealthSync", Width: 1040, Height: 700, MinWidth: 760, MinHeight: 520,
		TitleBarStyle: mygo.TitleBarHidden, Content: ui.View(h.app.View),
	})
	h.win.OnClose(func(e *mygo.CloseEvent) {
		// Closing the window keeps the server running in the menu bar.
		if !h.quitting {
			e.PreventDefault()
			h.hide()
		}
	})
	var err error
	h.tray, err = mygo.NewTray(mygo.TrayOptions{ID: "healthsync", Icon: trayIcon(), IconIsTemplate: runtime.GOOS == "darwin", ToolTip: "HealthSync"})
	if err != nil {
		log.Printf("tray: %v", err)
	} else {
		if runtime.GOOS == "linux" {
			h.tray.SetMenu(h.trayMenu())
		} else {
			h.tray.OnClick(h.show)
			h.tray.OnRightClick(func() {
				h.tray.SetMenu(h.trayMenu())
				defer h.tray.SetMenu(nil)
				h.tray.PopUpMenu()
			})
		}
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { h.tray.Destroy() })
	}
	go h.startServer()
	go h.ticker()
	h.show()
}

func (h *host) trayMenu() *mygo.Menu {
	zh := isChinese()
	t := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	status := t("服务器已停止", "Server stopped")
	if b, _ := h.Backend(); b != nil {
		status = t("运行中", "Running") + " · " + strings.TrimPrefix(h.inst.Cfg.BaseURL(), "http://")
	}
	h.mu.Lock()
	synced := h.synced
	h.mu.Unlock()
	last := t("本次运行还没有同步", "No sync since launch")
	if !synced.IsZero() {
		last = t("最近同步 ", "Last sync ") + synced.Format("15:04")
	}
	open := func(page string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { h.app.ShowPage(page); h.show() }
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: status, Disabled: true},
		{Label: last, Disabled: true},
		mygo.Separator(),
		{Label: t("打开 HealthSync", "Open HealthSync"), Click: open("overview")},
		{Label: t("配对新的 iPhone…", "Pair a New iPhone…"), Click: open("pair")},
		{Label: t("连接 AI 助手…", "Connect an AI Assistant…"), Click: open("mcp")},
		{Label: t("在浏览器打开控制面板", "Open Web Dashboard"), Click: func(*mygo.MenuItem, *mygo.Window) {
			if b, _ := h.Backend(); b != nil {
				h.OpenURL(b.DashboardURL())
			}
		}},
		mygo.Separator(),
		{Label: t("设置…", "Settings…"), Click: open("settings")},
		{Role: mygo.RoleQuit},
	})
}

func (h *host) show() {
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	h.win.Show()
	h.win.Focus()
	h.app.Refresh()
}

func (h *host) hide() {
	h.win.Hide()
	if runtime.GOOS == "darwin" {
		// Only the menu bar icon stays, as the server keeps running.
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
}

func (h *host) updateTray() {
	if h.tray == nil {
		return
	}
	tip := "HealthSync"
	if b, why := h.Backend(); b == nil {
		tip += " — " + why
	}
	h.tray.SetToolTip(tip)
}

// ticker refreshes the window every few seconds while it shows, for the pairing countdown
// and for anything the event hook missed.
func (h *host) ticker() {
	for range time.Tick(5 * time.Second) {
		visible := false
		mygo.RunOnMain(func() { visible = h.win.IsVisible() })
		if visible {
			h.app.Tick()
		}
	}
}

// trayIcon draws a heart, black on transparent for macOS to tint; pink elsewhere.
func trayIcon() []byte {
	const n = 36
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	ink := color.NRGBA{R: 255, G: 45, B: 85, A: 255}
	if runtime.GOOS == "darwin" {
		ink = color.NRGBA{A: 255}
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			// the heart curve (x²+y²-1)³ - x²y³ <= 0, scaled into the square
			fx := (float64(x) + 0.5 - n/2) / (n * 0.36)
			fy := -(float64(y) + 0.5 - n*0.47) / (n * 0.36)
			v := fx*fx + fy*fy - 1
			if v*v*v-fx*fx*fy*fy*fy <= 0 {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// ---------------------------------------------------------------- settings.json

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "HealthSync", "settings.json")
}

func loadSettings() model.Settings {
	var s model.Settings
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(s model.Settings) error {
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func isChinese() bool {
	for _, l := range mygo.App.PreferredLanguages() {
		return strings.HasPrefix(strings.ToLower(l), "zh")
	}
	return strings.HasPrefix(strings.ToLower(os.Getenv("LANG")), "zh")
}
