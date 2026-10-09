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

	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// fakeSecretScopes is the secret store as the admin save reads it: scope,
// never value.
type fakeSecretScopes map[string]secretstore.Secret

func (f fakeSecretScopes) Get(_ context.Context, name string) (secretstore.Secret, error) {
	if name == "down" {
		return secretstore.Secret{}, errors.New("database down")
	}
	sec, ok := f[name]
	if !ok {
		return secretstore.Secret{}, secretstore.ErrNotFound
	}
	return sec, nil
}

// issueSecrets holds the issue's PAT (#2066), allowed only on "tableau", and
// two that the save refuses for their scope.
var issueSecrets = fakeSecretScopes{
	"tableau-rest": {Name: "tableau-rest", AllowConnections: []string{"tableau"}},
	"elsewhere":    {Name: "elsewhere", AllowConnections: []string{"other"}},
	"finance-only": {Name: "finance-only", AllowConnections: []string{"tableau"}, AllowPersonas: []string{"finance"}},
}

// tableauConnection is the issue's session_login connection.
const tableauConnection = `{"config":{
	"base_url":"https://tableau.example.com/api/3.22",
	"auth_mode":"session_login",
	"session_login_url":"auth/signin",
	"session_login_body":"{\"credentials\":{\"personalAccessTokenName\":\"plexara-rest\",\"personalAccessTokenSecret\":\"{{secret:tableau-rest}}\",\"site\":{\"contentUrl\":\"acme\"}}}",
	"session_token_source":"body:credentials.token",
	"session_token_header":"X-Tableau-Auth"}}`

// detailOf is a refusal's problem detail, as an operator reads it.
func detailOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Detail string `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	return p.Detail
}

func secretsHandler(store *mockConnectionStore, secrets SecretScopes) *Handler {
	h := connTestHandler(store, true)
	h.deps.Secrets = secrets
	return h
}

func TestSaveStoresASecretReferenceAsWrittenAndReadsItBack(t *testing.T) {
	store := &mockConnectionStore{}
	h := secretsHandler(store, issueSecrets)

	w := putConnection(t, h, "tableau", tableauConnection)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, store.setCalls, 1)
	stored := store.setCalls[0].Config
	assert.Contains(t, stored["session_login_body"], "{{secret:tableau-rest}}", "stored as written, never resolved")

	// A credential that is only a reference reads back as written, not as
	// [REDACTED]: it names a secret and holds none.
	w = putConnection(t, h, "tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer",
		"credential":"{{secret:tableau-rest}}","static_headers":{"X-Sub":"{{secret:tableau-rest}}","X-Plain":"literal"}}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var inst platform.ConnectionInstance
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &inst))
	assert.Equal(t, "{{secret:tableau-rest}}", inst.Config["credential"])
	headers, _ := inst.Config["static_headers"].(map[string]any)
	assert.Equal(t, "{{secret:tableau-rest}}", headers["X-Sub"])
	assert.Equal(t, redactedValue, headers["X-Plain"], "a literal value is still redacted")
}

func TestSaveRefusesAnUnusableSecretReference(t *testing.T) {
	cases := map[string]struct {
		name, body, want string
	}{
		"not allowed on the connection": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{secret:elsewhere}}"}}`,
			`field credential: secret "elsewhere" may not be used by connection "tableau"; it is allowed on other`,
		},
		"missing": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{secret:nope}}"}}`,
			`field credential names secret "nope", which does not exist`,
		},
		"limited to personas": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{secret:finance-only}}"}}`,
			"clear its allow_personas",
		},
		"a field nothing fills": {
			"tableau", `{"config":{"base_url":"https://t.example.com/{{secret:tableau-rest}}","auth_mode":"none"}}`,
			"field base_url names a stored secret, which a api connection does not fill there",
		},
		"malformed": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{secret:Tableau}}"}}`,
			"not {{secret:<name>}}",
		},
		"a one-time code": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{totp:tableau-rest}}"}}`,
			"filled only in an api call's request",
		},
		"unreadable": {
			"tableau", `{"config":{"base_url":"https://t.example.com","auth_mode":"bearer","credential":"{{secret:down}}"}}`,
			"could not be read",
		},
		"the issue's body on another connection": {
			"other", tableauConnection,
			`secret "tableau-rest" may not be used by connection "other"`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store := &mockConnectionStore{}
			w := putConnection(t, secretsHandler(store, issueSecrets), c.name, c.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, detailOf(t, w), c.want)
			assert.Empty(t, store.setCalls, "nothing was stored")
		})
	}
}

func TestSaveRefusesAReferenceWithoutASecretStore(t *testing.T) {
	store := &mockConnectionStore{}
	w := putConnection(t, secretsHandler(store, nil), "tableau", tableauConnection)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, detailOf(t, w), "stored secrets are not available")
}

// The portal sends a stored session_login_secret back as [REDACTED]. When the
// body now names a stored secret instead, that carried value is dropped
// rather than merged back and refused (#2066).
func TestSaveDropsACarriedSessionSecret(t *testing.T) {
	store := &mockConnectionStore{getResult: &platform.ConnectionInstance{Kind: "api", Name: "tableau", Config: map[string]any{
		"session_login_secret": "old-pat-value",
	}}}
	body := strings.Replace(tableauConnection, `"auth_mode":"session_login",`, `"auth_mode":"session_login","session_login_secret":"[REDACTED]",`, 1)
	w := putConnection(t, secretsHandler(store, issueSecrets), "tableau", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, store.setCalls[0].Config, "session_login_secret")

	// A new value typed beside a body with nowhere to put it is still refused.
	store = &mockConnectionStore{}
	body = strings.Replace(tableauConnection, `"auth_mode":"session_login",`, `"auth_mode":"session_login","session_login_secret":"typed-now",`, 1)
	w = putConnection(t, secretsHandler(store, issueSecrets), "tableau", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "so clear it")
}

func TestCheckSecretReferencesByKind(t *testing.T) {
	h := secretsHandler(&mockConnectionStore{}, fakeSecretScopes{
		"vendor-token": {Name: "vendor-token", AllowConnections: []string{"vendor"}},
	})
	ctx := context.Background()
	// An mcp connection is addressed by its connection_name.
	require.NoError(t, h.checkSecretReferences(ctx, connectionKindMCP, "instance", map[string]any{
		"credential": "{{secret:vendor-token}}", "connection_name": "vendor",
	}))
	err := h.checkSecretReferences(ctx, connectionKindMCP, "instance", map[string]any{"credential": "{{secret:vendor-token}}"})
	require.ErrorContains(t, err, `connection "instance"`)
	// A trino connection fills nothing.
	err = h.checkSecretReferences(ctx, connectionKindTrino, "vendor", map[string]any{"password": "{{secret:vendor-token}}"})
	require.ErrorContains(t, err, "none of a trino connection's fields")
	require.NoError(t, h.checkSecretReferences(ctx, connectionKindTrino, "vendor", map[string]any{"password": "plain"}))
}

// redactConnectionConfig shows a reference in a map[string]string too.
func TestRedactShowsReferencesInStringMaps(t *testing.T) {
	out := redactConnectionConfig(map[string]any{"static_headers": map[string]string{"A": "{{secret:x}}", "B": "v"}})
	headers, _ := out["static_headers"].(map[string]any)
	assert.Equal(t, "{{secret:x}}", headers["A"])
	assert.Equal(t, redactedValue, headers["B"])
}
