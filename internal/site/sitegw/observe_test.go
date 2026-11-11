package sitegw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// observedHarness is the gateway in the listener's shape: the request
// observer outside it and the platform's mux behind it, holding the
// catch-all "/" and a route a site path can collide with.
func observedHarness(t *testing.T) (*harness, http.Handler, *observability.Metrics) {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	h := newHarness(Deps{Metrics: m})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, nextBody) })
	mux.HandleFunc("GET /api/v1/resources", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	observed := httpobs.Middleware(httpobs.Config{Mux: mux, Metrics: m})(h.gateway.Wrap(mux))
	return h, observed, m
}

func scrapeOf(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

func getVia(handler http.Handler, host, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+host+target, http.NoBody))
	return rec
}

// TestObserve_SiteTrafficIsItsOwnRouteAndSeries pins #2597: a request a site
// answers is recorded under the gateway's route, never the mux's catch-all or
// a platform route its path matches; it is counted per site and environment
// with where its body came from; a hostname under the pages domain that
// names nothing mints no site series; the portal's own traffic is untouched.
func TestObserve_SiteTrafficIsItsOwnRouteAndSeries(t *testing.T) {
	h, observed, m := observedHarness(t)
	h.mapHost(h.addRelease(1, nil,
		testFile{path: indexFile, body: "prod home", contentType: ctHTML},
		testFile{path: "api/v1/resources", body: "a site page", contentType: ctHTML}))

	require.Equal(t, http.StatusOK, getVia(observed, testSiteHost, "/").Code)
	require.Equal(t, http.StatusOK, getVia(observed, testSiteHost, "/").Code)
	require.Equal(t, http.StatusOK, getVia(observed, testSiteHost, "/api/v1/resources").Code)
	require.Equal(t, http.StatusNotFound, getVia(observed, "nobody.pages.example.com", "/").Code)
	require.Equal(t, http.StatusOK, getVia(observed, testPortal, "/api/v1/resources").Code)

	body := scrapeOf(t, m)
	for _, want := range []string{
		`http_server_request_duration_seconds_count{method="GET",route="site",status_class="2xx"} 3`,
		`http_server_request_duration_seconds_count{method="GET",route="site",status_class="4xx"} 1`,
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/resources",status_class="2xx"} 1`,
		`site_requests_total{environment="prod",kind="site",site="marketing",status_class="2xx"} 3`,
		`site_request_duration_seconds_count{environment="prod",kind="site",site="marketing"} 3`,
		`site_cache_reads_total{outcome="object_store"} 2`,
		`site_cache_reads_total{outcome="memory"} 1`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `route="/"`, "site traffic is not the catch-all's")
	assert.NotContains(t, body, `site="nobody"`, "a hostname nobody added mints no series")
}

// An application's page origin is recorded under the pages route and counted
// by the application's name.
func TestObserve_APageOriginIsCountedByApplication(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	g := New(Deps{Metrics: m, Config: siteconfig.Config{PagesDomain: testPagesDomain, Cache: siteconfig.CacheConfig{Dir: isolatedCacheDir()}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://shop.pages.example.com/", http.NoBody)
	observed := httpobs.Middleware(httpobs.Config{Metrics: m})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := g.observe(w, r, &site.Route{Application: "shop", Site: "ignored", Environment: "ignored"})
		http.NotFound(sw, r)
		sw.done()
	}))
	observed.ServeHTTP(rec, req)

	body := scrapeOf(t, m)
	assert.Contains(t, body, `http_server_request_duration_seconds_count{method="GET",route="pages",status_class="4xx"} 1`)
	assert.Contains(t, body, `site_requests_total{environment="",kind="pages",site="shop",status_class="4xx"} 1`)
}

// A response the handler never writes counts as the 200 net/http sends; the
// writer passes a flush through and unwraps to the writer underneath.
func TestObservedWriter_PassesThroughAndDefaultsTo200(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	g := New(Deps{Metrics: m, Config: siteconfig.Config{PagesDomain: testPagesDomain, Cache: siteconfig.CacheConfig{Dir: isolatedCacheDir()}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://x/", http.NoBody)
	sw := g.observe(rec, req, &site.Route{Site: "docs", Environment: "prod"})
	require.NoError(t, http.NewResponseController(sw).Flush())
	assert.True(t, rec.Flushed)
	assert.Equal(t, rec, sw.Unwrap())
	sw.done()
	assert.Contains(t, scrapeOf(t, m), `site_requests_total{environment="prod",kind="site",site="docs",status_class="2xx"} 1`)
}
