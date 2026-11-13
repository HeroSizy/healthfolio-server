package desktop

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
)

func svgIcon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var (
	iconOverview = svgIcon(`<path d="M3 3v18h18"/><path d="M7 15l4-4 3 3 5-6"/>`)
	iconHistory  = svgIcon(`<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/>`)
	iconPair     = svgIcon(`<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><path d="M14 14h3v3h-3zM20 14v.01M14 20h.01M17 20h4v-3"/>`)
	iconMCP      = svgIcon(`<path d="M12 3l1.9 4.6L18.5 9.5l-4.6 1.9L12 16l-1.9-4.6L5.5 9.5l4.6-1.9z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/>`)
	iconReports  = svgIcon(`<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/><path d="M8 14h3l1.5-3 2 6 1.5-3H17"/>`)
	iconSettings = svgIcon(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>`)
	iconCopy     = svgIcon(`<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>`)
	iconCheck    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>`))
	_            = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#fff" d="M12 20.5s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7.6a4.3 4.3 0 0 1 7.5 2.7c0 5.6-7.5 10.2-7.5 10.2z"/></svg>`))
)

//go:embed icon128.png
var iconPNG []byte

var appIcon, _ = ui.DecodeBitmap(iconPNG)

type navItem struct {
	id     string
	icon   *ui.SVG
	zh, en string
}

func (a *App) nav() []navItem {
	return []navItem{
		{"overview", iconOverview, "概览", "Overview"},
		{"history", iconHistory, "同步记录", "Sync History"},
		{"pair", iconPair, "配对和设备", "Pair & Devices"},
		{"mcp", iconMCP, "AI 助手", "AI Assistants"},
		{"reports", iconReports, "体检报告", "Checkups"},
	}
}

// settingsNav sits at the bottom of the sidebar, under the server status, as in Kero.
var settingsNav = navItem{"settings", iconSettings, "设置", "Settings"}

const sidebarW = 220

func (a *App) sidebar(c *ui.Context, pal *palette, bar ui.TitleBar) {
	ui.Column(c).Width(sidebarW).Shrink(0).Padding(max(bar.Height, 14)+6, 10, 12, 12).Gap(2).DragWindow().Children(func() {
		// brand
		ui.Row(c).AlignItems(ui.Center).Gap(10).Padding(4, 6, 14, 6).Children(func() {
			ui.Image(c, appIcon).Size(30, 30).Radius(8).Shadow(0, 2, 6, 0, pal.accent.Alpha(0.3))
			ui.Column(c).Gap(0).MinWidth(0).Children(func() {
				ui.Text(c, "Healthfolio").FontSize(14).FontWeight(650).TextColor(pal.text)
				name := a.ov.Name
				if name == "" {
					name = a.t("本地服务器", "Local server")
				}
				ui.Text(c, name).FontSize(11).TextColor(pal.textMuted).SingleLine()
			})
		})
		for _, it := range a.nav() {
			a.navRow(c, pal, it)
		}
		ui.Spacer(c)
		a.status(c, pal)
		if a.web {
			// the server's settings change on the computer running it, not from a browser
			a.liteLink(c, pal)
			return
		}
		ui.Box(c).Height(6)
		a.navRow(c, pal, settingsNav)
		a.updateChip(c, pal)
	})
}

// topBar is the sidebar of a narrow window: the brand and the status light, then the pages
// as pills that scroll sideways.
func (a *App) topBar(c *ui.Context, pal *palette) {
	ui.Column(c).Shrink(0).Gap(8).Padding(12, 12, 10, 12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Image(c, appIcon).Size(28, 28).Radius(7).Shadow(0, 2, 6, 0, pal.accent.Alpha(0.3))
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "Healthfolio").FontSize(14).FontWeight(650).TextColor(pal.text)
				ui.Text(c, a.ov.Name).FontSize(11).TextColor(pal.textMuted).SingleLine()
			})
			b, _ := a.host.Backend()
			ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(5, 10).Radius(10).Background(pal.chip).Children(func() {
				a.statusDot(c, pal, b != nil)
				ui.Text(c, a.t("运行中", "Running")).FontSize(12).FontWeight(600).TextColor(pal.text)
			})
		})
		ui.ScrollHorizontal(c).Scrollbars(false).Children(func() {
			ui.Row(c).Gap(4).Children(func() {
				for _, it := range a.nav() {
					sel := a.page == it.id
					r := ui.ButtonBase(c.Key("top-"+it.id)).Height(32).Padding(0, 12).Radius(16).AlignItems(ui.Center).Gap(6).
						Label(a.t(it.zh, it.en)).Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
					if sel {
						r.Background(pal.rowSelected).Border(1, pal.rowBorder)
					}
					r.Children(func() {
						fg := pal.textMuted
						if sel {
							fg = pal.accent
						}
						ui.Icon(c, it.icon).Size(14, 14).TextColor(fg)
						ui.Text(c, a.t(it.zh, it.en)).FontSize(13).FontWeight(map[bool]int{true: 600, false: 500}[sel]).TextColor(pal.text).SingleLine()
						if it.id == "pair" && len(a.pending) > 0 {
							ui.Text(c, strconv.Itoa(len(a.pending))).FontSize(11).FontWeight(600).TextColor(ui.Hex("#ffffff")).
								Padding(1, 6).Radius(8).Background(pal.accent)
						}
					})
					if r.Clicked() && !sel {
						a.page = it.id
						a.Refresh()
					}
				}
			})
		})
	})
}

// liteLink, at the bottom of the web dashboard's sidebar, opens the plain HTML dashboard,
// which loads at once and works in any browser.
func (a *App) liteLink(c *ui.Context, pal *palette) {
	ui.Box(c).Height(6)
	r := ui.ButtonBase(c.Key("lite")).Padding(6, 10).Radius(8).Label(a.t("简洁版面板", "Lite dashboard"))
	if r.Hovered() {
		r.Background(pal.rowHover)
	}
	r.Children(func() {
		ui.Text(c, a.t("简洁版面板 ↗", "Lite dashboard ↗")).FontSize(12).TextColor(pal.textMuted)
	})
	if r.Clicked() {
		if b, _ := a.host.Backend(); b != nil {
			a.host.OpenURL(strings.TrimSuffix(b.DashboardURL(), "/") + "/lite/")
		}
	}
}

// updateChip, at the bottom left of the window, says a newer version is out; clicking it
// opens the update window.
func (a *App) updateChip(c *ui.Context, pal *palette) {
	u := a.host.Updates()
	if u.Available == "" || u.Available == u.Version {
		return
	}
	ui.Box(c).Height(4)
	r := ui.ButtonBase(c.Key("update-chip")).Padding(7, 10).Radius(9).AlignItems(ui.Center).Gap(8).
		Label(a.t("有新版本", "Update available")).Background(pal.accent.Alpha(0.12))
	if r.Hovered() {
		r.Background(pal.accent.Alpha(0.2))
	}
	r.Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(pal.accent)
		ui.Text(c, a.t("新版本 "+u.Available+" 可用", "Version "+u.Available+" is available")).
			FontSize(12).FontWeight(600).TextColor(pal.accent).Grow(1).SingleLine()
		ui.Text(c, a.t("更新…", "Update…")).FontSize(12).TextColor(pal.accent)
	})
	if r.Clicked() {
		a.host.CheckForUpdates()
	}
}

func (a *App) navRow(c *ui.Context, pal *palette, it navItem) {
	sel := a.page == it.id
	r := ui.ButtonBase(c.Key(it.id)).Height(32).Padding(0, 10).Radius(9).AlignItems(ui.Center).Gap(10).
		Label(a.t(it.zh, it.en)).Transition(ui.ElementTransition{Colors: true, Shadows: true, Duration: 160 * time.Millisecond})
	switch {
	case sel:
		r.Background(pal.rowSelected).Border(1, pal.rowBorder).Shadow(0, 1, 3, 0, pal.rowShadow)
	case r.Hovered():
		r.Background(pal.rowHover)
	}
	r.Children(func() {
		fg := pal.textMuted
		if sel {
			fg = pal.accent
		}
		ui.Icon(c, it.icon).Size(16, 16).TextColor(fg)
		tc := pal.text
		if !sel {
			tc = pal.text.Alpha(0.85)
		}
		ui.Text(c, a.t(it.zh, it.en)).FontSize(13).FontWeight(map[bool]int{true: 600, false: 500}[sel]).TextColor(tc).Grow(1).SingleLine()
		switch it.id {
		case "pair":
			if n := len(a.pending); n > 0 {
				ui.Text(c, strconv.Itoa(n)).FontSize(11).FontWeight(600).TextColor(ui.Hex("#ffffff")).
					Padding(1, 6).Radius(8).Background(pal.accent)
			}
		}
	})
	if r.Clicked() && a.page != it.id {
		a.page = it.id
		if it.id == "settings" {
			a.resetForm()
		}
		a.Refresh()
	}
}

// statusDot is the status light: green with a still glow while the server runs.
func (a *App) statusDot(c *ui.Context, pal *palette, running bool) {
	dot := pal.ok
	if !running {
		dot = pal.danger
	}
	d := ui.Box(c).Size(8, 8).Radius(4).Background(dot)
	if !running {
		return
	}
	// A still glow: a looping animation redraws the window 60 times a second for as long
	// as the app runs, which kept a CPU core 40% busy. It swells once when a sync arrives,
	// and is still again 1.6 s later.
	glow := float32(0)
	if a.pulse != "" {
		p := d.AnimateTo("pulse"+a.pulse, 1, ui.Animation{Duration: 1600 * time.Millisecond, Ease: ui.EaseOut})
		glow = float32(math.Sin(float64(p) * math.Pi))
	}
	d.Shadow(0, 0, 6+16*glow, 2+8*glow, dot.Alpha(0.45+0.3*glow))
}

// status shows whether the server runs, and where phones reach it.
func (a *App) status(c *ui.Context, pal *palette) {
	b, why := a.host.Backend()
	ui.Column(c).Gap(4).Padding(10, 10).Radius(10).Background(pal.chip).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			label := a.t("运行中", "Running")
			if b == nil {
				label = a.t("已停止", "Stopped")
			}
			a.statusDot(c, pal, b != nil)
			ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(pal.text)
		})
		sub := a.ov.BaseURL
		if b == nil {
			sub = why
		}
		ui.Text(c, strings.TrimPrefix(sub, "http://")).FontSize(11).TextColor(pal.textMuted).MaxLines(2)
	})
}

// ---------------------------------------------------------------- helpers

func qrBitmap(text string) *ui.Bitmap {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return nil
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	n := len(bm)
	const scale, border = 8, 2
	size := (n + 2*border) * scale
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	black := color.NRGBA{A: 255}
	for y, row := range bm {
		for x, on := range row {
			if !on {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetNRGBA((x+border)*scale+dx, (y+border)*scale+dy, black)
				}
			}
		}
	}
	return ui.NewBitmap(img)
}

func itoa(n int) string { return strconv.Itoa(n) }

// num formats with thousands separators.
func num(f float64) string {
	neg := f < 0
	if neg {
		f = -f
	}
	var s string
	if f >= 100 || f == float64(int64(f)) {
		s = strconv.FormatInt(int64(f+0.5), 10)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
	} else {
		s = strconv.FormatFloat(f, 'f', 1, 64)
		if f < 10 {
			s = strconv.FormatFloat(f, 'f', 2, 64)
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
	}
	if neg {
		s = "-" + s
	}
	return s
}

func prettyType(t string) string {
	for _, p := range []string{"HKQuantityTypeIdentifier", "HKCategoryTypeIdentifier", "HKCorrelationTypeIdentifier", "HKDataTypeIdentifier"} {
		if strings.HasPrefix(t, p) {
			return t[len(p):]
		}
	}
	if t == "HKWorkoutTypeIdentifier" {
		return "Workout"
	}
	return t
}

// typeNames translates the common types for the Chinese UI.
var typeNames = map[string]string{
	"StepCount": "步数", "HeartRate": "心率", "RestingHeartRate": "静息心率", "HeartRateVariabilitySDNN": "心率变异性",
	"ActiveEnergyBurned": "活动能量", "BasalEnergyBurned": "静息能量", "DistanceWalkingRunning": "步行+跑步距离",
	"FlightsClimbed": "已爬楼层", "SleepAnalysis": "睡眠", "OxygenSaturation": "血氧", "BodyMass": "体重",
	"BodyFatPercentage": "体脂率", "RespiratoryRate": "呼吸频率", "AppleExerciseTime": "锻炼时间",
	"AppleStandTime": "站立时间", "MindfulSession": "正念", "Workout": "体能训练", "WalkingSpeed": "步行速度",
	"BloodPressureSystolic": "收缩压", "BloodPressureDiastolic": "舒张压", "BloodGlucose": "血糖", "VO2Max": "最大摄氧量",
}

func (a *App) typeLabel(t string) string {
	p := prettyType(t)
	if a.zh {
		if z, ok := typeNames[p]; ok {
			return z
		}
	}
	return p
}

func plural(n int, zh bool, zhUnit, en string) string {
	if zh {
		return fmt.Sprintf("%s %s", num(float64(n)), zhUnit)
	}
	if n == 1 {
		return "1 " + en
	}
	return num(float64(n)) + " " + en + "s"
}
