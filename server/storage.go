package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/herosizy/healthsync-server/model"
)

// storageTTL: the window and the dashboard ask for the overview every few seconds, and
// walking the folders each time is wasted work for a number that changes slowly.
const storageTTL = 30 * time.Second

type storageCache struct {
	mu sync.Mutex
	at time.Time
	v  model.Storage
}

// storage is the room the data takes, by kind, cached for storageTTL.
func (s *Server) storage() model.Storage {
	s.stor.mu.Lock()
	defer s.stor.mu.Unlock()
	if time.Since(s.stor.at) < storageTTL {
		return s.stor.v
	}
	s.stor.v, s.stor.at = s.measureStorage(), time.Now()
	return s.stor.v
}

func (s *Server) measureStorage() model.Storage {
	dir := s.cfg.DataDir
	db := s.db.path
	parts := []model.StoragePart{}
	add := func(kind, path string, bytes int64, files int) {
		if files > 0 {
			parts = append(parts, model.StoragePart{Kind: kind, Bytes: bytes, Files: files, Path: path})
		}
	}
	// the database and its journal files
	var dbBytes int64
	dbFiles := 0
	for _, f := range []string{db, db + "-wal", db + "-shm", db + "-journal"} {
		if fi, err := os.Stat(f); err == nil {
			dbBytes += fi.Size()
			dbFiles++
		}
	}
	add("database", db, dbBytes, dbFiles)
	counted := map[string]bool{}
	for _, k := range []struct{ kind, path string }{
		{"reports", filepath.Join(dir, "reports")},
		{"chats", s.chatsDir()},
		{"settings", s.settingsDir()},
	} {
		b, n := dirSize(k.path)
		add(k.kind, k.path, b, n)
		counted[filepath.Clean(k.path)] = true
	}
	if s.csv != nil {
		b, n := dirSize(s.csv.dir)
		add("csv", s.csv.dir, b, n)
		counted[filepath.Clean(s.csv.dir)] = true
	}
	// everything else in the data folder: the token, settings.json, the hook inbox…
	var other int64
	otherFiles := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if counted[filepath.Clean(p)] {
				return filepath.SkipDir
			}
			return nil
		}
		if p == db || strings.HasPrefix(p, db+"-") {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			other += fi.Size()
			otherFiles++
		}
		return nil
	})
	add("other", dir, other, otherFiles)
	st := model.Storage{Parts: parts}
	for _, p := range parts {
		st.Total += p.Bytes
	}
	st.DiskFree, st.DiskSize = diskSpace(dir)
	return st
}

// dirSize is the size of the files under path, and how many there are.
func dirSize(path string) (bytes int64, files int) {
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			bytes += fi.Size()
			files++
		}
		return nil
	})
	return bytes, files
}
