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

// The listener serves the public map paths only to the sites its check
// allows (#2393): a refusal is a 403 with no CORS grant and a body that does
// not repeat the site; an admitted read echoes the origin; both vary by the
// headers the site is read from.
func TestGated_PublicMapPaths(t *testing.T) {
	served := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { served++; w.WriteHeader(http.StatusPartialContent) })
	h := Gated(func(r *http.Request) bool { return r.Header.Get("Origin") != "https://freeloader.test" })(inner)

	do := func(method, path, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	for _, path := range []string{"/portal/maps/us.pmtiles", "/portal/maps/regions", "/portal/vendor/maplibre/maplibre-gl.js"} {
		w := do(http.MethodGet, path, "https://freeloader.test")
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: refused read = %d, want 403", path, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: refused read grants CORS %q", path, got)
		}
		if strings.Contains(w.Body.String(), "freeloader") {
			t.Errorf("%s: refusal repeats the site: %q", path, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Vary"), "Origin") {
			t.Errorf("%s: refusal does not vary by Origin", path)
		}
	}
	if served != 0 {
		t.Fatalf("a refused read reached the route %d times", served)
	}

	if w := do(http.MethodOptions, "/portal/maps/us.pmtiles", "https://freeloader.test"); w.Code != http.StatusForbidden {
		t.Errorf("refused preflight = %d, want 403", w.Code)
	}

	w := do(http.MethodGet, "/portal/maps/us.pmtiles", "https://stores.example.com")
	if w.Code != http.StatusPartialContent || served != 1 {
		t.Fatalf("admitted read = %d (served %d)", w.Code, served)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://stores.example.com" {
		t.Errorf("admitted read Allow-Origin = %q, want the origin", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") || !strings.Contains(got, "Referer") {
		t.Errorf("Vary = %q, want Origin and Referer", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("public read grants credentials: %q", got)
	}

	if got := do(http.MethodGet, "/portal/maps/us.pmtiles", "null").Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Errorf("admitted sandboxed frame Allow-Origin = %q, want null", got)
	}
	if w := do(http.MethodGet, "/portal/maps/us.pmtiles", ""); w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("a read naming no origin Allow-Origin = %q, want *", w.Header().Get("Access-Control-Allow-Origin"))
	}

	if w := do(http.MethodGet, "/api/v1/portal/assets", "https://freeloader.test"); w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("the check applies to the public map paths only")
	}
}
