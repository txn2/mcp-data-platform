package corshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCorsMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(inner)

	t.Run("sets CORS headers", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
		}

		methods := w.Header().Get("Access-Control-Allow-Methods")
		for _, m := range []string{"GET", "POST", "DELETE", "OPTIONS"} {
			if !strings.Contains(methods, m) {
				t.Errorf("Allow-Methods missing %q: %s", m, methods)
			}
		}

		allowHeaders := w.Header().Get("Access-Control-Allow-Headers")
		for _, h := range []string{"Mcp-Session-Id", "Mcp-Protocol-Version", "X-API-Key", "Last-Event-ID"} {
			if !strings.Contains(allowHeaders, h) {
				t.Errorf("Allow-Headers missing %q: %s", h, allowHeaders)
			}
		}

		exposeHeaders := w.Header().Get("Access-Control-Expose-Headers")
		if !strings.Contains(exposeHeaders, "Mcp-Session-Id") {
			t.Errorf("Expose-Headers missing Mcp-Session-Id: %s", exposeHeaders)
		}

		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want %q", got, "true")
		}
	})

	t.Run("handles OPTIONS preflight", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "OPTIONS", "/mcp", http.NoBody)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("OPTIONS status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("defaults origin to wildcard", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin = %q, want %q", got, "*")
		}
	})
}

// TestMiddleware_PublicReads: a map in a sandboxed asset frame reads its
// basemap and runtime with Origin: null and a Range header (#2068). Those
// paths answer any origin without credentials, accept Range, and expose the
// headers a ranged read is checked by; every other path is unchanged.
func TestMiddleware_PublicReads(t *testing.T) {
	var served bool
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusPartialContent)
	}))
	for _, p := range []string{"/portal/maps/sf.pmtiles", "/portal/vendor/maplibre/fonts/x/0-255.pbf"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, p, http.NoBody)
		req.Header.Set("Origin", "null")
		w := httptest.NewRecorder()
		served = false
		handler.ServeHTTP(w, req)
		if !served || w.Code != http.StatusPartialContent {
			t.Errorf("%s: the read was not served: %d", p, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: Allow-Origin = %q, want *", p, got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: a public read carries no credentials, got %q", p, got)
		}
		if got := w.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Content-Range") {
			t.Errorf("%s: Content-Range is not exposed: %q", p, got)
		}

		pre := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, p, http.NoBody)
		pre.Header.Set("Origin", "null")
		pre.Header.Set("Access-Control-Request-Headers", "range")
		w = httptest.NewRecorder()
		served = false
		handler.ServeHTTP(w, pre)
		if served || w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Range") {
			t.Errorf("%s: preflight = %d %q", p, w.Code, w.Header().Get("Access-Control-Allow-Headers"))
		}
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/vendor/reveal/reveal.js", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("another path's policy changed: Allow-Origin = %q", got)
	}
}
