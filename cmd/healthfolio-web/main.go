//go:build js

// Healthfolio-web is the web dashboard: the desktop app's window, compiled to WebAssembly,
// reading the server that serves the page through its dashboard API. scripts/build-web.sh
// builds it into server/webapp, which the server embeds and serves at /.
package main

import (
	"log"
	"net/url"
	"strings"
	"syscall/js"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/herosizy/healthsync-server/desktop"
	"github.com/herosizy/healthsync-server/model"
)

// host is the page around the window.
type host struct {
	win    *mygo.Window
	remote *desktop.Remote
	app    *desktop.App
}

func (h *host) Settings() model.Settings                    { return model.Settings{Onboarded: true} }
func (h *host) SaveSettings(model.Settings) error           { return nil }
func (h *host) DefaultDataDir() string                      { return "" }
func (h *host) StdioCommand() string                        { return "healthsync-server" }
func (h *host) ChooseFolder(_, _ string, done func(string)) { done("") }
func (h *host) Updates() model.Updates                      { return model.Updates{} }
func (h *host) CheckForUpdates()                            {}
func (h *host) SetAutomaticUpdates(bool)                    {}

func (h *host) Backend() (desktop.Backend, string) { return h.remote, "" }

func (h *host) Copy(text string) {
	if cb := js.Global().Get("navigator").Get("clipboard"); cb.Truthy() {
		cb.Call("writeText", text)
	}
}

func (h *host) OpenURL(url string) {
	js.Global().Call("open", url, "_blank", "noopener")
}

func (h *host) Update(fn func()) {
	if h.win == nil {
		fn()
		return
	}
	h.win.Update(fn)
}

func main() {
	h := &host{remote: desktop.NewRemote(js.Global().Get("location").Get("origin").String())}
	lang := strings.ToLower(js.Global().Get("navigator").Get("language").String())
	h.app = desktop.New(h, strings.HasPrefix(lang, "zh"))
	h.app.SetWeb()
	h.remote.Changed = h.app.Refresh
	h.remote.LoadSkill()
	mygo.App.WhenReady(func() {
		h.win = mygo.NewWindow(mygo.WindowOptions{Title: "Healthfolio", Content: ui.View(h.app.View)})
		// the page opens on the tab its address names, as the old dashboard did
		frag := strings.TrimPrefix(js.Global().Get("location").Get("hash").String(), "#")
		parts := strings.Split(frag, "/")
		switch frag = parts[0]; {
		case frag == "reports" && len(parts) > 1 && parts[1] != "":
			// #reports/<id>, as the iPhone app and older links open a report
			id, _ := url.PathUnescape(parts[1])
			h.app.OpenReport(id)
		case frag == "history" || frag == "pair" || frag == "mcp" || frag == "reports":
			h.app.ShowPage(frag)
		case frag == "devices":
			h.app.ShowPage("pair")
		default:
			h.app.Refresh()
		}
		// pick up new syncs while the page is in front; a hidden tab asks nothing
		go func() {
			for range time.Tick(5 * time.Second) {
				if js.Global().Get("document").Get("hidden").Bool() {
					continue
				}
				h.app.Tick()
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
