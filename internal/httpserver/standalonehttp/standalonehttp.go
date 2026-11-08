// Package standalonehttp serves an HTML or JSX asset as one self-contained
// HTML file that opens from disk with the network off (#2530).
//
// Every content route an asset is downloaded from has a .../standalone beside
// it. The route reads the asset through its content route, in-process and
// with the caller's own request, so who may download the file is exactly who
// may read the asset, by the same checks, and the file inlines only what that
// read already hands the caller. internal/standalone then inlines what the
// document loads: the platform's served runtimes and the asset's declared
// references are read from the platform's own routes with a request that
// carries nothing of the caller's, and a file on another host is fetched
// through the egress guard.
//
// An asset that reads the platform's data while it renders is answered 409
// with what it reads, and the portal offers the stored source instead
// (?source=stored on the content route, pkg/portal).
package standalonehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/httpserver/corshttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/pdfhttp"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/standalone"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Route is one standalone route and the content route beside it.
type Route struct {
	Pattern string
	Content string
	// Public is a route a caller reaches with no session, limited per client:
	// an export fetches files on other hosts on the caller's behalf.
	Public bool
}

// Routes are the standalone routes, each beside the content route it reads.
var Routes = []Route{
	{Pattern: "GET /api/v1/portal/assets/{id}/standalone", Content: "/api/v1/portal/assets/{id}/content"},
	{Pattern: "GET /portal/view/{token}/standalone", Content: "/portal/view/{token}/content", Public: true},
	{Pattern: "GET /portal/view/{token}/items/{assetId}/standalone", Content: "/portal/view/{token}/items/{assetId}/content", Public: true},
}

var wildcard = regexp.MustCompile(`\{([A-Za-z]+)\}`)

const (
	// MaxDocumentBytes bounds the asset a route reads to export.
	MaxDocumentBytes = 32 << 20
	// Public routes admit this many exports a minute per client, with this
	// burst.
	publicPerMinute = 6
	publicBurst     = 3
	// NoticesHeader carries the files the export left as their URLs, as
	// URL-escaped JSON, so the portal can name them beside the download.
	NoticesHeader = "X-Standalone-Notices"
	// maxNotices bounds how many the header names.
	maxNotices = 50
	// headerContentType is the response header naming a body's type.
	headerContentType = "Content-Type"
	// DefaultConcurrency is how many exports this replica builds at once.
	// An export can fetch up to its byte bound from other hosts and compile a
	// bundle, so a signed-in caller cannot start them without limit.
	DefaultConcurrency = 4
)

// ownPrefixes are the platform routes an export reads in-process: the asset's
// declared references, authorized by the token in their path, and the served
// runtimes. Each answers without a session, so the export gets what a reader's
// browser gets and nothing else.
var ownPrefixes = []string{assetrefs.PathPrefix, "/portal/vendor/"}

// Deps is what the routes export with.
type Deps struct {
	// Routes is the platform's assembled handler: the content route a
	// standalone route sits beside, and the reference and vendor routes, are
	// read through it.
	Routes http.Handler
	// External fetches a file on another host; it is the egress-guarded
	// client, and is handed a request carrying nothing of the caller's.
	External *http.Client
	// Limits bounds one export; zero fields take standalone.DefaultLimits.
	Limits standalone.Limits
	// Limiter admits an export on a public route; nil admits every one.
	Limiter interface {
		Allow(r *http.Request) bool
		RetryAfter() int
	}
	// Downloads reports whether the signed-in caller's persona includes
	// downloading; nil admits every caller.
	Downloads func(r *http.Request) bool
	// PublicBaseURL is portal.public_base_url: the content route writes a
	// declared reference against it when it is set, so it is the origin a
	// reference is recognized at.
	PublicBaseURL string
	// Concurrency is how many exports run at once; zero is
	// DefaultConcurrency.
	Concurrency int
}

// Handler serves the standalone routes.
type Handler struct {
	deps  Deps
	slots chan struct{}
}

// New returns the handler.
func New(d Deps) *Handler {
	if d.Concurrency <= 0 {
		d.Concurrency = DefaultConcurrency
	}
	return &Handler{deps: d, slots: make(chan struct{}, d.Concurrency)}
}

// MountFor mounts the standalone routes on mux, fetching other hosts through
// an egress guard that admits no private address, with public routes limited
// per client. The routes read through mux, so it is called once the mux is
// complete.
//
// The guard takes none of the renderer's exceptions (thumbnails.
// allow_private_cidrs): a download is asked for from a public share link by
// anyone holding it, and a file the asset names on an internal host is left
// as its URL and named in the notice rather than fetched on their behalf.
func MountFor(mux *http.ServeMux, p *platform.Platform) {
	if p == nil {
		return
	}
	cfg := p.Config()
	d, err := platformDeps(cfg.Portal.RateLimit.TrustedProxies, cfg.Portal.PublicBaseURL)
	if err != nil {
		slog.Warn("standalonehttp: standalone download disabled", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	d.Routes = corshttp.Middleware(mux)
	d.Downloads = pdfhttp.ExportsFor(p)
	New(d).Mount(mux)
}

// platformDeps is everything but the routes and the download gate: the
// egress-guarded client, the per-client limiter over the trusted proxies, and
// the origin a reference is written against.
func platformDeps(trustedProxies []string, publicBaseURL string) (Deps, error) {
	guard, err := egressguard.New(nil)
	if err != nil {
		return Deps{}, fmt.Errorf("building the egress guard: %w", err)
	}
	resolver, err := ratelimit.NewResolver(trustedProxies)
	if err != nil {
		return Deps{}, fmt.Errorf("reading the trusted proxies: %w", err)
	}
	return Deps{
		External: outbound.NewClient(outbound.Options{
			Kind: outbound.KindStandalone, Base: guard.Transport(), CheckRedirect: outbound.FollowRedirects,
		}),
		Limiter:       ratelimit.NewHTTPLimiter(publicPerMinute, publicBurst, resolver),
		PublicBaseURL: publicBaseURL,
	}, nil
}

// Mount registers every standalone route on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	for _, rt := range Routes {
		mux.Handle(rt.Pattern, h.route(rt))
	}
}

func (h *Handler) route(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.Public && h.deps.Limiter != nil && !h.deps.Limiter.Allow(r) {
			httpobs.MarkRateLimited(r, httpobs.LimiterStandaloneExport)
			w.Header().Set("Retry-After", strconv.Itoa(h.deps.Limiter.RetryAfter()))
			http.Error(w, "Too many downloads from this address. Try again shortly.", http.StatusTooManyRequests)
			return
		}
		if !rt.Public && h.deps.Downloads != nil && !h.deps.Downloads(r) {
			http.Error(w, "Your persona does not include downloading from the portal.", http.StatusForbidden)
			return
		}
		select {
		case h.slots <- struct{}{}:
			defer func() { <-h.slots }()
		case <-r.Context().Done():
			return
		}
		h.serve(w, r, contentPath(rt.Content, r))
	})
}

// contentPath is the content route beside the request's route, with the
// request's own wildcard values, each escaped as a single segment.
func contentPath(template string, r *http.Request) *url.URL {
	raw := wildcard.ReplaceAllStringFunc(template, func(m string) string {
		return url.PathEscape(r.PathValue(m[1 : len(m)-1]))
	})
	decoded := wildcard.ReplaceAllStringFunc(template, func(m string) string {
		return r.PathValue(m[1 : len(m)-1])
	})
	return &url.URL{Path: decoded, RawPath: raw}
}

// serve exports the asset the request's content route serves.
//
// @Summary      Download an HTML or JSX asset as a self-contained file
// @Description  Reads the asset through the route's .../content sibling with the caller's own request, so the same access rules apply and that route's refusal status is returned, and answers one HTML file that opens from disk with the network off: the platform's served runtimes, the asset's declared images and fonts, and files it names on other hosts are inlined, and a JSX asset is compiled and bundled with React, Recharts and lucide-react. A file that could not be fetched is left as its URL and named in the X-Standalone-Notices header (URL-escaped JSON). 409 when the asset reads the platform's data while it renders (a declared data file, an embedded form, the basemap archive), with the reasons; the stored source is then read with ?source=stored on the content route. 415 for any other content type; 422 for a JSX asset that does not build.
// @Tags         Portal
// @Produce      text/html
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {file}    file
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  standalonehttp.Refusal
// @Failure      415  {string}  string
// @Failure      422  {object}  standalonehttp.Refusal
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/standalone [get]
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, content *url.URL) {
	doc, ok := h.readDocument(w, r, content)
	if !ok {
		return
	}
	file, notices, err := standalone.Build(r.Context(), standalone.Input{
		Document: doc.body, ContentType: doc.contentType, Title: doc.title(),
		Origin:   referenceOrigin(r, h.deps.PublicBaseURL),
		Own:      ownFetcher{routes: h.deps.Routes},
		External: externalFetcher{client: h.deps.External},
		Limits:   h.deps.Limits,
	})
	if err != nil {
		refuseBuild(w, r, err)
		return
	}
	if header := noticesHeader(notices); header != "" {
		w.Header().Set(NoticesHeader, header)
	}
	blobserve.Serve(w, r, blobserve.Options{
		Name:        doc.fileName(),
		ContentType: "text/html; charset=utf-8",
		Data:        file,
	})
}

// Refusal is a 409 or 422 answer's body.
type Refusal struct {
	// Error is platform_data or build_failed.
	Error string `json:"error" example:"platform_data"`
	// Reasons are the sentences the portal shows.
	Reasons []string `json:"reasons"`
}

func refuseBuild(w http.ResponseWriter, r *http.Request, err error) {
	var data *standalone.PlatformDataError
	var build *standalone.BuildError
	switch {
	case errors.As(err, &data):
		writeRefusal(w, http.StatusConflict, Refusal{Error: "platform_data", Reasons: data.Reasons})
	case errors.As(err, &build):
		writeRefusal(w, http.StatusUnprocessableEntity, Refusal{Error: "build_failed", Reasons: build.Messages})
	case errors.Is(err, standalone.ErrUnsupported):
		http.Error(w, "Only an HTML or JSX asset is downloaded as a standalone file.", http.StatusUnsupportedMediaType)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.Canceled):
		// 503, not 504: a CDN in front of the deployment replaces a 504's
		// body, and this one says what happened (#1704).
		http.Error(w, "The standalone file took too long to build. Try again shortly.", http.StatusServiceUnavailable)
	default:
		slog.WarnContext(r.Context(), "standalonehttp: export failed", "error", logsan.SanitizeForLog(err.Error()))
		http.Error(w, "The standalone file could not be built.", http.StatusInternalServerError)
	}
}

func writeRefusal(w http.ResponseWriter, status int, body Refusal) {
	if body.Reasons == nil {
		body.Reasons = []string{}
	}
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, body)
}

// noticesHeader is the notices as URL-escaped JSON: one ASCII header value
// whatever the URLs held.
func noticesHeader(notices []standalone.Notice) string {
	if len(notices) == 0 {
		return ""
	}
	if len(notices) > maxNotices {
		notices = notices[:maxNotices]
	}
	b, err := json.Marshal(notices)
	if err != nil {
		return ""
	}
	return url.QueryEscape(string(b))
}

// referenceOrigin is the origin the content route wrote the asset's declared
// references against (assetrefs.BaseURL, the same call), which is how the
// export recognizes one as the platform's own.
func referenceOrigin(r *http.Request, publicBaseURL string) *url.URL {
	base := assetrefs.BaseURL(r, publicBaseURL)
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// document is what a content route answered.
type document struct {
	body        []byte
	contentType string
	disposition string
}

// fileName is the asset's own name with .html.
func (d document) fileName() string {
	return strings.TrimSuffix(d.title(), path.Ext(d.title())) + ".html"
}

// title is the asset's file name as its content route named it.
func (d document) title() string {
	_, params, err := mime.ParseMediaType(d.disposition)
	if name := params["filename"]; err == nil && name != "" {
		return name
	}
	return "document"
}

// readDocument reads the asset through its content route with the caller's
// own request, answering the caller with that route's refusal.
func (h *Handler) readDocument(w http.ResponseWriter, r *http.Request, content *url.URL) (document, bool) {
	fwd := r.Clone(httpobs.Detached(r.Context()))
	fwd.URL = content
	fwd.RequestURI = ""
	for _, name := range []string{"Range", "If-None-Match", "If-Modified-Since", "If-Range"} {
		fwd.Header.Del(name)
	}
	rec := newRecorder(MaxDocumentBytes)
	h.deps.Routes.ServeHTTP(rec, fwd)
	switch {
	case rec.code() != http.StatusOK:
		for _, name := range []string{"Retry-After", "WWW-Authenticate", "Location"} {
			if v := rec.header.Get(name); v != "" {
				w.Header().Set(name, v)
			}
		}
		http.Error(w, "The asset could not be read: "+http.StatusText(rec.code())+".", rec.code())
	case rec.tooLarge:
		http.Error(w, "The asset is larger than the platform exports.", http.StatusRequestEntityTooLarge)
	default:
		return document{
			body: rec.body.Bytes(), contentType: rec.header.Get(headerContentType),
			disposition: rec.header.Get("Content-Disposition"),
		}, true
	}
	return document{}, false
}

// recorder is a route's answer read in-process, kept up to a bound.
type recorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	limit    int64
	tooLarge bool
}

func newRecorder(limit int64) *recorder { return &recorder{header: http.Header{}, limit: limit} }

// Header is the response header the route sets.
func (r *recorder) Header() http.Header { return r.header }

// WriteHeader records the first status.
func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Write keeps the body up to the limit and notes a body past it.
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if int64(r.body.Len()+len(p)) > r.limit {
		r.tooLarge = true
		return len(p), nil
	}
	_, _ = r.body.Write(p) // a bytes.Buffer write does not fail
	return len(p), nil
}

func (r *recorder) code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// ownFetcher reads a platform path through the assembled routes, with a
// request of its own: no cookie, no authorization header, nothing of the
// caller's, and only on the reference and vendor routes.
type ownFetcher struct {
	routes http.Handler
}

// Fetch reads one reference or vendor path in-process.
func (f ownFetcher) Fetch(ctx context.Context, u *url.URL, limit int64) (body []byte, contentType string, err error) {
	if !ownPath(u.Path) {
		return nil, "", fmt.Errorf("the export reads no route at %s", u.Path)
	}
	req, err := http.NewRequestWithContext(httpobs.Detached(ctx), http.MethodGet, u.RequestURI(), http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("building the request: %w", err)
	}
	rec := newRecorder(limit)
	f.routes.ServeHTTP(rec, req)
	if rec.code() != http.StatusOK {
		return nil, "", fmt.Errorf("the platform answered %d", rec.code())
	}
	if rec.tooLarge {
		return nil, "", standalone.ErrTooLarge
	}
	return rec.body.Bytes(), rec.header.Get(headerContentType), nil
}

func ownPath(p string) bool {
	clean := path.Clean(p)
	for _, prefix := range ownPrefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

// externalFetcher fetches a file on another host through the egress guard,
// with a request carrying no header of the caller's.
type externalFetcher struct {
	client *http.Client
}

// Fetch reads one URL on another host.
func (f externalFetcher) Fetch(ctx context.Context, u *url.URL, limit int64) (body []byte, contentType string, err error) {
	if f.client == nil {
		return nil, "", errors.New("no client for other hosts")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("building the request: %w", err)
	}
	res, err := f.client.Do(req) // #nosec G704 -- the destination is the document author's; the client dials through the egress guard
	if err != nil {
		return nil, "", fmt.Errorf("fetching: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("the host answered %d", res.StatusCode)
	}
	body, err = io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, "", standalone.ErrTooLarge
	}
	return body, res.Header.Get(headerContentType), nil
}
