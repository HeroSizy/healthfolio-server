// Package model holds the plain data types shared by the server, the desktop app and its
// browser preview. It has no dependencies, so it builds for every platform, WebAssembly too.
package model

type TypeStat struct {
	Type   string `json:"type"`
	Count  int    `json:"count"`
	Latest string `json:"latest"`
}

type Stats struct {
	Total        int        `json:"total"`
	LastReceived *string    `json:"last_received"`
	Types        []TypeStat `json:"types"`
}

type LogEntry struct {
	ID         int64    `json:"id"`
	Time       string   `json:"time"`
	Kind       string   `json:"kind"` // ingest | report | pair | auth
	Device     string   `json:"device"`
	IP         string   `json:"ip"`
	Received   int      `json:"received"`
	Added      int      `json:"added"`
	Bytes      int      `json:"bytes"`
	DurationMS int      `json:"duration_ms"`
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	Types      []string `json:"types"`
}

type DayCount struct {
	Day   string `json:"day"`
	Added int    `json:"added"`
	Syncs int    `json:"syncs"`
	Fails int    `json:"fails"`
}

type Device struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	LastSeen string `json:"last_seen"`
	LastIP   string `json:"last_ip"`
	Revoked  bool   `json:"revoked"`
}

type ReportRow struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	Institution string `json:"institution"`
	Items       int    `json:"items"`
	Flagged     int    `json:"flagged"`
	Received    string `json:"received"`
}

type DailyPoint struct {
	Day   string  `json:"day"`
	Total float64 `json:"total"`
	Mean  float64 `json:"mean"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	N     int     `json:"n"`
	Unit  string  `json:"unit"`
}

type MCPConnect struct {
	Name       string            `json:"name"`
	Token      string            `json:"token"`
	URL        string            `json:"url"`
	Transport  string            `json:"transport"`
	Configs    map[string]string `json:"configs"`
	ConfigList []MCPConfig       `json:"config_list"`
}

type MCPConfig struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// Output is one place received data is written to.
type Output struct {
	Kind  string `json:"kind"` // sqlite | csv | hook
	Where string `json:"where"`
}

// Overview is the dashboard's summary.
type Overview struct {
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Protocol     int        `json:"protocol"`
	OS           string     `json:"os"`
	Started      string     `json:"started"`
	BaseURL      string     `json:"base_url"`
	TailscaleURL string     `json:"tailscale_url,omitempty"`
	MCPURL       string     `json:"mcp_url"`
	DataDir      string     `json:"data_dir"`
	Outputs      []Output   `json:"outputs"`
	Stats        Stats      `json:"stats"`
	Activity     []DayCount `json:"activity"`
	Devices      int        `json:"devices"`
	Reports      int        `json:"reports"`
	LastSync     *LogEntry  `json:"last_sync"`
	PairModes    []string   `json:"pair_modes"`
	Password     bool       `json:"password"`
	MCPEnabled   bool       `json:"mcp_enabled"`
}

// PairInfo is an active one-time pairing code and the link its QR code encodes.
type PairInfo struct {
	Code    string `json:"code"`
	Expires int64  `json:"expires"` // unix seconds
	Link    string `json:"link"`
	URL     string `json:"url"`
	AltURL  string `json:"alt_url,omitempty"` // the Tailscale address, when there is one
}

// PendingPair is a device waiting for someone to allow it to pair without a code.
type PendingPair struct {
	ID      string `json:"id"`
	Device  string `json:"device"`
	IP      string `json:"ip"`
	Created string `json:"created"`
}

// Settings is what the desktop app lets people change; it's saved as settings.json.
// Empty values mean the default.
type Settings struct {
	Port          int    `json:"port,omitempty"`
	DataDir       string `json:"data_dir,omitempty"`
	Name          string `json:"name,omitempty"`
	PublicURL     string `json:"public_url,omitempty"`
	CSVEnabled    bool   `json:"csv_enabled,omitempty"`
	CSVDir        string `json:"csv_dir,omitempty"`
	HookEnabled   bool   `json:"hook_enabled,omitempty"`
	Hook          string `json:"hook,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`
	NoBonjour     bool   `json:"no_bonjour,omitempty"`
	Notify        bool   `json:"notify,omitempty"` // desktop notification after each sync
	// NoCrashReports stops sending crash reports (errors and stack traces, never health data).
	NoCrashReports bool `json:"no_crash_reports,omitempty"`
}

// Updates is the state of the desktop app's self-updates, for the settings page.
type Updates struct {
	Version   string // the running version
	Supported bool   // this build can update itself (not a development build or a package)
	Automatic bool   // checks in the background and installs
	Available string // a newer version found, or ""
}
