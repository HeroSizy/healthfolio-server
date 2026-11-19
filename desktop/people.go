package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/model"
)

// The family: each person's data is kept apart, in a database of their own. The overview
// and the checkups show one person at a time (a.person); a bar of names switches between
// them, and the family cards on the overview show everyone at a glance.

var relations = map[string]string{
	"spouse": "配偶", "partner": "伴侣", "child": "孩子", "son": "儿子", "daughter": "女儿",
	"mother": "母亲", "father": "父亲", "parent": "父母", "sibling": "兄弟姐妹",
	"grandparent": "祖父母", "other": "其他",
}

// personName is a person's name, or what to call them while they have none.
func (a *App) personName(p model.Person) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Owner {
		return a.t("我", "Me")
	}
	return a.t("未命名", "Unnamed")
}

// relationLabel is how a person is related to the owner, in the window's language.
func (a *App) relationLabel(p model.Person) string {
	if p.Owner {
		return a.t("主人", "Owner")
	}
	if a.zh {
		if zh, ok := relations[p.Relation]; ok {
			return zh
		}
	}
	return p.Relation
}

// personKey is what the backend calls a person: "" for the owner, whose id can change
// when their phone claims it.
func personKey(p model.Person) string {
	if p.Owner {
		return ""
	}
	return p.ID
}

// choosePerson shows another person's data, dropping what was shown of the last one.
func (a *App) choosePerson(p model.Person) {
	key := personKey(p)
	if key == a.person {
		return
	}
	a.person = key
	a.selType, a.daily, a.typeFilter = "", nil, ""
	a.reports, a.rv = nil, reportView{}
	a.merging, a.mergeInto = "", ""
	// show the name at once; the numbers follow
	a.ov.Person = p
	a.Refresh()
}

// personBar is a row of names to switch whose data is shown, when there is more than one.
func (a *App) personBar(c *ui.Context, pal *palette) {
	people := a.ov.People
	if len(people) < 2 {
		return
	}
	cur := a.ov.Person.ID
	ui.ScrollHorizontal(c).Shrink(0).Scrollbars(false).Padding(0, a.gutter(), 10, a.gutter()).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			for _, p := range people {
				sel := p.ID == cur
				r := ui.ButtonBase(c.Key("pp-"+p.ID)).Height(30).Padding(0, 12).Radius(15).AlignItems(ui.Center).Gap(6).
					Label(a.personName(p))
				switch {
				case sel:
					r.Background(pal.accentSoft).Border(1, pal.accent.Alpha(0.45))
				case r.Hovered():
					r.Background(pal.rowHover)
				default:
					r.Background(pal.chip)
				}
				r.Children(func() {
					fg := pal.text
					if sel {
						fg = pal.accent
					}
					ui.Text(c, a.personName(p)).FontSize(13).FontWeight(map[bool]int{true: 650, false: 500}[sel]).TextColor(fg).SingleLine()
					if rel := a.relationLabel(p); rel != "" && !a.narrow {
						ui.Text(c, rel).FontSize(11).TextColor(pal.textFaint).SingleLine()
					}
				})
				if r.Clicked() {
					a.choosePerson(p)
				}
			}
		})
	})
}

// familySection is a card for each person: how much of their data there is, what their
// last checkup flagged, and when their phone last synced. A card shows that person's data;
// its menu merges them into someone else, after a second click.
func (a *App) familySection(c *ui.Context, pal *palette) {
	people := a.ov.People
	if len(people) < 2 {
		return
	}
	b, _ := a.host.Backend()
	a.section(c, pal, a.t("全家", "Family"), a.t("每个人的数据分开存放", "Each person's data is kept apart"), nil, func() {
		ui.Row(c).Gap(12).Wrap().AlignItems(ui.Stretch).Children(func() {
			for _, p := range people {
				a.personCard(c, pal, b, p)
			}
		})
	})
}

func (a *App) personCard(c *ui.Context, pal *palette, b Backend, p model.Person) {
	sel := p.ID == a.ov.Person.ID
	card := ui.Column(c.Key("pc-"+p.ID)).Grow(1).Basis(a.tileBasis()).MinWidth(0).Padding(10, 12, 14, 12).Radius(12).Gap(6).Background(pal.inset)
	if sel {
		card.Border(1, pal.accent.Alpha(0.45))
	}
	card.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			// the name shows this person's data
			r := ui.ButtonBase(c.Key("show")).Grow(1).MinWidth(0).Padding(4).Radius(8).AlignItems(ui.Center).Justify(ui.Start).Gap(8).
				Label(a.t("查看 ", "Show ") + a.personName(p))
			if r.Hovered() && !sel {
				r.Background(pal.rowHover)
			}
			r.Children(func() {
				ui.Text(c, a.personName(p)).FontSize(15).FontWeight(650).TextColor(pal.text).SingleLine().MinWidth(0)
				if rel := a.relationLabel(p); rel != "" {
					ui.Text(c, rel).FontSize(11).TextColor(pal.textMuted).Padding(1, 6).Radius(6).Background(pal.chip).SingleLine()
				}
			})
			if r.Clicked() {
				a.choosePerson(p)
			}
			if b != nil && !p.Owner {
				a.mergeMenu(c, b, p)
			}
		})
		ui.Column(c).Gap(6).Padding(0, 4).Children(func() { a.personFacts(c, pal, p) })
		if a.merging == p.ID {
			a.mergeConfirm(c, pal, b, p)
		}
	})
}

// personFacts are a family card's lines: how much data, the last checkup, the last sync.
func (a *App) personFacts(c *ui.Context, pal *palette, p model.Person) {
	ui.Text(c, fmt.Sprintf("%s · %s", plural(p.Records, a.zh, "条记录", "record"), plural(p.Reports, a.zh, "份体检", "checkup"))).
		FontSize(12).TextColor(pal.textMuted).SingleLine()
	if p.LatestReport != "" {
		col := pal.textMuted
		msg := a.t(p.LatestReport+" 体检无异常", "Checkup of "+p.LatestReport+": nothing flagged")
		if p.Flagged > 0 {
			col = pal.danger
			msg = a.t(fmt.Sprintf("%s 体检 %d 项异常", p.LatestReport, p.Flagged), fmt.Sprintf("Checkup of %s: %d flagged", p.LatestReport, p.Flagged))
		}
		ui.Text(c, msg).FontSize(12).TextColor(col).SingleLine()
	}
	synced := a.t("还没有同步", "Never synced")
	if p.LastReceived != nil {
		synced = a.t("最近同步 ", "Synced ") + ago(a.zh, *p.LastReceived)
	}
	if p.Devices > 0 {
		synced += " · " + plural(p.Devices, a.zh, "台设备", "device")
	}
	ui.Text(c, synced).FontSize(11).TextColor(pal.textFaint).SingleLine()
}

// mergeMenu offers to merge p into each other person: when one profile turns out to be
// someone who already had one (a second phone, a profile made by hand).
func (a *App) mergeMenu(c *ui.Context, b Backend, p model.Person) {
	ui.PopoverMenuButton(c.Key("pm-"+p.ID), "⋯", func(m *ui.Menu) {
		m.Submenu(a.t("合并到…", "Merge into…"), func(m *ui.Menu) {
			for _, q := range a.ov.People {
				if q.ID == p.ID {
					continue
				}
				if m.Item(a.personName(q)).Chosen() {
					a.merging, a.mergeInto = p.ID, q.ID
				}
			}
		})
	}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
}

// mergeConfirm asks once more before a merge, which can't be undone.
func (a *App) mergeConfirm(c *ui.Context, pal *palette, b Backend, p model.Person) {
	var into model.Person
	for _, q := range a.ov.People {
		if q.ID == a.mergeInto {
			into = q
		}
	}
	if into.ID == "" || b == nil {
		a.merging, a.mergeInto = "", ""
		return
	}
	ui.Column(c).Gap(8).Padding(10).Margin(4, 0, 0, 0).Radius(10).Background(pal.dangerSoft).Children(func() {
		ui.Text(c, a.t(
			fmt.Sprintf("把 %s 的记录、体检报告和设备都并入 %s，然后删除 %s。不能撤销。", a.personName(p), a.personName(into), a.personName(p)),
			fmt.Sprintf("Move %s's records, checkups and devices to %s, then remove %s. This can't be undone.", a.personName(p), a.personName(into), a.personName(p)),
		)).FontSize(12).TextColor(pal.text)
		ui.Row(c).Gap(8).Children(func() {
			ui.Spacer(c)
			if ui.Button(c, a.t("取消", "Cancel")).Clicked() {
				a.merging, a.mergeInto = "", ""
			}
			if ui.Button(c, a.t("合并", "Merge")).TextColor(pal.danger).Clicked() {
				if err := b.MergePeople(p.ID, into.ID); err != nil {
					a.toast = err.Error()
				} else {
					a.toast = a.t("已合并到 "+a.personName(into), "Merged into "+a.personName(into))
				}
				if personKey(p) == a.person {
					a.choosePerson(into)
				}
				a.merging, a.mergeInto = "", ""
				a.Refresh()
			}
		})
	})
}

// devicePerson shows whose data a device sends, with a menu to move it to someone else
// and, for a phone, whether it may manage the whole family.
func (a *App) devicePerson(c *ui.Context, pal *palette, b Backend, d model.Device) {
	people := a.ov.People
	name := d.PersonName
	if name == "" {
		for _, p := range people {
			if p.ID == d.Person {
				name = a.personName(p)
			}
		}
	}
	if d.Person == "" {
		if d.Scope == "mcp" {
			name = a.t("全家", "Everyone")
		} else {
			name = a.t("未指定", "No one")
		}
	}
	if b == nil || len(people) < 2 || d.Revoked {
		ui.Text(c, name).FontSize(12).TextColor(pal.textMuted).SingleLine()
		return
	}
	ui.PopoverMenuButton(c.Key("dp"), name, func(m *ui.Menu) {
		if d.Scope == "mcp" {
			if m.Item(a.t("全家", "Everyone")).Radio(d.Person == "").Chosen() {
				a.moveDevice(b, d, "")
			}
			m.Separator()
		}
		for _, p := range people {
			if m.Item(a.personName(p)).Radio(p.ID == d.Person).Chosen() && p.ID != d.Person {
				a.moveDevice(b, d, p.ID)
			}
		}
	})
	if d.Scope != "mcp" {
		on := d.Family
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, a.t("可管理全家", "Manages family")).FontSize(12).TextColor(pal.textMuted).SingleLine().
				Tooltip(a.t("打开后，这台手机可以查看和上传全家每个人的数据", "On: this phone can read and send data for everyone in the family"))
			if toggle(c, &on, pal).Changed() {
				if err := b.SetDeviceFamily(d.ID, on); err != nil {
					a.toast = err.Error()
				}
				a.Refresh()
			}
		})
	}
}

func (a *App) moveDevice(b Backend, d model.Device, person string) {
	if err := b.SetDevicePerson(d.ID, person); err != nil {
		a.toast = err.Error()
		return
	}
	a.toast = a.t("已更改", "Changed")
	a.Refresh()
}
