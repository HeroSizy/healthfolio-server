#!/bin/sh
# Builds the desktop app to a fixed path, dist/dev/HealthSync, and runs it; with -n it only
# builds. macOS asks "accept incoming connections?" per program path and signature, and Go's
# ad hoc signature changes with every build, so on macOS this signs the build with
# HEALTHSYNC_SIGN_ID or a keychain identity named "HealthSync Dev" (a self-signed one from
# Keychain Access will do), else the first valid "Apple Development" one.
set -e
cd "$(dirname "$0")/.."
run=1
if [ "$1" = -n ]; then
	run=
	shift
fi
out=dist/dev/HealthSync
mkdir -p dist/dev
# the web dashboard the server embeds, built once (scripts/build-web.sh rebuilds it)
[ -f server/webapp/app.wasm.gz ] || sh scripts/build-web.sh >/dev/null
go build -o "$out" ./cmd/healthsync
if [ "$(uname)" = Darwin ]; then
	names='s/^ *[0-9]*) [0-9A-F]* "\(.*\)".*$/\1/p'
	id=$HEALTHSYNC_SIGN_ID
	if [ -z "$id" ]; then
		id=$(security find-identity -p codesigning | sed -n "$names" | grep -m1 -x 'HealthSync Dev' ||
			security find-identity -v -p codesigning | sed -n "$names" | grep -m1 '^Apple Development: ' || true)
	fi
	if [ -n "$id" ]; then
		codesign --force --sign "$id" --identifier com.herosizy.healthsync "$out"
		echo "dev.sh: signed $out with \"$id\"" >&2
	fi
fi
if [ -n "$run" ]; then
	exec "$out" "$@"
fi
