// healthsync-server receives Apple Health data from the HealthSync iPhone app and keeps it
// in a local SQLite database, with a web dashboard for sync history and QR-code pairing.
// This is the headless build, for servers, NAS boxes and Docker; cmd/healthsync is the
// desktop app with a native window.
package main

import (
	"os"

	"github.com/herosizy/healthsync-server/internal/cli"
)

func main() { cli.Main(os.Args[1:]) }
