# Changelog

Releases happen on their own: bump `version` in mygo.json, add its section here, push to
main. The desktop app shows the section of a new version in its update window.

## 0.0.3

- Pairing without a code no longer fails at once when the Allow dialog cannot be shown (a
  Mac mini without a screen, or a locked screen). The request waits, and you can allow it
  in the app window or the dashboard, or pair with the 6-digit code.
- Pairing errors carry a reason code, so the iPhone app shows a clear message in your
  language. A refused pairing is now in the history, with the address it came from.
- A checkup report that is sent again replaces its items instead of leaving old rows.

## 0.0.2

- Turn AI assistant access (MCP) on or off in the app or the dashboard. The change applies
  at once.
- MCP prompts: weekly review, checkup review, sleep analysis, compare two periods.
- MCP resources: database schema, data types, latest checkup, last 7 days, daily values.
- A HealthSync skill for AI assistants, at `/skill.md`, with an install command in the app.
- The stdio MCP config uses the app itself, and reads the data folder from Settings.
- Rename paired devices. The token stays the same.
- Tailscale: the server sends its Tailscale address to the app and in the QR code, so the
  iPhone can sync away from home.
- Fix: old Bonjour adverts no longer stay after the app quits.
- Settings is at the bottom of the sidebar.
- Linux: the default data folder is now `~/.local/share/healthsync`.
- READMEs in English and Simplified Chinese.

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
