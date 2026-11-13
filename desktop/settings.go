package desktop

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) resetForm() {
	a.form = a.host.Settings()
	a.formPort = ""
	if a.form.Port > 0 {
		a.formPort = strconv.Itoa(a.form.Port)
	}
	a.formDirty, a.saveErr = false, ""
}

func (a *App) settingsPage(c *ui.Context, pal *palette) {
	a.header(c, pal, a.t("设置", "Settings"), a.t("数据存到哪里、怎么连接", "Where data goes and how phones connect"), func() {
		if ui.Button(c, a.t("还原", "Revert")).Disabled(!a.formDirty).Clicked() {
			a.resetForm()
		}
		if ui.PrimaryButton(c, a.t("保存并重启服务", "Save & Restart")).Disabled(!a.formDirty).Clicked() {
			a.save()
		}
	})
	f := &a.form
	changed := false
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(4, a.gutter(), 22, a.gutter()).Gap(18).Children(func() {
			if a.saveErr != "" {
				ui.Text(c, a.saveErr).FontSize(12).TextColor(pal.danger).Padding(10, 12).Radius(8).Background(pal.dangerSoft)
			}
			a.group(c, pal, a.t("存储位置", "Storage"), a.t("收到的数据会同时写到下面打开的每个位置", "Received data is written to every place turned on below"), func() {
				// SQLite: always on, folder is choosable
				a.settingRow(c, pal, "SQLite", a.t("所有数据、同步记录和设备都在 health.db 里，可以用任何 SQLite 工具打开", "Everything lives in health.db, readable with any SQLite tool"), func() {
					ui.Text(c, a.t("始终开启", "Always on")).FontSize(12).TextColor(pal.textFaint)
				})
				changed = a.folderRow(c, pal, "data", a.t("数据文件夹", "Data folder"), &f.DataDir, a.host.DefaultDataDir()) || changed
				a.divider(c, pal)
				a.settingRow(c, pal, a.t("每天一个 CSV", "Daily CSV files"), a.t("另外把每条记录追加到 YYYY-MM-DD.csv，方便 Excel、Numbers 打开", "Also append records to YYYY-MM-DD.csv, for Excel or Numbers"), func() {
					changed = toggle(c, &f.CSVEnabled, pal).Changed() || changed
				})
				if f.CSVEnabled {
					def := filepath.Join(firstNonEmpty(f.DataDir, a.host.DefaultDataDir()), "csv")
					changed = a.folderRow(c, pal, "csv", a.t("CSV 文件夹", "CSV folder"), &f.CSVDir, def) || changed
				}
				a.divider(c, pal)
				a.settingRow(c, pal, a.t("同步后运行命令", "Run a command after syncs"), a.t("例如提交到 git 仓库、推到 InfluxDB；数据在 $HEALTHSYNC_INBOX 里", "e.g. commit to git or push to InfluxDB; data is in $HEALTHSYNC_INBOX"), func() {
					changed = toggle(c, &f.HookEnabled, pal).Changed() || changed
				})
				if f.HookEnabled {
					changed = ui.TextInput(c.Key("hook"), &f.Hook).Placeholder("~/health/after-sync.sh").Font("monospace").FontSize(12).Changed() || changed
				}
			})
			a.group(c, pal, a.t("连接", "Connection"), "", func() {
				a.settingRow(c, pal, a.t("服务器名字", "Server name"), a.t("在 App 里显示的名字，默认是电脑名", "Shown in the app; the computer's name by default"), func() {
					changed = ui.TextInput(c.Key("name"), &f.Name).Placeholder(a.ov.Name).Width(200).Changed() || changed
				})
				a.divider(c, pal)
				a.settingRow(c, pal, a.t("端口", "Port"), a.t("默认 8787", "8787 by default"), func() {
					if ui.TextInput(c.Key("port"), &a.formPort).Placeholder("8787").Width(90).Changed() {
						p, _ := strconv.Atoi(strings.TrimSpace(a.formPort))
						f.Port = p
						changed = true
					}
				})
				a.divider(c, pal)
				ts := a.ov.TailscaleURL
				tsSub := a.t("这台电脑没连 Tailscale。连上后，iPhone 开着 Tailscale 在外面也能同步", "Not on a tailnet. With Tailscale on both, the iPhone syncs away from home too")
				if ts != "" {
					tsSub = a.t("已连接：二维码里会同时带上这个地址，iPhone 不在家时自动走 Tailscale", "Connected: the QR code carries this address too, and the iPhone uses it away from home")
				}
				a.settingRow(c, pal, "Tailscale", tsSub, func() {
					if ts == "" {
						ui.Text(c, a.t("未检测到", "Not detected")).FontSize(12).TextColor(pal.textFaint)
						return
					}
					if ui.Button(c, strings.TrimPrefix(ts, "http://")).Font("monospace").FontSize(12).Clicked() {
						a.host.Copy(ts)
						a.toast = a.t("已复制", "Copied")
					}
				})
				a.divider(c, pal)
				a.settingRow(c, pal, a.t("对外地址", "Public address"), a.t("二维码里的局域网地址；用反向代理时才需要改", "The LAN address in the QR code; only change it for a reverse proxy"), func() {
					changed = ui.TextInput(c.Key("url"), &f.PublicURL).Placeholder(a.ov.BaseURL).Width(240).Changed() || changed
				})
				a.divider(c, pal)
				bon := !f.NoBonjour
				a.settingRow(c, pal, a.t("局域网自动发现", "Discoverable on the LAN"), a.t("用 Bonjour 广播，App 不用输入地址", "Bonjour, so the app finds this computer by itself"), func() {
					if toggle(c, &bon, pal).Changed() {
						f.NoBonjour = !bon
						changed = true
					}
				})
			})
			a.group(c, pal, a.t("网页控制面板", "Web dashboard"), a.t("局域网里的其它设备也可以用浏览器打开", "Other devices on your network can open it in a browser"), func() {
				a.settingRow(c, pal, a.t("密码", "Password"), a.t("其它电脑打开面板时需要输入（用户名随意）。不设密码时只有这台电脑能打开", "Needed to open the dashboard from another computer (any user name). Without one, only this computer can open it"), func() {
					changed = ui.TextInput(c.Key("pw"), &f.AdminPassword).Password().Width(200).Changed() || changed
				})
				if b, _ := a.host.Backend(); b != nil {
					a.divider(c, pal)
					a.settingRow(c, pal, a.t("打开控制面板", "Open the dashboard"), b.DashboardURL(), func() {
						if ui.Button(c, a.t("在浏览器打开", "Open in Browser")).Clicked() {
							a.host.OpenURL(b.DashboardURL())
						}
					})
				}
			})
			a.group(c, pal, a.t("通知", "Notifications"), "", func() {
				a.settingRow(c, pal, a.t("同步完成时通知", "Notify after each sync"), a.t("在通知中心显示收到多少条", "Show how many records arrived"), func() {
					changed = toggle(c, &f.Notify, pal).Changed() || changed
				})
			})
			a.updatesGroup(c, pal)
			a.group(c, pal, a.t("隐私", "Privacy"), "", func() {
				send := !f.NoCrashReports
				a.settingRow(c, pal, a.t("发送崩溃报告", "Send crash reports"),
					a.t("只有出错的位置和版本号，不包含任何健康数据；重新打开 App 后生效", "Only where it failed and the version, never health data; applies after relaunching"), func() {
						if toggle(c, &send, pal).Changed() {
							f.NoCrashReports = !send
							changed = true
						}
					})
			})
		})
	})
	if changed {
		a.formDirty = true
	}
}

// updatesGroup shows the version and the self-update choices, which apply at once.
func (a *App) updatesGroup(c *ui.Context, pal *palette) {
	u := a.host.Updates()
	sub := a.t("当前版本 ", "Version ") + u.Version
	if u.Available != "" {
		sub += a.t(" · 有新版本 ", " · ") + u.Available + a.t("", " is available")
	}
	a.group(c, pal, a.t("更新", "Updates"), sub, func() {
		if !u.Supported {
			ui.Text(c, a.t("开发版或用软件包安装的版本不能自己更新", "Development builds and packaged installs can't update themselves")).
				FontSize(12).TextColor(pal.textMuted)
			return
		}
		auto := u.Automatic
		a.settingRow(c, pal, a.t("自动更新", "Update automatically"), a.t("每天检查一次，新版本在后台下载，下次打开时装好", "Checks daily, downloads in the background, installs at the next launch"), func() {
			if toggle(c, &auto, pal).Changed() {
				a.host.SetAutomaticUpdates(auto)
				a.Refresh()
			}
		})
		a.divider(c, pal)
		a.settingRow(c, pal, a.t("检查更新", "Check for updates"), a.t("下载的更新都经过签名校验", "Every update is checked against its signature"), func() {
			if ui.Button(c, a.t("立即检查…", "Check Now…")).Clicked() {
				a.host.CheckForUpdates()
			}
		})
	})
}

func (a *App) save() {
	s := a.form
	s.Onboarded = s.Onboarded || a.host.Settings().Onboarded
	go func() {
		err := a.host.SaveSettings(s)
		a.host.Update(func() {
			if err != nil {
				a.saveErr = err.Error()
				return
			}
			a.formDirty, a.saveErr = false, ""
			a.toast = a.t("已保存，服务已重启", "Saved; the server restarted")
			a.Refresh()
		})
	}()
}

func (a *App) group(c *ui.Context, pal *palette, title, sub string, body func()) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 2).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(650).TextColor(pal.text)
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(pal.textFaint).SingleLine()
			}
		})
		ui.Column(c).Gap(10).Padding(14, 16).Radius(12).Background(pal.inset).Children(body)
	})
}

func (a *App) divider(c *ui.Context, pal *palette) {
	ui.Box(c).Height(1).Background(pal.divider)
}

func (a *App) settingRow(c *ui.Context, pal *palette, title, sub string, control func()) {
	ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(560).TextColor(pal.text)
			if sub != "" {
				ui.Text(c, sub).FontSize(11.5).TextColor(pal.textMuted)
			}
		})
		control()
	})
}

// folderRow shows a folder with a Choose… button. It reports a change.
func (a *App) folderRow(c *ui.Context, pal *palette, key, title string, dir *string, def string) bool {
	changed := false
	ui.Row(c.Key(key)).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(pal.textMuted).Width(90).Shrink(0)
		shown := *dir
		if shown == "" {
			shown = def + a.t("（默认）", " (default)")
		}
		ui.Text(c, shown).Font("monospace").FontSize(11.5).TextColor(pal.text).Grow(1).MinWidth(0).Ellipsis("…").SingleLine().
			Padding(7, 10).Radius(7).Background(pal.card).Tooltip(shown)
		if ui.Button(c, a.t("选择…", "Choose…")).Clicked() {
			start := firstNonEmpty(*dir, def)
			a.host.ChooseFolder(title, start, func(p string) {
				if p == "" {
					return
				}
				a.host.Update(func() {
					*dir = p
					a.formDirty = true
				})
			})
		}
		if *dir != "" && ui.Button(c, a.t("默认", "Default")).Tooltip(def).Clicked() {
			*dir = ""
			changed = true
		}
	})
	return changed
}
