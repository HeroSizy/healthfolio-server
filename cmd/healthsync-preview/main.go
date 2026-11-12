// Healthsync-preview runs the desktop app's window with sample data, in a browser
// (GOOS=js GOARCH=wasm) or on the desktop. It is for screenshots and design work.
//
//	go run github.com/egoist/mygo/cmd/mygo dev -web ./cmd/healthsync-preview
//
// URL fragments pick a page and a language: #history, #pair, #mcp, #settings, #en.
package main

import (
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/desktop"
)

func main() {
	var win *mygo.Window
	update := func(fn func()) {
		if win == nil {
			fn()
			return
		}
		win.Update(fn)
	}
	s := desktop.NewSample(update)
	frag := fragment()
	app := desktop.New(s, !strings.Contains(frag, "en"))
	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{Title: "Healthfolio", Width: 1080, Height: 720, Content: ui.View(app.View)})
		if strings.Contains(frag, "demo") {
			// a sync arrives after 3 s, and on #pair-demo a phone pairs after 6 s
			go func() {
				time.Sleep(3 * time.Second)
				update(func() { s.SimulateSync(280) })
				app.Refresh()
				if strings.Contains(frag, "pair") {
					time.Sleep(3 * time.Second)
					update(func() { s.SimulatePair("Zen 的 iPhone") })
					app.Refresh()
				}
			}()
		}
		if strings.Contains(frag, "welcome") {
			app.ShowOnboarding()
			return
		}
		for _, p := range []string{"history", "pair", "mcp", "reports", "settings"} {
			if strings.Contains(frag, p) {
				app.ShowPage(p)
				if p == "pair" {
					_, _ = s.NewPairCode()
				}
				if p == "mcp" && strings.Contains(frag, "token") {
					app.DemoMCP()
				}
				return
			}
		}
		app.Refresh()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
