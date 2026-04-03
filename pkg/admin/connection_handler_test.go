package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// --- Mock ConnectionStore ---

type mockConnectionStore struct {
	instances []platform.ConnectionInstance
	getResult *platform.ConnectionInstance
	setErr    error
	deleteErr error
	listErr   error
	getErr    error
}

func (m *mockConnectionStore) List(_ context.Context) ([]platform.ConnectionInstance, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.instances, nil
}

func (m *mockConnectionStore) Get(_ context.Context, _, _ string) (*platform.ConnectionInstance, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getResult, nil
}

func (m *mockConnectionStore) Set(_ context.Context, _ platform.ConnectionInstance) error {
	return m.setErr
}

func (m *mockConnectionStore) Delete(_ context.Context, _, _ string) error {
	return m.deleteErr
}

// Verify interface compliance.
var _ ConnectionStore = (*mockConnectionStore)(nil)

// connTestHandler builds a Handler with the given connection store and mutable config store.
func connTestHandler(connStore ConnectionStore, mutable bool) *Handler {
	mode := "file"
	if mutable {
		mode = "database"
	}
	return NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: mode},
	}, nil)
}

// --- List ---

func TestListConnectionInstances(t *testing.T) {
	t.Run("success with entries", func(t *testing.T) {
		store := &mockConnectionStore{
			instances: []platform.ConnectionInstance{
				{Kind: "trino", Name: "prod", Description: "Production Trino", Config: map[string]any{"host": "trino.local"}},
				{Kind: "datahub", Name: "primary", Description: "Primary DataHub", Config: map[string]any{"url": "https://dh.local"}},
			},
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body, 2)
		assert.Equal(t, "trino", body[0].Kind)
		assert.Equal(t, "datahub", body[1].Kind)
	})

	t.Run("empty list returns empty array", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body, 0)
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{listErr: errors.New("db down")}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- Get ---

func TestGetConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{
			getResult: &platform.ConnectionInstance{
				Kind:        "trino",
				Name:        "prod",
				Description: "Production Trino",
				Config:      map[string]any{"host": "trino.local"},
				CreatedBy:   "admin@test.com",
			},
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "trino", body.Kind)
		assert.Equal(t, "prod", body.Name)
		assert.Equal(t, "trino.local", body.Config["host"])
	})

	t.Run("not found", func(t *testing.T) {
		store := &mockConnectionStore{
			getErr: platform.ErrConnectionNotFound,
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "not found")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{
			getErr: errors.New("db down"),
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- Set ---

func TestSetConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local","port":8080},"description":"New Trino"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Equal(t, "trino", result.Kind)
		assert.Equal(t, "prod", result.Name)
		assert.Equal(t, "New Trino", result.Description)
		assert.Equal(t, "trino.local", result.Config["host"])
	})

	t.Run("success with user context", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local"},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx := context.WithValue(req.Context(), adminUserKey, &User{Email: "admin@test.com"})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Equal(t, "admin@test.com", result.CreatedBy)
	})

	t.Run("invalid kind returns 400", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{},"description":"Bad kind"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/unknown/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "unknown connection kind")
	})

	t.Run("invalid body returns 400", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader("not-json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "invalid request body")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{setErr: errors.New("db down")}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local"},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("nil config gets default empty map", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"description":"No config"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.NotNil(t, result.Config)
		assert.Empty(t, result.Config)
	})

	t.Run("read-only mode returns 404 for PUT", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, false) // file mode = not mutable

		body := `{"config":{},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// In file mode, the PUT route is not registered so mux returns 405 or 404
		assert.True(t, w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusNotFound,
			"expected 404 or 405 in read-only mode, got %d", w.Code)
	})
}

// --- Delete ---

func TestDeleteConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		store := &mockConnectionStore{
			deleteErr: platform.ErrConnectionNotFound,
		}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "not found")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{
			deleteErr: errors.New("db down"),
		}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
