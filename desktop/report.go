package desktop

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/model"
)

// A checkup report in the window: its items, and the page of the original each item is
// printed on, with the item framed. The server draws PDF pages as images (Backend
// ReportPage), so this is the same in the desktop app and in a browser.

// reportView is the report open on the Checkups page.
type reportView struct {
	id     string
	detail *model.ReportDetail
	err    string
	item   int // the item chosen, or -1

	// the page shown
	file    string
	page    int
	pages   int
	bmp     *ui.Bitmap
	bmpKey  string
	loading string // the page being fetched
	pageErr string
	reveal  bool // bring the framed item into view in the next frame
}

// pageWidth is how many pixels wide pages are asked for: sharp on a Retina screen at the
// width the viewer has, not more.
const pageWidth = 1400

var flagged = regexp.MustCompile(`(?i)^(high|low|h|l|abnormal|↑|↓)$`)

// OpenReport shows a checkup report (#reports/<id> in the web dashboard's address).
func (a *App) OpenReport(id string) {
	a.page = "reports"
	a.openReport(id)
	a.Refresh()
}

func (a *App) openReport(id string) {
	a.rv = reportView{id: id, item: -1}
	b, _ := a.host.Backend()
	if b == nil {
		return
	}
	go func() {
		d, err := b.ReportDetail(id)
		a.host.Update(func() {
			if a.rv.id != id {
				return
			}
			if err != nil {
				a.rv.err = err.Error()
				return
			}
			a.rv.detail = &d
			// the first original shows straight away, before any item is chosen
			if len(d.Files) > 0 {
				a.showPage(d.Files[0].Name, 0)
			}
		})
	}()
}

// showPage fetches page n of file, unless it is the page shown or on its way.
func (a *App) showPage(file string, n int) {
	key := a.rv.id + "\x00" + file + "\x00" + strconv.Itoa(n)
	if key == a.rv.bmpKey || key == a.rv.loading {
		return
	}
	b, _ := a.host.Backend()
	if b == nil {
		return
	}
	a.rv.loading, a.rv.pageErr = key, ""
	id := a.rv.id
	go func() {
		img, err := b.ReportPage(id, file, n, pageWidth)
		var bmp *ui.Bitmap
		if err == nil {
			bmp, err = ui.DecodeBitmap(img.Data)
		}
		a.host.Update(func() {
			if a.rv.loading != key {
				return // another page was asked for since
			}
			a.rv.loading = ""
			if err != nil {
				a.rv.pageErr = err.Error()
				return
			}
			a.rv.bmp, a.rv.bmpKey, a.rv.file, a.rv.page, a.rv.pages = bmp, key, file, n, img.Pages
		})
	}()
}

func (a *App) chooseItem(i int) {
	a.rv.item = i
	if loc := a.itemLocation(i); loc != nil {
		a.showPage(loc.File, loc.Page)
		a.rv.reveal = true
	}
}

func (a *App) itemLocation(i int) *model.ItemLocation {
	if a.rv.detail == nil || i < 0 || i >= len(a.rv.detail.Report.Items) {
		return nil
	}
	return a.rv.detail.Report.Items[i].Location
}

func (a *App) reportDetailPage(c *ui.Context, pal *palette) {
	d := a.rv.detail
	title, sub := a.t("体检报告", "Checkup"), ""
	if d != nil {
		r := d.Report
		title = firstNonEmpty(r.Title, r.Institution, r.Date)
		sub = r.Date
		if r.Title != "" && r.Institution != "" {
			sub += " · " + r.Institution
		}
		sub += " · " + a.t(itoa(len(r.Items))+" 项", plural(len(r.Items), false, "", "item"))
	}
	a.header(c, pal, title, sub, func() {
		if ui.Button(c, a.t("‹ 全部报告", "‹ All checkups")).Clicked() {
			a.rv = reportView{}
		}
	})
	switch {
	case a.rv.err != "":
		ui.Column(c).Grow(1).Padding(0, a.gutter(), 22, a.gutter()).Children(func() { a.empty(c, pal, a.rv.err, 200) })
		return
	case d == nil:
		ui.Column(c).Grow(1).Padding(0, a.gutter(), 22, a.gutter()).Children(func() { a.empty(c, pal, a.t("正在打开…", "Opening…"), 200) })
		return
	}
	if a.narrow {
		// one column: the page on top, the items under it
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(4, a.gutter(), 22, a.gutter()).Gap(14).Children(func() {
				a.pageViewer(c, pal)
				a.reportItems(c, pal)
			})
		})
		return
	}
	ui.Row(c).Grow(1).MinHeight(0).Gap(16).Padding(4, a.gutter(), 16, a.gutter()).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).Basis(0).MinWidth(0).Children(func() { a.reportItems(c, pal) })
		ui.Scroll(c).Grow(1.35).Basis(0).MinWidth(0).Children(func() { a.pageViewer(c, pal) })
	})
}

// reportItems lists the items: name, result, range, and a mark when it can be found in
// the original.
func (a *App) reportItems(c *ui.Context, pal *palette) {
	items := a.rv.detail.Report.Items
	if len(items) == 0 {
		a.empty(c, pal, a.t("这份报告没有项目", "This report has no items"), 120)
		return
	}
	ui.Column(c).Radius(10).Background(pal.inset).Padding(4).Gap(1).Children(func() {
		for i, it := range items {
			sel := a.rv.item == i
			row := ui.ButtonBase(c.Key("item-"+itoa(i))).Padding(8, 10).Radius(8).AlignItems(ui.Center).Gap(10).
				Label(it.Name).Transition(ui.ElementTransition{Colors: true, Duration: 140 * time.Millisecond})
			switch {
			case sel:
				row.Background(pal.accentSoft)
			case row.Hovered():
				row.Background(pal.rowHover)
			}
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, it.Name).FontSize(13).FontWeight(map[bool]int{true: 600, false: 500}[sel]).TextColor(pal.text).MaxLines(2)
					if it.OriginalName != "" && it.OriginalName != it.Name {
						ui.Text(c, it.OriginalName).FontSize(11).TextColor(pal.textFaint).SingleLine()
					}
				})
				val := ui.Text(c, valueText(it.Value)+" "+it.Unit).FontSize(13).NoWrap()
				if flagged.MatchString(it.Flag) {
					val.TextColor(pal.danger).FontWeight(650)
				} else {
					val.TextColor(pal.text).FontWeight(550)
				}
				if !a.narrow {
					ui.Text(c, it.Range).FontSize(12).TextColor(pal.textMuted).NoWrap().Width(90).TextAlign(ui.End)
				}
				mark := ui.Icon(c, iconLocate).Size(14, 14).TextColor(pal.accent)
				if it.Location == nil {
					mark.Opacity(0) // keeps the column lined up
				}
			})
			if row.Clicked() {
				a.chooseItem(i)
			}
		}
	})
}

func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// pageViewer shows the page of the original, with the chosen item framed on it.
func (a *App) pageViewer(c *ui.Context, pal *palette) {
	d := a.rv.detail
	if len(d.Files) == 0 {
		a.empty(c, pal, a.t("服务器上没有这份报告的原件（App 备份时要勾选「原始报告文件」）", "No original of this report on the server (turn on Original report files in the app's backup)"), 160)
		return
	}
	ui.Column(c).Gap(10).Children(func() {
		// which file and page, and paging
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if len(d.Files) > 1 {
				for _, f := range d.Files {
					sel := f.Name == a.rv.file
					btn := ui.ButtonBase(c.Key("file-"+f.Name)).Padding(4, 10).Radius(8).Label(f.Name)
					if sel {
						btn.Background(pal.accentSoft)
					} else if btn.Hovered() {
						btn.Background(pal.rowHover)
					}
					btn.Children(func() {
						ui.Text(c, f.Name).FontSize(12).TextColor(map[bool]ui.Color{true: pal.accent, false: pal.textMuted}[sel]).SingleLine().MaxWidth(160)
					})
					if btn.Clicked() && !sel {
						a.showPage(f.Name, 0)
					}
				}
			} else {
				ui.Text(c, d.Files[0].Name).FontSize(12).TextColor(pal.textMuted).SingleLine()
			}
			ui.Spacer(c)
			if a.rv.pages > 1 {
				if ui.Button(c.Key("prev"), "‹").Disabled(a.rv.page == 0).Clicked() {
					a.showPage(a.rv.file, a.rv.page-1)
				}
				ui.Text(c, a.t(fmt.Sprintf("第 %d / %d 页", a.rv.page+1, a.rv.pages), fmt.Sprintf("Page %d of %d", a.rv.page+1, a.rv.pages))).
					FontSize(12).TextColor(pal.textMuted).NoWrap()
				if ui.Button(c.Key("next"), "›").Disabled(a.rv.page >= a.rv.pages-1).Clicked() {
					a.showPage(a.rv.file, a.rv.page+1)
				}
			}
		})
		switch {
		case a.rv.pageErr != "":
			a.empty(c, pal, a.rv.pageErr, 160)
			return
		case a.rv.bmp == nil:
			a.empty(c, pal, a.t("正在打开原件…", "Opening the original…"), 300)
			return
		}
		if loc := a.itemLocation(a.rv.item); loc != nil && loc.File != a.rv.file {
			ui.Text(c, a.t("这一项在 "+loc.File, "This item is in "+loc.File)).FontSize(12).TextColor(pal.textMuted)
		}
		w, h := a.rv.bmp.Size()
		sheet := ui.Column(c.Key("sheet")).AspectRatio(float32(w)/float32(max(1, h))).Radius(8).Clip().
			Background(ui.Hex("#ffffff")).Border(1, pal.cardBorder)
		// a new page fades in, still while it loads (the old one stays until then)
		in := sheet.AnimateTo(a.rv.bmpKey, 1, ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.EaseOut})
		sheet.Opacity(0.4 + 0.6*in)
		if a.rv.loading != "" {
			sheet.Opacity(0.55)
		}
		sheet.Children(func() {
			ui.Image(c, a.rv.bmp).Fill()
			loc := a.itemLocation(a.rv.item)
			if loc == nil || loc.File != a.rv.file || loc.Page != a.rv.page {
				return
			}
			// the frame stands a little off the text (1% of the page's width on every
			// side), so its border never covers the item it frames
			padX := 0.01
			padY := padX * float64(w) / float64(max(1, h))
			x0, y0 := max(0, loc.X-padX), max(0, loc.Y-padY)
			x1, y1 := min(1, loc.X+loc.W+padX), min(1, loc.Y+loc.H+padY)
			hl := ui.Box(c.Key("hl-"+itoa(a.rv.item))).Absolute().
				LeftPercent(float32(x0*100)).TopPercent(float32(y0*100)).
				WidthPercent(float32((x1-x0)*100)).HeightPercent(float32((y1-y0)*100)).
				Radius(5).Border(2, pal.accent).Background(pal.accent.Alpha(0.08))
			// the frame springs onto the item, then sits still
			pop := hl.AnimateTo("pop", 1, ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.3}})
			hl.Scale(1.15-0.15*pop, 1.15-0.15*pop).Opacity(pop).Shadow(0, 0, 0, 4*pop, pal.accent.Alpha(0.18))
			if a.rv.reveal && a.rv.loading == "" {
				hl.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter, Animation: &ui.Smooth})
				a.rv.reveal = false
			}
		})
	})
}
