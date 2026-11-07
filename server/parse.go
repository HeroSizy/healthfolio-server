package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Record is one health sample, in the same shape the iPhone app sends:
// type|start|end|value|unit|source
type Record struct {
	Type, Start, End, Value, Unit, Source string
}

var recordFields = []string{"type", "start", "end", "value", "unit", "source"}

func (r Record) Pipe() string {
	return strings.Join([]string{r.Type, r.Start, r.End, r.Value, r.Unit, r.Source}, "|")
}

// ParseBody accepts the formats the receiver has always accepted:
//   - one record per line, pipe separated: type|start|end|value|unit|source
//     (the source may itself contain "|")
//   - a JSON array of objects, or {"records": [...]}
//   - JSON Lines
//   - CSV / TSV with a header row starting with "type"
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
	if strings.HasPrefix(first, "type,") || strings.HasPrefix(first, "type\t") {
		return parseCSV(text, strings.Contains(first, "\t"))
	}
	var out []Record
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		p := strings.Split(line, "|")
		if len(p) < 6 {
			continue
		}
		out = append(out, Record{p[0], p[1], p[2], p[3], p[4], strings.Join(p[5:], "|")})
	}
	return out, nil
}

func parseJSON(text string) ([]Record, error) {
	var items []map[string]any
	var whole any
	if err := json.Unmarshal([]byte(text), &whole); err == nil {
		switch v := whole.(type) {
		case []any:
			items = toMaps(v)
		case map[string]any:
			if recs, ok := v["records"].([]any); ok {
				items = toMaps(recs)
			} else if _, ok := v["type"]; ok {
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
	out := make([]Record, 0, len(items))
	for _, m := range items {
		out = append(out, Record{str(m["type"]), str(m["start"]), str(m["end"]), str(m["value"]), str(m["unit"]), str(m["source"])})
	}
	return out, nil
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
	get := func(row []string, k string) string {
		if i, ok := idx[k]; ok && i < len(row) {
			return row[i]
		}
		return ""
	}
	out := make([]Record, 0, len(rows)-1)
	for _, row := range rows[1:] {
		out = append(out, Record{get(row, "type"), get(row, "start"), get(row, "end"), get(row, "value"), get(row, "unit"), get(row, "source")})
	}
	return out, nil
}

func asNumber(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f, err == nil
}
