package desktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The first launch walks through four steps instead of opening on an empty overview:
// what HealthSync does, naming this computer and picking the data folder, pairing the
// iPhone, and where things are. It shows only on a computer with no paired device and no
// data, so people who already use HealthSync never see it.

const obSteps = 4

type onboarding struct {
	checked bool // decided after the first load whether to show it
	show    bool
	step    int
	devices int // paired devices when the pairing step opened
	saving  bool
	err     string
}

// decideOnboarding runs on the main thread after a load.
func (a *App) decideOnboarding() {
	if a.ob.checked {
		return
	}
	a.ob.checked = true
	a.ob.show = !a.host.Settings().Onboarded && len(a.devices) == 0 && a.ov.Stats.Total == 0
}

// ShowOnboarding opens the steps again (the preview's #welcome).
func (a *App) ShowOnboarding() {
	a.ob = onboarding{checked: true, show: true}
	a.resetForm()
	a.Refresh()
}

func (a *App) onboardingView(c *ui.Context, pal *palette) {
	ui.Column(c).Fill().Center().Padding(40, 24).DragWindow().Children(func() {
		card := raised(ui.Column(c).Width(560).MaxWidth(560).Radius(18).Padding(30, 34, 24, 34).Gap(18), pal)
		in := card.AnimateTo("step"+itoa(a.ob.step), 1, ui.Animation{Duration: 240 * time.Millisecond, Ease: ui.EaseOut})
		card.Opacity(in).Translate(0, 8*(1-in)).Children(func() {
			switch a.ob.step {
			case 0:
				a.obWelcome(c, pal)
			case 1:
				a.obComputer(c, pal)
			case 2:
				a.obPair(c, pal)
			default:
				a.obDone(c, pal)
			}
		})
		// step dots
		ui.Row(c).Gap(6).Margin(18, 0, 0, 0).Children(func() {
			for i := 0; i < obSteps; i++ {
				d := ui.Box(c).Size(6, 6).Radius(3).Background(pal.text.Alpha(0.18))
				if i == a.ob.step {
					d.Width(18).Background(pal.accent)
				}
			}
		})
	})
}

func (a *App) obTitle(c *ui.Context, pal *palette, title, sub string) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, title).FontSize(22).FontWeight(700).TextColor(pal.text)
		if sub != "" {
			ui.Text(c, sub).FontSize(13).TextColor(pal.textMuted)
		}
	})
}

// obButtons is the row at the bottom of a step: an optional quiet button on the left and
// the main one on the right.
func (a *App) obButtons(c *ui.Context, quiet string, onQuiet func(), main string, disabled bool, onMain func()) {
	ui.Row(c).AlignItems(ui.Center).Gap(10).Margin(6, 0, 0, 0).Children(func() {
		if quiet != "" && ui.Button(c, quiet).Clicked() {
			onQuiet()
		}
		ui.Spacer(c)
		if ui.PrimaryButton(c, main).Disabled(disabled).Clicked() {
			onMain()
		}
	})
}

func (a *App) obWelcome(c *ui.Context, pal *palette) {
	ui.Image(c, appIcon).Size(56, 56).Radius(14).Shadow(0, 4, 14, 0, pal.accent.Alpha(0.3))
	a.obTitle(c, pal, a.t("欢迎使用 HealthSync", "Welcome to HealthSync"),
		a.t("把 iPhone 上的健康数据同步到这台电脑。", "Sync the health data on your iPhone to this computer."))
	ui.Column(c).Gap(12).Children(func() {
		for _, p := range [][3]string{
			{"⇄", "iPhone 上的「健康同步」App 把数据发到这里，在家时走局域网，不在家时可以走 Tailscale。", "The HealthSync app on your iPhone sends data here: over your home network, or over Tailscale when you're away."},
			{"⌂", "数据只存在这台电脑上的 SQLite 数据库里，不经过任何云服务。", "Data stays in a SQLite database on this computer. No cloud service in between."},
			{"✦", "需要时可以让 AI 助手（Claude 等）只读地查看你的数据。", "If you want, AI assistants such as Claude can read your data, read-only."},
		} {
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, p[0]).FontSize(14).FontWeight(700).TextColor(pal.accent).Size(28, 28).Radius(8).
					Background(pal.accentSoft).TextAlign(ui.Center).Shrink(0)
				ui.Text(c, a.t(p[1], p[2])).FontSize(13).TextColor(pal.text).Grow(1).MinWidth(0)
			})
		}
	})
	a.obButtons(c, a.t("跳过", "Skip"), a.obFinishSkip, a.t("开始设置", "Get Started"), false, func() { a.ob.step = 1 })
}

func (a *App) obComputer(c *ui.Context, pal *palette) {
	a.obTitle(c, pal, a.t("这台电脑", "This computer"),
		a.t("之后都可以在「设置」里改。", "You can change these later in Settings."))
	f := &a.form
	ui.Column(c).Gap(12).Padding(14, 16).Radius(12).Background(pal.inset).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, a.t("名字", "Name")).FontSize(12).TextColor(pal.textMuted).Width(90).Shrink(0)
			ui.TextInput(c.Key("ob-name"), &f.Name).Placeholder(firstNonEmpty(a.ov.Name, "Mac")).Grow(1)
		})
		ui.Text(c, a.t("iPhone 上会显示这个名字。", "The iPhone app shows this name.")).FontSize(11).TextColor(pal.textFaint).Margin(0, 0, 0, 100)
		a.folderRow(c, pal, "ob-data", a.t("数据文件夹", "Data folder"), &f.DataDir, a.host.DefaultDataDir())
		ui.Text(c, a.t("health.db、体检报告和备份都放在这里。", "health.db, checkups and backups go here.")).FontSize(11).TextColor(pal.textFaint).Margin(0, 0, 0, 100)
	})
	if a.ob.err != "" {
		ui.Text(c, a.ob.err).FontSize(12).TextColor(pal.danger).Padding(8, 10).Radius(8).Background(pal.dangerSoft)
	}
	label := a.t("下一步", "Continue")
	if a.ob.saving {
		label = a.t("正在启动…", "Starting…")
	}
	a.obButtons(c, a.t("上一步", "Back"), func() { a.ob.step = 0 }, label, a.ob.saving, func() {
		a.obSave(func() {
			a.ob.step = 2
			a.ob.devices = len(a.devices)
			if b, _ := a.host.Backend(); b != nil && a.pair == nil {
				a.newPair(b)
			}
		})
	})
}

// obSave stores the form with Onboarded set, which restarts the server, then calls next.
func (a *App) obSave(next func()) {
	s := a.form
	s.Onboarded = true
	s.Name = strings.TrimSpace(s.Name)
	a.ob.saving, a.ob.err = true, ""
	go func() {
		err := a.host.SaveSettings(s)
		a.host.Update(func() {
			a.ob.saving = false
			if err != nil {
				a.ob.err = err.Error()
				return
			}
			a.resetForm()
			next()
			a.Refresh()
		})
	}()
}

func (a *App) obPair(c *ui.Context, pal *palette) {
	b, why := a.host.Backend()
	paired := len(a.devices) > a.ob.devices
	if paired {
		a.obTitle(c, pal, a.t("配对好了", "Paired"), a.t("iPhone 现在可以把健康数据同步过来了。", "Your iPhone can now sync health data here."))
		ui.Row(c).AlignItems(ui.Center).Gap(12).Padding(14, 16).Radius(12).Background(pal.okSoft).Children(func() {
			ui.Text(c, "✓").FontSize(18).FontWeight(700).TextColor(pal.ok)
			ui.Text(c, a.devices[0].Name).FontSize(14).FontWeight(600).TextColor(pal.text)
		})
		a.obButtons(c, "", nil, a.t("下一步", "Continue"), false, func() { a.ob.step = 3 })
		return
	}
	a.obTitle(c, pal, a.t("配对 iPhone", "Pair your iPhone"),
		a.t("在 iPhone 上打开「健康同步」App，用相机或 App 里的「添加目的地 › 本地服务器」扫这个二维码。",
			"Open the HealthSync app on your iPhone and scan this code with the camera, or in the app: Add destination › Local server."))
	for _, p := range a.pending {
		a.pendingCard(c, pal, b, p)
	}
	ui.Row(c).Gap(20).AlignItems(ui.Center).Children(func() {
		a.qrBox(c, pal, b)
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			switch {
			case b == nil:
				ui.Text(c, why).FontSize(12).TextColor(pal.textMuted)
				c.After(time.Second)
			case a.pair == nil:
				if ui.PrimaryButton(c, a.t("生成配对码", "New pairing code")).AlignSelf(ui.Start).Clicked() {
					a.newPair(b)
				}
			default:
				p := a.pair
				left := max(0, time.Until(time.Unix(p.Expires, 0)))
				c.After(time.Second)
				ui.Text(c, a.t("配对码", "Pairing code")).FontSize(12).TextColor(pal.textMuted)
				ui.Text(c, p.Code[:3]+" "+p.Code[3:]).FontSize(30).FontWeight(700).LetterSpacing(4).TextColor(pal.text)
				ui.Text(c, a.t("剩余 ", "Expires in ")+itoa(int(left.Minutes()))+":"+pad2(int(left.Seconds())%60)).FontSize(12).TextColor(pal.textMuted)
				ui.Row(c).AlignItems(ui.Center).Gap(7).Margin(6, 0, 0, 0).Children(func() {
					ui.Box(c).Size(7, 7).Radius(4).Background(pal.accent)
					ui.Text(c, a.t("等待 iPhone…", "Waiting for the iPhone…")).FontSize(12).TextColor(pal.textMuted)
				})
				if left == 0 {
					a.newPair(b)
				}
			}
		})
	})
	a.obButtons(c, a.t("稍后再配对", "Pair Later"), func() { a.ob.step = 3 }, a.t("下一步", "Continue"), true, func() {})
}

func (a *App) obDone(c *ui.Context, pal *palette) {
	a.obTitle(c, pal, a.t("准备好了", "All set"), a.t("HealthSync 会在后台一直运行。", "HealthSync keeps running in the background."))
	ui.Column(c).Gap(12).Children(func() {
		for _, p := range [][2]string{
			{"关掉窗口后，服务器还在菜单栏里运行，iPhone 随时能同步。", "Closing the window keeps the server running in the menu bar, so the iPhone can sync any time."},
			{"「配对和设备」里可以再配对其它 iPhone，或撤销某台设备。", "Pair & Devices pairs another iPhone or revokes one."},
			{"「AI 助手」里可以给 Claude 等助手一个只读令牌。", "AI Assistants gives Claude and others a read-only token."},
			{"想在别的电脑上用网页面板，先在「设置」里设个面板密码。", "To open the web dashboard from another computer, set a dashboard password in Settings first."},
		} {
			ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(pal.accent).Margin(7, 0, 0, 0).Shrink(0)
				ui.Text(c, a.t(p[0], p[1])).FontSize(13).TextColor(pal.text).Grow(1).MinWidth(0)
			})
		}
	})
	a.obButtons(c, "", nil, a.t("开始使用", "Open HealthSync"), false, func() {
		a.ob.show = false
		a.page = "overview"
		a.Refresh()
	})
}

// obFinishSkip leaves the steps at once, and does not show them again.
func (a *App) obFinishSkip() {
	a.obSave(func() {
		a.ob.show = false
		a.page = "overview"
	})
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}
