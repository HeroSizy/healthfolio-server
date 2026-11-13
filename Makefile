# make run        the desktop app, built to a fixed path (dist/dev/HealthSync) and run
# make preview    the desktop app's window with sample data, in the browser
# make server     the headless server (what Docker runs)
# make app        packaged apps in build/ (.app/.dmg on macOS, .deb/.tar.gz on Linux, .exe)
# make test
# make web        the web dashboard (the window compiled to WebAssembly) into server/webapp,
#                 which the server embeds; server and app build it first
#
# A firewall such as LuLu, and macOS's "accept incoming connections?" prompt, key on the
# program's path and signature: `go run` builds to a new path each time. On macOS make run
# signs with SIGN_ID, or a keychain identity named "HealthSync Dev" (a self-signed one from
# Keychain Access will do), else the first "Apple Development" one: see scripts/dev.sh.

SIGN_ID ?=

.PHONY: run preview server app test web

run:
	HEALTHSYNC_SIGN_ID="$(SIGN_ID)" scripts/dev.sh

preview:
	go tool mygo dev -web ./cmd/healthsync-preview

web:
	sh scripts/build-web.sh

server: web
	go run . serve

app: web
	go tool mygo build

test:
	go vet ./... && go test ./...
