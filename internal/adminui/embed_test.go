package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAvailable_EmptyDist(t *testing.T) {
	// The dist directory only has .gitkeep, so Available() should return false.
	assert.False(t, Available())
}

func TestHandler_ReturnsHandler(t *testing.T) {
	h := Handler()
	assert.NotNil(t, h)
}

func TestHandler_ServesNotFoundForEmptyDist(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// With only .gitkeep, serving "/" falls back to index.html which doesn't exist
	// The file server returns 404 in this case
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_SPAFallback(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/dashboard", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// /dashboard doesn't exist, falls back to index.html which also doesn't exist
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
