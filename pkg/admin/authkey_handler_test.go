package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

func TestListAuthKeys(t *testing.T) {
	t.Run("returns sorted key list", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			keys: []auth.APIKeySummary{
				{Name: "alpha", Roles: []string{"admin"}},
				{Name: "bravo", Roles: []string{"analyst"}},
			},
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(2), body["total"])

		keys, ok := body["keys"].([]any)
		require.True(t, ok, "keys should be a slice")
		assert.Len(t, keys, 2)
	})

	t.Run("returns empty list", func(t *testing.T) {
		mgr := &mockAPIKeyManager{keys: []auth.APIKeySummary{}}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
	})
}

func TestCreateAuthKey(t *testing.T) {
	t.Run("creates key successfully", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

		body := `{"name":"new-key","roles":["admin"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp authKeyCreateResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "new-key", resp.Name)
		assert.Equal(t, "generated-key-value", resp.Key)
		assert.NotEmpty(t, resp.Warning)
	})

	t.Run("rejects missing name", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"roles":["admin"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "name is required", pd.Detail)
	})

	t.Run("rejects missing roles", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "roles is required", pd.Detail)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			generateFn: func(def auth.APIKey) (string, error) {
				return "", fmt.Errorf("key with name %q already exists", def.Name)
			},
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"existing","roles":["admin"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader("{bad"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestCreateAuthKeyWithStore(t *testing.T) {
	t.Run("persists key to store on create", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		body := `{"name":"persist-key","roles":["admin"],"email":"test@example.com"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.Len(t, store.setCalls, 1, "store.Set should have been called once")
		assert.Equal(t, "persist-key", store.setCalls[0].Name)
		assert.Equal(t, "test@example.com", store.setCalls[0].Email)
		assert.NotEmpty(t, store.setCalls[0].KeyHash, "key hash should be non-empty")
	})
}

func TestCreateAuthKeyWithStoreError(t *testing.T) {
	t.Run("create fails when store fails", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{setErr: fmt.Errorf("db connection lost")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		body := `{"name":"best-effort","roles":["analyst"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// Store error should fail the request — DB-first two-phase commit
		assert.Equal(t, http.StatusInternalServerError, w.Code, "create should fail when store errors")
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "failed to persist api key", pd.Detail)
		require.Len(t, store.setCalls, 1, "store.Set should have been called")
	})
}

func TestDeleteAuthKeyWithStore(t *testing.T) {
	t.Run("deletes key from store", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return true },
		}
		store := &mockAPIKeyStore{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/test-key", http.NoBody)
		req.SetPathValue("name", "test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.Len(t, store.deleteCalls, 1, "store.Delete should have been called once")
		assert.Equal(t, "test-key", store.deleteCalls[0])
	})
}

func TestDeleteAuthKeyWithStoreError(t *testing.T) {
	t.Run("delete fails when store fails", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return true },
		}
		store := &mockAPIKeyStore{deleteErr: fmt.Errorf("db connection lost")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/test-key", http.NoBody)
		req.SetPathValue("name", "test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// Store error should fail the request — DB-first two-phase commit
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "failed to delete api key from database", pd.Detail)
		require.Len(t, store.deleteCalls, 1, "store.Delete should have been called")
	})
}

func TestDeleteAuthKey(t *testing.T) {
	t.Run("deletes key successfully", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return true },
		}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/test-key", http.NoBody)
		req.SetPathValue("name", "test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "deleted", resp["status"])
	})

	t.Run("returns 404 for non-existent key", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return false },
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/nonexistent", http.NoBody)
		req.SetPathValue("name", "nonexistent")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("blocks deletion of file-only key", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			keys: []auth.APIKeySummary{
				{Name: "config-key", Source: "file", Roles: []string{"admin"}},
			},
		}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/config-key", http.NoBody)
		req.SetPathValue("name", "config-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "config file")
	})
}
