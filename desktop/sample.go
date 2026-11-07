package desktop

import (
	"fmt"
	"math"
	"time"

	"github.com/herosizy/healthsync-server/model"
)

// Sample is a Backend and Host with made-up data, for the browser preview and screenshots.
type Sample struct {
	settings model.Settings
	pair     *model.PairInfo
	update   func(func())
	devices  []model.Device

	manualUpdates bool
	mcpOff        bool
}

func NewSample(update func(func())) *Sample {
	now := time.Now()
	return &Sample{update: update, devices: []model.Device{
		{ID: 3, Name: "AI assistant", Created: now.Add(-2 * time.Hour).Format(time.RFC3339), LastSeen: now.Add(-20 * time.Minute).Format(time.RFC3339), LastIP: "192.168.1.20"},
		{ID: 2, Name: "iPhone", Created: now.AddDate(0, 0, -12).Format(time.RFC3339), LastSeen: now.Add(-3 * time.Minute).Format(time.RFC3339), LastIP: "192.168.1.42"},
		{ID: 1, Name: "Old iPhone", Created: now.AddDate(0, -2, 0).Format(time.RFC3339), LastSeen: now.AddDate(0, 0, -40).Format(time.RFC3339), LastIP: "192.168.1.37", Revoked: true},
	}}
}

func wave(i int, base, amp float64) float64 {
	return base + amp*math.Sin(float64(i)*0.7) + amp*0.4*math.Cos(float64(i)*1.9)
}

func (s *Sample) Overview() model.Overview {
	now := time.Now()
	act := make([]model.DayCount, 30)
	for i := range act {
		act[i] = model.DayCount{Day: now.AddDate(0, 0, i-29).Format("2006-01-02"), Added: int(wave(i, 900, 380)), Syncs: 6}
	}
	act[11].Added, act[11].Fails = 0, 2
	last := model.LogEntry{Time: now.Add(-3 * time.Minute).Format(time.RFC3339), Kind: "ingest", Device: "iPhone", OK: true, Received: 412, Added: 388}
	return model.Overview{
		Name: "Mac mini", Version: "0.2.0", Protocol: 2, OS: "mac", BaseURL: "http://192.168.1.10:8787", TailscaleURL: "http://100.101.102.103:8787", MCPURL: "http://192.168.1.10:8787/mcp",
		DataDir: "~/Library/Application Support/HealthSync", Outputs: []model.Output{{Kind: "sqlite", Where: "health.db"}},
		Stats: model.Stats{Total: 184_327, Types: []model.TypeStat{
			{Type: "HKQuantityTypeIdentifierHeartRate", Count: 96_210}, {Type: "HKQuantityTypeIdentifierActiveEnergyBurned", Count: 41_088},
			{Type: "HKQuantityTypeIdentifierStepCount", Count: 22_431}, {Type: "HKQuantityTypeIdentifierDistanceWalkingRunning", Count: 18_920},
			{Type: "HKCategoryTypeIdentifierSleepAnalysis", Count: 3_102}, {Type: "HKQuantityTypeIdentifierRestingHeartRate", Count: 812},
			{Type: "HKQuantityTypeIdentifierOxygenSaturation", Count: 640}, {Type: "HKQuantityTypeIdentifierBodyMass", Count: 88},
		}},
		Activity: act, Devices: 2, Reports: 3, LastSync: &last, PairModes: []string{"dialog", "code"}, MCPEnabled: !s.mcpOff,
	}
}

func (s *Sample) Logs(limit int, before int64, kind string) ([]model.LogEntry, error) {
	now := time.Now()
	var out []model.LogEntry
	for i := 0; i < 40; i++ {
		e := model.LogEntry{ID: int64(100 - i), Time: now.Add(-time.Duration(i*47+3) * time.Minute).Format(time.RFC3339), Kind: "ingest", Device: "iPhone",
			IP: "192.168.1.42", Received: 300 + i*7, Added: 280 + i*5, Bytes: 24_000 + i*900, DurationMS: 4 + i%5, OK: true,
			Types: []string{"HKQuantityTypeIdentifierHeartRate", "HKQuantityTypeIdentifierStepCount", "HKQuantityTypeIdentifierActiveEnergyBurned"}}
		switch i {
		case 4:
			e = model.LogEntry{ID: e.ID, Time: e.Time, Kind: "report", Device: "iPhone", IP: e.IP, Received: 46, Added: 46, OK: true, Message: "2026-10 体检"}
		case 7:
			e = model.LogEntry{ID: e.ID, Time: e.Time, Kind: "auth", IP: "192.168.1.77", Message: "bad token on /ingest"}
		case 9:
			e = model.LogEntry{ID: e.ID, Time: e.Time, Kind: "pair", Device: "AI assistant", OK: true, Message: "MCP token created in the app"}
		}
		if kind == "" || kind == e.Kind {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Sample) Devices() ([]model.Device, error) { return s.devices, nil }
func (s *Sample) RevokeDevice(id int64) error {
	for i := range s.devices {
		if s.devices[i].ID == id {
			s.devices[i].Revoked = true
		}
	}
	return nil
}
func (s *Sample) DeleteDevice(id int64) error { return nil }
func (s *Sample) SetMCPEnabled(on bool) error { s.mcpOff = !on; return nil }
func (s *Sample) SkillMD() string             { return "---\nname: healthsync\n---\n" }
func (s *Sample) RenameDevice(id int64, name string) error {
	for i := range s.devices {
		if s.devices[i].ID == id {
			s.devices[i].Name = name
		}
	}
	return nil
}
func (s *Sample) Reports() ([]model.ReportRow, error) {
	return []model.ReportRow{
		{ID: "a", Date: "2026-10-18", Institution: "Annual checkup", Items: 46, Flagged: 3, Received: time.Now().Add(-3 * time.Hour).Format(time.RFC3339)},
		{ID: "b", Date: "2025-10-11", Institution: "Annual checkup", Items: 44, Flagged: 2, Received: time.Now().AddDate(0, 0, -20).Format(time.RFC3339)},
		{ID: "c", Date: "2024-09-27", Institution: "Annual checkup", Items: 41, Flagged: 1, Received: time.Now().AddDate(0, 0, -20).Format(time.RFC3339)},
	}, nil
}
func (s *Sample) Daily(typ string, days int) ([]model.DailyPoint, error) {
	out := make([]model.DailyPoint, days)
	for i := range out {
		v := wave(i, 72, 9)
		out[i] = model.DailyPoint{Day: time.Now().AddDate(0, 0, i-days+1).Format("2006-01-02"), Total: v * 120, Mean: v, Min: v - 15, Max: v + 40, N: 120, Unit: "count/min"}
	}
	return out, nil
}
func (s *Sample) NewPairCode() (model.PairInfo, error) {
	p := model.PairInfo{Code: "327152", Expires: time.Now().Add(9*time.Minute + 41*time.Second).Unix(), URL: "http://192.168.1.10:8787"}
	p.Link = "healthsync://pair?code=327152&name=Mac+mini&url=http%3A%2F%2F192.168.1.10%3A8787"
	s.pair = &p
	return p, nil
}
func (s *Sample) CurrentPair() (model.PairInfo, bool) {
	if s.pair == nil {
		return model.PairInfo{}, false
	}
	return *s.pair, true
}
func (s *Sample) CancelPair()                  { s.pair = nil }
func (s *Sample) Pending() []model.PendingPair { return nil }
func (s *Sample) AnswerPending(string, bool) bool {
	return true
}
func (s *Sample) NewMCPToken(name string) (model.MCPConnect, error) {
	tok := "hS3x9Qm2LkP0vT7wYc4RzA8n"
	cfgs := []model.MCPConfig{
		{ID: "claude_code", Title: "Claude Code", Text: fmt.Sprintf(`claude mcp add --transport http healthsync http://192.168.1.10:8787/mcp --header "Authorization: Bearer %s"`, tok)},
		{ID: "json", Title: "JSON", Text: `{
  "mcpServers": {
    "healthsync": {
      "type": "http",
      "url": "http://192.168.1.10:8787/mcp",
      "headers": { "Authorization": "Bearer ` + tok + `" }
    }
  }
}`},
	}
	return model.MCPConnect{Name: "Mac mini", Token: tok, URL: "http://192.168.1.10:8787/mcp", ConfigList: cfgs}, nil
}
func (s *Sample) DashboardURL() string { return "http://127.0.0.1:8787/" }

// Host
func (s *Sample) Settings() model.Settings            { return s.settings }
func (s *Sample) SaveSettings(v model.Settings) error { s.settings = v; return nil }
func (s *Sample) Backend() (Backend, string)          { return s, "" }
func (s *Sample) DefaultDataDir() string              { return "~/Library/Application Support/HealthSync" }
func (s *Sample) StdioCommand() string {
	return "/Applications/HealthSync.app/Contents/MacOS/HealthSync"
}
func (s *Sample) Copy(string)                                 {}
func (s *Sample) OpenURL(string)                              {}
func (s *Sample) ChooseFolder(_, _ string, done func(string)) { done("/Volumes/T7/HealthSync") }
func (s *Sample) Update(fn func())                            { s.update(fn) }
func (s *Sample) Updates() model.Updates {
	return model.Updates{Version: "0.0.1", Supported: true, Automatic: !s.manualUpdates}
}
func (s *Sample) CheckForUpdates()            {}
func (s *Sample) SetAutomaticUpdates(on bool) { s.manualUpdates = !on }
