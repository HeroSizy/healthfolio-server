package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Checkup reports read back by a paired phone, to restore them from the backup:
//
//	GET /backup/reports                    → {"reports": [{id, folder, date, received, files: [{name, size, sha256}]}]}
//	GET /backup/reports/{id}               → report.json exactly as it was kept
//	GET /backup/reports/{id}/files/{name}  → one original file
//
// Any paired device's token can read them (the AI assistant tokens too, since this only
// reads); none of them change anything.

// fileHashes remembers the SHA-256 of each original by path, size and time, so listing
// the reports again doesn't read every PDF again.
var fileHashes = struct {
	sync.Mutex
	m map[string]hashEntry
}{m: map[string]hashEntry{}}

type hashEntry struct {
	size int64
	mod  time.Time
	sum  string
}

func fileSHA256(path string, fi os.FileInfo) (string, error) {
	fileHashes.Lock()
	e, ok := fileHashes.m[path]
	fileHashes.Unlock()
	if ok && e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
		return e.sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	fileHashes.Lock()
	fileHashes.m[path] = hashEntry{fi.Size(), fi.ModTime(), sum}
	fileHashes.Unlock()
	return sum, nil
}

// readDevice lets any paired token in, read-only ones too.
func (s *Server) readDevice(w http.ResponseWriter, r *http.Request, what string) bool {
	if _, ok := s.authDevice(r); ok {
		return true
	}
	s.db.Log(LogEntry{Kind: "auth", IP: clientIP(r), Message: "bad token on " + what})
	writeJSON(w, 401, map[string]any{"error": "bad token", "reason": "bad_token"})
	return false
}

// GET /backup/reports
func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	if !s.readDevice(w, r, "/backup/reports") {
		return
	}
	reps, err := s.db.ReportFolders()
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	out := []map[string]any{}
	for _, rep := range reps {
		if _, err := os.Stat(filepath.Join(rep.Folder, "report.json")); err != nil {
			continue // the folder was moved or deleted by hand: nothing to restore
		}
		files := []map[string]any{}
		for _, name := range originalFiles(rep.Folder) {
			p := filepath.Join(rep.Folder, name)
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			sum, err := fileSHA256(p, fi)
			if err != nil {
				continue
			}
			files = append(files, map[string]any{"name": name, "size": fi.Size(), "sha256": sum})
		}
		out = append(out, map[string]any{
			"id": rep.ID, "folder": filepath.Base(rep.Folder), "date": rep.Date,
			"received": rep.Received, "files": files,
		})
	}
	writeJSON(w, 200, map[string]any{"reports": out})
}

// reportFolder is the folder of report id, or "" when there is none.
func (s *Server) reportFolder(id string) string {
	_, folder, err := s.db.ReportDetail(id)
	if err != nil {
		return ""
	}
	return folder
}

// GET /backup/reports/{id}
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	if !s.readDevice(w, r, "/backup/reports") {
		return
	}
	folder := s.reportFolder(r.PathValue("id"))
	b, err := os.ReadFile(filepath.Join(folder, "report.json"))
	if folder == "" || err != nil {
		errJSON(w, 404, "no such report")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// GET /backup/reports/{id}/files/{name}
func (s *Server) handleGetReportFile(w http.ResponseWriter, r *http.Request) {
	if !s.readDevice(w, r, "/backup/reports") {
		return
	}
	folder := s.reportFolder(r.PathValue("id"))
	name := r.PathValue("name")
	if folder == "" || name != filepath.Base(name) || name == "report.json" || strings.HasPrefix(name, ".") {
		errJSON(w, 404, "no such file")
		return
	}
	f, err := os.Open(filepath.Join(folder, name))
	if err != nil {
		errJSON(w, 404, "no such file")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		errJSON(w, 404, "no such file")
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}
