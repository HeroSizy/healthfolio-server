package server

// MCP tools for what is kept as files in the data folder rather than in the database:
// the original files of checkup reports, the iPhone app's AI conversations and its
// settings. All read-only.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxMCPFile is the largest original an assistant gets in one answer.
const maxMCPFile = 15 << 20

var mcpFileTools = []tool{
	{Name: "list_checkup_reports", Title: "Checkup reports",
		Description: "Lists the checkup reports: id, date, institution, title, number of items and of flagged items, and the original files (PDFs, photos) kept for each. Use get_checkups for the values and get_report_file to read an original.",
		InputSchema: obj(map[string]any{}), Annotations: readOnly},
	{Name: "get_report_file", Title: "Original report file",
		Description: "Returns one original file of a checkup report (a PDF or a photo) so you can read it yourself, for example to check a value or read notes the app did not extract. Each item from get_checkups has loc_file, loc_page (from 0) and a box (loc_x, loc_y, loc_w, loc_h as fractions of the page from its top left) telling where it is printed.",
		InputSchema: obj(map[string]any{
			"report_id": prop("string", "The report id, from list_checkup_reports"),
			"name":      prop("string", "The file name, from list_checkup_reports"),
		}, "report_id", "name"), Annotations: readOnly},
	{Name: "list_ai_chats", Title: "AI conversations",
		Description: "Lists the conversations the user had with the AI assistant in the HealthSync iPhone app (backed up to this computer): id, title, last update, size. Read one with get_ai_chat.",
		InputSchema: obj(map[string]any{}), Annotations: readOnly},
	{Name: "get_ai_chat", Title: "One AI conversation",
		Description: "Returns one conversation from the HealthSync iPhone app, as the app saved it.",
		InputSchema: obj(map[string]any{"id": prop("string", "The conversation id, from list_ai_chats")}, "id"), Annotations: readOnly},
	{Name: "get_app_settings", Title: "iPhone app settings",
		Description: "Returns the HealthSync iPhone app's settings as last backed up (never keys or passwords): which data it syncs, its destinations, its AI provider, and so on.",
		InputSchema: obj(map[string]any{}), Annotations: readOnly},
}

// mcpContent is a tool answer that is not JSON text, such as a file.
type mcpContent []any

func (m *MCP) callFiles(name string, a map[string]any) (any, bool, error) {
	switch name {
	case "list_checkup_reports":
		rows, err := m.rows(`SELECT r.id, r.date, r.institution, r.title, r.items, r.folder, r.originals,
				(SELECT COUNT(*) FROM checkup c WHERE c.report_id = r.id AND c.flag IN ('high','low','H','L','abnormal')) AS flagged
			FROM reports r ORDER BY r.date DESC`)
		if err != nil {
			return nil, true, err
		}
		for _, r := range rows {
			folder, _ := r["folder"].(string)
			files := []map[string]any{}
			for _, n := range originalFiles(folder) {
				if fi, err := os.Stat(filepath.Join(folder, n)); err == nil {
					files = append(files, map[string]any{"name": n, "type": contentType(n), "size": fi.Size()})
				}
			}
			r["files"] = files
			if s, ok := r["originals"].(string); ok && s != "" {
				var v any
				_ = json.Unmarshal([]byte(s), &v)
				r["originals"] = v
			}
			delete(r, "folder")
		}
		return rows, true, nil
	case "get_report_file":
		var folder string
		err := m.db.QueryRow(`SELECT COALESCE(folder,'') FROM reports WHERE id = ?`, argStr(a, "report_id")).Scan(&folder)
		n := argStr(a, "name")
		if err != nil || folder == "" {
			return nil, true, fmt.Errorf("no report %q; call list_checkup_reports", argStr(a, "report_id"))
		}
		if n != filepath.Base(n) || n == "report.json" || strings.HasPrefix(n, ".") {
			return nil, true, fmt.Errorf("no file %q in this report", n)
		}
		p := filepath.Join(folder, n)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			return nil, true, fmt.Errorf("no file %q in this report; call list_checkup_reports", n)
		}
		if fi.Size() > maxMCPFile {
			return nil, true, fmt.Errorf("%s is %d MB, more than %d MB", n, fi.Size()>>20, maxMCPFile>>20)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, true, err
		}
		mime := contentType(n)
		data := base64.StdEncoding.EncodeToString(b)
		if strings.HasPrefix(mime, "image/") {
			return mcpContent{map[string]any{"type": "image", "data": data, "mimeType": mime}}, true, nil
		}
		return mcpContent{map[string]any{"type": "resource", "resource": map[string]any{
			"uri": "healthsync://reports/" + argStr(a, "report_id") + "/files/" + n, "mimeType": mime, "blob": data}}}, true, nil
	case "list_ai_chats":
		dir := filepath.Join(m.dir, "chats")
		entries, _ := os.ReadDir(dir)
		out := []map[string]any{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			var head struct {
				ID       any               `json:"id"`
				Title    string            `json:"title"`
				Messages []json.RawMessage `json:"messages"`
			}
			_ = json.Unmarshal(b, &head)
			row := map[string]any{"id": fmt.Sprint(head.ID), "title": head.Title, "bytes": len(b)}
			if head.Messages != nil {
				row["messages"] = len(head.Messages)
			}
			if fi, err := e.Info(); err == nil {
				row["updated"] = fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}
			out = append(out, row)
		}
		sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["updated"]) > fmt.Sprint(out[j]["updated"]) })
		return out, true, nil
	case "get_ai_chat":
		b, err := os.ReadFile(filepath.Join(m.dir, "chats", chatFile(argStr(a, "id"))))
		if err != nil {
			return nil, true, fmt.Errorf("no conversation %q; call list_ai_chats", argStr(a, "id"))
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return string(b), true, nil
		}
		return v, true, nil
	case "get_app_settings":
		dir := filepath.Join(m.dir, "settings")
		entries, _ := os.ReadDir(dir)
		var newest os.FileInfo
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".json") &&
				!strings.HasPrefix(e.Name(), ".") && (newest == nil || fi.ModTime().After(newest.ModTime())) {
				newest = fi
			}
		}
		if newest == nil {
			return nil, true, fmt.Errorf("the iPhone app has not backed up its settings to this computer")
		}
		b, err := os.ReadFile(filepath.Join(dir, newest.Name()))
		if err != nil {
			return nil, true, err
		}
		var v any
		_ = json.Unmarshal(b, &v)
		return v, true, nil
	}
	return nil, false, nil
}
