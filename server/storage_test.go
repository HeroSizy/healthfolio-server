package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorage(t *testing.T) {
	s, ts := newTestServer(t)
	do(t, "POST", ts.URL+"/backup/chats", "master", `{"id":"c1","title":"x"}`)
	_ = os.MkdirAll(filepath.Join(s.cfg.DataDir, "reports", "r1"), 0o700)
	_ = os.WriteFile(filepath.Join(s.cfg.DataDir, "reports", "r1", "a.pdf"), make([]byte, 1000), 0o600)
	st := s.measureStorage()
	by := map[string]int64{}
	var sum int64
	for _, p := range st.Parts {
		by[p.Kind] = p.Bytes
		sum += p.Bytes
	}
	if by["database"] == 0 || by["reports"] != 1000 || by["chats"] == 0 {
		t.Fatalf("parts: %+v", st.Parts)
	}
	if sum != st.Total {
		t.Fatalf("total %d, parts add up to %d", st.Total, sum)
	}
	if st.DiskSize == 0 || st.DiskFree == 0 || st.DiskFree > st.DiskSize {
		t.Fatalf("disk: %d free of %d", st.DiskFree, st.DiskSize)
	}
}
