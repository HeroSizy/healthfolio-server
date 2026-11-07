package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// recordsVersion is what /health advertises as "records": 2 means /ingest keeps every
// field of the app's JSON Lines rows (uuid, source and device, time zone, metadata) and
// takes deletions.
const recordsVersion = 2

// Record is one health sample. The first six fields are the pipe format the iPhone app has
// always sent (type|start|end|value|unit|source); the rest come with JSON Lines.
type Record struct {
	Type, Start, End, Value, Unit, Source string

	UUID           string // HealthKit sample UUID: deduplication and deletions key on it
	SourceBundle   string
	SourceVersion  string
	DeviceModel    string // productType, e.g. Watch7,5
	DeviceSoftware string
	UserEntered    bool
	TZ             string
	Metadata       string // JSON object text, keys sorted; "" when none

	// Deleted marks a deletion: only UUID (and DeletedAt, if given) matter.
	Deleted   bool
	DeletedAt string
}

var recordFields = []string{"type", "start", "end", "value", "unit", "source"}

func (r Record) Pipe() string {
	return strings.Join([]string{r.Type, r.Start, r.End, r.Value, r.Unit, r.Source}, "|")
}

// samples returns the rows that are not deletions.
func samples(rows []Record) []Record {
	out := make([]Record, 0, len(rows))
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, r)
		}
	}
	return out
}

// ParseBody accepts the formats the receiver has always accepted:
//   - one record per line, pipe separated: type|start|end|value|unit|source
//     (the source may itself contain "|")
//   - a JSON array of objects, or {"records": [...], "deleted": ["uuid", ...]}
//   - JSON Lines; a line {"uuid": "…", "deleted": true} deletes that sample
//   - CSV / TSV with a header row naming the columns (type, start, …; uuid optional)
func ParseBody(body []byte) ([]Record, error) {
	text := strings.TrimSpace(string(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	if text == "" {
		return nil, nil
	}
	switch text[0] {
	case '[', '{':
		return parseJSON(text)
	}
	first, _, _ := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if sep, ok := csvHeader(first); ok {
		return parseCSV(text, sep == '\t')
	}
	var out []Record
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		p := strings.Split(line, "|")
		if len(p) < 6 {
			continue
		}
		out = append(out, Record{Type: p[0], Start: p[1], End: p[2], Value: p[3], Unit: p[4], Source: strings.Join(p[5:], "|")})
	}
	return out, nil
}

// csvHeader reports whether line is a CSV/TSV header row: it names a type column and
// has no pipe (the pipe format's first field is a type such as StepCount, never "type").
func csvHeader(line string) (rune, bool) {
	if strings.Contains(line, "|") {
		return 0, false
	}
	for _, sep := range []rune{'\t', ','} {
		for _, f := range strings.Split(line, string(sep)) {
			if strings.TrimSpace(f) == "type" {
				return sep, true
			}
		}
	}
	return 0, false
}

func parseJSON(text string) ([]Record, error) {
	var items []map[string]any
	var deleted []any
	var whole any
	if err := json.Unmarshal([]byte(text), &whole); err == nil {
		switch v := whole.(type) {
		case []any:
			items = toMaps(v)
		case map[string]any:
			recs, hasRecs := v["records"].([]any)
			dels, hasDels := v["deleted"].([]any)
			switch {
			case hasRecs || hasDels:
				items, deleted = toMaps(recs), dels
			default:
				items = []map[string]any{v} // a single JSON Lines row
			}
		}
	} else {
		for i, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			items = append(items, m)
		}
	}
	out := make([]Record, 0, len(items)+len(deleted))
	for _, m := range items {
		if r, ok := recordFromMap(m); ok {
			out = append(out, r)
		}
	}
	for _, d := range deleted {
		if u := str(d); u != "" {
			out = append(out, Record{UUID: u, Deleted: true})
		}
	}
	return out, nil
}

func recordFromMap(m map[string]any) (Record, bool) {
	r := Record{
		Type: str(m["type"]), Start: str(m["start"]), End: str(m["end"]), Value: str(m["value"]),
		Unit: str(m["unit"]), Source: str(m["source"]), UUID: str(m["uuid"]),
		SourceBundle: str(m["source_bundle"]), SourceVersion: str(m["source_version"]),
		DeviceModel: str(m["device_model"]), DeviceSoftware: str(m["device_software"]),
		UserEntered: truthy(m["user_entered"]), TZ: str(m["tz"]),
	}
	switch d := m["deleted"].(type) {
	case bool:
		r.Deleted = d
	case string: // a time stamp
		r.Deleted, r.DeletedAt = d != "", d
	}
	if r.Deleted {
		if r.DeletedAt == "" {
			r.DeletedAt = str(m["deleted_at"])
		}
		return r, r.UUID != ""
	}
	if md, ok := m["metadata"].(map[string]any); ok && len(md) > 0 {
		r.Metadata = metadataJSON(md)
	}
	return r, r.Type != ""
}

// metadataJSON writes a metadata object as JSON text with sorted keys and string values,
// as the app stores it in SQLite destinations.
func metadataJSON(md map[string]any) string {
	flat := make(map[string]string, len(md))
	for k, v := range md {
		flat[k] = str(v)
	}
	b, _ := json.Marshal(flat) // encoding/json sorts map keys
	return string(b)
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || strings.EqualFold(x, "true") || strings.EqualFold(x, "yes")
	}
	return false
}

func toMaps(v []any) []map[string]any {
	out := make([]map[string]any, 0, len(v))
	for _, x := range v {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// parseCSV reads the app's CSV/TSV export (uuid, type, start, end, value, unit, source,
// device, user_entered, timezone) as well as the older six-column one.
func parseCSV(text string, tab bool) ([]Record, error) {
	r := csv.NewReader(strings.NewReader(text))
	if tab {
		r.Comma = '\t'
	}
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[strings.TrimSpace(h)] = i
	}
	get := func(row []string, keys ...string) string {
		for _, k := range keys {
			if i, ok := idx[k]; ok && i < len(row) {
				return row[i]
			}
		}
		return ""
	}
	out := make([]Record, 0, len(rows)-1)
	for _, row := range rows[1:] {
		out = append(out, Record{
			Type: get(row, "type"), Start: get(row, "start"), End: get(row, "end"), Value: get(row, "value"),
			Unit: get(row, "unit"), Source: get(row, "source"), UUID: get(row, "uuid"),
			DeviceModel: get(row, "device", "device_model"), UserEntered: truthy(get(row, "user_entered")),
			TZ: get(row, "timezone", "tz"),
		})
	}
	return out, nil
}

func asNumber(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f, err == nil
}
