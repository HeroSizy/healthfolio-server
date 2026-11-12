package server

import "testing"

// Stats is cached, but a sync shows up in it at once.
func TestStatsCacheFollowsIngest(t *testing.T) {
	s, ts := newTestServer(t)
	st, err := s.db.Stats()
	if err != nil || st.Total != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	do(t, "POST", ts.URL+"/ingest", "master",
		"HKQuantityTypeIdentifierStepCount|2026-11-01T08:00:00+08:00|2026-11-01T08:10:00+08:00|500|count|phone\n")
	if st, _ = s.db.Stats(); st.Total != 1 || len(st.Types) != 1 || st.LastReceived == nil {
		t.Fatalf("after ingest: %+v", st)
	}
	st.Types[0].Count = 99 // a caller changing its copy does not change the cache
	if again, _ := s.db.Stats(); again.Types[0].Count != 1 {
		t.Fatalf("cache shared with caller: %+v", again)
	}
	do(t, "POST", ts.URL+"/ingest", "master",
		"HKQuantityTypeIdentifierHeartRate|2026-11-01T08:00:00+08:00|2026-11-01T08:00:00+08:00|60|count/min|watch\n")
	if st, _ = s.db.Stats(); st.Total != 2 || len(st.Types) != 2 {
		t.Fatalf("after second ingest: %+v", st)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"Zen's iPhone":             "Zen's iPhone",
		"  a\n\nAllow only if\tx ": "a Allow only if x",
		"evil‮gnp.exe":             "evilgnp.exe",
		"zero​width\x00\x07":       "zerowidth",
		"中文 名字":                    "中文 名字",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
