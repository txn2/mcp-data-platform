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

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Migration 000050 canonicalized the OAuth config keys that existed when it
// ran, and nothing stopped a later write from putting the legacy spelling
// back. A row holding both authenticated with the canonical value while the
// portal displayed the operator's own entry beside it as an equal, and every
// call failed with the connection reading as fully configured (#1682). The
// write boundary is where that is settled.

func putConnection(t *testing.T, h *Handler, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/api/"+name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSetConnectionInstance_CanonicalizesTheLegacyOAuthVocabulary(t *testing.T) {
	store := &mockConnectionStore{}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "analytics", `{"config":{
		"base_url":"https://analytics.example.com",
		"auth_mode":"oauth2_authorization_code",
		"oauth2_authorization_url":"https://idp.example.com/auth",
		"oauth2_token_url":"https://idp.example.com/token",
		"oauth2_client_id":"platform-client",
		"oauth2_client_secret":"shh",
		"oauth2_scopes":["analytics.readonly"],
		"oauth2_endpoint_auth_style":"params"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Len(t, store.setCalls, 1)
	stored := store.setCalls[0].Config
	assert.Equal(t, "oauth", stored["auth_mode"])
	assert.Equal(t, "authorization_code", stored["oauth_grant"])
	assert.Equal(t, "https://idp.example.com/token", stored["oauth_token_url"])
	assert.Equal(t, "https://idp.example.com/auth", stored["oauth_authorization_url"])
	assert.Equal(t, "platform-client", stored["oauth_client_id"])
	assert.Equal(t, "shh", stored["oauth_client_secret"])
	assert.Equal(t, "analytics.readonly", stored["oauth_scope"])
	assert.Equal(t, "params", stored["oauth_endpoint_auth_style"])
	for _, legacy := range []string{
		"oauth2_authorization_url", "oauth2_token_url", "oauth2_client_id",
		"oauth2_client_secret", "oauth2_scopes", "oauth2_endpoint_auth_style",
	} {
		assert.NotContains(t, stored, legacy, "the legacy key was persisted alongside the canonical one")
	}
}

func TestSetConnectionInstance_RefusesDisagreeingOAuthVocabularies(t *testing.T) {
	store := &mockConnectionStore{}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "analytics", `{"config":{
		"base_url":"https://analytics.example.com",
		"auth_mode":"oauth",
		"oauth_grant":"authorization_code",
		"oauth_token_url":"https://idp.example.com/token",
		"oauth_client_id":"PENDING-REPLACE-ME",
		"oauth_client_secret":"shh",
		"oauth2_client_id":"986495125425.apps.googleusercontent.com"}}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Contains(t, pd.Detail, "oauth_client_id")
	assert.Contains(t, pd.Detail, "oauth2_client_id")
	// The refusal names keys, never values: one of the pairs is a secret.
	assert.NotContains(t, pd.Detail, "PENDING-REPLACE-ME")
	assert.NotContains(t, pd.Detail, "986495125425")
	assert.Empty(t, store.setCalls, "a refused write reached the store")
}

func TestSetConnectionInstance_ResolvesARedactedSecretAcrossVocabularies(t *testing.T) {
	// The stored row is legacy, the editor now speaks canonical, and the
	// secret arrives as the placeholder. Without canonicalizing the stored
	// config for the merge, the literal "[REDACTED]" would be saved as the
	// client secret and every call would fail with an invalid_client.
	store := &mockConnectionStore{getResult: &platform.ConnectionInstance{
		Kind: "api", Name: "analytics",
		Config: map[string]any{
			"base_url":             "https://analytics.example.com",
			"auth_mode":            "oauth2_authorization_code",
			"oauth2_token_url":     "https://idp.example.com/token",
			"oauth2_client_id":     "platform-client",
			"oauth2_client_secret": "the-real-secret",
		},
	}}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "analytics", `{"config":{
		"base_url":"https://analytics.example.com",
		"auth_mode":"oauth",
		"oauth_grant":"authorization_code",
		"oauth_authorization_url":"https://idp.example.com/auth",
		"oauth_token_url":"https://idp.example.com/token",
		"oauth_client_id":"platform-client",
		"oauth_client_secret":"[REDACTED]"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Len(t, store.setCalls, 1)
	assert.Equal(t, "the-real-secret", store.setCalls[0].Config["oauth_client_secret"])
}

func TestSetConnectionInstance_NamesTheCanonicalModeAndGrantInAValidationRefusal(t *testing.T) {
	// The refusal an operator of an authorization_code connection reads used
	// to name the client_credentials mode, which sent them looking for a
	// misconfiguration that did not exist (#1681).
	store := &mockConnectionStore{}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "analytics", `{"config":{
		"base_url":"https://analytics.example.com",
		"auth_mode":"oauth",
		"oauth_grant":"authorization_code",
		"oauth_authorization_url":"https://idp.example.com/auth",
		"oauth_token_url":"https://idp.example.com/token",
		"oauth_client_secret":"shh"}}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Contains(t, pd.Detail, "oauth_client_id is required")
	assert.Contains(t, pd.Detail, `auth_mode is "oauth"`)
	assert.Contains(t, pd.Detail, `oauth_grant is "authorization_code"`)
	assert.NotContains(t, pd.Detail, "oauth2_client_credentials")
}

func TestSetConnectionInstance_CanonicalConfigSurvivesTheRoundTrip(t *testing.T) {
	// The shape the ticket reports as unrenderable in the portal, saved and
	// read back: what the editor writes is what the GET returns, so a second
	// save is not a second vocabulary.
	store := &mockConnectionStore{}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "google-analytics", `{"config":{
		"base_url":"https://analyticsdata.googleapis.com",
		"auth_mode":"oauth",
		"oauth_grant":"authorization_code",
		"oauth_authorization_url":"https://accounts.google.com/o/oauth2/v2/auth",
		"oauth_token_url":"https://oauth2.googleapis.com/token",
		"oauth_client_id":"986495125425.apps.googleusercontent.com",
		"oauth_client_secret":"shh",
		"oauth_scope":"https://www.googleapis.com/auth/analytics.readonly",
		"oauth_endpoint_auth_style":"params",
		"oauth_prompt":"consent"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got platform.ConnectionInstance
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "oauth", got.Config["auth_mode"])
	assert.Equal(t, "authorization_code", got.Config["oauth_grant"])
	assert.Equal(t, "consent", got.Config["oauth_prompt"])
	assert.Equal(t, redactedValue, got.Config["oauth_client_secret"])
}

func TestSetConnectionInstance_ResolvesARedactedSecretAgainstAnUnreconcilableStoredRow(t *testing.T) {
	// A stored row can hold the two vocabularies with values that disagree --
	// that is the row #1682 was reported from, written before the write
	// boundary refused it. It cannot be canonicalized without discarding one
	// of them, so the merge reads it as it stands rather than failing the
	// save: the operator is re-saving to fix exactly that row.
	store := &mockConnectionStore{getResult: &platform.ConnectionInstance{
		Kind: "api", Name: "analytics",
		Config: map[string]any{
			"base_url":            "https://analytics.example.com",
			"auth_mode":           "oauth",
			"oauth_grant":         "authorization_code",
			"oauth_client_id":     "PENDING-REPLACE-ME",
			"oauth_client_secret": "the-real-secret",
			"oauth2_client_id":    "986495125425.apps.googleusercontent.com",
		},
	}}
	h := connTestHandler(store, true)

	w := putConnection(t, h, "analytics", `{"config":{
		"base_url":"https://analytics.example.com",
		"auth_mode":"oauth",
		"oauth_grant":"authorization_code",
		"oauth_authorization_url":"https://idp.example.com/auth",
		"oauth_token_url":"https://idp.example.com/token",
		"oauth_client_id":"986495125425.apps.googleusercontent.com",
		"oauth_client_secret":"[REDACTED]"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Len(t, store.setCalls, 1)
	stored := store.setCalls[0].Config
	assert.Equal(t, "the-real-secret", stored["oauth_client_secret"])
	// The save is what clears the collision: one vocabulary is written back.
	assert.Equal(t, "986495125425.apps.googleusercontent.com", stored["oauth_client_id"])
	assert.NotContains(t, stored, "oauth2_client_id")
}
