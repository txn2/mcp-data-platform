package connoauthapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// oauth-status reported the shape it resolved and nothing about the value it
// ignored, so a connection carrying both OAuth vocabularies read as fully
// configured while authenticating with a credential the operator never saw
// (#1682). The status is where an operator looks, so it says which vocabulary
// is live and which keys are inert.

func statusFor(t *testing.T, cfg map[string]any) connoauth.OAuthStatus {
	t.Helper()
	srv := fakeIDPServer(t, func(http.ResponseWriter, *http.Request) {})
	fx := setupOAuthFixture(t, srv)
	fx.connStore.getResult.Config = cfg

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connections/mcp/alpha/oauth-status", http.NoBody)
	w := httptest.NewRecorder()
	fx.handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var status connoauth.OAuthStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&status))
	return status
}

func TestConnectionOAuthStatus_ReportsAMixedVocabularyAndTheShadowedKeys(t *testing.T) {
	status := statusFor(t, map[string]any{
		"endpoint":            "http://upstream/mcp",
		"auth_mode":           "oauth",
		"oauth_grant":         "authorization_code",
		"oauth_client_id":     "PENDING-REPLACE-ME",
		"oauth_client_secret": "canonical-secret",
		"oauth2_client_id":    "986495125425.apps.googleusercontent.com",
		"oauth2_scopes":       []any{"analytics.readonly"},
	})

	assert.Equal(t, connoauth.VocabularyMixed, status.ConfigVocabulary)
	assert.Equal(t, []string{"oauth2_client_id"}, status.ShadowedConfigKeys)
}

func TestConnectionOAuthStatus_ReportsACanonicalVocabularyWithNothingShadowed(t *testing.T) {
	status := statusFor(t, map[string]any{
		"endpoint":            "http://upstream/mcp",
		"auth_mode":           "oauth",
		"oauth_grant":         "authorization_code",
		"oauth_client_id":     "test-client",
		"oauth_client_secret": "test-secret",
	})

	assert.Equal(t, connoauth.VocabularyCanonical, status.ConfigVocabulary)
	assert.Empty(t, status.ShadowedConfigKeys)
}

func TestConnectionOAuthStatus_ReportsALegacyVocabulary(t *testing.T) {
	status := statusFor(t, map[string]any{
		"endpoint":             "http://upstream/mcp",
		"auth_mode":            "oauth2_authorization_code",
		"oauth2_client_id":     "test-client",
		"oauth2_client_secret": "test-secret",
	})

	assert.Equal(t, connoauth.VocabularyLegacy, status.ConfigVocabulary)
	assert.Empty(t, status.ShadowedConfigKeys)
}
