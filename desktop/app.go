// Package desktop is the native window of the Healthfolio desktop app, drawn with MyGo's
// package ui. It knows nothing about SQLite or HTTP: it reads everything through Backend,
// so the same views run in the app and, with sample data, in a browser preview.
package desktop

import (
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/model"
)

// Backend is the running server.
type Backend interface {
	Overview() model.Overview
	Logs(limit int, before int64, kind string) ([]model.LogEntry, error)
	Devices() ([]model.Device, error)
	RevokeDevice(id int64) error
	DeleteDevice(id int64) error
	RenameDevice(id int64, name string) error
	SetMCPEnabled(on bool) error
	SkillMD() string
	Reports() ([]model.ReportRow, error)
	Daily(typ string, days int) ([]model.DailyPoint, error)
	NewPairCode() (model.PairInfo, error)
	CurrentPair() (model.PairInfo, bool)
	CancelPair()
	Pending() []model.PendingPair
	AnswerPending(id string, allow bool) bool
	NewMCPToken(name string) (model.MCPConnect, error)
	DashboardURL() string
}

// Host is what the app around the window provides.
type Host interface {
	Settings() model.Settings
	// SaveSettings stores the settings and restarts the server with them.
	SaveSettings(model.Settings) error
	// Backend is the running server, or nil with the reason it isn't running.
	Backend() (Backend, string)
	DefaultDataDir() string
	// StdioCommand is the executable an AI assistant runs with "mcp" for stdio.
	StdioCommand() string
	Copy(text string)
	OpenURL(url string)
	// ChooseFolder asks for a folder and calls done with it ("" if cancelled), on any goroutine.
	ChooseFolder(title, start string, done func(path string))
	// Update runs fn on the main thread and redraws the window.
	Update(fn func())
	// Updates is the state of the app's self-updates; CheckForUpdates checks as the user
	// asked, in the update window; SetAutomaticUpdates turns background checks on or off.
	Updates() model.Updates
	CheckForUpdates()
	SetAutomaticUpdates(on bool)
}

// App is the window's state. The view and Update callbacks run on the main thread.
type App struct {
	host Host
	zh   bool
	page string

	// loaded data
	ov         model.Overview
	logs       []model.LogEntry
	logKind    int
	moreLogs   bool
	devices    []model.Device
	renaming   int64 // the device whose name is being edited, or 0
	renameText string
	reports    []model.ReportRow
	pair       *model.PairInfo
	pairQR     *ui.Bitmap
	pending    []model.PendingPair
	mcp        *model.MCPConnect
	selType    string
	daily      []model.DailyPoint
	dayRange   int // index into dayRanges
	typeFilter string

	logList  ui.ListState
	devList  ui.ListState
	typeList ui.ListState
	repList  ui.ListState

	// settings form
	form      model.Settings
	formPort  string
	formDirty bool
	saveErr   string

	ob onboarding

	toast   string
	loading sync.Mutex
}

var dayRanges = []int{7, 30, 365}

func New(host Host, zh bool) *App {
	a := &App{host: host, zh: zh, page: "overview", dayRange: 1}
	a.logList.RoundRows, a.repList.RoundRows = true, true
	a.resetForm()
	return a
}

// t picks the Chinese or the English string.
func (a *App) t(zh, en string) string {
	if a.zh {
		return zh
	}
	return en
}

// ShowPage switches to a page (used by the tray menu).
func (a *App) ShowPage(p string) {
	a.page = p
	a.Refresh()
}

// Refresh reloads what the current page shows, off the main thread.
func (a *App) Refresh() {
	go a.load()
}

func (a *App) load() {
	if !a.loading.TryLock() {
		return
	}
	defer a.loading.Unlock()
	b, _ := a.host.Backend()
	if b == nil {
		a.host.Update(func() {})
		return
	}
	ov := b.Overview()
	kinds := []string{"", "ingest", "report", "pair", "auth", "hook"}
	logs, _ := b.Logs(200, 0, kinds[a.logKind])
	devs, _ := b.Devices()
	reps, _ := b.Reports()
	pend := b.Pending()
	var pair *model.PairInfo
	if p, ok := b.CurrentPair(); ok {
		pair = &p
	}
	var daily []model.DailyPoint
	if a.selType != "" {
		daily, _ = b.Daily(a.selType, dayRanges[a.dayRange])
	}
	a.host.Update(func() {
		a.ov, a.logs, a.devices, a.reports, a.pending, a.daily = ov, logs, devs, reps, pend, daily
		a.moreLogs = len(logs) == 200
		a.setPair(pair)
		a.decideOnboarding()
		if a.selType == "" && len(ov.Stats.Types) > 0 {
			a.selType = ov.Stats.Types[0].Type
			go a.loadDaily()
		}
	})
}

func (a *App) loadDaily() {
	b, _ := a.host.Backend()
	if b == nil || a.selType == "" {
		return
	}
	typ, days := a.selType, dayRanges[a.dayRange]
	d, _ := b.Daily(typ, days)
	a.host.Update(func() {
		if a.selType == typ {
			a.daily = d
		}
	})
}

func (a *App) setPair(p *model.PairInfo) {
	if p == nil {
		a.pair, a.pairQR = nil, nil
		return
	}
	if a.pair == nil || a.pair.Link != p.Link || a.pairQR == nil {
		a.pairQR = qrBitmap(p.Link)
	}
	a.pair = p
}

// Tick is called every few seconds while the window shows, to pick up new syncs.
func (a *App) Tick() { a.Refresh() }

// View draws the window.
func (a *App) View(c *ui.Context) {
	pal := paletteFor(c)
	c.SetTheme(themed(c, pal))
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	bar := c.TitleBar()
	row := ui.Row(c).Fill().AlignItems(ui.Stretch)
	backdrop(c, row, pal)
	if a.ob.show {
		row.Children(func() { a.onboardingView(c, pal) })
		return
	}
	row.Children(func() {
		a.sidebar(c, pal, bar)
		ui.Column(c).Grow(1).MinWidth(0).Padding(10, 10, 10, 0).DragWindow().Children(func() {
			card := raised(ui.Column(c).Grow(1).MinHeight(0).Radius(14), pal).Clip()
			card.Children(func() { a.content(c, pal) })
		})
	})
}

func (a *App) content(c *ui.Context, pal *palette) {
	b, why := a.host.Backend()
	if b == nil && a.page != "settings" {
		a.stopped(c, pal, why)
		return
	}
	switch a.page {
	case "history":
		a.historyPage(c, pal)
	case "pair":
		a.pairPage(c, pal)
	case "mcp":
		a.mcpPage(c, pal)
	case "reports":
		a.reportsPage(c, pal)
	case "settings":
		a.settingsPage(c, pal)
	default:
		a.overviewPage(c, pal)
	}
}

func (a *App) stopped(c *ui.Context, pal *palette, why string) {
	ui.Column(c).Fill().Center().Gap(10).Padding(40).Children(func() {
		ui.Text(c, a.t("服务器没有运行", "The server isn't running")).FontSize(18).FontWeight(600)
		ui.Text(c, why).TextColor(pal.textMuted).TextAlign(ui.Center).MaxWidth(420)
		if ui.PrimaryButton(c, a.t("打开设置", "Open Settings")).Clicked() {
			a.page = "settings"
		}
	})
}

// header is a page's title row inside the card.
func (a *App) header(c *ui.Context, pal *palette, title, sub string, trailing func()) {
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(12).Padding(18, 22, 10, 22).DragWindow().Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(650).TextColor(pal.text).SingleLine()
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(pal.textMuted).SingleLine()
			}
		})
		if trailing != nil {
			trailing()
		}
	})
}

func ago(zh bool, iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		if zh {
			return "从未"
		}
		return "never"
	}
	d := time.Since(t)
	pick := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	switch {
	case d < time.Minute:
		return pick("刚刚", "just now")
	case d < time.Hour:
		return itoa(int(d.Minutes())) + pick(" 分钟前", " min ago")
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + pick(" 小时前", " h ago")
	}
	return itoa(int(d.Hours()/24)) + pick(" 天前", " d ago")
}

func when(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	t = t.Local()
	if t.YearDay() == time.Now().YearDay() && t.Year() == time.Now().Year() {
		return t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

// DemoMCP fills the AI assistants page as if a token had just been made (preview only).
func (a *App) DemoMCP() {
	go func() {
		b, _ := a.host.Backend()
		m, _ := b.NewMCPToken("AI assistant")
		a.host.Update(func() { a.mcp = &m })
	}()
}
