package telemetry

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReportCarriesNoPersonalData(t *testing.T) {
	got := make(chan string, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
	}))
	defer ts.Close()
	t.Setenv("HEALTHSYNC_SENTRY_DSN", strings.Replace(ts.URL, "http://", "http://key@", 1)+"/1")

	if f := Init("server", "0.0.1", true); Enabled() {
		f()
		t.Fatal("off is ignored")
	}
	flush := Init("server", "0.0.1", false)
	if !Enabled() {
		t.Fatal("not enabled with a DSN")
	}
	Error("ingest", errors.New("sqlite: disk full"))
	flush()

	body := <-got
	host, _ := os.Hostname()
	if !strings.Contains(body, "sqlite: disk full") || !strings.Contains(body, "healthsync@0.0.1") {
		t.Errorf("report misses the error or release:\n%s", body)
	}
	if host != "" && strings.Contains(body, `"server_name":"`+host+`"`) {
		t.Errorf("report carries the host name")
	}
	if strings.Contains(body, `"request"`) {
		t.Errorf("report carries a request")
	}
}
