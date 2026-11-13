#!/bin/sh
# Builds the web dashboard (the desktop window compiled to WebAssembly, cmd/healthfolio-web)
# into server/webapp, which the server embeds. Run it before building the server or the
# apps; without it they serve the lite HTML dashboard at / instead.
set -eu
cd "$(dirname "$0")/.."
out=server/webapp
GOOS=js GOARCH=wasm go build -trimpath -tags mygo_noinspector \
	-ldflags "-s -w -X github.com/egoist/mygo.production=1" -o "$out/app.wasm" ./cmd/healthfolio-web
gzip -9 -n -f "$out/app.wasm"
cp -f "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
chmod 644 "$out/wasm_exec.js"
ls -l "$out"
