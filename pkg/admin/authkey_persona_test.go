package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// keyPersonaDeps wires the key routes to a real persona registry and the real
// role mapper the MCP authorizer uses, so a test's verdict on a key is the one
// a tools/list call with that key would reach (#1705).
func keyPersonaDeps(t *testing.T, mgr *mockAPIKeyManager, personas ...*persona.Persona) Deps {
	t.Helper()
	reg := persona.NewRegistry()
	for _, p := range personas {
		require.NoError(t, reg.Register(p))
	}
	return Deps{
		// The create route is registered only when keys can be persisted.
		ConfigStore:     &mockConfigStore{mode: "database"},
		APIKeyManager:   mgr,
		APIKeyStore:     &mockAPIKeyStore{},
		PersonaRegistry: reg,
		PersonaResolver: &persona.OIDCRoleMapper{
			PersonaMapping: map[string]string{"sso_admins": "admin"},
			Registry:       reg,
		},
	}
}

var (
	keyTestAdmin   = &persona.Persona{Name: "admin", Roles: []string{"dp_admin"}, Priority: 10}
	keyTestAnalyst = &persona.Persona{Name: "analyst", Roles: []string{"dp_analyst"}}
)

func createKey(t *testing.T, h http.Handler, body string) (int, authKeyCreateResponse) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp authKeyCreateResponse
	if w.Code == http.StatusCreated {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return w.Code, resp
}

func TestCreateAuthKey_PersonaResolution(t *testing.T) {
	t.Run("a role no persona carries is created with a warning naming the granting roles", func(t *testing.T) {
		h := NewHandler(keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin, keyTestAnalyst), nil)

		code, resp := createKey(t, h, `{"name":"ci","roles":["nobody-has-this"]}`)

		require.Equal(t, http.StatusCreated, code)
		assert.Equal(t, "generated-key-value", resp.Key)
		assert.Empty(t, resp.Persona)
		require.Len(t, resp.Warnings, 1)
		assert.Equal(t,
			`No persona carries any of the roles "nobody-has-this", so this key authenticates and lists no tools. `+
				`Roles the personas carry: "dp_admin", "dp_analyst", "sso_admins".`,
			resp.Warnings[0])
	})

	t.Run("a persona's name typed as a role says so and names that persona's roles", func(t *testing.T) {
		h := NewHandler(keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin), nil)

		code, resp := createKey(t, h, `{"name":"ci","roles":["admin"]}`)

		require.Equal(t, http.StatusCreated, code)
		require.Len(t, resp.Warnings, 1)
		assert.Contains(t, resp.Warnings[0], `"admin" is the name of a persona, not one of its roles; that persona carries "dp_admin".`)
	})

	t.Run("a persona carrying no roles is named as unreachable", func(t *testing.T) {
		h := NewHandler(keyPersonaDeps(t, &mockAPIKeyManager{}, &persona.Persona{Name: "orphan"}), nil)

		_, resp := createKey(t, h, `{"name":"ci","roles":["orphan"]}`)

		require.Len(t, resp.Warnings, 1)
		assert.Contains(t, resp.Warnings[0], "This deployment defines no persona that carries a role.")
		assert.Contains(t, resp.Warnings[0], "that persona carries no roles, so no key reaches it.")
	})

	t.Run("a role a persona carries names the persona and warns of nothing", func(t *testing.T) {
		h := NewHandler(keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin, keyTestAnalyst), nil)

		code, resp := createKey(t, h, `{"name":"ci","roles":["dp_analyst"]}`)

		require.Equal(t, http.StatusCreated, code)
		assert.Equal(t, "analyst", resp.Persona)
		assert.Empty(t, resp.Warnings)
	})

	t.Run("an explicitly mapped role resolves the way the authorizer resolves it", func(t *testing.T) {
		h := NewHandler(keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin, keyTestAnalyst), nil)

		_, resp := createKey(t, h, `{"name":"ci","roles":["dp_analyst","sso_admins"]}`)

		assert.Equal(t, "admin", resp.Persona)
		assert.Empty(t, resp.Warnings)
	})

	t.Run("with no persona registry the warning still names the granting roles", func(t *testing.T) {
		deps := keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin)
		deps.PersonaRegistry = nil
		h := NewHandler(deps, nil)

		_, resp := createKey(t, h, `{"name":"ci","roles":["admin"]}`)

		require.Len(t, resp.Warnings, 1)
		assert.Equal(t,
			`No persona carries any of the roles "admin", so this key authenticates and lists no tools. Roles the personas carry: "dp_admin", "sso_admins".`,
			resp.Warnings[0])
	})

	t.Run("with no resolver wired nothing is claimed about the key", func(t *testing.T) {
		deps := keyPersonaDeps(t, &mockAPIKeyManager{}, keyTestAdmin)
		deps.PersonaResolver = nil
		h := NewHandler(deps, nil)

		code, resp := createKey(t, h, `{"name":"ci","roles":["nobody-has-this"]}`)

		require.Equal(t, http.StatusCreated, code)
		assert.Empty(t, resp.Persona)
		assert.Empty(t, resp.Warnings)
	})
}

func TestListAuthKeys_PersonaResolution(t *testing.T) {
	mgr := &mockAPIKeyManager{keys: []auth.APIKeySummary{
		{Name: "good", Roles: []string{"dp_analyst"}, Source: "database"},
		{Name: "typo", Roles: []string{"admin"}, Source: "file"},
	}}
	h := NewHandler(keyPersonaDeps(t, mgr, keyTestAdmin, keyTestAnalyst), nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Decoded as raw JSON: the embedded summary's fields have to sit beside
	// persona and no_persona at one level, as the portal reads them.
	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Len(t, body.Keys, 2)

	assert.Equal(t, "good", body.Keys[0]["name"])
	assert.Equal(t, "database", body.Keys[0]["source"])
	assert.Equal(t, "analyst", body.Keys[0]["persona"])
	_, flagged := body.Keys[0]["no_persona"]
	assert.False(t, flagged, "a key that reaches a persona carries no no_persona flag")

	assert.Equal(t, "typo", body.Keys[1]["name"])
	assert.Equal(t, true, body.Keys[1]["no_persona"])
	_, named := body.Keys[1]["persona"]
	assert.False(t, named, "a key that reaches no persona names none")
}
