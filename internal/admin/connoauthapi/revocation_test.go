package connoauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// recordingClearer records which connections were forgotten, and can fail.
type recordingClearer struct {
	cleared []string
	err     error
}

func (c *recordingClearer) Clear(_ context.Context, kind, name string) error {
	if c.err != nil {
		return c.err
	}
	c.cleared = append(c.cleared, kind+"/"+name)
	return nil
}

// connectThrough drives the real oauth-start then callback pair against a
// token endpoint that issues a credential, and returns the callback's status.
func connectThrough(t *testing.T, h *seamMux) int {
	t.Helper()
	startReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/oauth-start",
		strings.NewReader(`{"return_url":"/portal/admin/connections"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Host = "localhost:8080"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code, "start body=%s", startW.Body.String())
	var startResp startConnectionOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	cbReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/admin/oauth/callback?code=test-code&state="+url.QueryEscape(startResp.State), http.NoBody)
	cbReq.Host = "localhost:8080"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)
	return cbW.Code
}

// revocationFixture mounts the OAuth routes with a clearer wired.
func revocationFixture(t *testing.T, clearer RevocationClearer) *seamMux {
	t.Helper()
	tokenSrv := fakeIDPServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"Bearer"}`))
	})
	kind := &fakeOAuthKindHandler{parseCfg: connoauth.Config{
		AuthorizationURL:  "https://idp.example/authorize",
		TokenURL:          tokenSrv.URL + "/token",
		ClientID:          "test-client",
		ClientSecret:      "test-secret",
		EndpointAuthStyle: oauth2.AuthStyleInHeader,
	}}
	connStore := &mockConnectionStore{getResult: &platform.ConnectionInstance{
		Kind: connoauth.KindMCP, Name: "alpha",
		Config: map[string]any{
			"endpoint":                "http://upstream/mcp",
			"auth_mode":               "oauth",
			"oauth_grant":             "authorization_code",
			"oauth_authorization_url": "https://idp.example/authorize",
			"oauth_token_url":         tokenSrv.URL + "/token",
			"oauth_client_id":         "test-client",
			"oauth_client_secret":     "test-secret",
		},
	}}
	pkce := pkcestore.NewMemoryStore()
	t.Cleanup(func() { _ = pkce.Close() })
	return testMux(Config{
		Connections: connStore,
		PKCEStore:   pkce,
		Tokens:      connoauth.NewMemoryStore(),
		Kinds:       OAuthKindHandlers{connoauth.KindMCP: kind},
		Revocations: clearer,
	})
}

// TestConnectForgetsTheOpenRevocation is what makes the NEXT revocation news:
// the alert is announced once per open revocation, so authorizing a connection
// again has to close the one it had.
func TestConnectForgetsTheOpenRevocation(t *testing.T) {
	clearer := &recordingClearer{}
	h := revocationFixture(t, clearer)

	require.Equal(t, http.StatusFound, connectThrough(t, h))
	assert.Equal(t, []string{"mcp/alpha"}, clearer.cleared)
}

// TestConnectSurvivesAFailedClear pins the priority: the credential is
// persisted either way, and an operator who has just reconnected must not be
// sent back through the browser flow because a bookkeeping row would not
// delete.
func TestConnectSurvivesAFailedClear(t *testing.T) {
	h := revocationFixture(t, &recordingClearer{err: errors.New("db down")})
	assert.Equal(t, http.StatusFound, connectThrough(t, h))
}

// TestConnectWithoutAClearer covers the deployment that wires none.
func TestConnectWithoutAClearer(t *testing.T) {
	h := revocationFixture(t, nil)
	assert.Equal(t, http.StatusFound, connectThrough(t, h))
}
