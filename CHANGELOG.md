# Changelog

Releases happen on their own: bump `version` in mygo.json, add its section here, push to
main. The desktop app shows the section of a new version in its update window.

## 0.0.16

- The data folder and the health database are readable by your user only. Older versions
  left them readable by other users of the computer.
- The web dashboard stops taking passwords from an address after 10 wrong ones in 15
  minutes, so a weak password can't be guessed by trying many.
- When the window is narrow, the four numbers on the overview wrap into two rows instead
  of being cut short.
- The sync history shows the server's own messages in Chinese too, and «Rejected» covers
  wrong passwords as well as wrong tokens.

## 0.0.15

- HealthSync is now **Healthfolio**, with a new icon, to match the iPhone app (健康册). Your
  data, pairing, settings and AI assistant setups stay as they are: the data folder, the
  `healthsync-server` command and its Docker image keep their names.
- The project moved to github.com/HeroSizy/healthfolio-server; the old address redirects.

## 0.0.14

- The macOS app is signed with a Developer ID and notarized by Apple: it opens with a
  double-click, without right-click › Open or a trip to Privacy & Security.

## 0.0.13

- Checkup reports keep every item when a report prints the same name twice (for example
  white blood cells in the blood count and in the urine test). Before, the second one
  replaced the first. Items lost this way come back by themselves from the report's
  `report.json` the first time this version starts.
- Checkup originals are found again after the data folder was moved or copied to another
  computer.
- The dashboard opens without a password only for a browser on this computer that goes
  straight to `127.0.0.1` or `localhost`. Requests that come through a reverse proxy or a
  tunnel on the same computer, or from a web page that points its own name at 127.0.0.1,
  now need the password like any other computer, and never see the shared token.
- Saving the settings in the desktop app no longer leaves a database connection open each
  time, and the menu bar menu can't crash while the server restarts.

## 0.0.12

- AI assistant setup: VS Code gets its own snippet (it uses "servers", not "mcpServers"),
  and the Claude Desktop snippet passes the token through an environment variable, as
  mcp-remote advises, so it can't be split at the space.

## 0.0.11

- A paired iPhone can read its checkup reports back (the report and every original file),
  so the app can restore them from this server after a reinstall or on a new phone.

## 0.0.10

- Checkup reports keep everything the app sends: where each item is in the original, the
  name as printed, the title and the list of originals. Every original file is kept (a
  report can have several), checked against its SHA-256.
- In the dashboard, click a checkup to see its items, and click an item to see it framed
  on its page of the original PDF or photo. In the app, Checkups › Items opens it.
- AI assistants (MCP) can now also list the checkup reports, read their original files,
  see where each item is printed, and read the iPhone app's AI conversations and settings.

## 0.0.9

- The health database is about a quarter smaller: an index that repeated another one is
  gone, the database is compacted once in the background after the update (a few
  seconds), and its write-ahead log no longer stays as big as the largest sync made it.

## 0.0.8

- The overview of the app and of the dashboard shows how much room the data takes: in all
  and by kind (health database, checkups, AI chat backups, app settings backups, CSV
  files, other), with how much the disk has left.

## 0.0.7

- Deleting a conversation in the iPhone app also deletes its backup on this computer
  (`DELETE /backup/chats/{id}`, `POST /backup/chats/delete`; `/health` lists
  `chats_delete`).

## 0.0.6

- The first launch walks you through setting up: what HealthSync does, the computer's
  name and data folder, pairing your iPhone with a QR code, and where things are. It shows
  only on a computer with no paired device and no data, and you can skip it.

## 0.0.5

- Pairing without a code now takes two steps and shows a 4-digit confirmation code on the
  phone and in the Allow dialog, so you can tell your phone's request from someone else's.
  The phone can cancel a request, and the dialog then closes. Each address can ask 6 times
  in 10 minutes.
- Pairing the same phone again updates its entry instead of adding another one.
- The phone can check that its token still works, and learns whether it was revoked.
- The iPhone app can back up its AI conversations and its settings (never keys or
  passwords) to this computer, in the `chats` and `settings` folders of the data folder.
- Tokens for AI assistants are read-only: they can read health data through MCP, but
  cannot send data or make other tokens. Tokens made before 0.0.5 keep working as before;
  make a new one to get a read-only token.
- The dashboard needs a password when it is opened from another computer. On the
  computer running HealthSync it opens without one. The shared token is shown only there.
- When a new version is out, the bottom left of the window says so. The HealthSync menu in
  the menu bar has Check for Updates….
- The app no longer keeps a CPU core busy while it runs.
- A slow Tailscale no longer slows down replies: its address is looked up in the background.
- The overview chart counts records by the day they were taken, not the day they arrived.
- A sync sent in many batches is one row in the sync history, not dozens.
- A checkup whose files could not be saved in the database fails, so the app sends it again.

## 0.0.4

- Fix: pairing was refused as "not from the local network" when the iPhone reached the
  computer through its link-local IPv6 address (what a `.local` name often resolves to,
  like `fe80::…%en0`). IPv6 with a zone and IPv4-mapped IPv6 addresses now count as local.
  The dashboard's "local network only" setting uses the same check.

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
