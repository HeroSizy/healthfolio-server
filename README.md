# healthsync-server

A small self-hosted server that receives Apple Health data from the **HealthSync** iPhone app
and keeps it in a local SQLite database you own. One static binary (or a Docker image), no
cloud, no account.

- **Dashboard** (native in the app, web in the headless build): records per day, every data type with daily charts, a full sync history
  (who sent what, how many new records, failures and rejected tokens), checkup reports.
- **Pair by QR code**: click *Pair a new iPhone*, scan with the iPhone camera, done. Each
  paired device gets its own token, which you can revoke from the dashboard.
- **Zero-config discovery**: advertises `_healthsync._tcp` over Bonjour/mDNS, so the app
  finds the server on your Wi-Fi by itself.
- **Your data, plain SQLite**: `records` table + a `daily` view, readable with `sqlite3`,
  Datasette, Grafana, pandas, … Optionally also one CSV per day, and a hook to run your own
  script after each sync (commit to git, push to InfluxDB, …).
- **MCP for AI assistants**: ask Claude (or any MCP client) "how did my resting heart rate
  change this month?" — read-only tools over HTTP (`/mcp`) or stdio (`healthsync-server mcp`).
- Runs on macOS, Linux (x86/ARM, Raspberry Pi, NAS), Windows, FreeBSD.

中文说明见[下方](#中文)。

## Two ways to run it

- **HealthSync, the desktop app** (macOS, Windows, Linux): the server with a menu bar icon
  and a native window, drawn on the GPU by [MyGo](https://github.com/egoist/mygo) (no
  webview, no Electron). Pair by QR, watch syncs arrive, connect AI assistants, and choose
  where data is stored (any folder, daily CSV files, a command to run after syncs) in
  Settings, saved to `settings.json` in the user config folder.
- **healthsync-server, headless** (servers, NAS, Raspberry Pi, Docker): the same server with
  the web dashboard. The desktop binary runs this way too with `--headless`.

![Overview](docs/overview.png)

| Pairing | Settings |
|---|---|
| ![Pairing, dark](docs/pair-dark.png) | ![Settings](docs/settings.png) |

### Desktop app

Download it from [Releases](../../releases) (`.dmg`, `-setup.exe`, `.deb`/`.tar.gz`). The
macOS build isn't notarized yet: open it the first time with right-click › Open. From source:

```sh
make run          # build to dist/dev/HealthSync and run it
make preview      # the window with sample data, in a browser (WebAssembly)
make app          # packaged apps in build/
```

Closing the window keeps the server running in the menu bar; quit from the menu bar icon.

## Headless server

### Binary

Download the archive for your system from [Releases](../../releases), then:

```sh
./healthsync-server            # starts on port 8787
```

Open `http://<this-computer>:8787/` in a browser, go to **Pair & devices**, click
**Pair a new iPhone** and scan the QR code. Or run `./healthsync-server pair` to get the
code and QR in the terminal.

To keep it running: `contrib/healthsync.service` (systemd) or
`contrib/com.healthsync.server.plist` (macOS launchd).

### Docker

```sh
docker run -d --name healthsync --restart unless-stopped \
  --network host -v "$PWD/data:/data" ghcr.io/herosizy/healthsync-server
```

or `docker compose up -d` with the included [docker-compose.yml](docker-compose.yml).

`--network host` lets the app discover the server and puts the real LAN address in the QR
code. On Docker Desktop (Mac/Windows), host networking isn't available: publish the port
(`-p 8787:8787`) and set `HEALTHSYNC_PUBLIC_URL=http://<your-LAN-IP>:8787`; pair by QR.

### From source

```sh
go install github.com/herosizy/healthsync-server@latest
```

Pure Go (no cgo), so cross-compiling is just `GOOS=linux GOARCH=arm64 go build`.

## Configuration

Every flag has an environment variable.

| Flag | Env | Default | |
|---|---|---|---|
| `-port` | `HEALTHSYNC_PORT` | `8787` | |
| `-bind` | `HEALTHSYNC_BIND` | `0.0.0.0` | |
| `-data` | `HEALTHSYNC_DATA` | see below | `health.db`, `reports/`, `token` |
| `-name` | `HEALTHSYNC_NAME` | computer name | shown in the app |
| `-public-url` | `HEALTHSYNC_PUBLIC_URL` | detected LAN IP | address put into the QR code |
| `-admin-password` | `HEALTHSYNC_ADMIN_PASSWORD` | none | dashboard login (any user name) |
| `-dashboard-lan-only` | `HEALTHSYNC_DASHBOARD_ANYWHERE=1` to disable | on | dashboard only answers LAN/loopback clients |
| `-csv` | `HEALTHSYNC_CSV` | off | also write `<dir>/YYYY-MM-DD.csv` |
| `-hook` | `HEALTHSYNC_HOOK` | off | command to run after data arrives |
| `-no-bonjour` | `HEALTHSYNC_NO_BONJOUR=1` | | |
| `-no-dialog` | `HEALTHSYNC_NO_DIALOG=1` | | macOS: no "Allow?" dialog, codes only |
| | `HEALTHSYNC_DASHBOARD_APPROVAL=1` | | Linux/Windows: let devices request pairing without a code and approve them in the dashboard |
| | `HEALTHSYNC_TOKEN` | generated | fixed shared token |

Default data directory: `~/Library/Application Support/HealthSync` (macOS),
`~/.local/share/health-sync` (Linux), `%APPDATA%\HealthSync` (Windows), `/data` (Docker).

### Security

The server is meant for your home network. Pairing (`POST /pair`) is only accepted from
private/loopback addresses, and so is the dashboard unless you turn that off. Pairing codes
are 6 digits, single use, valid 10 minutes, and burn after 5 wrong guesses. Device tokens
are stored hashed. If you expose the server outside your LAN (e.g. through Tailscale or a
reverse proxy), set `HEALTHSYNC_ADMIN_PASSWORD` and put TLS in front of it.

### Hook

With `-hook 'my-script.sh'`, received records are also written to `<data>/inbox/app-*.txt`
(pipe format, see below) and the command runs once a sync has been quiet for 20 s. It gets:

| Variable | |
|---|---|
| `HEALTHSYNC_EVENT` | `ingest` or `report` |
| `HEALTHSYNC_INBOX` | the inbox directory (your script should move or delete processed files) |
| `HEALTHSYNC_REPORT_DIR` | folder of the checkup report just received |
| `HEALTHSYNC_DATA`, `HEALTHSYNC_DB` | data directory and database path |

Its output and exit status show up in the dashboard's sync history.

## AI assistants (MCP)

The server speaks the [Model Context Protocol](https://modelcontextprotocol.io), read-only.
Tools: `list_data_types`, `daily_summary`, `get_records`, `get_checkups`, `query_sql`
(single SELECT on a read-only connection).

**Over the network** (Streamable HTTP): in the dashboard, *Pair & devices → AI assistants →
Create token*, which shows a ready-to-paste command, e.g. for Claude Code:

```sh
claude mcp add --transport http healthsync http://192.168.1.10:8787/mcp \
  --header "Authorization: Bearer <token>"
```

**On the same computer** (stdio, no token needed, reads `health.db` directly):

```json
{ "mcpServers": { "healthsync": { "command": "/usr/local/bin/healthsync-server", "args": ["mcp"] } } }
```

Keep in mind that whatever the assistant reads is sent to its model provider.

## Database

```sql
-- every sample, de-duplicated on (type, start, end, source, value)
SELECT * FROM records WHERE type = 'HKQuantityTypeIdentifierStepCount' ORDER BY start DESC LIMIT 10;
-- per-day aggregates
SELECT day, total FROM daily WHERE type = 'HKQuantityTypeIdentifierStepCount' ORDER BY day DESC;
-- checkup values imported in the app
SELECT date, name, value, unit, flag FROM checkup ORDER BY date DESC;
```

Also `sync_log` (history shown in the dashboard), `devices` and `reports`.

## API

The app talks to these endpoints; other clients can too.

| | |
|---|---|
| `GET /health` | `{"ok":true,"name":…,"version":2,"records":2,"os":…,"pair_modes":[…],"mcp":"/mcp"}`. `"records": 2` means `/ingest` keeps the full JSON Lines records and takes deletions (below). |
| `POST /pair` | `{"device":"…","code":"123456"}` → `{"token":…}`. Without a code: `428` + `needs_code`, or wait for approval (macOS dialog / dashboard). |
| `POST /ingest` | `Authorization: Bearer <token>`. Body: one record per line `type\|start\|end\|value\|unit\|source`, or a JSON array / `{"records":[…],"deleted":["uuid",…]}`, JSON Lines, or CSV/TSV with a header. → `{"ok":true,"received":n,"added":{…},"deleted":n}` || `POST /report` | `{"report":{"id","date","institution","items":[{name,key,value,unit,range,flag}]},"folder":…,"files":[{"name","data"(base64)}]}` |
| `GET /stats` | counts per type |
| `POST /mcp` | MCP (Streamable HTTP, JSON responses), Bearer token |
| `POST /mcp/tokens` | Bearer device token, `{"name":"Claude"}` → a new revocable token for an AI agent plus ready-to-copy configs: `{"url","token","transport","configs":{"claude_code","json","stdio_bridge","agent_prompt"},"config_list":[{"id","title","text"}]}` |

QR codes encode `healthsync://pair?url=<server>&code=<code>&name=<name>`.

**Full records.** JSON rows may carry `uuid`, `source_bundle`, `source_version`, `device_model`, `device_software`, `user_entered`, `tz` and `metadata` (an object of strings), as the app's JSON Lines export does; they are stored in the same-named columns of `records`. A sample is stored once per `uuid`, and a row that arrived earlier in the pipe format, without one, gets the uuid and the other fields when it comes again. A line `{"uuid":"…","deleted":true}` (optionally `"deleted_at":"<ISO 8601>"`) marks that sample deleted: the row stays with `deleted_at` set, and statistics, the `daily` view, the `v_*` views (`v_heart_rate`, `v_steps`, `v_sleep`, `v_workouts`, …, as in the app's D1/Supabase schema) and MCP leave it out. Daily CSV files and the hook inbox keep the six pipe columns and get no deletions.

---

## 中文

**healthsync-server** 是「健康同步」iPhone App 的本地服务器：一个二进制文件或一个 Docker
镜像，接收 App 发来的 Apple 健康数据，存到你自己的 SQLite 数据库里，不经过任何云。

- **控制面板**：浏览器打开 `http://<这台电脑>:8787/`，能看到每天新增多少条、每种数据的日图表、
  完整的同步记录（哪台设备、收到多少、新增多少、失败和令牌错误）、体检报告。
- **扫码配对**：点「配对新的 iPhone」，用 iPhone 相机扫二维码就配好了。每台设备有自己的令牌，
  可以在面板里单独撤销。
- **自动发现**：局域网 Bonjour 广播，App 能自己找到服务器。
- **MCP**：让 Claude 等 AI 助手只读查询你的健康数据（`/mcp` 或 `healthsync-server mcp`）。
- **桌面 App**：MyGo 原生界面 + 菜单栏图标，在设置里自己选数据存到哪个文件夹、要不要每天导出 CSV、同步后运行什么命令。
- 支持 macOS、Linux（含树莓派、NAS）、Windows；Docker 推荐 `--network host`。

```sh
./healthsync-server              # 启动，默认端口 8787
./healthsync-server pair         # 在终端里显示配对码和二维码
docker compose up -d             # 或者用 Docker
```

和原来的 `receiver.py` 完全兼容：同样的接口、同样的数据库表和默认数据目录、同样的令牌文件
（`~/.config/health-sync/token`），已经配好的 App 不用重新配对。

## License

MIT
