package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Backups of the iPhone app's own data, so that a new phone (or the same one after
// reinstalling) can get it back. All of them need a paired phone's token.
//
//	POST /backup/chats              {"id": "...", ...} or {"chats": [{...}, ...]}
//	GET  /backup/chats              → {"chats": [{id, title, updated, bytes}]}
//	GET  /backup/chats/{id}         → the conversation as it was sent
//	POST /backup/settings           {...}: the app's settings, never any key or password
//	GET  /backup/settings           → the settings this phone sent last
//
// Conversations go to data/chats/<id>.json: sending one with the same id again replaces it.
// Settings go to data/settings/<device>.json, one file per paired phone.

const (
	maxChatsBody    = 32 << 20
	maxSettingsBody = 1 << 20
)

var chatIDChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// chatFile is the file name for a conversation id: the id itself when it is a plain one
// (a UUID), else a cleaned-up version with a short hash so different ids can't collide.
func chatFile(id string) string {
	clean := strings.Trim(chatIDChars.ReplaceAllString(id, "_"), "._")
	if clean == id && len(id) <= 100 {
		return id + ".json"
	}
	if len(clean) > 60 {
		clean = clean[:60]
	}
	h := sha256.Sum256([]byte(id))
	return clean + "-" + hex.EncodeToString(h[:4]) + ".json"
}

// writeFileAtomic writes through a temporary file, so a crash never leaves half a file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (s *Server) chatsDir() string    { return filepath.Join(s.cfg.DataDir, "chats") }
func (s *Server) settingsDir() string { return filepath.Join(s.cfg.DataDir, "settings") }

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if r.ContentLength > limit {
		errJSON(w, 413, "too large")
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		errJSON(w, 413, "too large or unreadable: "+err.Error())
		return nil, false
	}
	return b, true
}

// POST /backup/chats
func (s *Server) handleBackupChats(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	dev, ok := s.authPhone(w, r, "/backup/chats")
	if !ok {
		return
	}
	body, ok := readBody(w, r, maxChatsBody)
	if !ok {
		return
	}
	var chats []json.RawMessage
	var wrapped struct {
		Chats []json.RawMessage `json:"chats"`
	}
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &chats); err != nil {
			errJSON(w, 400, "bad JSON: "+err.Error())
			return
		}
	default:
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			errJSON(w, 400, "bad JSON: "+err.Error())
			return
		}
		chats = wrapped.Chats
		if wrapped.Chats == nil {
			chats = []json.RawMessage{trimmed} // a single conversation
		}
	}
	ids := make([]string, 0, len(chats))
	for i, raw := range chats {
		var head struct {
			ID any `json:"id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			errJSON(w, 400, fmt.Sprintf("chat %d: not a JSON object", i))
			return
		}
		id := strings.TrimSpace(fmt.Sprint(head.ID))
		if head.ID == nil || id == "" {
			errJSON(w, 400, fmt.Sprintf("chat %d has no id", i))
			return
		}
		ids = append(ids, id)
	}
	for i, raw := range chats {
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") != nil {
			pretty.Reset()
			pretty.Write(raw)
		}
		if err := writeFileAtomic(filepath.Join(s.chatsDir(), chatFile(ids[i])), pretty.Bytes()); err != nil {
			errJSON(w, 500, err.Error())
			return
		}
	}
	s.db.Log(LogEntry{Kind: "backup", Device: dev.Name, IP: clientIP(r), OK: true, Received: len(ids), Added: len(ids),
		Bytes: len(body), DurationMS: int(time.Since(start).Milliseconds()), Message: fmt.Sprintf("%d AI chat(s)", len(ids))})
	writeJSON(w, 200, map[string]any{"ok": true, "saved": len(ids), "ids": ids})
}

type chatInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Updated string `json:"updated"`
	Bytes   int64  `json:"bytes"`
}

// GET /backup/chats
func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authPhone(w, r, "/backup/chats"); !ok {
		return
	}
	entries, _ := os.ReadDir(s.chatsDir())
	out := []chatInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.chatsDir(), e.Name()))
		if err != nil {
			continue
		}
		var head struct {
			ID    any    `json:"id"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(b, &head)
		info, _ := e.Info()
		ci := chatInfo{ID: fmt.Sprint(head.ID), Title: head.Title, Bytes: int64(len(b))}
		if info != nil {
			ci.Updated = info.ModTime().UTC().Format(time.RFC3339)
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	writeJSON(w, 200, map[string]any{"chats": out})
}

// GET /backup/chats/{id}
func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authPhone(w, r, "/backup/chats"); !ok {
		return
	}
	b, err := os.ReadFile(filepath.Join(s.chatsDir(), chatFile(r.PathValue("id"))))
	if err != nil {
		errJSON(w, 404, "no such chat")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// removeChats deletes the backups of ids and returns those that were there.
func (s *Server) removeChats(ids []string) (deleted, missing []string, err error) {
	deleted, missing = []string{}, []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		switch e := os.Remove(filepath.Join(s.chatsDir(), chatFile(id))); {
		case e == nil:
			deleted = append(deleted, id)
		case os.IsNotExist(e):
			missing = append(missing, id)
		default:
			return deleted, missing, e
		}
	}
	return deleted, missing, nil
}

// DELETE /backup/chats/{id}: 200 either way, so deleting twice is fine; "deleted" says
// whether there was a backup.
func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/backup/chats")
	if !ok {
		return
	}
	deleted, _, err := s.removeChats([]string{r.PathValue("id")})
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if len(deleted) > 0 {
		s.db.Log(LogEntry{Kind: "backup", Device: dev.Name, IP: clientIP(r), OK: true, Message: "deleted 1 AI chat"})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": len(deleted) > 0})
}

// POST /backup/chats/delete {"ids": [...]}
func (s *Server) handleDeleteChats(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/backup/chats")
	if !ok {
		return
	}
	body, ok := readBody(w, r, maxSettingsBody)
	if !ok {
		return
	}
	var req struct {
		IDs []any `json:"ids"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.IDs == nil {
		errJSON(w, 400, `send {"ids": [...]}`)
		return
	}
	ids := make([]string, 0, len(req.IDs))
	for _, v := range req.IDs {
		if v != nil {
			ids = append(ids, fmt.Sprint(v))
		}
	}
	deleted, missing, err := s.removeChats(ids)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if len(deleted) > 0 {
		s.db.Log(LogEntry{Kind: "backup", Device: dev.Name, IP: clientIP(r), OK: true, Message: fmt.Sprintf("deleted %d AI chat(s)", len(deleted))})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": deleted, "missing": missing})
}

// secretKey matches setting names that hold a secret. The app is not supposed to send
// any; if one slips through it is dropped rather than written to disk.
var secretKey = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|password|passwd|token|credential|private[_-]?key|authorization|bearer)`)

// dropSecrets removes secret-looking keys anywhere in v and returns their paths.
func dropSecrets(v any, path string) []string {
	var dropped []string
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if _, isStr := x.(string); secretKey.MatchString(k) && (isStr || x == nil) {
				delete(t, k)
				dropped = append(dropped, p)
				continue
			}
			dropped = append(dropped, dropSecrets(x, p)...)
		}
	case []any:
		for i, x := range t {
			dropped = append(dropped, dropSecrets(x, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// settingsFile is one file per paired phone; the shared token has its own.
func (s *Server) settingsFile(dev Device) string {
	name := "shared-token.json"
	if dev.ID != 0 {
		name = fmt.Sprintf("device-%d.json", dev.ID)
	}
	return filepath.Join(s.settingsDir(), name)
}

// POST /backup/settings
func (s *Server) handleBackupSettings(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/backup/settings")
	if !ok {
		return
	}
	body, ok := readBody(w, r, maxSettingsBody)
	if !ok {
		return
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil || v == nil {
		errJSON(w, 400, "settings must be a JSON object")
		return
	}
	dropped := dropSecrets(v, "")
	if dropped == nil {
		dropped = []string{}
	}
	saved := map[string]any{"device": dev.Name, "device_id": dev.DeviceID, "saved": nowISO(), "settings": v}
	b, _ := json.MarshalIndent(saved, "", "  ")
	if err := writeFileAtomic(s.settingsFile(dev), b); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	msg := "app settings"
	if len(dropped) > 0 {
		msg += "; dropped secret-looking keys: " + strings.Join(dropped, ", ")
	}
	s.db.Log(LogEntry{Kind: "backup", Device: dev.Name, IP: clientIP(r), OK: true, Bytes: len(body), Message: msg})
	writeJSON(w, 200, map[string]any{"ok": true, "dropped": dropped})
}

// GET /backup/settings: this phone's settings, or, for a phone that never sent any (a new
// one), the settings saved most recently by any phone.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authPhone(w, r, "/backup/settings")
	if !ok {
		return
	}
	b, err := os.ReadFile(s.settingsFile(dev))
	if err != nil {
		var newest time.Time
		entries, _ := os.ReadDir(s.settingsDir())
		for _, e := range entries {
			info, ierr := e.Info()
			if ierr != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if info.ModTime().After(newest) {
				if nb, rerr := os.ReadFile(filepath.Join(s.settingsDir(), e.Name())); rerr == nil {
					b, err, newest = nb, nil, info.ModTime()
				}
			}
		}
	}
	if err != nil {
		errJSON(w, 404, "no settings saved yet")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
