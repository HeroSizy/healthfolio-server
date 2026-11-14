package server

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/herosizy/healthsync-server/model"
	pdfium "github.com/klippa-app/go-pdfium"
	pdfreq "github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// Pages of the originals of checkup reports, as images, so the window (desktop or browser)
// can show where an item is printed without a PDF viewer of its own. PDFs are drawn by
// PDFium compiled to WebAssembly (pure Go: the same on every platform and in Docker);
// photos are sent as they are.
//
// PDFium starts on the first page asked for (≈0.3 s) and stops again after a minute
// without any, so the server keeps nothing running while nobody looks at reports.

const (
	pdfIdle      = time.Minute
	pageCacheMax = 24               // rendered pages kept in memory
	minPageWidth = 200              // pixels
	maxPageWidth = 2400             // pixels
	maxPDFBytes  = 100 << 20        // the largest original a report may have
	pdfTimeout   = 30 * time.Second // waiting for PDFium
)

var errNoPage = errors.New("no such page")

type pageRenderer struct {
	mu    sync.Mutex
	pool  pdfium.Pool
	inst  pdfium.Pdfium
	idle  *time.Timer
	cache map[string]model.PageImage
	order []string // cache keys, oldest first
}

var pages = &pageRenderer{cache: map[string]model.PageImage{}}

// start brings PDFium up, if it is not; mu is held.
func (r *pageRenderer) start() error {
	if r.inst != nil {
		return nil
	}
	pool, err := webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 1, MaxTotal: 1})
	if err != nil {
		return fmt.Errorf("PDF renderer: %w", err)
	}
	inst, err := pool.GetInstance(pdfTimeout)
	if err != nil {
		_ = pool.Close()
		return fmt.Errorf("PDF renderer: %w", err)
	}
	r.pool, r.inst = pool, inst
	return nil
}

// stopLater stops PDFium after pdfIdle without work; mu is held.
func (r *pageRenderer) stopLater() {
	if r.idle != nil {
		r.idle.Stop()
	}
	r.idle = time.AfterFunc(pdfIdle, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.stop()
	})
}

// stop shuts PDFium down; mu is held.
func (r *pageRenderer) stop() {
	if r.inst != nil {
		_ = r.inst.Close()
		_ = r.pool.Close()
		r.inst, r.pool = nil, nil
	}
}

// page is page n (from 0) of the file at path, at most width pixels wide.
func (r *pageRenderer) page(path string, n, width int) (model.PageImage, error) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return model.PageImage{}, os.ErrNotExist
	}
	width = max(minPageWidth, min(width, maxPageWidth))
	key := fmt.Sprintf("%s|%d|%d|%d|%d", path, fi.Size(), fi.ModTime().UnixNano(), n, width)
	typ := contentType(path)

	r.mu.Lock()
	defer r.mu.Unlock()
	if img, ok := r.cache[key]; ok {
		return img, nil
	}
	var img model.PageImage
	switch {
	case typ == "application/pdf":
		img, err = r.renderPDF(path, fi.Size(), n, width)
	case typ == "image/jpeg" || typ == "image/png":
		if n != 0 {
			return model.PageImage{}, errNoPage
		}
		var b []byte
		if b, err = os.ReadFile(path); err == nil {
			img = model.PageImage{Data: b, Type: typ, Pages: 1}
		}
	default:
		return model.PageImage{}, fmt.Errorf("%s can't be shown here (%s)", filepath.Base(path), strings.TrimPrefix(typ, "image/"))
	}
	if err != nil {
		return model.PageImage{}, err
	}
	r.cache[key] = img
	r.order = append(r.order, key)
	if len(r.order) > pageCacheMax {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
	return img, nil
}

// renderPDF draws one page of a PDF as a PNG; mu is held.
func (r *pageRenderer) renderPDF(path string, size int64, n, width int) (model.PageImage, error) {
	if size > maxPDFBytes {
		return model.PageImage{}, fmt.Errorf("%s is too large to show (%d MB)", filepath.Base(path), size>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return model.PageImage{}, err
	}
	if err := r.start(); err != nil {
		return model.PageImage{}, err
	}
	defer r.stopLater()
	doc, err := r.inst.OpenDocument(&pdfreq.OpenDocument{File: &data})
	if err != nil {
		return model.PageImage{}, fmt.Errorf("could not open %s: %w", filepath.Base(path), err)
	}
	defer r.inst.FPDF_CloseDocument(&pdfreq.FPDF_CloseDocument{Document: doc.Document})
	count, err := r.inst.FPDF_GetPageCount(&pdfreq.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return model.PageImage{}, err
	}
	if n < 0 || n >= count.PageCount {
		return model.PageImage{}, errNoPage
	}
	res, err := r.inst.RenderPageInPixels(&pdfreq.RenderPageInPixels{
		Page:  pdfreq.Page{ByIndex: &pdfreq.PageByIndex{Document: doc.Document, Index: n}},
		Width: width, Height: width * 4, // the width decides; a page is never 4 times taller
	})
	if err != nil {
		return model.PageImage{}, fmt.Errorf("could not draw page %d of %s: %w", n+1, filepath.Base(path), err)
	}
	defer res.Cleanup()
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, res.Result.RenderedImage); err != nil {
		return model.PageImage{}, err
	}
	return model.PageImage{Data: buf.Bytes(), Type: "image/png", Pages: count.PageCount}, nil
}

// reportPage is a page of one original of a report, for the window and the dashboard.
func (s *Server) reportPage(id, name string, n, width int) (model.PageImage, error) {
	_, folder, err := s.db.ReportDetail(id)
	if err != nil || folder == "" || name != filepath.Base(name) || name == "report.json" || strings.HasPrefix(name, ".") {
		return model.PageImage{}, os.ErrNotExist
	}
	return pages.page(filepath.Join(folder, name), n, width)
}

// reportDetail is a report with the originals kept with it.
func (s *Server) reportDetail(id string) (model.ReportDetail, error) {
	rep, folder, err := s.db.ReportDetail(id)
	if err != nil {
		return model.ReportDetail{}, err
	}
	files := []model.ReportFile{}
	for _, name := range originalFiles(folder) {
		fi, err := os.Stat(filepath.Join(folder, name))
		if err != nil {
			continue
		}
		files = append(files, model.ReportFile{Name: name, Size: fi.Size(), Type: contentType(name)})
	}
	return model.ReportDetail{Report: rep, Files: files}, nil
}
