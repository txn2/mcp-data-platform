package admin

import (
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp authKeyCreateResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "new-key", resp.Name)
		assert.Equal(t, "generated-key-value", resp.Key)
		assert.NotEmpty(t, resp.Warning)
		assert.Equal(t, 1, cs.saveCalls, "syncConfig should be called")
	})

	t.Run("rejects missing name", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"roles":["admin"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "roles is required", pd.Detail)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			generateFn: func(name string, _ []string) (string, error) {
				return "", fmt.Errorf("key with name %q already exists", name)
			},
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		body := `{"name":"existing","roles":["admin"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader("{bad"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDeleteAuthKey(t *testing.T) {
	t.Run("deletes key successfully", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return true },
		}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/auth/keys/test-key", http.NoBody)
		req.SetPathValue("name", "test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "deleted", resp["status"])
		assert.Equal(t, 1, cs.saveCalls, "syncConfig should be called")
	})

	t.Run("returns 404 for non-existent key", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			removeFn: func(_ string) bool { return false },
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/auth/keys/nonexistent", http.NoBody)
		req.SetPathValue("name", "nonexistent")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
