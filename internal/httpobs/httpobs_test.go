package httpobs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func newMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

type userKey struct{}

// cloningAuth is the shape of every auth layer in the tree: it clones the
// request (r.WithContext) before the handler under it runs, so a pattern the
// inner mux writes lands on the clone.
func cloningAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, "user")))
	})
}

// assembled is a listener shaped like the platform's: a top mux with a
// nested, auth-cloned admin mux, a nested mux served with the same request,
// a CORS layer answering OPTIONS before the mux, and the middleware outside.
func assembled(t *testing.T, m *observability.Metrics, slow time.Duration) http.Handler {
	t.Helper()
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/v1/admin/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	admin.HandleFunc("GET /api/v1/admin/slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * slow)
		w.WriteHeader(http.StatusOK)
	})
	admin.HandleFunc("GET /api/v1/admin/boom", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	admin.HandleFunc("GET /api/v1/admin/limited", func(w http.ResponseWriter, r *http.Request) {
		MarkRateLimited(r, LimiterOAuthToken)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	admin.HandleFunc("GET /api/v1/admin/limited-unmarked", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	// a mux nested under the admin mux again, served with the same request
	audit := http.NewServeMux()
	audit.HandleFunc("GET /api/v1/admin/audit/events/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	admin.Handle("/api/v1/admin/audit/", audit)

	public := http.NewServeMux()
	public.HandleFunc("GET /portal/view/{token}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	mux := http.NewServeMux()
	mux.Handle("/api/v1/admin/", Routed(admin, cloningAuth))
	mux.Handle("/portal/view/", public) // same request pointer: r.Pattern is visible as is
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		require.True(t, ok, "the session SSE handler type-asserts http.Flusher")
		f.Flush()
		time.Sleep(2 * slow)
	})
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, _ *http.Request) {
		// a streamable MCP message: an event stream that ends with the result
		w.Header().Set("Content-Type", "text/event-stream")
		time.Sleep(2 * slow)
		_, _ = w.Write([]byte("event: message\ndata: {}\n\n"))
	})
	mux.HandleFunc("GET /controller", func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, http.NewResponseController(w).Flush(), "the SDK flushes through ResponseController")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })

	cors := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	return Middleware(Config{Mux: mux, Metrics: m, SlowThreshold: slow})(cors(mux))
}

func TestMiddleware_RecordsTheTemplate(t *testing.T) {
	m := newMetrics(t)
	h := assembled(t, m, time.Hour)

	send := func(method, target, auth string) int {
		req := httptest.NewRequestWithContext(context.Background(), method, target, http.NoBody)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusOK, send(http.MethodGet, "/api/v1/admin/users/42", "Bearer x"))
	require.Equal(t, http.StatusUnauthorized, send(http.MethodGet, "/api/v1/admin/users/42", ""), "refused before the inner mux")
	require.Equal(t, http.StatusOK, send(http.MethodGet, "/portal/view/tok", ""))
	require.Equal(t, http.StatusAccepted, send(http.MethodPost, "/anything/else", ""))
	require.Equal(t, http.StatusOK, send(http.MethodOptions, "/api/v1/admin/users/1", ""), "preflight")
	require.Equal(t, http.StatusOK, send(http.MethodGet, "/controller", ""))
	require.Equal(t, http.StatusTooManyRequests, send(http.MethodGet, "/api/v1/admin/limited", "Bearer x"))
	require.Equal(t, http.StatusTooManyRequests, send(http.MethodGet, "/api/v1/admin/limited-unmarked", "Bearer x"))
	require.Equal(t, http.StatusOK, send(http.MethodGet, "/api/v1/admin/audit/events/e1", "Bearer x"))
	require.Equal(t, http.StatusNotFound, send(http.MethodGet, "/api/v1/admin/audit/", "Bearer x"), "the nested mux has no route for its own prefix")
	require.Equal(t, http.StatusUnauthorized, send(http.MethodGet, "/api/v1/admin/audit/events/e1", ""))

	body := scrape(t, m)
	for _, want := range []string{
		// the nested template, through the clone, for the served AND the refused request
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/admin/users/{id}",status_class="2xx"} 1`,
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/admin/users/{id}",status_class="4xx"} 1`,
		// a mux nested twice over reports its own pattern once served; refused
		// before it, or under a path it has no route for, the request reports
		// the mount the admin mux resolved
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/admin/audit/events/{id}",status_class="2xx"} 1`,
		`http_server_request_duration_seconds_count{method="GET",route="/api/v1/admin/audit/",status_class="4xx"} 2`,
		// a nested mux served with the same request reports its own pattern
		`http_server_request_duration_seconds_count{method="GET",route="GET /portal/view/{token}",status_class="2xx"} 1`,
		// the catch-all, split by method
		`http_server_request_duration_seconds_count{method="POST",route="/",status_class="2xx"} 1`,
		// a preflight the CORS layer answered is labeled by the mux's prefix pattern
		`http_server_request_duration_seconds_count{method="OPTIONS",route="/api/v1/admin/",status_class="2xx"} 1`,
		`http_rate_limited_total{limiter="oauth_token"} 1`,
		`http_rate_limited_total{limiter="unknown"} 1`,
	} {
		require.Contains(t, body, want)
	}
	require.NotContains(t, body, `route="/api/v1/admin/users/42"`, "the path must never be a label")
}

func TestMiddleware_UnmatchedAndConnect(t *testing.T) {
	m := newMetrics(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /only", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Middleware(Config{Mux: mux, Metrics: m})(mux)

	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/nothing"},
		{http.MethodPost, "/only"},
		{http.MethodConnect, "/only"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), c.method, c.target, http.NoBody))
	}
	body := scrape(t, m)
	require.Contains(t, body, `route="unmatched",status_class="4xx"} 1`) // 404
	require.Contains(t, body, `route="unmatched",status_class="4xx"} 1`) // 405
	require.Contains(t, body, `method="CONNECT",route="unmatched"`)      // never the raw path
	require.NotContains(t, body, `route="/only"`, "CONNECT reports the path as its pattern; it must not reach the label")
	require.NotContains(t, body, `route="/nothing"`)
}

func TestMiddleware_FixedRouteListener(t *testing.T) {
	m := newMetrics(t)
	receiver := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		MarkRateLimited(r, LimiterWebhook)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	h := Middleware(Config{Route: "/hooks/", Metrics: m})(receiver)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/esp", http.NoBody))
	body := scrape(t, m)
	require.Contains(t, body, `http_server_request_duration_seconds_count{method="POST",route="/hooks/",status_class="4xx"} 1`)
	require.Contains(t, body, `http_rate_limited_total{limiter="webhook"} 1`)
}

func TestMiddleware_SlowLogAndStreams(t *testing.T) {
	m := newMetrics(t)
	const slow = 20 * time.Millisecond
	h := assembled(t, m, slow)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/admin/slow"}, {http.MethodGet, "/stream"}, {http.MethodGet, "/api/v1/admin/users/7"}, {http.MethodPost, "/mcp"},
	} {
		req := httptest.NewRequestWithContext(context.Background(), c.method, c.target, http.NoBody)
		req.Header.Set("Authorization", "Bearer x")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	out := logs.String()
	require.Contains(t, out, `msg="slow HTTP request"`)
	require.Contains(t, out, `route="GET /api/v1/admin/slow"`)
	require.Contains(t, out, `method=GET`)
	require.Contains(t, out, `status=200`)
	require.Contains(t, out, `route="POST /mcp"`, "a streamable message answered as an event stream is a slow request")
	require.Equal(t, 2, strings.Count(out, "slow HTTP request"), "the open stream and the fast request are not slow: %s", out)
	require.NotContains(t, out, "/stream")
	// the stream is still measured, under its method
	require.Contains(t, scrape(t, m), `http_server_request_duration_seconds_count{method="GET",route="GET /stream",status_class="2xx"} 1`)
}

func TestMiddleware_ServerSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(context.Background())
	})

	var seen trace.SpanContext
	var seenHeader string
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/v1/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		seen = trace.SpanContextFromContext(r.Context())
		seenHeader = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux := http.NewServeMux()
	mux.Handle("/api/v1/admin/", Routed(admin, cloningAuth))
	h := Middleware(Config{Mux: mux})(mux)

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/users/9", http.NoBody)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/gone", http.NoBody))

	spans := sr.Ended()
	require.Len(t, spans, 2)
	s := spans[0]
	require.Equal(t, "GET /api/v1/admin/users/{id}", s.Name())
	require.Equal(t, trace.SpanKindServer, s.SpanKind())
	require.Equal(t, traceID, s.SpanContext().TraceID().String(), "the caller's trace is continued")
	require.Equal(t, "00f067aa0ba902b7", s.Parent().SpanID().String(), "under the caller's span")
	require.Equal(t, s.SpanContext().SpanID(), seen.SpanID(), "the handler runs inside the server span")
	require.Equal(t, "00-"+traceID+"-"+s.SpanContext().SpanID().String()+"-01", seenHeader,
		"the request header now names the server span, for a handler that reads its trace off the headers")
	attrs := map[string]string{}
	for _, kv := range s.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	require.Equal(t, "GET", attrs["http.request.method"])
	require.Equal(t, "/api/v1/admin/users/{id}", attrs["http.route"])
	require.Equal(t, "500", attrs["http.response.status_code"])
	require.Equal(t, "500", attrs["error.type"])
	require.Equal(t, observability.StatusServerErr, attrs["status_category"])

	u := spans[1]
	require.Equal(t, "DELETE", u.Name(), "an unmatched request is named by its method alone")
	for _, kv := range u.Attributes() {
		require.NotEqual(t, "http.route", string(kv.Key))
	}
}

func TestRoutedAndMarkOutsideTheMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {
		MarkRateLimited(r, LimiterPDFExport) // no scope: a no-op
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	Routed(mux, nil).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestDetached_InProcessReentryKeepsTheCallersRoute: a handler that serves a
// second route in-process (the PDF route reading the content route) detaches
// the scope, so the caller's request stays recorded under its own template.
func TestDetached_InProcessReentryKeepsTheCallersRoute(t *testing.T) {
	m := newMetrics(t)
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/v1/admin/assets/{id}/content", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("doc")) })
	mux := http.NewServeMux()
	mux.Handle("/api/v1/admin/", Routed(admin, cloningAuth))
	mux.HandleFunc("GET /api/v1/admin/assets/{id}/pdf", func(w http.ResponseWriter, r *http.Request) {
		fwd := r.Clone(Detached(r.Context()))
		fwd.URL.Path = "/api/v1/admin/assets/" + r.PathValue("id") + "/content"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, fwd)
		w.WriteHeader(rec.Code)
	})
	h := Middleware(Config{Mux: mux, Metrics: m})(mux)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/assets/a1/pdf", http.NoBody)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body := scrape(t, m)
	require.Contains(t, body, `route="GET /api/v1/admin/assets/{id}/pdf"`)
	require.NotContains(t, body, `route="GET /api/v1/admin/assets/{id}/content"`, "the in-process read is the PDF route's own work")
}

func TestResponseWriter_StatusDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &responseWriter{ResponseWriter: rec}
	require.Equal(t, http.StatusOK, w.statusCode(), "nothing written is a 200")
	_, _ = w.Write([]byte("x"))
	require.Equal(t, http.StatusOK, w.statusCode())
	w.WriteHeader(http.StatusTeapot) // a second header is ignored, as net/http ignores it
	require.Equal(t, http.StatusOK, w.statusCode())
	require.Same(t, rec, w.Unwrap())
	require.False(t, w.isEventStream())
}

func TestConfig_SlowThreshold(t *testing.T) {
	require.Equal(t, DefaultSlowThreshold, Config{}.slowThreshold())
	require.Equal(t, DefaultSlowThreshold, Config{SlowThreshold: -time.Second}.slowThreshold())
	require.Equal(t, time.Minute, Config{SlowThreshold: time.Minute}.slowThreshold())
}

func TestRoutePath(t *testing.T) {
	require.Equal(t, "/x/{id}", routePath("GET /x/{id}"))
	require.Equal(t, "/x/", routePath("/x/"))
	require.Equal(t, "", routePath(RouteUnmatched))
}
