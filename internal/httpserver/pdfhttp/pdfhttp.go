// Package pdfhttp serves an HTML document as a PDF printed by the platform's
// headless renderer (#1983).
//
// Every route that serves a document's bytes at .../content has a .../pdf
// beside it. The PDF route reads the document through its own content route,
// in-process and with the caller's request, so who may export a document is
// exactly who may read it, by the same checks; then it prints the document in
// the renderer with its backgrounds, one page per slide for a deck, each slide
// at its last build step.
//
// The print used to happen in the reader's browser, from a hidden frame. A
// browser prints backgrounds only when the reader asks, so a dark deck came
// out light text on white, and the fix for that repainted every deck light
// (#1772); a browser print view also lays out every build step of a slide as
// a page of its own. Far more decks are exported to be sent than to be
// printed, and the renderer prints what the reader sees.
package pdfhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/httpserver/thumbwire"
	"github.com/txn2/mcp-data-platform/internal/inproc"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Route is one PDF route and the content route beside it whose document it
// prints. Content names the content route with the PDF route's wildcards.
type Route struct {
	Pattern string
	Content string
	// Public is a route a caller reaches with no session, which is limited
	// per client: a print holds the renderer far longer than a read.
	Public bool
}

// Routes are the PDF routes, each beside the content route it prints.
var Routes = []Route{
	{Pattern: "GET /api/v1/portal/assets/{id}/pdf", Content: "/api/v1/portal/assets/{id}/content"},
	{Pattern: "GET /api/v1/portal/assets/{id}/versions/{version}/pdf", Content: "/api/v1/portal/assets/{id}/versions/{version}/content"},
	{Pattern: "GET /api/v1/admin/assets/{id}/pdf", Content: "/api/v1/admin/assets/{id}/content"},
	{Pattern: "GET /api/v1/admin/assets/{id}/versions/{version}/pdf", Content: "/api/v1/admin/assets/{id}/versions/{version}/content"},
	{Pattern: "GET /api/v1/resources/{id}/pdf", Content: "/api/v1/resources/{id}/content"},
	{Pattern: "GET /api/v1/resources/{id}/versions/{version}/pdf", Content: "/api/v1/resources/{id}/versions/{version}/content"},
	{Pattern: "GET /portal/view/{token}/pdf", Content: "/portal/view/{token}/content", Public: true},
	{Pattern: "GET /portal/view/{token}/items/{assetId}/pdf", Content: "/portal/view/{token}/items/{assetId}/content", Public: true},
}

// wildcard is one {name} in a route.
var wildcard = regexp.MustCompile(`\{([A-Za-z]+)\}`)

const (
	// DefaultTimeout bounds one print: loading the document, waiting for it
	// to lay itself out, and reading the PDF back.
	DefaultTimeout = 90 * time.Second
	// DefaultConcurrency is how many prints this replica runs at once. The
	// renderer is shared with the tile worker, and a print holds a browser
	// context for as long as the document takes to settle.
	DefaultConcurrency = 2
	// fileTimeout bounds one file the document loads from the platform.
	fileTimeout = 15 * time.Second
	// MaxDocumentBytes bounds the document a route reads to print. A slide
	// deck or a report is well under it; reading more than it would hold a
	// second copy of a large file in memory to refuse it.
	MaxDocumentBytes = 32 << 20
	// Public routes admit this many prints a minute per client, with this
	// burst.
	publicPerMinute = 6
	publicBurst     = 3
	// Width and height are the window a document is laid out in: the
	// viewer's frame for a deck. A deck's print view sizes each page from
	// its own configured slide size, not from this.
	pageWidth  = 1280
	pageHeight = 720
)

// ownPrefixes are the platform routes a printed document loads from: its
// declared references and the served slide runtime. Each answers without a
// session -- a reference is authorized by the token in its path -- so the
// document gets what a reader's browser gets and nothing else.
var ownPrefixes = []string{"/portal/refs/", "/portal/vendor/"}

// Printer prints a page to PDF.
type Printer interface {
	PrintPDF(ctx context.Context, p headless.Page) ([]byte, error)
}

// Deps is what the routes print with.
type Deps struct {
	// Routes is the platform's assembled handler. The content route a PDF
	// route sits beside is read through it.
	Routes http.Handler
	// Printer is the headless renderer.
	Printer Printer
	// Timeout bounds one print; zero is DefaultTimeout.
	Timeout time.Duration
	// Concurrency is how many prints run at once; zero is
	// DefaultConcurrency.
	Concurrency int
	// Limiter admits a print on a public route; nil admits every one.
	Limiter interface {
		Allow(r *http.Request) bool
		RetryAfter() int
	}
}

// Handler serves the PDF routes.
type Handler struct {
	deps  Deps
	slots chan struct{}
}

// New returns the handler.
func New(d Deps) *Handler {
	if d.Timeout <= 0 {
		d.Timeout = DefaultTimeout
	}
	if d.Concurrency <= 0 {
		d.Concurrency = DefaultConcurrency
	}
	return &Handler{deps: d, slots: make(chan struct{}, d.Concurrency)}
}

// MountFor mounts the PDF routes on mux, printed by the renderer the
// platform's thumbnails section names, with public routes limited per client
// as the public viewer is. The routes read documents back through mux, so it
// is called once the mux is complete. With no renderer answering, a route
// says so rather than failing silently.
func MountFor(mux *http.ServeMux, p *platform.Platform) {
	if p == nil {
		return
	}
	renderer, err := thumbwire.NewRenderer(p.Config())
	if err != nil {
		slog.Warn("pdfhttp: PDF export disabled", "error", err)
		return
	}
	resolver, err := ratelimit.NewResolver(p.Config().Portal.RateLimit.TrustedProxies)
	if err != nil {
		slog.Warn("pdfhttp: PDF export disabled", "error", err)
		return
	}
	limiter := ratelimit.NewHTTPLimiter(publicPerMinute, publicBurst, resolver)
	New(Deps{Routes: mux, Printer: renderer, Limiter: limiter}).Mount(mux)
}

// Mount registers every PDF route on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	for _, rt := range Routes {
		mux.Handle(rt.Pattern, h.route(rt))
	}
}

// route serves one PDF route.
func (h *Handler) route(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.Public && h.deps.Limiter != nil && !h.deps.Limiter.Allow(r) {
			w.Header().Set("Retry-After", strconv.Itoa(h.deps.Limiter.RetryAfter()))
			http.Error(w, "Too many PDF exports from this address. Try again shortly.", http.StatusTooManyRequests)
			return
		}
		h.serve(w, r, contentPath(rt.Content, r))
	})
}

// contentPath is the content route beside the request's PDF route, with the
// request's own wildcard values, each escaped as a single segment: a value
// carrying an encoded slash names the same route it was sent to, not
// another.
func contentPath(template string, r *http.Request) *url.URL {
	raw := wildcard.ReplaceAllStringFunc(template, func(m string) string {
		return url.PathEscape(r.PathValue(m[1 : len(m)-1]))
	})
	decoded := wildcard.ReplaceAllStringFunc(template, func(m string) string {
		return r.PathValue(m[1 : len(m)-1])
	})
	return &url.URL{Path: decoded, RawPath: raw}
}

// serve prints the document the request's content route serves.
//
// @Summary      Export an HTML document as PDF
// @Description  Prints the HTML document the route's .../content sibling serves, in the platform's headless renderer, with its backgrounds and colors as shown on screen. A slide deck prints one page per slide, each slide at its last build step. The document is read through its content route with the caller's own request, so the same access rules apply and that route's refusal is returned as it is. 415 for a document that is not HTML; 503 when no renderer answers.
// @Tags         Portal
// @Produce      application/pdf
// @Param        id       path  string  true  "Asset or resource ID"
// @Param        version  path  int     false "Asset version (the versions route only)"
// @Success      200  {file}    file
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      415  {string}  string
// @Failure      503  {string}  string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/pdf [get]
// @Router       /portal/assets/{id}/versions/{version}/pdf [get]
// @Router       /admin/assets/{id}/pdf [get]
// @Router       /admin/assets/{id}/versions/{version}/pdf [get]
// @Router       /resources/{id}/pdf [get]
// @Router       /resources/{id}/versions/{version}/pdf [get]
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, content *url.URL) {
	doc, ok := h.readDocument(w, r, content)
	if !ok {
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	case <-r.Context().Done():
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.deps.Timeout)
	defer cancel()
	pdf, err := h.deps.Printer.PrintPDF(ctx, h.page(doc.body))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("printing past the deadline: %w: %w", context.DeadlineExceeded, err)
		}
		h.printFailed(w, r, err)
		return
	}
	blobserve.Serve(w, r, blobserve.Options{
		Name:        doc.pdfName(),
		ContentType: "application/pdf",
		Data:        pdf,
	})
}

// document is what a content route answered.
type document struct {
	body        []byte
	disposition string
}

// pdfName is the document's own file name with a .pdf extension, or
// document.pdf when the content route named none.
func (d document) pdfName() string {
	_, params, err := mime.ParseMediaType(d.disposition)
	name := params["filename"]
	if err != nil || name == "" {
		return "document.pdf"
	}
	return strings.TrimSuffix(name, path.Ext(name)) + ".pdf"
}

// readDocument reads the document through the content route beside this
// one, with the caller's own request, and answers the caller with that
// route's refusal when it refuses.
func (h *Handler) readDocument(w http.ResponseWriter, r *http.Request, content *url.URL) (document, bool) {
	fwd := r.Clone(r.Context())
	fwd.URL = content
	fwd.RequestURI = ""
	// The whole document, fresh: a validator or a range would answer 304 or
	// 206 for bytes the caller never received.
	for _, name := range []string{"Range", "If-None-Match", "If-Modified-Since", "If-Range"} {
		fwd.Header.Del(name)
	}
	rec := &documentRecorder{header: http.Header{}}
	h.deps.Routes.ServeHTTP(rec, fwd)
	switch {
	case rec.code() != http.StatusOK:
		for name, values := range rec.header {
			if name != "Content-Length" {
				w.Header()[name] = values
			}
		}
		w.WriteHeader(rec.code())
		_, _ = w.Write(rec.body.Bytes())
	case !rec.html:
		http.Error(w, "Only an HTML document is exported to PDF.", http.StatusUnsupportedMediaType)
	case rec.tooLarge:
		http.Error(w, "The document is larger than the platform prints.", http.StatusRequestEntityTooLarge)
	default:
		return document{body: rec.body.Bytes(), disposition: rec.header.Get("Content-Disposition")}, true
	}
	return document{}, false
}

// documentRecorder is the content route's answer, read in-process. It keeps
// a refusal's body, and an HTML document's up to MaxDocumentBytes; anything
// else it is written is not kept, so refusing a large file holds no copy of
// it.
type documentRecorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	html     bool
	tooLarge bool
}

// Header is the response header the route sets.
func (r *documentRecorder) Header() http.Header { return r.header }

// WriteHeader records the first status, and whether the answer is HTML.
func (r *documentRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.html = contenttype.Normalize(r.header.Get("Content-Type")) == contenttype.HTML
}

// Write keeps what readDocument needs of the body.
func (r *documentRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	keep := r.status != http.StatusOK || (r.html && !r.tooLarge)
	if keep && r.status == http.StatusOK && r.body.Len()+len(p) > MaxDocumentBytes {
		r.tooLarge, keep = true, false
		r.body.Reset()
	}
	if keep {
		_, _ = r.body.Write(p) // a bytes.Buffer write does not fail
	}
	return len(p), nil
}

// code is the status the route answered, 200 when it wrote none.
func (r *documentRecorder) code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// printFailed answers a print that did not produce a PDF.
func (*Handler) printFailed(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("pdfhttp: printing a document failed",
		"path", logsan.SanitizeForLog(r.URL.Path), "error", logsan.SanitizeForLog(err.Error()))
	switch {
	case errors.Is(err, headless.ErrUnavailable):
		http.Error(w, "The PDF renderer is not available. Try again shortly.", http.StatusServiceUnavailable)
	case errors.Is(err, headless.ErrPDFTooLarge):
		http.Error(w, "The PDF is larger than the platform returns.", http.StatusRequestEntityTooLarge)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "The document did not finish laying itself out in time to be printed.", http.StatusServiceUnavailable)
	default:
		http.Error(w, "The document could not be printed.", http.StatusServiceUnavailable)
	}
}

// page is the document as the renderer prints it.
func (h *Handler) page(doc []byte) headless.Page {
	return headless.Page{
		Document: WithPrintStep(doc),
		Files:    h.files,
		Ready:    Ready,
		Width:    pageWidth,
		Height:   pageHeight,
		Scale:    1,
	}
}

// files answers the document's requests for its own origin from the
// platform's anonymous routes.
func (h *Handler) files(p string) (headless.File, bool) {
	clean := path.Clean(p)
	for _, prefix := range ownPrefixes {
		if strings.HasPrefix(clean, prefix) {
			body, contentType, ok := inproc.Get(h.deps.Routes, clean, fileTimeout)
			return headless.File{Body: body, ContentType: contentType}, ok
		}
	}
	return headless.File{}, false
}
