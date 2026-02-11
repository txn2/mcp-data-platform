package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // register swagger docs
)

func TestNewHandler(t *testing.T) {
	t.Run("creates handler with knowledge handler", func(t *testing.T) {
		kh := NewKnowledgeHandler(nil, nil, nil)
		h := NewHandler(Deps{Knowledge: kh}, nil)
		require.NotNil(t, h)
		assert.NotNil(t, h.mux)
		assert.Equal(t, kh, h.deps.Knowledge)
		assert.Nil(t, h.authMiddle)
	})

	t.Run("creates handler with auth middleware", func(t *testing.T) {
		authMiddle := func(next http.Handler) http.Handler { return next }
		h := NewHandler(Deps{}, authMiddle)
		require.NotNil(t, h)
		assert.NotNil(t, h.authMiddle)
	})

	t.Run("creates handler with nil knowledge handler", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)
		require.NotNil(t, h)
		assert.Nil(t, h.deps.Knowledge)
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
	h := NewHandler(Deps{Knowledge: kh}, nil)

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

func TestHandler_SystemRoutesRegistered(t *testing.T) {
	h := NewHandler(Deps{Config: testConfig()}, nil)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/system/info"},
		{http.MethodGet, "/api/v1/admin/tools"},
		{http.MethodGet, "/api/v1/admin/connections"},
		{http.MethodGet, "/api/v1/admin/config"},
		{http.MethodGet, "/api/v1/admin/docs/index.html"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code,
				"route %s %s should be registered", rt.method, rt.path)
		})
	}
}

func TestHandler_ServeHTTP_WithoutAuthMiddleware(t *testing.T) {
	store := &mockInsightStore{
		listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
	}
	kh := NewKnowledgeHandler(store, &mockChangesetStore{}, nil)
	h := NewHandler(Deps{Knowledge: kh}, nil)

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
		h := NewHandler(Deps{Knowledge: kh}, authMiddle)

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
		h := NewHandler(Deps{Knowledge: kh}, authMiddle)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("docs bypass auth middleware", func(t *testing.T) {
		authMiddle := func(_ http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeError(w, http.StatusUnauthorized, "authentication required")
			})
		}
		h := NewHandler(Deps{Knowledge: kh}, authMiddle)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/docs/index.html", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.NotEqual(t, http.StatusUnauthorized, w.Code,
			"docs should bypass auth middleware")
	})
}

func TestHandler_NilKnowledgeHandler_SystemRoutesStillWork(t *testing.T) {
	h := NewHandler(Deps{}, nil)

	// System routes are always registered
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFeatureUnavailable_Knowledge(t *testing.T) {
	cfg := testConfig()
	cfg.Knowledge.Enabled = true

	t.Run("knowledge enabled no DB returns 409", func(t *testing.T) {
		h := NewHandler(Deps{Config: cfg}, nil)

		paths := []string{
			"/api/v1/admin/knowledge/insights",
			"/api/v1/admin/knowledge/insights/some-id",
			"/api/v1/admin/knowledge/changesets",
		}
		for _, path := range paths {
			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusConflict, w.Code, "path %s", path)
			pd := decodeProblem(w.Body.Bytes())
			assert.Contains(t, pd.Detail, "knowledge")
			assert.Contains(t, pd.Detail, "database")
		}
	})

	t.Run("knowledge disabled returns 404", func(t *testing.T) {
		disabledCfg := testConfig()
		disabledCfg.Knowledge.Enabled = false
		h := NewHandler(Deps{Config: disabledCfg}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestFeatureUnavailable_Audit(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Enabled = true

	t.Run("audit enabled no DB returns 409", func(t *testing.T) {
		h := NewHandler(Deps{Config: cfg}, nil)

		paths := []string{
			"/api/v1/admin/audit/events",
			"/api/v1/admin/audit/events/some-id",
			"/api/v1/admin/audit/stats",
		}
		for _, path := range paths {
			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusConflict, w.Code, "path %s", path)
			pd := decodeProblem(w.Body.Bytes())
			assert.Contains(t, pd.Detail, "audit")
			assert.Contains(t, pd.Detail, "database")
		}
	})

	t.Run("audit disabled returns 404", func(t *testing.T) {
		disabledCfg := testConfig()
		disabledCfg.Audit.Enabled = false
		h := NewHandler(Deps{Config: disabledCfg}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit/events", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
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
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	var body problemDetail
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "about:blank", body.Type)
	assert.Equal(t, "Bad Request", body.Title)
	assert.Equal(t, http.StatusBadRequest, body.Status)
	assert.Equal(t, "bad request", body.Detail)
}
