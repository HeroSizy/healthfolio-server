package server

// MCP prompts and resources: ready-made questions an assistant can offer the user, and
// read-only documents (the database schema, the data types, the latest checkup, the last
// 7 days, the HealthSync skill).

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const mcpOffMessage = "AI assistant access (MCP) is turned off in Healthfolio. Turn it on in the app or the dashboard."

//go:embed skill/SKILL.md
var skillMD string

// SkillMD is the HealthSync skill for AI assistants (Claude Code and others).
func SkillMD() string { return skillMD }

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

type prompt struct {
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Arguments   []promptArg `json:"arguments,omitempty"`
	text        func(a map[string]string) string
}

const promptRules = "\n\nUse the Healthfolio tools to get the data; do not guess numbers. Give dates and units. " +
	"Say when data is missing or sparse. This is not medical advice: suggest a doctor for anything that needs one. " +
	"Answer in the language the user writes in."

var mcpPrompts = []prompt{
	{Name: "weekly_review", Title: "Weekly health review",
		Description: "Review the last 7 days: activity, heart, sleep and workouts, compared with the 4 weeks before.",
		Arguments:   []promptArg{{Name: "week_ending", Description: "Last day of the week, YYYY-MM-DD (default: today)"}},
		text: func(a map[string]string) string {
			end := firstNonEmpty(a["week_ending"], time.Now().Format("2006-01-02"))
			return "Give me a review of my health data for the 7 days ending " + end + ". " +
				"Call list_data_types first. Then use daily_summary for steps, active energy, resting heart rate, HRV and sleep, " +
				"and query_sql on v_workouts for workouts. Compare each with the average of the 4 weeks before. " +
				"Start with the 3 most important changes, then a short table per area."
		}},
	{Name: "checkup_review", Title: "Explain a checkup report",
		Description: "Explain the results of a checkup report and how they changed since earlier reports.",
		Arguments:   []promptArg{{Name: "date", Description: "Report date, YYYY-MM-DD (default: the latest report)"}},
		text: func(a map[string]string) string {
			which := "my latest checkup report"
			if d := a["date"]; d != "" {
				which = "my checkup report from " + d
			}
			return "Explain " + which + ". Use get_checkups. List the values flagged high or low first, with the reference range. " +
				"For each flagged value, show how it changed across all my earlier reports. Then summarize the normal values in one short paragraph. " +
				"Where it helps, relate them to my Apple Health data (for example weight, resting heart rate, activity)."
		}},
	{Name: "sleep_analysis", Title: "Sleep analysis",
		Description: "Analyze sleep duration, timing and stages over recent nights.",
		Arguments:   []promptArg{{Name: "days", Description: "Number of nights (default 14)"}},
		text: func(a map[string]string) string {
			days := firstNonEmpty(a["days"], "14")
			return "Analyze my sleep over the last " + days + " nights. Use query_sql on v_sleep (stage, start_at, end_at): " +
				"per night, give bedtime, wake time, time asleep and time per stage. Show the averages, the most irregular nights, " +
				"and any link with resting heart rate or HRV the next morning."
		}},
	{Name: "compare_periods", Title: "Compare two periods",
		Description: "Compare health metrics between two periods, for example last month and this month.",
		Arguments: []promptArg{
			{Name: "period_a", Description: "First period, e.g. 2026-09-01..2026-09-30 or \"last month\"", Required: true},
			{Name: "period_b", Description: "Second period, e.g. 2026-10-01..2026-10-31 or \"this month\"", Required: true},
			{Name: "metrics", Description: "Metrics to compare, e.g. steps, resting heart rate, weight (default: the main ones)"},
		},
		text: func(a map[string]string) string {
			metrics := firstNonEmpty(a["metrics"], "steps, active energy, resting heart rate, HRV, sleep, weight and workouts")
			return "Compare my " + metrics + " between " + a["period_a"] + " and " + a["period_b"] + ". " +
				"Use daily_summary or query_sql. Show a table with the average per day for each period, the change, and the change in percent. " +
				"Then say which changes are large enough to matter."
		}},
}

func getPrompt(name string, args map[string]string) (map[string]any, error) {
	for _, p := range mcpPrompts {
		if p.Name != name {
			continue
		}
		for _, a := range p.Arguments {
			if a.Required && strings.TrimSpace(args[a.Name]) == "" {
				return nil, fmt.Errorf("argument %q is required", a.Name)
			}
		}
		return map[string]any{
			"description": p.Description,
			"messages":    []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": p.text(args) + promptRules}}},
		}, nil
	}
	return nil, fmt.Errorf("unknown prompt %q", name)
}

type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

var mcpResources = []resource{
	{URI: "healthsync://schema", Name: "schema", Title: "Database schema", MimeType: "text/markdown",
		Description: "Tables, views and columns of health.db, for query_sql."},
	{URI: "healthsync://data-types", Name: "data-types", Title: "Data types", MimeType: "application/json",
		Description: "Every data type with its number of samples, unit and first and last day."},
	{URI: "healthsync://checkups/latest", Name: "latest-checkup", Title: "Latest checkup", MimeType: "application/json",
		Description: "All items of the most recent checkup report."},
	{URI: "healthsync://summary/7d", Name: "summary-7d", Title: "Last 7 days", MimeType: "application/json",
		Description: "Daily totals and averages of the main metrics for the last 7 days."},
	{URI: "healthsync://skill", Name: "skill", Title: "Healthfolio skill", MimeType: "text/markdown",
		Description: "How to answer questions with this server: the tools, the views and good queries."},
}

var mcpResourceTemplates = []map[string]any{
	{"uriTemplate": "healthsync://daily/{type}", "name": "daily", "title": "Daily values of one type", "mimeType": "application/json",
		"description": "The last 30 days of one data type (e.g. StepCount, RestingHeartRate)."},
}

const schemaDoc = "# health.db\n\n" +
	"## records\nOne row per Apple Health sample.\n\n" +
	"| Column | Meaning |\n|---|---|\n" +
	"| type | Data type, e.g. StepCount, HeartRate, SleepAnalysis, Workout |\n" +
	"| start, end | ISO 8601 with the phone's time zone |\n" +
	"| value, num | The value as text, and as a number when it is one |\n" +
	"| unit, source | Unit, and the app or device that wrote it |\n" +
	"| uuid, source_bundle, source_version, device_model, device_software, user_entered, tz | From the app's full records |\n" +
	"| metadata | JSON text; use json_extract |\n" +
	"| deleted_at | Set when the sample was deleted in Apple Health: add `WHERE deleted_at IS NULL` |\n" +
	"| received | When the server got it |\n\n" +
	"## Views (live samples only)\n" +
	"- `daily(type, day, unit, total, mean, min, max, n)`\n" +
	"- `v_heart_rate(bpm)`, `v_resting_heart_rate(bpm)`, `v_hrv(ms)`, `v_steps(steps)`, `v_weight(kg)`, `v_spo2(percent)`: also uuid, start_at, end_at, source, device_model\n" +
	"- `v_sleep(uuid, start_at, end_at, stage, source)`\n" +
	"- `v_workouts(uuid, start_at, end_at, activity, duration_s, distance_km, active_energy_kcal, avg_heart_rate, source)`\n\n" +
	"## checkup\nOne row per checkup item: report_id, date, institution, name, key, value, num, unit, range, flag (high/low/H/L/abnormal), " +
	"original_name (as printed, when it differs), and where it is printed: loc_file, loc_page (from 0), loc_x, loc_y, loc_w, loc_h (fractions of the page from its top left).\n\n" +
	"## reports\nOne row per checkup report: id, date, institution, title, items, folder, received, originals (JSON list of name, type, size, sha256). " +
	"Read an original with the get_report_file tool.\n\n" +
	"## Not in the database\nThe iPhone app's AI conversations (list_ai_chats, get_ai_chat) and its settings (get_app_settings).\n"

var summaryTypes = []string{"StepCount", "ActiveEnergyBurned", "RestingHeartRate", "HeartRateVariabilitySDNN", "BodyMass", "OxygenSaturation"}

func (m *MCP) readResource(uri string) (map[string]any, error) {
	text := func(mime, s string) map[string]any { return map[string]any{"uri": uri, "mimeType": mime, "text": s} }
	asJSON := func(v any, err error) (map[string]any, error) {
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(v)
		return text("application/json", string(b)), nil
	}
	switch {
	case uri == "healthsync://schema":
		return text("text/markdown", schemaDoc), nil
	case uri == "healthsync://skill":
		return text("text/markdown", skillMD), nil
	case uri == "healthsync://data-types":
		return asJSON(m.call("list_data_types", nil))
	case uri == "healthsync://checkups/latest":
		return asJSON(m.rows(`SELECT date, institution, name, key, value, unit, range, flag FROM checkup
			WHERE report_id = (SELECT report_id FROM checkup ORDER BY date DESC LIMIT 1) ORDER BY name`))
	case uri == "healthsync://summary/7d":
		out := map[string]any{}
		from := time.Now().AddDate(0, 0, -6).Format("2006-01-02")
		for _, t := range summaryTypes {
			full, err := m.resolveType(t)
			if err != nil {
				continue
			}
			rows, err := m.rows(`SELECT day, unit, ROUND(total, 1) AS total, ROUND(mean, 1) AS mean, n FROM daily WHERE type = ? AND day >= ? ORDER BY day`, full, from)
			if err == nil && len(rows) > 0 {
				out[t] = rows
			}
		}
		return asJSON(out, nil)
	case strings.HasPrefix(uri, "healthsync://daily/"):
		return asJSON(m.call("daily_summary", map[string]any{"type": strings.TrimPrefix(uri, "healthsync://daily/")}))
	}
	return nil, fmt.Errorf("unknown resource %q", uri)
}
