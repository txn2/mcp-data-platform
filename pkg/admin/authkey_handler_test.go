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

	"github.com/txn2/mcp-data-platform/internal/apikeyissue"
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
		assert.Equal(t, "roles is required, or bind the key to a user with user_email", pd.Detail)
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		mgr := &mockAPIKeyManager{
			generateFn: func(def auth.APIKey) (string, error) {
				return "", fmt.Errorf("api key name %q is already held: %w", def.Name, auth.ErrKeyNameTaken)
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
		assert.Equal(t, "this deployment stores no api keys", decodeProblem(w.Body.Bytes()).Detail)
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

// fakeBoundPrincipals knows the people the platform has seen sign in, and can
// fail, which is what a create is refused on.
type fakeBoundPrincipals struct {
	people map[string]auth.BoundPrincipal
	err    error
}

func (f *fakeBoundPrincipals) BoundPrincipal(_ context.Context, email string) (*auth.BoundPrincipal, error) {
	if f.err != nil {
		return nil, f.err
	}
	person, ok := f.people[email]
	if !ok {
		return nil, auth.ErrNoBoundPrincipal
	}
	return &person, nil
}

// TestCreateAuthKeyBoundToAUser is #1759 on the admin surface: an operator
// issues a key against somebody's account, and that account decides what the
// key is unless the operator narrows it.
func TestCreateAuthKeyBoundToAUser(t *testing.T) {
	// The resolver is taken as the interface the Deps field holds, so the
	// "no resolver" case is a genuinely nil interface -- what the composition
	// root assigns when this deployment has no directory to resolve through.
	newHandler := func(people auth.PrincipalSource) (http.Handler, *mockAPIKeyStore) {
		store := &mockAPIKeyStore{}
		return NewHandler(Deps{
			APIKeyManager:   &mockAPIKeyManager{},
			APIKeyStore:     store,
			BoundPrincipals: people,
			PersonaRegistry: &mockPersonaRegistry{},
			ConfigStore:     &mockConfigStore{mode: "database"},
		}, nil), store
	}
	known := &fakeBoundPrincipals{people: map[string]auth.BoundPrincipal{
		"analyst@example.com": {
			Subject: "sub-42",
			Email:   "analyst@example.com",
			Roles:   []string{"analyst"},
		},
	}}

	t.Run("a key with no roles of its own follows the person", func(t *testing.T) {
		h, store := newHandler(known)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"chatgpt","user_email":"analyst@example.com"}`)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		require.Len(t, store.createCalls, 1)
		stored := store.createCalls[0]
		assert.Equal(t, "analyst@example.com", stored.UserEmail)
		assert.Empty(t, stored.Roles, "no override is stored, so the key follows the person on every request")
		assert.Equal(t, "analyst@example.com", stored.Email,
			"a bound key carries its person's address, never a synthetic one")

		var resp authKeyCreateResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "analyst@example.com", resp.UserEmail)
		assert.Equal(t, []string{"analyst"}, resp.Roles,
			"the response states what the key will actually reach")
	})

	t.Run("roles narrow the key and are stored", func(t *testing.T) {
		h, store := newHandler(known)

		w := serveKeyRequest(h, http.MethodPost, "",
			`{"name":"narrowed","user_email":"analyst@example.com","roles":["viewer"]}`)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, []string{"viewer"}, store.createCalls[0].Roles)
	})

	t.Run("refuses an account the platform has never seen sign in", func(t *testing.T) {
		h, store := newHandler(&fakeBoundPrincipals{people: map[string]auth.BoundPrincipal{}})

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"invited","user_email":"invited@example.com"}`)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "invited@example.com",
			"the refusal names the account it could not issue against")
		assert.Empty(t, store.createCalls, "nothing is stored for an account that does not resolve")
	})

	t.Run("refuses a binding on a deployment that resolves no accounts", func(t *testing.T) {
		h, store := newHandler(nil)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"k","user_email":"analyst@example.com"}`)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, store.createCalls)
	})

	t.Run("a bound request needs no roles, an unbound one does", func(t *testing.T) {
		h, _ := newHandler(known)

		w := serveKeyRequest(h, http.MethodPost, "", `{"name":"service-key"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"a key bound to nobody reaches nothing without roles of its own")
	})
}

// TestListAuthKeysReportsWhatABoundKeyReaches holds that a key issued against
// an account with no roles of its own is listed by the persona its owner
// reaches, not as reaching none. Badging it "No persona" is the opposite of
// true: it reaches whatever its owner does.
func TestListAuthKeysReportsWhatABoundKeyReaches(t *testing.T) {
	mgr := &mockAPIKeyManager{
		keys: []auth.APIKeySummary{
			{Name: "user:analyst@example.com:chatgpt", UserEmail: "analyst@example.com", Roles: []string{}},
			{Name: "etl", Roles: []string{"admin"}},
		},
	}
	h := NewHandler(Deps{
		APIKeyManager:   mgr,
		PersonaRegistry: &mockPersonaRegistry{},
		BoundPrincipals: &fakeBoundPrincipals{people: map[string]auth.BoundPrincipal{
			"analyst@example.com": {Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"admin"}},
		}},
	}, nil)

	w := serveKeyRequest(h, http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, w.Code)

	var body authKeyListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Keys, 2)
	for _, k := range body.Keys {
		assert.False(t, k.NoPersona, "key %q was listed as reaching no persona", k.Name)
	}
}

// TestListAuthKeysFallsBackWhenTheAccountCannotBeResolved holds that a listing
// still answers when the resolver cannot: the key's own roles are reported
// rather than the whole page failing.
func TestListAuthKeysFallsBackWhenTheAccountCannotBeResolved(t *testing.T) {
	mgr := &mockAPIKeyManager{
		keys: []auth.APIKeySummary{
			{Name: "user:gone@example.com:k", UserEmail: "gone@example.com", Roles: []string{}},
		},
	}
	h := NewHandler(Deps{
		APIKeyManager:   mgr,
		PersonaRegistry: &mockPersonaRegistry{},
		BoundPrincipals: &fakeBoundPrincipals{err: errors.New("database unavailable")},
	}, nil)

	w := serveKeyRequest(h, http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, w.Code)
}

// TestCreateAuthKeyChecksTheExpiry holds the two ways a requested lifetime is
// refused, each in its own words rather than as a generic bad request.
func TestCreateAuthKeyChecksTheExpiry(t *testing.T) {
	for _, tt := range []struct{ name, body, detail string }{
		{"unparseable", `{"name":"k","roles":["admin"],"expires_in":"soon"}`, "invalid expires_in duration"},
		{"not in the future", `{"name":"k","roles":["admin"],"expires_in":"-1h"}`, "expires_in must be a positive duration"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(Deps{
				APIKeyManager:   &mockAPIKeyManager{},
				APIKeyStore:     &mockAPIKeyStore{},
				PersonaRegistry: &mockPersonaRegistry{},
				ConfigStore:     &mockConfigStore{mode: "database"},
			}, nil)

			w := serveKeyRequest(h, http.MethodPost, "", tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, tt.detail)
		})
	}
}

// TestCreateAuthKeyRefusesTheSelfIssuedNamespace holds that an administrator
// cannot give a key bound to nobody the name of a key somebody issued for
// themselves: its apparent owner could neither see nor revoke it, and it would
// take the name they chose.
func TestCreateAuthKeyRefusesTheSelfIssuedNamespace(t *testing.T) {
	store := &mockAPIKeyStore{}
	h := NewHandler(Deps{
		APIKeyManager:   &mockAPIKeyManager{},
		APIKeyStore:     store,
		PersonaRegistry: &mockPersonaRegistry{},
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	w := serveKeyRequest(h, http.MethodPost, "",
		`{"name":"user:analyst@example.com:laptop","roles":["admin"]}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, apikeyissue.SelfIssuedPrefix)
	assert.Empty(t, store.createCalls, "a key was stored in the reserved namespace")
}
