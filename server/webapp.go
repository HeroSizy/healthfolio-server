package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
)

// The web dashboard is the desktop app's window (package desktop) compiled to
// WebAssembly by scripts/build-web.sh into webapp/: app.wasm.gz and Go's wasm_exec.js.
// A build without them (go build alone, without the script) serves the plain HTML
// dashboard at / instead, as before.
//
//go:embed webapp
var webappFS embed.FS

type webAsset struct {
	data []byte // as stored: app.wasm gzipped, the rest plain
	gz   bool
	etag string
	typ  string
}

var (
	webOnce   sync.Once
	webAssets map[string]webAsset
)

func loadWebApp() {
	webAssets = map[string]webAsset{}
	add := func(name, file, typ string, gz bool) {
		b, err := webappFS.ReadFile("webapp/" + file)
		if err != nil {
			return
		}
		sum := sha256.Sum256(b)
		webAssets[name] = webAsset{data: b, gz: gz, typ: typ, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	add("/app.wasm", "app.wasm.gz", "application/wasm", true)
	add("/wasm_exec.js", "wasm_exec.js", "text/javascript; charset=utf-8", false)
	if _, ok := webAssets["/app.wasm"]; !ok {
		webAssets = nil
		return
	}
	if _, ok := webAssets["/wasm_exec.js"]; !ok {
		webAssets = nil
		return
	}
	for name, body := range map[string]string{"/": webPage, "/boot.js": webBoot} {
		sum := sha256.Sum256([]byte(body))
		typ := "text/html; charset=utf-8"
		if strings.HasSuffix(name, ".js") {
			typ = "text/javascript; charset=utf-8"
		}
		webAssets[name] = webAsset{data: []byte(body), typ: typ, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	webAssets["/index.html"] = webAssets["/"]
}

// hasWebApp reports whether this build carries the WebAssembly dashboard.
func hasWebApp() bool {
	webOnce.Do(loadWebApp)
	return webAssets != nil
}

// serveWebApp answers for the files of the WebAssembly dashboard, and reports whether it
// did. Every file is checked again on each load (no-cache) but costs nothing when it has
// not changed: the 5 MB app comes back as 304 by its ETag.
func serveWebApp(w http.ResponseWriter, r *http.Request) bool {
	if !hasWebApp() {
		return false
	}
	a, ok := webAssets[r.URL.Path]
	if !ok {
		return false
	}
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", a.etag)
	h.Set("Content-Type", a.typ)
	h.Set("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	if !a.gz {
		_, _ = w.Write(a.data)
		return true
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		_, _ = w.Write(a.data)
		return true
	}
	zr, err := gzip.NewReader(bytes.NewReader(a.data))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return true
	}
	_, _ = io.Copy(w, zr)
	return true
}

// webPage shows a spinner while the app loads (5 MB the first time), then the window
// fills it. Its colors follow the system's, as the window's do, so it doesn't flash.
const webPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Healthfolio</title>
<link rel="icon" href="icon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="apple-touch-icon.png">
<style>
:root { color-scheme: light dark; }
html, body { margin: 0; height: 100%; background: #f2f2f5; }
@media (prefers-color-scheme: dark) { html, body { background: #161618; } }
body { display: flex; flex-direction: column; gap: 14px; align-items: center; justify-content: center; color: #8e8e93;
  font: 13px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", sans-serif; }
.logo { width: 56px; height: 56px; border-radius: 14px; background: url("icon.svg") center/100% no-repeat;
  box-shadow: 0 6px 20px rgba(255, 55, 95, .25); animation: breathe 1.6s ease-in-out infinite; }
@keyframes breathe { 50% { transform: scale(.94); opacity: .8; } }
.bar { width: 160px; height: 3px; border-radius: 2px; background: rgba(127,127,127,.2); overflow: hidden; }
.bar i { display: block; height: 100%; width: 0; background: #ff375f; transition: width .2s; }
.error { max-width: 80%; white-space: pre-wrap; text-align: center; }
a { color: #ff375f; }
</style>
</head>
<body>
<div class="logo" aria-hidden="true"></div>
<div class="bar"><i id="progress"></i></div>
<noscript>JavaScript is off. <a href="lite/">Open the lite dashboard</a>.</noscript>
<script src="wasm_exec.js"></script>
<script src="boot.js"></script>
</body>
</html>
`

// webBoot loads the app, showing how much of it has arrived; if it can't run (an old
// browser, no WebAssembly), it sends the browser to the lite dashboard.
const webBoot = `"use strict";
(async () => {
  const bar = document.getElementById("progress");
  const fail = (e) => {
    const p = document.createElement("p");
    p.className = "error";
    p.innerHTML = 'The dashboard did not load: ' + String(e).replace(/[<>&]/g, "") + '<br><a href="lite/">Open the lite dashboard</a>';
    document.body.replaceChildren(p);
  };
  if (!window.WebAssembly) { location.replace("lite/" + location.hash); return; }
  try {
    const res = await fetch("app.wasm");
    if (!res.ok) throw new Error(res.status + " " + res.statusText);
    // the server sends it gzipped, so Content-Length is the gzipped size: count up to it
    const total = +res.headers.get("Content-Length") || 5.2e6;
    const reader = res.body.getReader();
    const chunks = [];
    let got = 0;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      got += value.length;
      bar.style.width = Math.min(95, 100 * got / (total * 3.7)) + "%";
    }
    bar.style.width = "100%";
    const bytes = await new Blob(chunks).arrayBuffer();
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    // once the window is on the page, the loading logo goes (its animation would keep
    // running under the window otherwise)
    new MutationObserver((_, obs) => {
      if (!document.querySelector(".mygo-window")) return;
      obs.disconnect();
      document.querySelectorAll(".logo, .bar").forEach(el => el.remove());
    }).observe(document.body, { childList: true });
    go.run(instance);
  } catch (e) { fail(e); }
})();
`
