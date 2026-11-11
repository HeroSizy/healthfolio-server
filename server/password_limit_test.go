package server

import (
	"net/http"
	"testing"
)

// Wrong dashboard passwords from one address are cut off after maxPasswordFails; the
// right one works before that, and a request without a password is no attempt.
func TestDashboardPasswordLimit(t *testing.T) {
	s, ts := newTestServer(t)
	s.cfg.AdminPassword = "correct horse"
	try := func(pw string, withAuth bool) int {
		req, _ := http.NewRequest("GET", ts.URL+"/api/overview", nil)
		req.Header.Set("Via", "1.1 proxy") // not this computer's own browser
		if withAuth {
			req.SetBasicAuth("admin", pw)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < 20; i++ {
		if code := try("", false); code != 401 {
			t.Fatalf("no password: %d", code)
		}
	}
	if code := try("correct horse", true); code != 200 {
		t.Fatalf("right password: %d", code)
	}
	for i := 0; i < maxPasswordFails; i++ {
		if code := try("guess", true); code != 401 {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code := try("correct horse", true); code != 429 {
		t.Fatalf("after %d wrong ones: %d", maxPasswordFails, code)
	}
}
