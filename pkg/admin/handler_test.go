package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHandler(t *testing.T) {
	t.Run("creates handler with knowledge handler", func(t *testing.T) {
		kh := NewKnowledgeHandler(nil, nil, nil)
		h := NewHandler(kh, nil)
		require.NotNil(t, h)
		assert.NotNil(t, h.mux)
		assert.Equal(t, kh, h.knowledge)
		assert.Nil(t, h.authMiddle)
	})

	t.Run("creates handler with auth middleware", func(t *testing.T) {
		authMiddle := func(next http.Handler) http.Handler { return next }
		h := NewHandler(nil, authMiddle)
		require.NotNil(t, h)
		assert.NotNil(t, h.authMiddle)
	})

	t.Run("creates handler with nil knowledge handler", func(t *testing.T) {
		h := NewHandler(nil, nil)
		require.NotNil(t, h)
		assert.Nil(t, h.knowledge)
	})
}

func TestHandler_RoutesRegistered(t *testing.T) {
	store := &mockInsightStore{
		listResult:  []mockListResult{{insights: nil, total: 0, err: nil}},
		statsResult: &mockStatsResult{stats: &emptyStats, err: nil},
	}
	csStore := &mockChangesetStore{
		listResult: []mockChangesetListResult{{changesets: nil, total: 0, err: nil}},
	}
	kh := NewKnowledgeHandler(store, csStore, nil)
	h := NewHandler(kh, nil)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/knowledge/insights"},
		{http.MethodGet, "/api/v1/admin/knowledge/insights/stats"},
		{http.MethodGet, "/api/v1/admin/knowledge/insights/test-id"},
		{http.MethodGet, "/api/v1/admin/knowledge/changesets"},
		{http.MethodGet, "/api/v1/admin/knowledge/changesets/test-id"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			// Should not be 404 — the route is registered.
			assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code,
				"route %s %s should be registered", rt.method, rt.path)
		})
	}
}

func TestHandler_ServeHTTP_WithoutAuthMiddleware(t *testing.T) {
	store := &mockInsightStore{
		listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
	}
	kh := NewKnowledgeHandler(store, &mockChangesetStore{}, nil)
	h := NewHandler(kh, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_ServeHTTP_WithAuthMiddleware(t *testing.T) {
	store := &mockInsightStore{
		listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
	}
	kh := NewKnowledgeHandler(store, &mockChangesetStore{}, nil)

	t.Run("auth middleware blocks request", func(t *testing.T) {
		authMiddle := func(_ http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeError(w, http.StatusUnauthorized, "authentication required")
			})
		}
		h := NewHandler(kh, authMiddle)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("auth middleware passes through", func(t *testing.T) {
		authMiddle := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}
		h := NewHandler(kh, authMiddle)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestHandler_NilKnowledgeHandler_NoRoutes(t *testing.T) {
	h := NewHandler(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// With nil knowledge handler, no routes are registered, so we get 404.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "value", body["key"])
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad request")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "bad request", body["error"])
}
