package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests handle two valid states:
//   - Clean checkout / CI / make verify: dist has only .gitkeep → Available() = false
//   - After make frontend-build: dist has built assets → Available() = true
//
// make verify runs embed-clean first, so the CI path is always exercised.

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

func TestHandler_Root(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if Available() {
		// SPA fallback rewrites to /index.html; http.FileServer redirects
		// /index.html back to ./ (Go hides the default index file).
		assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	} else {
		// No index.html in dist — SPA fallback returns 404.
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}
}

func TestHandler_SPAFallback(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if Available() {
		assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	} else {
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}
}
