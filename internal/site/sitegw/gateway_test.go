package sitegw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const (
	testPagesDomain = "pages.example.com"
	testPortal      = "portal.example.com"
	testSiteHost    = "www.client.example"
	testOtherHost   = "elsewhere.example"
	testPreviewHost = "dev--marketing.pages.example.com"
	testBucket      = "site-bucket"
	testSiteID      = "site_1"

	headerETag = "ETag"
	headerLoc  = "Location"
	nextBody   = "from next"
	ctHTML     = "text/html; charset=utf-8"
	ctCSS      = "text/css"
)

// memStore is a Store over maps, counting the reads a test asserts on.
type memStore struct {
	mu       sync.Mutex
	hosts    map[string]*site.Route
	previews map[string]*site.Route
	files    map[string][]site.File
	verified []string

	routeHostErr, previewErr, filesErr, verifiedErr error

	routeHostReads, previewReads, fileReads, verifiedReads int
	previewAsked                                           []string
}

func newMemStore() *memStore {
	return &memStore{hosts: map[string]*site.Route{}, previews: map[string]*site.Route{}, files: map[string][]site.File{}}
}

func (s *memStore) RouteHost(_ context.Context, hostname string) (*site.Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routeHostReads++
	if s.routeHostErr != nil {
		return nil, s.routeHostErr
	}
	if r, ok := s.hosts[hostname]; ok {
		return r, nil
	}
	return nil, site.ErrNoDomain
}

func (s *memStore) RoutePreview(_ context.Context, siteName, environment string) (*site.Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previewReads++
	s.previewAsked = append(s.previewAsked, siteName+"/"+environment)
	if s.previewErr != nil {
		return nil, s.previewErr
	}
	if r, ok := s.previews[siteName+"/"+environment]; ok {
		return r, nil
	}
	return nil, site.ErrNoDomain
}

func (s *memStore) Files(_ context.Context, releaseID string) ([]site.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileReads++
	if s.filesErr != nil {
		return nil, s.filesErr
	}
	return s.files[releaseID], nil
}

func (s *memStore) VerifiedHosts(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifiedReads++
	if s.verifiedErr != nil {
		return nil, s.verifiedErr
	}
	return s.verified, nil
}

// memObjects is an Objects over a map, recording what was fetched.
type memObjects struct {
	mu     sync.Mutex
	bodies map[string][]byte
	gets   []string
	ranges []int64
	getErr error
	// sized answers a range for any object as a function of the offset, for
	// a file too large to hold.
	sized bool
}

func newMemObjects() *memObjects { return &memObjects{bodies: map[string][]byte{}} }

func objectName(bucket, key string) string { return bucket + "/" + key }

func (o *memObjects) GetObject(_ context.Context, bucket, key string) (data []byte, contentType string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gets = append(o.gets, objectName(bucket, key))
	if o.getErr != nil {
		return nil, "", o.getErr
	}
	body, ok := o.bodies[objectName(bucket, key)]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return body, "", nil
}

func (o *memObjects) GetObjectRange(_ context.Context, bucket, key string, offset, length int64) (data []byte, size int64, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ranges = append(o.ranges, offset)
	if o.getErr != nil {
		return nil, 0, o.getErr
	}
	if o.sized {
		out := make([]byte, length)
		for i := range out {
			out[i] = patternByte(offset + int64(i))
		}
		return out, length, nil
	}
	body := o.bodies[objectName(bucket, key)]
	if offset >= int64(len(body)) {
		return nil, int64(len(body)), nil
	}
	return body[offset:min(offset+length, int64(len(body)))], int64(len(body)), nil
}

func patternByte(off int64) byte { return byte('a' + off%26) }

// harness drives a Gateway through Wrap, as the listener does.
type harness struct {
	store    *memStore
	objects  *memObjects
	gateway  *Gateway
	handler  http.Handler
	nextHits int
}

func newHarness(deps Deps) *harness {
	h := &harness{store: newMemStore(), objects: newMemObjects()}
	deps.Store, deps.Objects, deps.Bucket = h.store, h.objects, testBucket
	if deps.Config.Cache.Dir == "" {
		deps.Config.Cache.Dir = isolatedCacheDir()
	}
	if deps.Config.PagesDomain == "" {
		deps.Config.PagesDomain = testPagesDomain
	}
	if deps.PortalHost == "" {
		deps.PortalHost = testPortal
	}
	h.gateway = New(deps)
	h.handler = h.gateway.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.nextHits++
		_, _ = io.WriteString(w, nextBody)
	}))
	return h
}

func (h *harness) do(method, host, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+target, nil)
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) get(host, target string) *httptest.ResponseRecorder {
	return h.do(http.MethodGet, host, target, nil)
}

// testFile is one file of a release, with the compressed forms it was stored
// in, each named by its content.
type testFile struct {
	path, body, contentType string
	gz, br                  string
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// addRelease stores a release's files and records its manifest.
func (h *harness) addRelease(number int, redirects []site.Redirect, files ...testFile) *site.Release {
	rel := &site.Release{ID: "rel_" + strconv.Itoa(number), SiteID: testSiteID, Number: number, Redirects: redirects}
	put := func(enc, filePath, body string) {
		h.objects.bodies[objectName(testBucket, sitesvc.ObjectKey(testSiteID, rel.ID, enc, filePath))] = []byte(body)
	}
	manifestFiles := make([]site.File, 0, len(files))
	for _, f := range files {
		entry := site.File{Path: f.path, SHA256: digestOf(f.body), ContentType: f.contentType, Size: int64(len(f.body))}
		put("", f.path, f.body)
		if f.gz != "" {
			entry.GzipSize = int64(len(f.gz))
			put("gz", f.path, f.gz)
		}
		if f.br != "" {
			entry.BrotliSize = int64(len(f.br))
			put("br", f.path, f.br)
		}
		manifestFiles = append(manifestFiles, entry)
	}
	h.store.files[rel.ID] = manifestFiles
	return rel
}

// mapHost makes testSiteHost a verified hostname serving rel.
func (h *harness) mapHost(rel *site.Release) {
	h.store.hosts[testSiteHost] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod", Release: rel}
	h.store.verified = append(h.store.verified, testSiteHost)
	h.gateway.Invalidate(testSiteHost)
}

// repoint moves host to rel in the store, as a promote does: a new row read,
// the route a replica holds left as it was.
func (h *harness) repoint(host string, rel *site.Release) {
	h.store.hosts[host] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod", Release: rel}
}

func TestWrap_RoutesByHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		status   int
		body     string
		nextHits int
	}{
		{name: "a host neither under the pages domain nor verified passes through", host: testOtherHost, status: http.StatusOK, body: nextBody, nextHits: 1},
		{name: "the portal's own host passes through", host: testPortal, status: http.StatusOK, body: nextBody, nextHits: 1},
		{name: "a verified host serves its release", host: testSiteHost, status: http.StatusOK, body: "prod home"},
		{name: "a verified host with a port and upper case is the same host", host: "WWW.client.example:8443", status: http.StatusOK, body: "prod home"},
		{name: "a label holding -- serves that environment", host: testPreviewHost, status: http.StatusOK, body: "dev home"},
		{name: "an unknown environment is 404 and never reaches next", host: "nope--marketing.pages.example.com", status: http.StatusNotFound, body: "404 page not found\n"},
		{name: "the bare pages domain is 404", host: testPagesDomain, status: http.StatusNotFound, body: "404 page not found\n"},
		{name: "a name two labels under the pages domain is 404", host: "a.b.pages.example.com", status: http.StatusNotFound, body: "404 page not found\n"},
		{name: "a label with no -- is an application, none here", host: "shop.pages.example.com", status: http.StatusNotFound, body: "404 page not found\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "prod home", contentType: ctHTML}))
			dev := h.addRelease(2, nil, testFile{path: indexFile, body: "dev home", contentType: ctHTML})
			h.store.previews["marketing/dev"] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "dev", Release: dev}

			rec := h.get(tt.host, "/")
			assert.Equal(t, tt.status, rec.Code)
			assert.Equal(t, tt.body, rec.Body.String())
			assert.Equal(t, tt.nextHits, h.nextHits)
		})
	}
}

func TestWrap_ThePortalsOwnHostCostsNoStoreRead(t *testing.T) {
	h := newHarness(Deps{})
	h.get(testPortal, "/")
	assert.Equal(t, 0, h.store.verifiedReads)
	assert.Equal(t, 0, h.store.routeHostReads)
	assert.Equal(t, 0, h.store.previewReads)
}

func TestWrap_ALabelHoldingTheSeparatorNamesEnvironmentThenSite(t *testing.T) {
	h := newHarness(Deps{})
	h.get("stage--docs.pages.example.com", "/")
	assert.Equal(t, []string{"docs/stage"}, h.store.previewAsked)
	assert.Zero(t, h.store.routeHostReads, "a preview is not a verified domain")
}

func TestWrap_TheBarePagesDomainReadsNoRoute(t *testing.T) {
	h := newHarness(Deps{})
	h.get(testPagesDomain, "/")
	assert.Zero(t, h.store.previewReads)
	assert.Zero(t, h.store.routeHostReads)
}

func TestWrap_ARouteThatCannotBeReadIs503(t *testing.T) {
	h := newHarness(Deps{})
	h.store.previewErr = errors.New("database is down")
	rec := h.get(testPreviewHost, "/")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "database is down")
	assert.Zero(t, h.nextHits)
}

func TestWrap_AnErrNotFoundFromTheStoreIs404(t *testing.T) {
	h := newHarness(Deps{})
	h.store.previewErr = site.ErrNotFound
	assert.Equal(t, http.StatusNotFound, h.get(testPreviewHost, "/").Code)
}

func TestServes_KeepsTheLastListWhenAReadFails(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	require.Equal(t, http.StatusOK, h.get(testSiteHost, "/").Code)

	h.store.verifiedErr = errors.New("database is down")
	h.gateway.Invalidate(testSiteHost)
	assert.Equal(t, http.StatusOK, h.get(testSiteHost, "/").Code, "the hostnames read before still serve")
}

func TestServes_ReadsTheHostnamesOncePerWindow(t *testing.T) {
	h := newHarness(Deps{})
	setRead := func(at time.Time) {
		h.gateway.mu.Lock()
		defer h.gateway.mu.Unlock()
		h.gateway.hostsAt = at
	}
	h.get(testOtherHost, "/")
	setRead(time.Now().Add(time.Hour))
	h.get(testOtherHost, "/")
	assert.Equal(t, 1, h.store.verifiedReads, "a second request inside the window answers from memory")
	setRead(time.Now().Add(-2 * hostsTTL))
	h.get(testOtherHost, "/")
	assert.Equal(t, 2, h.store.verifiedReads, "a request past the window reads them again")
}

func baseRelease(h *harness) *site.Release {
	return h.addRelease(1, []site.Redirect{
		{From: "/old", To: "/new", Status: http.StatusMovedPermanently},
		{From: "/moved/", To: "/moved-here", Status: http.StatusFound},
		{From: "/blog/*", To: "/posts/:splat", Status: http.StatusFound},
		{From: "/q", To: "/t?keep=1", Status: http.StatusTemporaryRedirect},
		{From: "/out", To: "https://elsewhere.example/x", Status: http.StatusPermanentRedirect},
	},
		testFile{path: indexFile, body: "home", contentType: ctHTML},
		testFile{path: "about.html", body: "about", contentType: ctHTML},
		testFile{path: "docs/index.html", body: "docs index", contentType: ctHTML},
		testFile{path: "style.css", body: "body{}", contentType: ctCSS},
		testFile{path: notFound, body: "custom missing", contentType: ctHTML},
	)
}

func TestServeSite_ResolvesAPathToAFileOrARule(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		target   string
		status   int
		body     string
		location string
	}{
		{name: "the index at the root", target: "/", status: http.StatusOK, body: "home"},
		{name: "a file by its exact name", target: "/style.css", status: http.StatusOK, body: "body{}"},
		{name: "name.html for an extensionless path", target: "/about", status: http.StatusOK, body: "about"},
		{name: "a directory with no slash redirects", target: "/docs", status: http.StatusMovedPermanently, location: "/docs/"},
		{name: "a directory with its slash serves its index", target: "/docs/", status: http.StatusOK, body: "docs index"},
		{name: "an exact rule", target: "/old", status: http.StatusMovedPermanently, location: "/new"},
		{name: "a rule written with a trailing slash matches without one", target: "/moved", status: http.StatusFound, location: "/moved-here"},
		{name: "a splat rule carries what it matched", target: "/blog/2024/launch", status: http.StatusFound, location: "/posts/2024/launch"},
		{name: "a splat rule matches its own prefix", target: "/blog", status: http.StatusFound, location: "/posts/"},
		{name: "a rule carries the query", target: "/old?a=1&b=2", status: http.StatusMovedPermanently, location: "/new?a=1&b=2"},
		{name: "a rule's own query is not added to", target: "/q?a=1", status: http.StatusTemporaryRedirect, location: "/t?keep=1"},
		{name: "a rule may go to another origin", target: "/out", status: http.StatusPermanentRedirect, location: "https://elsewhere.example/x"},
		{name: "a missing path gets the release's 404 page", target: "/missing", status: http.StatusNotFound, body: "custom missing"},
		{name: "a HEAD of a missing path has the status and no body", method: http.MethodHead, target: "/missing", status: http.StatusNotFound},
		{name: "a HEAD of a file has no body", method: http.MethodHead, target: "/", status: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(baseRelease(h))
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			rec := h.do(method, testSiteHost, tt.target, nil)
			assert.Equal(t, tt.status, rec.Code)
			if tt.location != "" {
				assert.Equal(t, tt.location, rec.Header().Get(headerLoc))
				return
			}
			assert.Equal(t, tt.body, rec.Body.String())
			assert.Equal(t, nosniff, rec.Header().Get(hdrNosniff))
		})
	}
}

func TestServeSite_ACustomNotFoundPageIsNeverCachedLong(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	rec := h.get(testSiteHost, "/missing")
	assert.Equal(t, "no-cache", rec.Header().Get(hdrCache))
	assert.Empty(t, rec.Header().Get(headerETag))
}

func TestServeSite_WithNoNotFoundPageTheFallbackIsPlainText(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	rec := h.get(testSiteHost, "/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not found\n", rec.Body.String())
	assert.Equal(t, plainText, rec.Header().Get(hdrType))
}

func TestServeSite_AnswersOnlyGetAndHead(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(baseRelease(h))
			rec := h.do(method, testSiteHost, "/", nil)
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
		})
	}
}

func TestServeSite_AnEnvironmentWithNoReleaseIs404(t *testing.T) {
	h := newHarness(Deps{})
	h.store.previews["marketing/dev"] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "dev"}
	rec := h.get(testPreviewHost, "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "no release yet")
}

func TestServeSite_AManifestThatCannotBeReadIs503(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	h.store.filesErr = errors.New("database is down")
	rec := h.get(testSiteHost, "/")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "database is down")
}

func TestServeSite_AManifestIsReadOncePerRelease(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	h.get(testSiteHost, "/")
	h.get(testSiteHost, "/about")
	assert.Equal(t, 1, h.store.fileReads)
}

func TestServeSite_AFileThatCannotBeReadSaysNoEncoding(t *testing.T) {
	tests := []struct {
		name, target string
	}{
		{name: "a 200 file", target: "/"},
		{name: "the 404 page", target: "/missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(h.addRelease(1, nil,
				testFile{path: indexFile, body: "home", contentType: ctHTML, gz: "g"},
				testFile{path: notFound, body: "gone", contentType: ctHTML, gz: "g2"},
			))
			h.objects.getErr = errors.New("bucket key site/secret-path is gone")
			rec := h.do(http.MethodGet, testSiteHost, tt.target, http.Header{headerAcceptEnc: {"gzip"}})
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assert.Empty(t, rec.Header().Get(headerContentEnc))
			assert.NotContains(t, rec.Body.String(), "secret-path")
		})
	}
}

func TestContentNegotiation(t *testing.T) {
	const identity, gz, br = "identity body", "gzip body", "brotli body"
	both := testFile{path: "app.js", body: identity, contentType: "text/javascript", gz: gz, br: br}
	gzOnly := testFile{path: "app.js", body: identity, contentType: "text/javascript", gz: gz}
	plain := testFile{path: "app.js", body: identity, contentType: "text/javascript"}

	tests := []struct {
		name     string
		file     testFile
		accept   string
		encoding string
		body     string
	}{
		{name: "br is preferred over gzip", file: both, accept: "gzip, br", encoding: "br", body: br},
		{name: "br is preferred whatever the weights", file: both, accept: "gzip;q=1.0, br;q=0.1", encoding: "br", body: br},
		{name: "gzip when br is not accepted", file: both, accept: "gzip", encoding: "gzip", body: gz},
		{name: "q=0 excludes br", file: both, accept: "br;q=0, gzip", encoding: "gzip", body: gz},
		{name: "q=0.0 excludes gzip", file: both, accept: "gzip; q=0.0", body: identity},
		{name: "upper case names are read", file: both, accept: "GZIP", encoding: "gzip", body: gz},
		{name: "nothing accepted is the file itself", file: both, accept: "", body: identity},
		{name: "br accepted with no br stored falls to gzip", file: gzOnly, accept: "br, gzip", encoding: "gzip", body: gz},
		{name: "br alone with no br stored is the file itself", file: gzOnly, accept: "br", body: identity},
		{name: "a file with no variants is itself", file: plain, accept: "br, gzip", body: identity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(h.addRelease(1, nil, tt.file))
			rec := h.do(http.MethodGet, testSiteHost, "/app.js", http.Header{headerAcceptEnc: {tt.accept}})
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.encoding, rec.Header().Get(headerContentEnc))
			assert.Equal(t, tt.body, rec.Body.String())
			assert.Equal(t, headerAcceptEnc, rec.Header().Get(hdrVary))
		})
	}
}

func TestContentNegotiation_EachFormHasItsOwnValidator(t *testing.T) {
	h := newHarness(Deps{})
	file := testFile{path: "app.js", body: "identity body", contentType: "text/javascript", gz: "gzip body", br: "brotli body"}
	h.mapHost(h.addRelease(1, nil, file))

	etags := map[string]string{}
	for _, accept := range []string{"", "gzip", "br"} {
		rec := h.do(http.MethodGet, testSiteHost, "/app.js", http.Header{headerAcceptEnc: {accept}})
		etag := rec.Header().Get(headerETag)
		require.NotEmpty(t, etag)
		etags[accept] = etag

		notModified := h.do(http.MethodGet, testSiteHost, "/app.js", http.Header{headerAcceptEnc: {accept}, "If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, notModified.Code, "accept %q", accept)
		assert.Empty(t, notModified.Body.String())
	}
	assert.Len(t, map[string]bool{etags[""]: true, etags["gzip"]: true, etags["br"]: true}, 3, "a validator per stored form")

	stale := h.do(http.MethodGet, testSiteHost, "/app.js", http.Header{headerAcceptEnc: {"br"}, "If-None-Match": {etags["gzip"]}})
	assert.Equal(t, http.StatusOK, stale.Code, "another form's validator does not match")
}

func TestServeFile_ARangeOfAnIdentityFileIsPartial(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(h.addRelease(1, nil, testFile{path: "movie.bin", body: "0123456789", contentType: "application/octet-stream"}))
	rec := h.do(http.MethodGet, testSiteHost, "/movie.bin", http.Header{"Range": {"bytes=2-5"}})
	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, "2345", rec.Body.String())
	assert.Equal(t, "bytes 2-5/10", rec.Header().Get("Content-Range"))
}

func TestServeFile_ALargeFileIsReadByRanges(t *testing.T) {
	h := newHarness(Deps{})
	rel := &site.Release{ID: "rel_big", SiteID: testSiteID, Number: 9}
	size := maxDiskItem + 1
	h.store.files[rel.ID] = []site.File{{Path: "big.bin", SHA256: "abc", ContentType: "application/octet-stream", Size: size}}
	h.objects.sized = true
	h.mapHost(rel)

	rec := h.do(http.MethodGet, testSiteHost, "/big.bin", http.Header{"Range": {"bytes=1000-1003"}})
	require.Equal(t, http.StatusPartialContent, rec.Code)
	want := []byte{patternByte(1000), patternByte(1001), patternByte(1002), patternByte(1003)}
	assert.Equal(t, want, rec.Body.Bytes())
	assert.Equal(t, []int64{1000}, h.objects.ranges, "only the part played is read")
	assert.Empty(t, h.objects.gets, "the whole object is never fetched")
}

func TestServeFile_CacheControlFollowsTheFilesName(t *testing.T) {
	tests := []struct {
		path, want string
	}{
		{path: "site.3f2a9c1b.css", want: cacheImmutable},
		{path: "css/site.3f2a9c1b.css", want: cacheImmutable},
		{path: "index-D1a2B3c4.js", want: cacheImmutable},
		{path: "vendor.abcdef12.js", want: cacheImmutable},
		{path: "site-navigation.css", want: cacheRevalidate},
		{path: "app.min.js", want: cacheRevalidate},
		{path: "logo.png", want: cacheRevalidate},
		{path: "a-b1.css", want: cacheRevalidate},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			h := newHarness(Deps{})
			h.mapHost(h.addRelease(1, nil, testFile{path: tt.path, body: "x", contentType: ctCSS}))
			rec := h.get(testSiteHost, "/"+tt.path)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.want, rec.Header().Get(hdrCache))
		})
	}
}

// holdRoute sets how long this replica holds host's route, standing in for
// the clock.
func holdRoute(g *Gateway, host string, until time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.routes[host]
	e.until = until
	g.routes[host] = e
}

func promoteFixture(t *testing.T) (*harness, *site.Release) {
	t.Helper()
	h := newHarness(Deps{})
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "release one", contentType: ctHTML}))
	require.Equal(t, "release one", h.get(testSiteHost, "/").Body.String())
	next := h.addRelease(2, nil, testFile{path: indexFile, body: "release two", contentType: ctHTML})
	return h, next
}

func TestPromote_AMoveShowsAtOnceAfterInvalidate(t *testing.T) {
	h, next := promoteFixture(t)
	holdRoute(h.gateway, testSiteHost, time.Now().Add(time.Hour))
	h.repoint(testSiteHost, next)

	assert.Equal(t, "release one", h.get(testSiteHost, "/").Body.String(), "held for the route's moment without a notice")
	reads := h.store.routeHostReads

	h.gateway.Invalidate(testSiteHost)
	assert.Equal(t, "release two", h.get(testSiteHost, "/").Body.String())
	assert.Equal(t, reads+1, h.store.routeHostReads)
}

func TestPromote_AMoveOnAnotherReplicaShowsWhenTheRouteExpires(t *testing.T) {
	h, next := promoteFixture(t)
	h.repoint(testSiteHost, next)
	holdRoute(h.gateway, testSiteHost, time.Now().Add(-time.Millisecond))
	assert.Equal(t, "release two", h.get(testSiteHost, "/").Body.String())
}

func TestPromote_TheHeldRouteLastsNoLongerThanTheTTL(t *testing.T) {
	h, _ := promoteFixture(t)
	h.gateway.mu.Lock()
	until := h.gateway.routes[testSiteHost].until
	h.gateway.mu.Unlock()
	assert.LessOrEqual(t, time.Until(until), routeTTL)
}

func TestRoute_RemembersAHostWithNoRouteForAMoment(t *testing.T) {
	h := newHarness(Deps{})
	h.get(testPreviewHost, "/")
	holdRoute(h.gateway, testPreviewHost, time.Now().Add(time.Hour))
	assert.Equal(t, http.StatusNotFound, h.get(testPreviewHost, "/").Code)
	assert.Equal(t, 1, h.store.previewReads)
}

func TestRoute_DropsTheTableAtItsBound(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	h.gateway.mu.Lock()
	for i := range maxRoutes {
		h.gateway.routes["host"+strconv.Itoa(i)] = routeEntry{}
	}
	h.gateway.mu.Unlock()
	h.get(testSiteHost, "/")
	h.gateway.mu.Lock()
	defer h.gateway.mu.Unlock()
	assert.Len(t, h.gateway.routes, 1)
}

func TestManifest_DropsTheTableAtItsBound(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	h.gateway.mu.Lock()
	for i := range maxRoutes {
		h.gateway.manifest["rel"+strconv.Itoa(i)] = &manifest{}
	}
	h.gateway.mu.Unlock()
	h.get(testSiteHost, "/")
	h.gateway.mu.Lock()
	defer h.gateway.mu.Unlock()
	assert.Len(t, h.gateway.manifest, 1)
}

func TestWrap_AnApplicationLabelWithNoPagesIs404(t *testing.T) {
	h := newHarness(Deps{})
	rec := h.get("shop.pages.example.com", "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.nextHits)
}

func TestExists(t *testing.T) {
	apps := &memApps{byName: appsNamed("shop")}
	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "a verified hostname", host: testSiteHost, want: true},
		{name: "an environment that exists", host: testPreviewHost, want: true},
		{name: "an application that exists", host: "shop.pages.example.com", want: true},
		{name: "an application that does not", host: "nope.pages.example.com"},
		{name: "an environment that does not", host: "nope--marketing.pages.example.com"},
		{name: "the bare pages domain", host: testPagesDomain},
		{name: "a hostname nobody verified", host: testOtherHost},
		{name: "a name two labels down", host: "a.b.pages.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(Deps{Pages: &Pages{Apps: apps, Assets: &memAssets{}}})
			h.mapHost(baseRelease(h))
			h.store.previews["marketing/dev"] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "dev"}
			assert.Equal(t, tt.want, h.gateway.Exists(context.Background(), tt.host))
		})
	}
}

func TestExists_AnApplicationHostnameWithNoPagesIsRefused(t *testing.T) {
	h := newHarness(Deps{})
	assert.False(t, h.gateway.Exists(context.Background(), "shop.pages.example.com"))
}

func TestGateway_ServesABodyFromMemoryOnTheSecondRead(t *testing.T) {
	h := newHarness(Deps{})
	h.mapHost(baseRelease(h))
	first := h.get(testSiteHost, "/")
	second := h.get(testSiteHost, "/")
	assert.Equal(t, first.Body.String(), second.Body.String())
	assert.Len(t, h.objects.gets, 1)
}

func TestGateway_ASecondProcessReadsTheDiskCopy(t *testing.T) {
	dir := t.TempDir()
	cfg := siteconfig.Config{PagesDomain: testPagesDomain, Cache: siteconfig.CacheConfig{Dir: dir}}

	first := newHarness(Deps{Config: cfg})
	first.mapHost(baseRelease(first))
	require.Equal(t, "home", first.get(testSiteHost, "/").Body.String())

	second := newHarness(Deps{Config: cfg})
	second.mapHost(baseRelease(second))
	assert.Equal(t, "home", second.get(testSiteHost, "/").Body.String())
	assert.Empty(t, second.objects.gets, "the disk copy answered, so object storage was not asked")
}

func TestOpen_ChoosesWhereAFileIsReadFrom(t *testing.T) {
	const cacheKey, objectKey = "digest/", "sites/s/releases/1/files/a"
	dir := t.TempDir()
	cfg := siteconfig.Config{PagesDomain: testPagesDomain, Cache: siteconfig.CacheConfig{Dir: dir}}

	t.Run("a file with a disk copy past the memory bound is the file", func(t *testing.T) {
		h := newHarness(Deps{Config: cfg})
		h.gateway.cache.writeDisk(cacheKey, []byte("on disk"))
		content, closer, err := h.gateway.open(context.Background(), cacheKey, objectKey, maxMemoryItem+1)
		require.NoError(t, err)
		defer closer()
		body, err := io.ReadAll(content)
		require.NoError(t, err)
		assert.Equal(t, "on disk", string(body))
		assert.Empty(t, h.objects.gets)
	})

	t.Run("a file past every bound is read by ranges", func(t *testing.T) {
		h := newHarness(Deps{Config: cfg})
		content, closer, err := h.gateway.open(context.Background(), cacheKey, objectKey, maxDiskItem+1)
		require.NoError(t, err)
		defer closer()
		_, isSeeker := content.(*objectSeeker)
		assert.True(t, isSeeker)
	})

	t.Run("a small file that cannot be read is an error", func(t *testing.T) {
		h := newHarness(Deps{})
		h.objects.getErr = errors.New("no such object")
		_, closer, err := h.gateway.open(context.Background(), "other-key", objectKey, 10)
		defer closer()
		require.ErrorContains(t, err, "reading object")
	})
}

func TestEncoding_AnUnparsableWeightIsNotAnExclusion(t *testing.T) {
	f := site.File{GzipSize: 1}
	assert.Equal(t, "gz", encoding("gzip;q=abc", f))
	assert.Equal(t, "gz", encoding("gzip;foo=bar", f))
}

func TestMatchRedirect_TheFirstRuleWins(t *testing.T) {
	rules := []site.Redirect{{From: "/a", To: "/one", Status: 301}, {From: "/a", To: "/two", Status: 302}}
	to, status, ok := matchRedirect(rules, "/a")
	require.True(t, ok)
	assert.Equal(t, "/one", to)
	assert.Equal(t, 301, status)
}

func TestMatchRedirect_ASplatDoesNotMatchASiblingPrefix(t *testing.T) {
	rules := []site.Redirect{{From: "/blog/*", To: "/x/:splat", Status: 301}}
	_, _, ok := matchRedirect(rules, "/blogroll")
	assert.False(t, ok)
}

func TestResolve(t *testing.T) {
	m := &manifest{files: map[string]site.File{
		"a":               {Path: "a"},
		"a.html":          {Path: "a.html"},
		"docs/index.html": {Path: "docs/index.html"},
	}}
	tests := []struct {
		name string
		path string
		dir  bool
		want string
	}{
		{name: "the exact file wins over name.html", path: "/a", want: "a"},
		{name: "a directory's index when the path ended in a slash", path: "/docs", dir: true, want: "docs/index.html"},
		{name: "no index without the slash", path: "/docs"},
		{name: "nothing by that name", path: "/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := m.resolve(tt.path, tt.dir)
			assert.Equal(t, tt.want != "", ok)
			assert.Equal(t, tt.want, f.Path)
		})
	}
}
