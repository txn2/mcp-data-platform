package httpobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every HTTP request resolves the route template it matched, before the auth
// layer runs and again after the mux served it (#2716): a mux of the portal's
// shape, 40 patterns, and one request for a templated route under it.
func BenchmarkRoutedTemplate(b *testing.B) {
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, section := range []string{"assets", "collections", "scripts", "sites", "applications", "approvals", "resources", "prompts"} {
		base := "/api/v1/portal/" + section
		mux.Handle("GET "+base, ok)
		mux.Handle("POST "+base, ok)
		mux.Handle("GET "+base+"/{id}", ok)
		mux.Handle("PATCH "+base+"/{id}", ok)
		mux.Handle("GET "+base+"/{id}/versions/{version}", ok)
	}
	h := Routed(mux, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/sites/hello/versions/7", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), scopeKey{}, &scope{}))
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, req)
	}
	if got := scopeOf(req.Context()).route; got != "GET /api/v1/portal/sites/{id}/versions/{version}" {
		b.Fatalf("route = %q", got)
	}
}
