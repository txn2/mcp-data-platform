package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- stubs ---

type stubAuthorizer struct{ dec Decision }

func (s stubAuthorizer) Authorize(_ context.Context) Decision { return s.dec }

func granted() Decision {
	return Decision{Authenticated: true, Allowed: true, UserID: "u1", Email: "u1@example.com", Persona: "ops"}
}

func newTestHandler(t *testing.T, upstreamURL string, dec Decision) *Handler {
	t.Helper()
	h, err := New(Config{URL: upstreamURL}, stubAuthorizer{dec: dec})
	require.NoError(t, err)
	return h
}

func doGet(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	mux.ServeHTTP(rec, req)
	return rec
}

// --- config validation ---

func TestNew_RejectsBadURL(t *testing.T) {
	_, err := New(Config{URL: "://nope"}, stubAuthorizer{})
	require.Error(t, err)

	_, err = New(Config{URL: "ftp://prom:9090"}, stubAuthorizer{})
	require.Error(t, err)

	_, err = New(Config{URL: "prometheus:9090"}, stubAuthorizer{}) // no scheme
	require.Error(t, err)
}

func TestNew_EmptyURLIsValid(t *testing.T) {
	h, err := New(Config{}, stubAuthorizer{})
	require.NoError(t, err)
	assert.Nil(t, h.base)
}

// --- authz / rate limit / unconfigured gating ---

func TestServe_Unauthenticated401(t *testing.T) {
	h := newTestHandler(t, "http://prom:9090", Decision{Authenticated: false})
	rec := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestServe_Forbidden403(t *testing.T) {
	h := newTestHandler(t, "http://prom:9090", Decision{Authenticated: true, Allowed: false})
	rec := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "observability:read")
}

func TestServe_Unconfigured503(t *testing.T) {
	h := newTestHandler(t, "", granted()) // empty URL
	rec := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "observability backend not configured")
}

func TestServe_RateLimited429(t *testing.T) {
	h, err := New(Config{URL: "http://127.0.0.1:0", RateLimitPerSecond: 1}, stubAuthorizer{dec: granted()})
	require.NoError(t, err)
	// First request consumes the single token (it will fail at the
	// upstream dial, but that is after the rate-limit gate). The second
	// must be rejected with 429 before any upstream call.
	_ = doGet(t, h, "/api/v1/observability/query?query=up")
	rec := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

// --- forwarding against a fake Prometheus ---

func TestServe_ForwardsAndPassesThrough(t *testing.T) {
	const body = `{"status":"success","data":{"resultType":"vector","result":[]}}`
	var gotPath, gotQuery, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()

	h, err := New(Config{URL: upstream.URL, BasicAuthUser: "prom", BasicAuthPass: "secret"}, stubAuthorizer{dec: granted()})
	require.NoError(t, err)

	resp := doGet(t, h, "/api/v1/observability/query?query=apigateway_inbound_requests_total")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, body, resp.Body.String(), "body must pass through unchanged")
	assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
	assert.Equal(t, "/api/v1/query", gotPath, "fixed upstream path")
	assert.Equal(t, "apigateway_inbound_requests_total", gotQuery)
	assert.True(t, strings.HasPrefix(gotAuth, "Basic "), "basic auth forwarded")
}

func TestServe_QueryRangeUsesRangePath(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()

	h := newTestHandler(t, upstream.URL, granted())
	resp := doGet(t, h, "/api/v1/observability/query_range?query=up&start=1&end=2&step=15")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "/api/v1/query_range", gotPath)
}

func TestServe_UpstreamErrorPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // bad PromQL -> Prometheus 400
		_, _ = io.WriteString(w, `{"status":"error","errorType":"bad_data"}`)
	}))
	defer upstream.Close()

	h := newTestHandler(t, upstream.URL, granted())
	resp := doGet(t, h, "/api/v1/observability/query?query=)(")
	assert.Equal(t, http.StatusBadRequest, resp.Code, "upstream status passed through")
	assert.Contains(t, resp.Body.String(), "bad_data")
}

func TestServe_UpstreamUnreachable502(t *testing.T) {
	// Closed server -> connection refused (not a timeout) -> 502.
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := upstream.URL
	upstream.Close()

	h := newTestHandler(t, url, granted())
	resp := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusBadGateway, resp.Code)
}

func TestServe_UpstreamTimeout504(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()

	h, err := New(Config{URL: upstream.URL, Timeout: 20 * time.Millisecond}, stubAuthorizer{dec: granted()})
	require.NoError(t, err)
	resp := doGet(t, h, "/api/v1/observability/query?query=up")
	assert.Equal(t, http.StatusGatewayTimeout, resp.Code)
}

// --- helpers ---

func TestIsTimeout(t *testing.T) {
	assert.True(t, isTimeout(context.DeadlineExceeded))
	assert.False(t, isTimeout(errors.New("connection refused")))
}
