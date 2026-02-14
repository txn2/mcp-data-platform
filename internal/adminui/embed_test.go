package adminui

import (
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

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_SPAFallback_ServesIndexHTML(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequest(http.MethodGet, "/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}

func TestSPAHandler_StaticAsset_ServedByFileServer(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "console.log")
}

func TestSPAHandler_CSSAsset(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "body{}")
}

func TestSPAHandler_NoIndexHTML_Returns404(t *testing.T) {
	emptyFS := fstest.MapFS{}
	h := newSPAHandler(emptyFS)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSPAHandler_NoRedirectLoop(t *testing.T) {
	h := newSPAHandler(testFS())

	// The exact bug: /index.html must NOT produce a 301
	req := httptest.NewRequest(http.MethodGet, "/index.html", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// /index.html is a real file, so FileServer serves it (200), not redirect
	require.Equal(t, http.StatusOK, rec.Code, "index.html must not redirect")
}

func TestSPAHandler_NestedUnknownRoute_Fallback(t *testing.T) {
	h := newSPAHandler(testFS())

	req := httptest.NewRequest(http.MethodGet, "/settings/profile", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html>SPA</html>")
}
