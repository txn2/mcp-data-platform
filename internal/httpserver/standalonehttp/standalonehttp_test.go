package standalonehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/standalone"
)

// platformStub is the platform's routes as the export reads them: a content
// route per asset, the reference route and the vendor route. It records the
// headers each own read carried.
type platformStub struct {
	mu      sync.Mutex
	docs    map[string]stubDoc
	cookies []string
	auths   []string
}

type stubDoc struct {
	body, ct, name string
	status         int
}

func (p *platformStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/portal/refs/"), strings.HasPrefix(r.URL.Path, "/portal/vendor/"):
		p.mu.Lock()
		p.cookies = append(p.cookies, r.Header.Get("Cookie"))
		p.auths = append(p.auths, r.Header.Get("Authorization"))
		p.mu.Unlock()
		switch r.URL.Path {
		case "/portal/refs/a1/tok-logo":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "PNG")
		case "/portal/vendor/reveal/reveal.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, "var Reveal={};")
		default:
			http.NotFound(w, r)
		}
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/portal/assets/"), "/content")
	if strings.HasPrefix(r.URL.Path, "/portal/view/") {
		id = "shared"
	}
	if r.Header.Get("Cookie") != "session=reader" && !strings.HasPrefix(r.URL.Path, "/portal/view/") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d, ok := p.docs[id]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if d.status != 0 {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "busy", d.status)
		return
	}
	w.Header().Set("Content-Type", d.ct)
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.name+`"`)
	_, _ = io.WriteString(w, d.body)
}

type denyAll struct{}

func (denyAll) Allow(*http.Request) bool { return false }
func (denyAll) RetryAfter() int          { return 30 }

func newServer(t *testing.T, stub *platformStub, ext *httptest.Server, d Deps) *http.ServeMux {
	t.Helper()
	d.Routes = stub
	if ext != nil {
		d.External = ext.Client()
	}
	mux := http.NewServeMux()
	New(d).Mount(mux)
	return mux
}

func get(t *testing.T, mux http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://platform.example"+target, http.NoBody)
	req.Header.Set("Cookie", "session=reader")
	req.Header.Set("Authorization", "Bearer reader-key")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestStandalone_PortalRouteAnswersTheFile: the asset is read through its
// content route with the caller's request, its declared logo and the served
// runtime are inlined, and the file is named for the asset.
func TestStandalone_PortalRouteAnswersTheFile(t *testing.T) {
	var extHeaders http.Header
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "EXT")
	}))
	defer ext.Close()
	stub := &platformStub{docs: map[string]stubDoc{"a1": {
		ct: "text/html; charset=utf-8", name: "Quarterly deck.html",
		body: `<html><body><img src="http://platform.example/portal/refs/a1/tok-logo"><img src="` + ext.URL + `/pic.png">` +
			`<img src="https://unreachable.invalid/x.png"><script src="/portal/vendor/reveal/reveal.js"></script></body></html>`,
	}}}
	mux := newServer(t, stub, ext, Deps{})
	rec := get(t, mux, "/api/v1/portal/assets/a1/standalone")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), `filename="Quarterly deck.html"`)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"), rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	body := rec.Body.String()
	assert.Contains(t, body, "data:image/png;base64,UE5H")
	assert.Contains(t, body, "data:image/png;base64,RVhU")
	assert.Contains(t, body, "var Reveal={};")
	assert.NotContains(t, body, "reader-key")
	assert.NotContains(t, body, "session=reader")

	for _, c := range stub.cookies {
		assert.Empty(t, c, "an own read carries no cookie of the caller's")
	}
	for _, a := range stub.auths {
		assert.Empty(t, a, "an own read carries no authorization of the caller's")
	}
	assert.Empty(t, extHeaders.Get("Cookie"))
	assert.Empty(t, extHeaders.Get("Authorization"))

	raw, err := url.QueryUnescape(rec.Header().Get(NoticesHeader))
	require.NoError(t, err)
	var notices []standalone.Notice
	require.NoError(t, json.Unmarshal([]byte(raw), &notices))
	require.Len(t, notices, 1)
	assert.Equal(t, "https://unreachable.invalid/x.png", notices[0].URL)
}

// TestStandalone_ContentRouteRefusalIsRelayed with its status and headers.
func TestStandalone_ContentRouteRefusalIsRelayed(t *testing.T) {
	stub := &platformStub{docs: map[string]stubDoc{"busy": {status: http.StatusServiceUnavailable}}}
	mux := newServer(t, stub, nil, Deps{})
	rec := get(t, mux, "/api/v1/portal/assets/busy/standalone")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "7", rec.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusNotFound, get(t, mux, "/api/v1/portal/assets/missing/standalone").Code)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/assets/busy/standalone", http.NoBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the content route's own check decides")
}

// TestStandalone_PlatformDataIsAConflict with the reasons, and other types are
// not exported.
func TestStandalone_PlatformDataIsAConflict(t *testing.T) {
	stub := &platformStub{docs: map[string]stubDoc{
		"form": {ct: "text/html", name: "f.html", body: `<div>mcp:form:signup</div>`},
		"csv":  {ct: "text/csv", name: "d.csv", body: "a,b"},
		"bad":  {ct: "text/jsx", name: "b.jsx", body: `import x from "left-pad"; export default function A(){return <p/>}`},
	}}
	mux := newServer(t, stub, nil, Deps{})
	rec := get(t, mux, "/api/v1/portal/assets/form/standalone")
	require.Equal(t, http.StatusConflict, rec.Code)
	var refusal Refusal
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
	assert.Equal(t, "platform_data", refusal.Error)
	assert.NotEmpty(t, refusal.Reasons)

	assert.Equal(t, http.StatusUnsupportedMediaType, get(t, mux, "/api/v1/portal/assets/csv/standalone").Code)

	rec = get(t, mux, "/api/v1/portal/assets/bad/standalone")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
	assert.Equal(t, "build_failed", refusal.Error)
}

// TestStandalone_PublicRoutesAreLimitedAndPortalRoutesGated: a public route
// past its limit is refused with Retry-After, and a portal route is held to the
// caller's download setting.
func TestStandalone_PublicRoutesAreLimitedAndPortalRoutesGated(t *testing.T) {
	stub := &platformStub{docs: map[string]stubDoc{
		"shared": {ct: "text/html", name: "s.html", body: "<p>hi</p>"},
		"a1":     {ct: "text/html", name: "a.html", body: "<p>hi</p>"},
	}}
	mux := newServer(t, stub, nil, Deps{Limiter: denyAll{}, Downloads: func(*http.Request) bool { return false }})
	rec := get(t, mux, "/portal/view/tok123/standalone")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusForbidden, get(t, mux, "/api/v1/portal/assets/a1/standalone").Code)

	open := newServer(t, stub, nil, Deps{})
	rec = get(t, open, "/portal/view/tok123/items/a1/standalone")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestStandalone_ExportsRunWithinTheConcurrency: with every slot taken, an
// export waits for one, and a caller who gives up is answered nothing.
func TestStandalone_ExportsRunWithinTheConcurrency(t *testing.T) {
	stub := &platformStub{docs: map[string]stubDoc{"a1": {ct: "text/html", name: "a.html", body: "<p>hi</p>"}}}
	h := New(Deps{Routes: stub, Concurrency: 1})
	mux := http.NewServeMux()
	h.Mount(mux)
	h.slots <- struct{}{} // the one slot is held
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://platform.example/api/v1/portal/assets/a1/standalone", http.NoBody)
	req.Header.Set("Cookie", "session=reader")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Empty(t, rec.Body.String(), "no export ran past the held slot")
	<-h.slots
	assert.Equal(t, http.StatusOK, get(t, mux, "/api/v1/portal/assets/a1/standalone").Code)
	assert.Equal(t, DefaultConcurrency, cap(New(Deps{}).slots))
}

func TestOwnFetcher_ReadsOnlyTheReferenceAndVendorRoutes(t *testing.T) {
	f := ownFetcher{routes: &platformStub{}}
	_, _, err := f.Fetch(context.Background(), &url.URL{Path: "/api/v1/admin/users"}, 100)
	require.Error(t, err)
	_, _, err = f.Fetch(context.Background(), &url.URL{Path: "/portal/vendor/../../api/v1/admin"}, 100)
	require.Error(t, err)
	_, _, err = f.Fetch(context.Background(), &url.URL{Path: "/portal/vendor/reveal/reveal.js"}, 3)
	require.ErrorIs(t, err, standalone.ErrTooLarge)
	_, _, err = f.Fetch(context.Background(), &url.URL{Path: "/portal/refs/a1/missing"}, 100)
	require.Error(t, err)
}

func TestExternalFetcher_BoundsAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", 10))
	}))
	defer srv.Close()
	f := externalFetcher{client: srv.Client()}
	u, _ := url.Parse(srv.URL + "/big")
	_, _, err := f.Fetch(context.Background(), u, 5)
	require.ErrorIs(t, err, standalone.ErrTooLarge)
	body, _, err := f.Fetch(context.Background(), u, 10)
	require.NoError(t, err)
	assert.Len(t, body, 10)
	u, _ = url.Parse(srv.URL + "/gone")
	_, _, err = f.Fetch(context.Background(), u, 10)
	require.ErrorContains(t, err, "404")
	_, _, err = externalFetcher{}.Fetch(context.Background(), u, 10)
	require.Error(t, err)
}

func TestNoticesHeader_IsBoundedASCII(t *testing.T) {
	assert.Empty(t, noticesHeader(nil))
	many := make([]standalone.Notice, maxNotices+5)
	for i := range many {
		many[i] = standalone.Notice{URL: "https://example.org/é\r\nX-Injected: 1", Reason: "r"}
	}
	h := noticesHeader(many)
	assert.NotContains(t, h, "\r")
	assert.NotContains(t, h, "\n")
	for _, c := range h {
		assert.Less(t, c, rune(128))
	}
	raw, err := url.QueryUnescape(h)
	require.NoError(t, err)
	var got []standalone.Notice
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	assert.Len(t, got, maxNotices)
}

func TestDocument_FileName(t *testing.T) {
	assert.Equal(t, "Quarterly deck.html", document{disposition: `attachment; filename="Quarterly deck.jsx"`}.fileName())
	assert.Equal(t, "document.html", document{}.fileName())
}

// TestPlatformDeps_BuildsTheGuardedClientAndTheLimiter, and refuses trusted
// proxies it cannot read.
func TestPlatformDeps_BuildsTheGuardedClientAndTheLimiter(t *testing.T) {
	d, err := platformDeps(nil, "https://portal.example.com")
	require.NoError(t, err)
	assert.NotNil(t, d.External)
	assert.NotNil(t, d.Limiter)
	assert.Equal(t, "https://portal.example.com", d.PublicBaseURL)
	_, err = platformDeps([]string{"not a cidr"}, "")
	require.Error(t, err)
}

// TestRefuseBuild_TimeoutAndFailure: a build past its deadline is a 503 (a CDN
// replaces a 504's body), anything unexpected a 500 that repeats nothing.
func TestRefuseBuild_TimeoutAndFailure(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	refuseBuild(rec, req, context.DeadlineExceeded)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = httptest.NewRecorder()
	refuseBuild(rec, req, errors.New("secret detail"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret detail")

	assert.Equal(t, http.StatusOK, newRecorder(1).code())
}

// TestReferenceOrigin_FollowsTheConfiguredBase as the content route does.
func TestReferenceOrigin_FollowsTheConfiguredBase(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://internal.example:8080/x", http.NoBody)
	assert.Equal(t, "portal.example.com", referenceOrigin(req, "https://portal.example.com").Host)
	assert.Equal(t, "internal.example:8080", referenceOrigin(req, "").Host)
	assert.Nil(t, referenceOrigin(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody), "::bad"))
}
