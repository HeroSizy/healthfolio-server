package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/herosizy/healthsync-server/model"
	"time"
)

// twoPagePDF is a small, valid PDF of two A4 pages: the first with a black bar across
// its top half, the second blank.
func twoPagePDF() []byte {
	content := "0 0 0 rg 50 600 495 100 re f"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 5 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestReportPages(t *testing.T) {
	s, ts := newTestServer(t)
	pdf := twoPagePDF()
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 40, 30)), nil)
	body := `{"folder":"2026-11-02-Lab","report":{"id":"R2","date":"2026-11-02","institution":"Lab",
		"items":[{"name":"Glucose","value":5.1,"location":{"file":"report.pdf","page":0,"x":0.1,"y":0.2,"w":0.8,"h":0.1}}]},
		"files":[{"name":"report.pdf","data":"` + base64.StdEncoding.EncodeToString(pdf) + `"},
		         {"name":"photo.jpg","data":"` + base64.StdEncoding.EncodeToString(jb.Bytes()) + `"},
		         {"name":"scan.heic","data":"AAAA"}]}`
	if code, r := do(t, "POST", ts.URL+"/report", "master", body); code != 200 {
		t.Fatalf("report: %d %v", code, r)
	}

	// the detail, as the window reads it
	d, err := s.reportDetail("R2")
	if err != nil || len(d.Report.Items) != 1 || d.Report.Items[0].Location == nil || len(d.Files) != 3 {
		t.Fatalf("detail: %+v %v", d, err)
	}

	page := func(name string, n int, w string) (*http.Response, []byte) {
		return rawGet(t, ts.URL+"/api/reports/R2/files/"+name+"/pages/"+fmt.Sprint(n)+w)
	}
	start := time.Now()
	res, b := page("report.pdf", 0, "?w=600")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Pages") != "2" {
		t.Fatalf("page 0: %d %v %s", res.StatusCode, res.Header, b)
	}
	t.Logf("first page (PDFium starting) in %v", time.Since(start))
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 600 || h < 840 || h > 852 {
		t.Fatalf("page 0 is %dx%d, want 600 wide, A4", w, h)
	}
	// the bar is drawn where the PDF puts it: dark at 20% from the top, white at 80%
	dark := func(x, y int) bool { r, g, b, _ := img.At(x, y).RGBA(); return r+g+b < 3*0x4000 }
	if !dark(300, img.Bounds().Dy()*20/100) || dark(300, img.Bounds().Dy()*80/100) {
		t.Fatal("page 0 is not drawn as the PDF says")
	}
	if res, _ := page("report.pdf", 1, ""); res.StatusCode != 200 {
		t.Fatalf("page 1: %d", res.StatusCode)
	}
	for _, c := range []struct {
		name string
		n    int
		want int
	}{{"report.pdf", 2, 404}, {"report.json", 0, 404}, {"..%2Fx", 0, 404}, {"missing.pdf", 0, 404}, {"photo.jpg", 1, 404}, {"scan.heic", 0, 422}} {
		if res, _ := page(c.name, c.n, ""); res.StatusCode != c.want {
			t.Errorf("%s page %d: %d, want %d", c.name, c.n, res.StatusCode, c.want)
		}
	}
	// a photo is sent as it is
	if res, b := page("photo.jpg", 0, ""); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(b, jb.Bytes()) {
		t.Fatalf("photo: %d %v", res.StatusCode, res.Header)
	}
	// a page asked for again comes from the cache
	start = time.Now()
	if res, _ := page("report.pdf", 0, "?w=600"); res.StatusCode != 200 || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("cached page: %d in %v", res.StatusCode, time.Since(start))
	}
	// widths are kept between 200 and 2400 pixels
	if _, b := page("report.pdf", 1, "?w=99999"); !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatal("huge width")
	} else if im, _ := png.Decode(bytes.NewReader(b)); im.Bounds().Dx() != maxPageWidth {
		t.Fatalf("width %d, want %d", im.Bounds().Dx(), maxPageWidth)
	}
	// AI tokens can't read pages: the dashboard API is for the dashboard
	req, _ := http.NewRequest("GET", ts.URL+"/api/reports/R2/files/report.pdf/pages/0", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.9")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 403 {
		t.Fatalf("page through a proxy without a password: %d", r.StatusCode)
	}
}

// PDFium stops after a while without work, and starts again when asked.
func TestPageRendererStopsWhenIdle(t *testing.T) {
	s, ts := newTestServer(t)
	pdf := twoPagePDF()
	body := `{"folder":"x","report":{"id":"R3","items":[]},"files":[{"name":"a.pdf","data":"` + base64.StdEncoding.EncodeToString(pdf) + `"}]}`
	if code, _ := do(t, "POST", ts.URL+"/report", "master", body); code != 200 {
		t.Fatal(code)
	}
	if _, err := s.reportPage("R3", "a.pdf", 1, 300); err != nil {
		t.Fatal(err)
	}
	pages.mu.Lock()
	running := pages.inst != nil
	pages.stop() // what the idle timer does
	pages.cache = map[string]model.PageImage{}
	pages.order = nil
	pages.mu.Unlock()
	if !running {
		t.Fatal("PDFium was not running after a page")
	}
	if img, err := s.reportPage("R3", "a.pdf", 0, 300); err != nil || img.Pages != 2 {
		t.Fatalf("after a stop: %+v %v", img.Pages, err)
	}
}
