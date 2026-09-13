package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
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

	t.Run("reads the key store before answering", func(t *testing.T) {
		mgr := &mockAPIKeyManager{keys: []auth.APIKeySummary{}}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}}, nil)

		w := serveKeyRequest(h, http.MethodGet, "", "")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, mgr.syncs, "the listing is answered after one sync from the store")
	})

	t.Run("answers 500 rather than a listing from memory when the store cannot be read", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			keys:     []auth.APIKeySummary{{Name: "stale", Roles: []string{"admin"}}},
			syncErrs: []error{errors.New("connection reset")},
		}
		h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}}, nil)

		w := serveKeyRequest(h, http.MethodGet, "", "")

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "failed to read api keys", decodeProblem(w.Body.Bytes()).Detail)
		assert.NotContains(t, w.Body.String(), "stale")
	})
}

// serveKeyRequest sends one request to the key routes: the listing or create
// route when name is empty, the named key's route otherwise.
func serveKeyRequest(h http.Handler, method, name, body string) *httptest.ResponseRecorder {
	path := "/api/v1/admin/auth/keys"
	if name != "" {
		path += "/" + name
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if name != "" {
		req.SetPathValue("name", name)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreateAuthKey(t *testing.T) {
	t.Run("creates key successfully", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, APIKeyStore: &mockAPIKeyStore{}, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

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
	newHandler := func(mgr *mockAPIKeyManager, store *mockAPIKeyStore, notifier *fakeReloadNotifier) http.Handler {
		deps := Deps{
			APIKeyManager:   mgr,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     &mockConfigStore{mode: "database"},
		}
		if store != nil {
			deps.APIKeyStore = store
		}
		if notifier != nil {
			deps.ReloadNotifier = notifier
		}
		return NewHandler(deps, nil)
	}

	t.Run("stores the key, then re-reads the store and tells the other replicas", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{}
		notifier := &fakeReloadNotifier{}
		h := newHandler(mgr, store, notifier)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"persist-key","roles":["admin"],"email":"test@example.com"}`)

		assert.Equal(t, http.StatusCreated, w.Code)
		require.Len(t, store.createCalls, 1, "store.Create should have been called once")
		assert.Equal(t, "persist-key", store.createCalls[0].Name)
		assert.Equal(t, "test@example.com", store.createCalls[0].Email)
		assert.NotEmpty(t, store.createCalls[0].KeyHash, "key hash should be non-empty")
		assert.Equal(t, 2, mgr.syncs, "one sync before the name check, one after the write")
		assert.Equal(t, 1, notifier.apikeys)
	})

	t.Run("a name another replica stored first answers 409", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{createErr: fmt.Errorf("persisting: %w", platform.ErrAPIKeyExists)}
		notifier := &fakeReloadNotifier{}
		h := newHandler(mgr, store, notifier)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"taken","roles":["admin"]}`)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, `key with name "taken" already exists`, decodeProblem(w.Body.Bytes()).Detail)
		assert.Zero(t, notifier.apikeys, "a refused create broadcasts nothing")
	})

	t.Run("a store that cannot be read before the name check answers 500 and creates nothing", func(t *testing.T) {
		mgr := &mockAPIKeyManager{syncErrs: []error{errors.New("connection reset")}}
		store := &mockAPIKeyStore{}
		h := newHandler(mgr, store, nil)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"new","roles":["admin"]}`)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "failed to read api keys", decodeProblem(w.Body.Bytes()).Detail)
		assert.Zero(t, mgr.generates)
		assert.Empty(t, store.createCalls)
	})

	t.Run("a re-read that fails after the write leaves the created key standing", func(t *testing.T) {
		mgr := &mockAPIKeyManager{syncErrs: []error{nil, errors.New("connection reset")}}
		store := &mockAPIKeyStore{}
		notifier := &fakeReloadNotifier{}
		h := newHandler(mgr, store, notifier)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"new","roles":["admin"]}`)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, 1, notifier.apikeys, "the other replicas are still told")
	})

	t.Run("with no key store there is nowhere for the key to exist", func(t *testing.T) {
		h := newHandler(&mockAPIKeyManager{}, nil, nil)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"new","roles":["admin"]}`)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "failed to persist api key", decodeProblem(w.Body.Bytes()).Detail)
	})
}

func TestCreateAuthKeyWithStoreError(t *testing.T) {
	t.Run("create fails when store fails", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{createErr: errors.New("db connection lost")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"best-effort","roles":["analyst"]}`)

		assert.Equal(t, http.StatusInternalServerError, w.Code, "create should fail when store errors")
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "failed to persist api key", pd.Detail)
		require.Len(t, store.createCalls, 1, "store.Create should have been called")
	})
}

func TestDeleteAuthKeyWithStore(t *testing.T) {
	t.Run("deletes key from store", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		w := serveKeyRequest(h, http.MethodDelete, "test-key", "")

		assert.Equal(t, http.StatusOK, w.Code)
		require.Len(t, store.deleteCalls, 1, "store.Delete should have been called once")
		assert.Equal(t, "test-key", store.deleteCalls[0])
		assert.Equal(t, 2, mgr.syncs, "one sync before the file-key check, one after the delete")
	})
}

func TestDeleteAuthKeyWithStoreError(t *testing.T) {
	t.Run("delete fails when store fails", func(t *testing.T) {
		mgr := &mockAPIKeyManager{}
		store := &mockAPIKeyStore{deleteErr: errors.New("db connection lost")}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			APIKeyManager:   mgr,
			APIKeyStore:     store,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     cs,
		}, nil)

		w := serveKeyRequest(h, http.MethodDelete, "test-key", "")

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "failed to delete api key from database", pd.Detail)
		require.Len(t, store.deleteCalls, 1, "store.Delete should have been called")
	})
}

func TestDeleteAuthKey(t *testing.T) {
	newHandler := func(mgr *mockAPIKeyManager, store *mockAPIKeyStore, notifier *fakeReloadNotifier) http.Handler {
		deps := Deps{
			APIKeyManager:   mgr,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     &mockConfigStore{mode: "database"},
		}
		if store != nil {
			deps.APIKeyStore = store
		}
		if notifier != nil {
			deps.ReloadNotifier = notifier
		}
		return NewHandler(deps, nil)
	}

	t.Run("deletes key successfully", func(t *testing.T) {
		notifier := &fakeReloadNotifier{}
		h := newHandler(&mockAPIKeyManager{}, &mockAPIKeyStore{}, notifier)

		w := serveKeyRequest(h, http.MethodDelete, "test-key", "")

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "deleted", resp["status"])
		assert.Equal(t, 1, notifier.apikeys)
	})

	t.Run("a name the store does not hold answers 404, not a database failure", func(t *testing.T) {
		notifier := &fakeReloadNotifier{}
		store := &mockAPIKeyStore{deleteErr: fmt.Errorf("deleting: %w", platform.ErrAPIKeyNotFound)}
		h := newHandler(&mockAPIKeyManager{}, store, notifier)

		w := serveKeyRequest(h, http.MethodDelete, "nonexistent", "")

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "key not found", decodeProblem(w.Body.Bytes()).Detail)
		assert.Zero(t, notifier.apikeys)
	})

	t.Run("with no key store there is no key to delete", func(t *testing.T) {
		h := newHandler(&mockAPIKeyManager{}, nil, nil)

		w := serveKeyRequest(h, http.MethodDelete, "anything", "")

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("a store that cannot be read answers 500 and deletes nothing", func(t *testing.T) {
		store := &mockAPIKeyStore{}
		mgr := &mockAPIKeyManager{syncErrs: []error{errors.New("connection reset")}}
		h := newHandler(mgr, store, nil)

		w := serveKeyRequest(h, http.MethodDelete, "test-key", "")

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Empty(t, store.deleteCalls)
	})

	t.Run("blocks deletion of file-only key", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			keys: []auth.APIKeySummary{
				{Name: "config-key", Source: "file", Roles: []string{"admin"}},
			},
		}
		store := &mockAPIKeyStore{}
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{APIKeyManager: mgr, APIKeyStore: store, PersonaRegistry: &mockPersonaRegistry{}, ConfigStore: cs}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/config-key", http.NoBody)
		req.SetPathValue("name", "config-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "config file")
		assert.Empty(t, store.deleteCalls)
	})
}
