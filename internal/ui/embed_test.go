package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFS builds a synthetic in-memory filesystem for testing the SPA handler
// independent of whether the real frontend was built.
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte("<html>SPA</html>")},
		"assets/app.js":    {Data: []byte("console.log('app')")},
		"assets/style.css": {Data: []byte("body{}")},
	}
}

func TestAvailable(t *testing.T) {
	// Available() returns whether built frontend assets are embedded.
	// Both states are valid depending on whether the frontend was built.
	if Available() {
		t.Log("dist has built frontend assets (local build present)")
	} else {
		t.Log("dist has .gitkeep only (clean checkout / CI)")
	}
}

func TestHandler_ReturnsHandler(t *testing.T) {
	h := Handler()
	assert.NotNil(t, h)
}

func TestSPAHandler_Root_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_SPAFallback_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_StaticAsset_ServedByFileServer(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app.js", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "console.log")
}

func TestSPAHandler_CSSAsset(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/style.css", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "body{}")
}

func TestSPAHandler_NoIndexHTML_Returns404(t *testing.T) {
	emptyFS := fstest.MapFS{}
	h := newSPAHandler(emptyFS)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSPAHandler_NoRedirectLoop(t *testing.T) {
	h := newSPAHandler(testFS())

	// The exact bug: /index.html must NOT produce a 301
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/index.html", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// /index.html is a real file, so FileServer serves it (200), not redirect
	require.Equal(t, http.StatusOK, rec.Code, "index.html must not redirect")
}

func TestSPAHandler_NestedUnknownRoute_Fallback(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings/profile", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

// A hashed build file the embedded build does not have answers 404, never
// index.html: during a rolling update a page from one build asks a replica on
// the other build for its files, and HTML under a stylesheet's or a chunk's
// URL breaks the page and can be cached as that file (#2139).
func TestSPAHandler_MissingAsset_Returns404(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{"/assets/index-DOESNOTEXIST.css", "/assets/AutomationsPage-DOESNOTEXIST.js", "/assets/nested/missing.js"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, path)
		assert.NotContains(t, rec.Body.String(), "<html>SPA</html>", path)
	}
}

// index.html names the current build's hashed files, so it is never served
// from a cache: a reload always picks up the file names this build has (#2139).
func TestSPAHandler_IndexHTML_NoCache(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{"/", "/automations/abc/runs/def", "/assets-overview"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "<html>SPA</html>", path)
		assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), path)
	}
}

// The asset viewer route shares Vite's build directory prefix: a share link
// redirects to /portal/assets/{id}?share=..., and that page is the SPA, not a
// missing build file (#2205).
func TestSPAHandler_AssetViewerRoute_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	for _, path := range []string{
		"/assets/490c49f53b801512ef219da0be06dac6?share=abc",
		"/assets/2fbb2fbd-0184-4478-a003-fedb9001f31d",
		"/assets/a1/provenance",
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "<html>SPA</html>", path)
	}
}
