# Changelog

Releases happen on their own: bump `version` in mygo.json, add its section here, push to
main. The desktop app shows the section of a new version in its update window.

## 0.0.1

First release.

- Receives Apple Health data from the HealthSync iPhone app over the local network, into
  SQLite: the pipe format, JSON, JSON Lines with uuid, source, device, time zone and
  metadata, deletions, and CSV.
- Desktop app for macOS, Windows and Linux: a menu bar icon and a native window with an
  overview, sync history, QR pairing and devices, AI assistant (MCP) setup, checkups and
  settings, including where data is stored.
- Headless server and Docker image with a web dashboard.
- Read-only MCP server for AI assistants, over HTTP and stdio.
- Updates itself, with signed updates.
- Sends crash reports (errors and versions, never health data); turn them off in Settings
  or with `HEALTHSYNC_NO_TELEMETRY=1`.
