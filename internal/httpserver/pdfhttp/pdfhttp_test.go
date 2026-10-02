package pdfhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
)

const deck = `<!DOCTYPE html><html><head><title>Q3</title></head><body>` +
	`<script src="/portal/vendor/reveal/reveal.js"></script></body></html>`

// fakePrinter records the page it was handed and answers with pdf or err.
type fakePrinter struct {
	mu    sync.Mutex
	pages []headless.Page
	pdf   []byte
	err   error
	gate  chan struct{}
}

func (f *fakePrinter) PrintPDF(ctx context.Context, p headless.Page) ([]byte, error) {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, fmt.Errorf("printing: %w", ctx.Err())
		}
	}
	f.mu.Lock()
	f.pages = append(f.pages, p)
	f.mu.Unlock()
	return f.pdf, f.err
}

// routes stands in for the assembled mux: the content routes answer only a
// caller carrying the session cookie, as the real ones answer only a caller
// their auth middleware admits.
func routes(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	content := func(ct, body, name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("session"); err != nil || c.Value != "ok" {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"title":"authentication required"}`))
				return
			}
			if r.Header.Get("Range") != "" || r.Header.Get("If-None-Match") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
			_, _ = w.Write([]byte(body))
		}
	}
	mux.Handle("GET /api/v1/portal/assets/{id}/content", content("text/html; charset=utf-8", deck, "Q3 review.html"))
	mux.Handle("GET /api/v1/portal/assets/{id}/versions/{version}/content", content("text/html", deck, ""))
	mux.Handle("GET /api/v1/resources/{id}/content", content("text/markdown", "# hi", "notes.md"))
	mux.HandleFunc("GET /portal/vendor/reveal/reveal.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte("window.Reveal={}"))
	})
	mux.HandleFunc("GET /api/v1/admin/secret", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("admin"))
	})
	return mux
}

func serve(t *testing.T, h *Handler, mux *http.ServeMux, path string, cookie bool) *httptest.ResponseRecorder {
	t.Helper()
	h.Mount(mux)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	if cookie {
		req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	}
	// A validator the content route would answer 304 to: the PDF route reads
	// the whole document regardless.
	req.Header.Set("If-None-Match", `"etag"`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPDFRoutePrintsTheDocumentItsContentRouteServes(t *testing.T) {
	mux := routes(t)
	p := &fakePrinter{pdf: []byte("%PDF-1.7 deck")}
	rec := serve(t, New(Deps{Routes: mux, Printer: p}), mux, "/api/v1/portal/assets/a1/pdf", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Q3 review.pdf"`) {
		t.Errorf("Content-Disposition = %q, want the document's own name as a PDF", cd)
	}
	if rec.Body.String() != "%PDF-1.7 deck" {
		t.Errorf("body = %q", rec.Body.String())
	}
	page := p.pages[0]
	if !strings.Contains(string(page.Document), `<head>`+PrintStep+`<title>`) {
		t.Error("the print step is not at the head of the document")
	}
	if page.Ready != Ready || page.Width != pageWidth || page.Height != pageHeight {
		t.Errorf("page = %+v", page)
	}
	if f, ok := page.Files("/portal/vendor/reveal/reveal.js"); !ok || string(f.Body) != "window.Reveal={}" {
		t.Errorf("the served runtime was not answered: %v %q", ok, f.Body)
	}
	if _, ok := page.Files("/api/v1/admin/secret"); ok {
		t.Error("the document reached a platform route outside its own files")
	}
	if _, ok := page.Files("/portal/vendor/../../api/v1/admin/secret"); ok {
		t.Error("a path that climbs out of the served files was answered")
	}
}

func TestPDFRouteAnswersWithTheContentRoutesRefusal(t *testing.T) {
	mux := routes(t)
	p := &fakePrinter{pdf: []byte("%PDF")}
	rec := serve(t, New(Deps{Routes: mux, Printer: p}), mux, "/api/v1/portal/assets/a1/pdf", false)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authentication required") {
		t.Fatalf("HTTP %d %q, want the content route's 401", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if len(p.pages) != 0 {
		t.Error("a document the caller may not read was printed")
	}
}

func TestPDFRouteRefusesADocumentThatIsNotHTML(t *testing.T) {
	mux := routes(t)
	rec := serve(t, New(Deps{Routes: mux, Printer: &fakePrinter{}}), mux, "/api/v1/resources/r1/pdf", true)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("HTTP %d, want 415", rec.Code)
	}
}

func TestPDFRouteNamesAnUnnamedDocument(t *testing.T) {
	mux := routes(t)
	rec := serve(t, New(Deps{Routes: mux, Printer: &fakePrinter{pdf: []byte("%PDF")}}), mux,
		"/api/v1/portal/assets/a1/versions/2/pdf", true)
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="document.pdf"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestPDFRouteReportsAPrintThatFailed(t *testing.T) {
	cases := map[string]struct {
		err  error
		code int
		want string
	}{
		"no renderer": {fmt.Errorf("dial: %w", headless.ErrUnavailable), http.StatusServiceUnavailable, "renderer is not available"},
		"too large":   {headless.ErrPDFTooLarge, http.StatusRequestEntityTooLarge, "larger"},
		"timed out":   {context.DeadlineExceeded, http.StatusServiceUnavailable, "in time"},
		"other":       {errors.New("headless: the page's readiness check threw"), http.StatusServiceUnavailable, "could not be printed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mux := routes(t)
			rec := serve(t, New(Deps{Routes: mux, Printer: &fakePrinter{err: tc.err}}), mux, "/api/v1/portal/assets/a1/pdf", true)
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("HTTP %d %q, want %d %q", rec.Code, rec.Body.String(), tc.code, tc.want)
			}
		})
	}
}

// A caller that gives up while every print slot is taken is not printed for.
func TestPDFRouteWaitsForASlotAndLeavesWhenTheCallerDoes(t *testing.T) {
	mux := routes(t)
	p := &fakePrinter{pdf: []byte("%PDF"), gate: make(chan struct{})}
	h := New(Deps{Routes: mux, Printer: p, Concurrency: 1})
	h.Mount(mux)

	busy := make(chan *httptest.ResponseRecorder)
	go func() {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/assets/a1/pdf", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		busy <- rec
	}()
	// The first print holds the only slot until the gate opens; fill the
	// slot channel's view by waiting until the handler has taken it.
	for len(h.slots) == 0 {
		select {
		case rec := <-busy:
			t.Fatalf("the first print finished before the gate opened: %d", rec.Code)
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/portal/assets/a1/pdf", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if len(p.pages) != 0 {
		t.Error("a caller that left was printed for")
	}
	close(p.gate)
	if first := <-busy; first.Code != http.StatusOK {
		t.Fatalf("the first print answered %d", first.Code)
	}
}

func TestWithPrintStep(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"head":        {`<!doctype html><html><head lang="x"><title>t</title>`, `<!doctype html><html><head lang="x">` + PrintStep + `<title>t</title>`},
		"html only":   {`<!doctype html><HTML><body>b`, `<!doctype html><HTML>` + PrintStep + `<body>b`},
		"fragment":    {`<p>hi</p>`, PrintStep + `<p>hi</p>`},
		"header text": {`<html><header>x</header>`, `<html>` + PrintStep + `<header>x</header>`},
	}
	for name, tc := range cases {
		if got := string(WithPrintStep([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestNewDefaults(t *testing.T) {
	h := New(Deps{})
	if h.deps.Timeout != DefaultTimeout || cap(h.slots) != DefaultConcurrency {
		t.Errorf("defaults = %v, %d", h.deps.Timeout, cap(h.slots))
	}
}

// An id carrying an encoded slash is one segment of the route it was sent
// to, never the path of another content route.
func TestPDFRouteKeepsAnEncodedSlashInItsSegment(t *testing.T) {
	mux := routes(t)
	var got []string
	mux.HandleFunc("GET /api/v1/admin/assets/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.PathValue("id"))
		w.WriteHeader(http.StatusNotFound)
	})
	rec := serve(t, New(Deps{Routes: mux, Printer: &fakePrinter{}}), mux, "/api/v1/admin/assets/X%2Fversions%2F3/pdf", true)
	if rec.Code != http.StatusNotFound || len(got) != 1 || got[0] != "X/versions/3" {
		t.Fatalf("HTTP %d, ids %v: the id was not kept as one segment of the admin content route", rec.Code, got)
	}
}

// A large file that is not HTML is refused without being held, and an HTML
// document past the cap is refused too.
func TestPDFRouteRefusesWithoutHoldingALargeDocument(t *testing.T) {
	big := strings.Repeat("x", MaxDocumentBytes+1)
	for name, ct := range map[string]string{"binary": "application/octet-stream", "html": "text/html"} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/resources/{id}/content", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ct)
				for i := 0; i < len(big); i += 1 << 20 {
					_, _ = w.Write([]byte(big[i:min(i+1<<20, len(big))]))
				}
			})
			p := &fakePrinter{}
			rec := serve(t, New(Deps{Routes: mux, Printer: p}), mux, "/api/v1/resources/r1/pdf", true)
			want := http.StatusUnsupportedMediaType
			if ct == "text/html" {
				want = http.StatusRequestEntityTooLarge
			}
			if rec.Code != want || len(p.pages) != 0 {
				t.Fatalf("HTTP %d, %d prints; want %d and none", rec.Code, len(p.pages), want)
			}
		})
	}
}

// A refusal keeps the headers that tell the caller what to do.
func TestPDFRouteKeepsTheRefusalsHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /portal/view/{token}/content", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	rec := serve(t, New(Deps{Routes: mux, Printer: &fakePrinter{}}), mux, "/portal/view/tok/pdf", false)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "7" {
		t.Fatalf("HTTP %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// stubLimiter admits the first n requests.
type stubLimiter struct{ n int }

func (s *stubLimiter) Allow(*http.Request) bool { s.n--; return s.n >= 0 }
func (*stubLimiter) RetryAfter() int            { return 10 }

// A public route is limited per client; a route behind a session is not.
func TestPDFRouteLimitsPublicPrints(t *testing.T) {
	mux := routes(t)
	mux.HandleFunc("GET /portal/view/{token}/content", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(deck))
	})
	h := New(Deps{Routes: mux, Printer: &fakePrinter{pdf: []byte("%PDF")}, Limiter: &stubLimiter{n: 1}})
	h.Mount(mux)
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("/portal/view/tok/pdf"); rec.Code != http.StatusOK {
		t.Fatalf("first public print: HTTP %d", rec.Code)
	}
	rec := do("/portal/view/tok/pdf")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "10" {
		t.Fatalf("second public print: HTTP %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := do("/api/v1/portal/assets/a1/pdf"); rec.Code != http.StatusOK {
		t.Fatalf("a session's print was limited: HTTP %d", rec.Code)
	}
}

// A print the route's own deadline cut off is reported as not finishing in
// time, whatever error the renderer gave.
func TestPDFRouteReportsItsOwnDeadline(t *testing.T) {
	mux := routes(t)
	p := &fakePrinter{gate: make(chan struct{})}
	rec := serve(t, New(Deps{Routes: mux, Printer: p, Timeout: time.Millisecond}), mux, "/api/v1/portal/assets/a1/pdf", true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "in time") {
		t.Fatalf("HTTP %d %q", rec.Code, rec.Body.String())
	}
}
