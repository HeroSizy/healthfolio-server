package desktop

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/herosizy/healthsync-server/model"
)

// ---------------------------------------------------------------- overview

func (a *App) overviewPage(c *ui.Context, pal *palette) {
	o := a.ov
	a.header(c, pal, a.t("概览", "Overview"), a.t("iPhone 同步到这台电脑的健康数据", "Health data your iPhone syncs to this computer"), nil)
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(4, 22, 22, 22).Gap(16).Children(func() {
			// tiles
			ui.Row(c).Gap(12).Children(func() {
				last, lastHint := a.t("从未", "Never"), ""
				if s := o.LastSync; s != nil {
					last = ago(a.zh, s.Time)
					dev := s.Device
					if dev == "shared token" {
						dev = a.t("共享令牌", "shared token")
					}
					if s.OK {
						lastHint = fmt.Sprintf("%s · +%s", dev, num(float64(s.Added)))
					} else {
						lastHint = dev + " · " + a.t("失败", "failed")
					}
				}
				a.tile(c, pal, "records", a.t("健康记录", "Records"), num(float64(o.Stats.Total)), plural(len(o.Stats.Types), a.zh, "种类型", "type"), 0)
				a.tile(c, pal, "last", a.t("最近同步", "Last sync"), last, lastHint, 1)
				a.tile(c, pal, "devices", a.t("已配对设备", "Paired devices"), itoa(o.Devices), "", 2)
				a.tile(c, pal, "reports", a.t("体检报告", "Checkups"), itoa(o.Reports), "", 3)
			})
			// activity
			a.section(c, pal, a.t("每天的记录", "Records per day"), a.t("最近 30 天，按数据本身的日期", "Last 30 days, by the day they were taken"), nil, func() {
				a.activityChart(c, pal, o.Activity)
			})
			// types + daily chart
			ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
					a.section(c, pal, a.t("数据类型", "Data types"), "", nil, func() { a.typesList(c, pal) })
				})
				ui.Column(c).Grow(1.4).Basis(0).MinWidth(0).Children(func() {
					title := a.t("选择一个类型", "Pick a type")
					unit := ""
					if a.selType != "" {
						title = a.typeLabel(a.selType)
						if len(a.daily) > 0 && a.daily[0].Unit != "" {
							unit = a.daily[0].Unit
						}
					}
					a.section(c, pal, title, unit, func() {
						if ui.Segmented(c, &a.dayRange, "7d", "30d", "1y").Changed() {
							go a.loadDaily()
						}
					}, func() { a.dailyChart(c, pal) })
				})
			})
			a.storageSection(c, pal, o.Storage)
		})
	})
}

var storageKinds = map[string][2]string{
	"database": {"健康数据库", "Health database"}, "reports": {"体检报告", "Checkups"},
	"chats": {"AI 对话备份", "AI chat backups"}, "settings": {"App 设置备份", "App settings backups"},
	"csv": {"CSV 文件", "CSV files"}, "other": {"其它", "Other"},
}

// storageSection shows how much room the data takes, by kind, and what the disk has left.
func (a *App) storageSection(c *ui.Context, pal *palette, st model.Storage) {
	if len(st.Parts) == 0 {
		return
	}
	sub := a.t("共 ", "") + bytesLabel(st.Total) + a.t("", " in all")
	if st.DiskSize > 0 {
		sub += a.t(" · 磁盘剩余 ", " · ") + bytesLabel(st.DiskFree) + a.t(" / ", " free of ") + bytesLabel(st.DiskSize)
	}
	colors := []ui.Color{pal.accent, pal.ok, ui.Hex("#5b8def"), ui.Hex("#c08be8"), ui.Hex("#e0a243"), pal.textFaint}
	a.section(c, pal, a.t("存储空间", "Storage"), sub, nil, func() {
		ui.Column(c).Gap(12).Padding(14, 16).Radius(12).Background(pal.inset).Children(func() {
			// one bar: the parts side by side, by size
			ui.Row(c).Height(10).Radius(5).Clip().Background(pal.text.Alpha(0.08)).Children(func() {
				for i, p := range st.Parts {
					if p.Bytes > 0 && st.Total > 0 {
						ui.Box(c).Grow(float32(p.Bytes) / float32(st.Total)).Basis(0).MinWidth(2).Height(10).Background(colors[min(i, len(colors)-1)])
					}
				}
			})
			for i, p := range st.Parts {
				name := storageKinds[p.Kind]
				ui.Row(c.Key("st-"+p.Kind)).AlignItems(ui.Center).Gap(10).Children(func() {
					ui.Box(c).Size(8, 8).Radius(4).Background(colors[min(i, len(colors)-1)]).Shrink(0)
					ui.Text(c, firstNonEmpty(a.t(name[0], name[1]), p.Kind)).FontSize(13).TextColor(pal.text).Width(130).Shrink(0)
					ui.Text(c, p.Path).FontSize(11).Font("monospace").TextColor(pal.textFaint).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(p.Path)
					ui.Text(c, plural(p.Files, a.zh, "个文件", "file")).FontSize(12).TextColor(pal.textMuted).Shrink(0)
					ui.Text(c, bytesLabel(p.Bytes)).FontSize(13).FontWeight(600).TextColor(pal.text).Width(80).TextAlign(ui.End).Shrink(0)
				})
			}
		})
	})
}

// bytesLabel is a size such as 465 MB or 1.2 GB.
func bytesLabel(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 || f >= 100 {
		return fmt.Sprintf("%.0f %s", f, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// tile is a number with its label, rising in as the page opens.
func (a *App) tile(c *ui.Context, pal *palette, key, label, value, hint string, i int) {
	t := ui.Column(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Gap(3).Padding(14, 16).Radius(12).Background(pal.inset)
	appear := t.AnimateTo("in", 1, ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.2}, Delay: time.Duration(i) * 60 * time.Millisecond})
	t.Opacity(appear).Translate(0, (1-appear)*8)
	t.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(pal.textMuted).SingleLine()
		ui.Text(c, value).FontSize(24).FontWeight(650).TextColor(pal.text).SingleLine().
			ContentTransition(ui.ContentTransition{Numeric: true})
		ui.Text(c, hint).FontSize(11).TextColor(pal.textFaint).SingleLine()
	})
}

// section is a titled block inside the card.
func (a *App) section(c *ui.Context, pal *palette, title, sub string, trailing func(), body func()) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(620).TextColor(pal.text).SingleLine()
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(pal.textFaint).SingleLine()
			}
			ui.Spacer(c)
			if trailing != nil {
				trailing()
			}
		})
		body()
	})
}

func (a *App) activityChart(c *ui.Context, pal *palette, act []model.DayCount) {
	if len(act) == 0 {
		a.empty(c, pal, a.t("还没有同步过", "Nothing synced yet"), 150)
		return
	}
	labels := make([]string, len(act))
	vals := make([]float64, len(act))
	for i, d := range act {
		labels[i] = d.Day
		vals[i] = float64(d.Records)
	}
	a.barChart(c, pal, "activity", labels, vals, func(i int) bool { return act[i].Fails > 0 }, 150, "")
}

func (a *App) dailyChart(c *ui.Context, pal *palette) {
	if a.selType == "" || len(a.daily) == 0 {
		a.empty(c, pal, a.t("这段时间没有数据", "No data in this range"), 220)
		return
	}
	cumulative := isCumulative(a.selType)
	labels := make([]string, len(a.daily))
	vals := make([]float64, len(a.daily))
	for i, p := range a.daily {
		labels[i] = p.Day
		if cumulative {
			vals[i] = p.Total
		} else {
			vals[i] = p.Mean
		}
	}
	a.barChart(c, pal, "daily-"+a.selType, labels, vals, nil, 220, a.daily[0].Unit)
}

func isCumulative(t string) bool {
	for _, k := range []string{"Count", "Distance", "Energy", "Flights", "Time", "Steps"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// barChart draws bars that grow in from the baseline.
func (a *App) barChart(c *ui.Context, pal *palette, key string, labels []string, vals []float64, fail func(int) bool, h float32, unit string) {
	_, hi := chart.Extent(vals...)
	if hi <= 0 {
		hi = 1
	}
	x := chart.NewBand(labels...).Padding(0.28, 0.1)
	y := chart.NewLinear(0, hi).Nice(4)
	yt := y.Ticks(4)
	for i := range yt {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(yt[i].Label, ",", ""), 64); err == nil {
			yt[i].Label = num(v)
		}
	}
	// a handful of day labels along the bottom
	var xt []chart.Tick
	step := max(1, len(labels)/6)
	for i, t := range x.Ticks() {
		if i%step == 0 {
			if len(t.Label) == 10 {
				t.Label = t.Label[5:]
			}
			xt = append(xt, t)
		}
	}
	label := func(side chart.Side, tick chart.Tick) {
		ui.Text(c, tick.Label).NoWrap().FontSize(10).TextColor(pal.textFaint)
	}
	plot := chart.Plot(c.Key(key), chart.Axes{Left: yt, Bottom: xt, Label: label}, func(area ui.Element) {
		grow := area.AnimateTo("grow", 1, ui.Animation{Spring: ui.SpringOptions{Duration: 700 * time.Millisecond, Bounce: 0.1}})
		area.Draw(func(p *ui.Painter, r ui.Rect) {
			for _, t := range yt {
				p.Line(r.X, chart.Y(r, t.At), r.X+r.W, chart.Y(r, t.At), 1, pal.divider)
			}
		})
		for i, v := range vals {
			col := pal.bar
			if fail != nil && fail(i) && v == 0 {
				col, v = pal.barFail, hi*0.02
			}
			tip := labels[i] + "  " + num(vals[i])
			if unit != "" {
				tip += " " + unit
			}
			chart.Rect(c, x.Band(i), y.Span(0, v*float64(grow))).Radius(3).Background(col).Tooltip(tip)
		}
	})
	plot.Height(h).Gap(6)
}

func (a *App) empty(c *ui.Context, pal *palette, msg string, h float32) {
	ui.Column(c).Height(h).Center().Radius(10).Background(pal.inset).Children(func() {
		ui.Text(c, msg).FontSize(13).TextColor(pal.textMuted)
	})
}

func (a *App) typesList(c *ui.Context, pal *palette) {
	types := a.ov.Stats.Types
	if len(types) == 0 {
		a.empty(c, pal, a.t("还没有数据", "No data yet"), 220)
		return
	}
	ui.Column(c).Height(220).Radius(10).Background(pal.inset).Padding(4).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			for _, t := range types {
				sel := t.Type == a.selType
				r := ui.ButtonBase(c.Key("t-"+t.Type)).Padding(7, 10).Radius(8).AlignItems(ui.Center).Gap(8)
				if sel {
					r.Background(pal.card).Shadow(0, 1, 3, 0, pal.rowShadow)
				} else if r.Hovered() {
					r.Background(pal.rowHover)
				}
				r.Children(func() {
					ui.Text(c, a.typeLabel(t.Type)).FontSize(13).TextColor(pal.text).Grow(1).SingleLine().Tooltip(t.Type)
					ui.Text(c, num(float64(t.Count))).FontSize(12).TextColor(pal.textMuted)
				})
				if r.Clicked() {
					a.selType = t.Type
					go a.loadDaily()
				}
			}
		})
	})
}

// ---------------------------------------------------------------- history

var kindLabels = [][2]string{{"全部", "All"}, {"数据", "Data"}, {"体检", "Checkups"}, {"配对", "Pairing"}, {"被拒绝", "Rejected"}, {"钩子", "Hook"}}

func (a *App) historyPage(c *ui.Context, pal *palette) {
	labels := make([]string, len(kindLabels))
	for i, k := range kindLabels {
		labels[i] = a.t(k[0], k[1])
	}
	a.header(c, pal, a.t("同步记录", "Sync History"), a.t("每一次手机发来的数据、配对和被拒绝的请求", "Every sync, pairing and rejected request"), func() {
		if ui.Segmented(c, &a.logKind, labels...).Changed() {
			a.Refresh()
		}
	})
	if len(a.logs) == 0 {
		ui.Column(c).Grow(1).Padding(0, 22, 22, 22).Children(func() { a.empty(c, pal, a.t("还没有记录", "Nothing yet"), 200) })
		return
	}
	cols := []ui.TableColumn{
		{Title: a.t("时间", "Time"), Width: 96},
		{Title: a.t("事件", "Event"), Width: 120},
		{Title: a.t("设备", "Device")},
		{Title: a.t("收到", "Received"), Width: 80, Align: ui.End},
		{Title: a.t("新增", "New"), Width: 80, Align: ui.End},
		{Title: a.t("详情", "Detail")},
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(0, 12, 12, 12).Children(func() {
		ui.Table(c, &a.logList, cols, len(a.logs), func(row, col int) {
			e := a.logs[row]
			switch col {
			case 0:
				ui.Text(c, when(e.Time)).FontSize(12).TextColor(pal.textMuted).Tooltip(e.Time)
			case 1:
				a.eventBadge(c, pal, e)
			case 2:
				dev := e.Device
				if dev == "shared token" {
					dev = a.t("共享令牌", "shared token")
				}
				ui.Column(c).Gap(1).Children(func() {
					ui.Text(c, dev).FontSize(13).SingleLine()
					meta := e.IP
					if e.DurationMS > 0 {
						meta += fmt.Sprintf(" · %d ms", e.DurationMS)
					}
					if e.Bytes > 0 {
						meta += fmt.Sprintf(" · %s KB", num(float64(e.Bytes)/1024))
					}
					ui.Text(c, strings.TrimPrefix(meta, " · ")).FontSize(11).TextColor(pal.textFaint).SingleLine()
				})
			case 3:
				if e.Received > 0 {
					ui.Text(c, num(float64(e.Received))).FontSize(13)
				}
			case 4:
				if e.Kind == "ingest" || e.Kind == "report" {
					ui.Text(c, num(float64(e.Added))).FontSize(13).FontWeight(600)
				}
			case 5:
				msg := e.Message
				if len(e.Types) > 0 {
					names := make([]string, 0, len(e.Types))
					for _, t := range e.Types {
						names = append(names, a.typeLabel(t))
					}
					msg = strings.Join(names, ", ")
				}
				ui.Text(c, msg).FontSize(12).TextColor(pal.textMuted).MaxLines(2).Tooltip(msg)
			}
		}).Grow(1)
	})
}

func (a *App) eventBadge(c *ui.Context, pal *palette, e model.LogEntry) {
	names := map[string][2]string{"ingest": {"同步数据", "Data"}, "report": {"体检报告", "Checkup"}, "pair": {"配对", "Pairing"}, "auth": {"令牌错误", "Bad token"}, "hook": {"钩子", "Hook"}, "backup": {"备份", "Backup"}}
	n := names[e.Kind]
	fg, bg := pal.ok, pal.okSoft
	if !e.OK {
		fg, bg = pal.danger, pal.dangerSoft
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(2, 8).Radius(10).Background(bg).AlignSelf(ui.Start).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(fg)
		ui.Text(c, a.t(n[0], n[1])).FontSize(11).FontWeight(600).TextColor(fg).SingleLine()
	})
}

// ---------------------------------------------------------------- pairing

func (a *App) pairPage(c *ui.Context, pal *palette) {
	a.header(c, pal, a.t("配对和设备", "Pair & Devices"), a.t("用 iPhone 相机扫一下就配好", "Scan with the iPhone camera and you're done"), nil)
	b, _ := a.host.Backend()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(4, 22, 22, 22).Gap(18).Children(func() {
			for _, p := range a.pending {
				a.pendingCard(c, pal, b, p)
			}
			ui.Row(c).Gap(24).AlignItems(ui.Center).Padding(18).Radius(14).Background(pal.inset).Children(func() {
				a.qrBox(c, pal, b)
				ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
					ui.Text(c, a.t("和「健康册」App 配对", "Pair with the Healthfolio app")).FontSize(16).FontWeight(650)
					for i, s := range [][2]string{
						{"点「生成配对码」，这里会出现一次性二维码。", "Click “New pairing code” to show a one-time QR code."},
						{"用 iPhone 相机扫码，或在 App → 添加目的地 → 本地服务器 里扫。", "Scan it with the iPhone camera, or in the app: Add destination → Local server."},
						{"App 会自动保存地址和自己的令牌。", "The app saves the address and its own token."},
					} {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, itoa(i+1)).FontSize(11).FontWeight(700).TextColor(pal.accent).Size(18, 18).Radius(9).
								Background(pal.accentSoft).TextAlign(ui.Center)
							ui.Text(c, a.t(s[0], s[1])).FontSize(13).TextColor(pal.textMuted).Grow(1)
						})
					}
					if p := a.pair; p != nil {
						left := time.Until(time.Unix(p.Expires, 0))
						if left < 0 {
							left = 0
						}
						c.After(time.Second)
						ui.Text(c, p.Code[:3]+" "+p.Code[3:]).FontSize(34).FontWeight(700).LetterSpacing(4).TextColor(pal.text).Margin(6, 0, 0, 0)
						ui.Text(c, fmt.Sprintf("%s %d:%02d · %s", a.t("剩余", "Expires in"), int(left.Minutes()), int(left.Seconds())%60, p.URL)).
							FontSize(12).TextColor(pal.textMuted)
						ui.Row(c).Gap(8).Children(func() {
							if ui.Button(c, a.t("复制链接", "Copy link")).Clicked() {
								a.host.Copy(p.Link)
								a.toast = a.t("已复制", "Copied")
							}
							if ui.Button(c, a.t("取消", "Cancel")).Clicked() && b != nil {
								b.CancelPair()
								a.setPair(nil)
							}
						})
						if left == 0 {
							a.Refresh()
						}
					} else if ui.PrimaryButton(c, a.t("生成配对码", "New pairing code")).AlignSelf(ui.Start).Clicked() && b != nil {
						a.newPair(b)
					}
				})
			})
			a.devicesSection(c, pal, b)
		})
	})
}

func (a *App) newPair(b Backend) {
	go func() {
		p, err := b.NewPairCode()
		a.host.Update(func() {
			if err != nil {
				a.toast = err.Error()
				return
			}
			a.setPair(&p)
		})
	}()
}

func (a *App) qrBox(c *ui.Context, pal *palette, b Backend) {
	box := ui.Column(c).Size(200, 200).Shrink(0).Center().Radius(14).Background(pal.card).Border(1, pal.cardBorder)
	box.Children(func() {
		if a.pairQR != nil {
			in := box.AnimateTo(a.pair.Code, 1, ui.Animation{Spring: ui.SpringOptions{Duration: 500 * time.Millisecond, Bounce: 0.25}})
			ui.Image(c, a.pairQR).Size(180, 180).Radius(8).Scale(0.85+0.15*in, 0.85+0.15*in).Opacity(in)
		} else {
			ui.Icon(c, iconPair).Size(56, 56).TextColor(pal.textFaint)
		}
	})
}

func (a *App) pendingCard(c *ui.Context, pal *palette, b Backend, p model.PendingPair) {
	ui.Row(c.Key("pend-"+p.ID)).AlignItems(ui.Center).Gap(12).Padding(14, 16).Radius(12).Background(pal.accentSoft).Border(1, pal.accent.Alpha(0.4)).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, a.t("有设备请求配对", "A device wants to pair")).FontSize(14).FontWeight(650)
			ui.Text(c, p.Device+" · "+p.IP).FontSize(12).TextColor(pal.textMuted)
			if p.Confirm != "" {
				ui.Text(c, a.t("手机上显示的确认码一致时再允许", "Allow only if the phone shows the same code")).FontSize(12).TextColor(pal.textMuted)
			}
		})
		if p.Confirm != "" {
			ui.Text(c, p.Confirm).FontSize(26).FontWeight(700).LetterSpacing(4)
		}
		if ui.Button(c, a.t("拒绝", "Deny")).Clicked() && b != nil {
			b.AnswerPending(p.ID, false)
			a.Refresh()
		}
		if ui.PrimaryButton(c, a.t("允许", "Allow")).Clicked() && b != nil {
			b.AnswerPending(p.ID, true)
			a.Refresh()
		}
	})
}

func (a *App) devicesSection(c *ui.Context, pal *palette, b Backend) {
	a.section(c, pal, a.t("已配对设备", "Paired devices"), a.t("每台设备有自己的令牌，可以单独撤销", "Each has its own token you can revoke"), nil, func() {
		if len(a.devices) == 0 {
			a.empty(c, pal, a.t("还没有配对的设备", "No paired devices yet"), 90)
			return
		}
		ui.Column(c).Radius(10).Background(pal.inset).Padding(4).Children(func() {
			for i, d := range a.devices {
				if i > 0 {
					ui.Box(c).Height(1).Background(pal.divider).Margin(0, 10)
				}
				if b != nil && a.renaming == d.ID {
					a.renameRow(c, pal, b, d)
					continue
				}
				ui.Row(c.Key(d.ID)).AlignItems(ui.Center).Gap(12).Padding(10, 12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, d.Name).FontSize(13).FontWeight(600).SingleLine()
							if d.Revoked {
								ui.Text(c, a.t("已撤销", "Revoked")).FontSize(11).TextColor(pal.danger).Padding(1, 6).Radius(6).Background(pal.dangerSoft)
							}
						})
						seen := "–"
						if d.LastSeen != "" {
							seen = ago(a.zh, d.LastSeen)
						}
						ui.Text(c, fmt.Sprintf("%s %s · %s %s · %s", a.t("配对于", "paired"), when(d.Created), a.t("最近", "last seen"), seen, d.LastIP)).
							FontSize(11).TextColor(pal.textFaint).SingleLine()
					})
					if b == nil {
						return
					}
					if ui.Button(c, a.t("重命名", "Rename")).Clicked() {
						a.renaming, a.renameText = d.ID, d.Name
					}
					if d.Revoked {
						if ui.Button(c, a.t("删除", "Delete")).Clicked() {
							_ = b.DeleteDevice(d.ID)
							a.Refresh()
						}
					} else if ui.Button(c, a.t("撤销", "Revoke")).TextColor(pal.danger).Clicked() {
						_ = b.RevokeDevice(d.ID)
						a.toast = a.t("已撤销，这台设备需要重新配对", "Revoked: this device must pair again")
						a.Refresh()
					}
				})
			}
		})
	})
}

// renameRow edits a device's name in place. Enter or Save keeps it; the token stays.
func (a *App) renameRow(c *ui.Context, pal *palette, b Backend, d model.Device) {
	ui.Row(c.Key(d.ID)).AlignItems(ui.Center).Gap(8).Padding(8, 12).Children(func() {
		submitted := ui.TextInput(c.Key("rename"), &a.renameText).Placeholder(d.Name).AutoFocus().Grow(1).MinWidth(0).Submitted()
		if ui.Button(c, a.t("取消", "Cancel")).Clicked() {
			a.renaming = 0
		}
		name := strings.TrimSpace(a.renameText)
		save := ui.PrimaryButton(c, a.t("保存", "Save")).Disabled(name == "" || name == d.Name).Clicked()
		if (save || submitted) && name != "" {
			if err := b.RenameDevice(d.ID, name); err != nil {
				a.toast = err.Error()
				return
			}
			a.renaming = 0
			a.toast = a.t("已重命名", "Renamed")
			a.Refresh()
		}
	})
}

// ---------------------------------------------------------------- MCP

func (a *App) mcpPage(c *ui.Context, pal *palette) {
	b, _ := a.host.Backend()
	a.header(c, pal, a.t("AI 助手（MCP）", "AI Assistants (MCP)"), a.t("让 Claude 等 AI 助手只读查询你的健康数据", "Let Claude and other assistants read your health data"), nil)
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(4, 22, 22, 22).Gap(16).Children(func() {
			on := a.ov.MCPEnabled
			a.group(c, pal, a.t("访问权限", "Access"), "", func() {
				sub := a.t("关掉后，所有 AI 助手都连不上，令牌保留，重新打开就能用", "When off, no assistant can connect. Tokens are kept and work again when you turn it back on")
				a.settingRow(c, pal, a.t("允许 AI 助手读取健康数据", "Let AI assistants read health data"), sub, func() {
					if toggle(c, &on, pal).Changed() && b != nil {
						if err := b.SetMCPEnabled(on); err != nil {
							a.toast = err.Error()
						}
						a.ov.MCPEnabled = on
						a.mcp = nil
						a.Refresh()
					}
				})
			})
			if !on {
				return
			}
			ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(18).Radius(14).Background(pal.inset).Children(func() {
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					ui.Text(c, a.t("连接一个 AI 助手", "Connect an AI assistant")).FontSize(15).FontWeight(650)
					ui.Text(c, a.t("每个助手用单独的令牌，可以在「配对和设备」里撤销。助手读到的数据会发给它的模型服务商。",
						"Each assistant gets its own token, revocable under Pair & Devices. What it reads is sent to its model provider.")).
						FontSize(12).TextColor(pal.textMuted)
					ui.Text(c, a.ov.MCPURL).FontSize(12).Font("monospace").TextColor(pal.text).Selectable()
				})
				if ui.PrimaryButton(c, a.t("生成连接方式", "Create connection")).Clicked() && b != nil {
					go func() {
						m, err := b.NewMCPToken("AI assistant")
						a.host.Update(func() {
							if err != nil {
								a.toast = err.Error()
								return
							}
							a.mcp = &m
						})
					}()
				}
			})
			if a.mcp != nil {
				ui.Text(c, a.t("现在复制，之后不会再显示这个令牌。", "Copy it now: this token won't be shown again.")).FontSize(12).TextColor(pal.accent)
				titles := map[string]string{"claude_code": "Claude Code", "json": a.t("JSON 配置（Cursor 等用 mcpServers 的客户端）", "JSON (Cursor and other mcpServers clients)"),
						"vscode": a.t("VS Code（.vscode/mcp.json）", "VS Code (.vscode/mcp.json)"),
					"stdio_bridge": a.t("Claude Desktop（桥接）", "Claude Desktop (bridge)"), "agent_prompt": a.t("给任何 AI agent 的说明", "Instructions for any agent")}
				for i, cfg := range a.mcp.ConfigList {
					a.copyBlock(c, pal, i, firstNonEmpty(titles[cfg.ID], cfg.Title), cfg.Text)
				}
			}
			cmd, _ := json.Marshal(a.host.StdioCommand())
			a.copyBlock(c, pal, 99, a.t("本机 stdio（不需要令牌，用的就是这个 App，不用另外安装）", "On this computer, over stdio (no token; uses this app, nothing else to install)"),
				`{"mcpServers": {"healthsync": {"command": `+string(cmd)+`, "args": ["mcp"]}}}`)
			a.group(c, pal, a.t("提示词、资源和 Skill", "Prompts, resources and skill"), "", func() {
				ui.Text(c, a.t("连上后，AI 助手里可以直接选这些提示词：每周健康回顾、体检报告解读、睡眠分析、两个时间段对比。也能读取数据库结构、最新体检、近 7 天概况等资源。",
					"Once connected, the assistant offers these prompts: weekly review, checkup review, sleep analysis, compare two periods. It can also read resources such as the database schema, the latest checkup and the last 7 days.")).
					FontSize(12).TextColor(pal.textMuted)
				ui.Text(c, a.t("Skill 告诉 AI 助手怎么查你的数据。用下面的命令装到 Claude Code：", "The skill tells the assistant how to query your data. Install it in Claude Code with this command:")).
					FontSize(12).TextColor(pal.textMuted)
			})
			a.copyBlock(c, pal, 100, "Claude Code skill",
				"mkdir -p ~/.claude/skills/healthsync && curl -fsSL "+strings.TrimSuffix(a.ov.MCPURL, "/mcp")+"/skill.md -o ~/.claude/skills/healthsync/SKILL.md")
			if b != nil {
				a.settingRow(c, pal, "SKILL.md", a.t("用于 Claude.ai、Claude Desktop 等其它客户端：复制后新建一个 skill", "For Claude.ai, Claude Desktop and other clients: copy it into a new skill"), func() {
					if ui.Button(c, a.t("复制 SKILL.md", "Copy SKILL.md")).Clicked() {
						a.host.Copy(b.SkillMD())
						a.toast = a.t("已复制", "Copied")
					}
				})
			}
		})
	})
}

func (a *App) copyBlock(c *ui.Context, pal *palette, i int, title, text string) {
	blk := ui.Column(c.Key(i)).Gap(6)
	in := blk.AnimateTo("in", 1, ui.Animation{Duration: 260 * time.Millisecond, Ease: ui.EaseOut, Delay: time.Duration(i%10) * 50 * time.Millisecond})
	blk.Opacity(in).Translate(0, (1-in)*6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(pal.textMuted).Grow(1)
			if ui.Button(c, "").Label(a.t("复制", "Copy")).Tooltip(a.t("复制", "Copy")).Children(func() {
				ui.Icon(c, iconCopy).Size(14, 14)
				ui.Text(c, a.t("复制", "Copy")).FontSize(12)
			}).Clicked() {
				a.host.Copy(text)
				a.toast = a.t("已复制", "Copied")
			}
		})
		ui.Text(c, text).Font("monospace").FontSize(11.5).TextColor(pal.text).Padding(10, 12).Radius(8).Background(pal.inset).Selectable()
	})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- reports

func (a *App) reportsPage(c *ui.Context, pal *palette) {
	a.header(c, pal, a.t("体检报告", "Checkups"), a.t("在 App 里导入的体检报告（PDF、照片 → 结构化数值）", "Reports imported in the app (PDF or photo → values)"), nil)
	if len(a.reports) == 0 {
		ui.Column(c).Grow(1).Padding(0, 22, 22, 22).Children(func() { a.empty(c, pal, a.t("还没有体检报告", "No checkups yet"), 200) })
		return
	}
	cols := []ui.TableColumn{
		{Title: a.t("日期", "Date"), Width: 110}, {Title: a.t("机构", "Institution")},
		{Title: a.t("项目", "Items"), Width: 80, Align: ui.End}, {Title: a.t("异常", "Flagged"), Width: 80, Align: ui.End},
		{Title: a.t("收到", "Received"), Width: 110},
		{Title: "", Width: 110},
	}
	b, _ := a.host.Backend()
	ui.Column(c).Grow(1).MinHeight(0).Padding(0, 12, 12, 12).Children(func() {
		ui.Table(c, &a.repList, cols, len(a.reports), func(row, col int) {
			r := a.reports[row]
			switch col {
			case 0:
				ui.Text(c, r.Date).FontSize(13)
			case 1:
				ui.Text(c, r.Institution).FontSize(13).SingleLine()
			case 2:
				ui.Text(c, itoa(r.Items)).FontSize(13)
			case 3:
				t := ui.Text(c, itoa(r.Flagged)).FontSize(13)
				if r.Flagged > 0 {
					t.TextColor(pal.danger).FontWeight(600)
				}
			case 4:
				ui.Text(c, when(r.Received)).FontSize(12).TextColor(pal.textMuted)
			case 5:
				// the items and where each is in the originals, in the web dashboard
				if b != nil && ui.Button(c.Key("rep-open-"+r.ID), a.t("查看项目", "Items")).Tooltip(a.t("在网页面板里查看项目，点项目可在原件里定位", "See the items in the web dashboard; click one to find it in the original")).Clicked() {
					a.host.OpenURL(strings.TrimSuffix(b.DashboardURL(), "/") + "/#reports/" + url.PathEscape(r.ID))
				}
			}
		}).Grow(1)
	})
}
