package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// failingPKCEStore wraps a real in-memory PKCE store but forces Put to
// return a fixed error. Take and Close delegate to the embedded
// MemoryStore, so the wrapper still satisfies pkcestore.Store while
// letting tests drive the oauth-start persist-failure branch that
// pkcestore.NewMemoryStore can never reach (its Put never errors).
type failingPKCEStore struct {
	*pkcestore.MemoryStore
	putErr error
}

func (f *failingPKCEStore) Put(_ context.Context, _ string, _ *pkcestore.State) error {
	return f.putErr
}

// Verify the wrapper is a drop-in for the Store the handler depends on.
var _ pkcestore.Store = (*failingPKCEStore)(nil)

// TestStartGatewayOAuth_PKCEPutErrorReturns500 drives the persist-
// failure branch in startGatewayOAuth: when the PKCE store's Put
// returns an error, the handler must not hand the operator an
// authorization URL whose state can never be redeemed. Instead it logs
// the failure and returns HTTP 500 so the UI surfaces a retriable
// error rather than silently minting a dead flow.
func TestStartGatewayOAuth_PKCEPutErrorReturns500(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor",
			Config: authCodeConnectionConfig("https://auth.example.com/authorize", "https://auth.example.com/token"),
		},
	}
	failing := &failingPKCEStore{
		MemoryStore: pkcestore.NewMemoryStore(),
		putErr:      errors.New("pkce backend unavailable"),
	}
	t.Cleanup(func() { _ = failing.Close() })

	tk := gatewaykit.New("primary")
	tk.SetConnOAuthStore(connoauth.NewMemoryStore())
	t.Cleanup(func() { _ = tk.Close() })
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		PKCEStore:       failing,
		ConnOAuthStore:  connoauth.NewMemoryStore(),
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", http.NoBody)
	req.Host = "platform.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to record OAuth state",
		"a PKCE persist failure must surface as 500 so the UI does not open a dead authorization URL")
}

// TestStartConnectionOAuth_PKCEPutErrorReturns500 is the unified
// connection-flow counterpart: startConnectionOAuth must reject with
// 500 when the PKCE store's Put fails, before any authorization URL or
// ConnectStarted audit event is emitted.
func TestStartConnectionOAuth_PKCEPutErrorReturns500(t *testing.T) {
	fake := &fakeOAuthKindHandler{
		parseCfg: connoauth.Config{
			AuthorizationURL: "https://idp.example/authorize",
			TokenURL:         "https://idp.example/token",
			ClientID:         "test-client",
			ClientSecret:     "test-secret",
		},
	}
	connStore := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: connoauth.KindMCP, Name: "alpha",
			Config: map[string]any{
				"auth_mode":   "oauth",
				"oauth_grant": "authorization_code",
			},
		},
	}
	failing := &failingPKCEStore{
		MemoryStore: pkcestore.NewMemoryStore(),
		putErr:      errors.New("pkce backend unavailable"),
	}
	t.Cleanup(func() { _ = failing.Close() })

	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
		PKCEStore:       failing,
		ConnOAuthStore:  connoauth.NewMemoryStore(),
		OAuthKinds:      OAuthKindHandlers{connoauth.KindMCP: fake},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/connections/mcp/alpha/oauth-start", http.NoBody)
	req.Host = "platform.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to record OAuth state")
}

// TestGatewayOAuthCallback_ReturnURLRewriteLogged exercises the
// safeReturnURL rewrite branch in gatewayOAuthCallback: when the
// pending PKCE state carries an off-origin ReturnURL, the successful
// callback must redirect to the safe fallback (not the attacker URL)
// and take the "returnURL rewritten" warn branch. Proves the
// open-redirect guard fires end-to-end through the real handler.
func TestGatewayOAuthCallback_ReturnURLRewriteLogged(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor",
			Config: authCodeConnectionConfig("https://auth.example.com/authorize", tokenSrv.URL),
		},
	}
	h, _ := gatewayOAuthHandlerWithToolkit(t, store)

	// Step 1: oauth-start with an off-origin return_url that
	// safeReturnURL must rewrite.
	startBody := bytes.NewBufferString(`{"return_url":"https://evil.example/x"}`)
	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", startBody)
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code, "start body=%s", startW.Body.String())
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	// Step 2: successful callback — reaches the redirect, and the
	// rewrite branch because pending.ReturnURL != dest.
	cbURL := "/api/v1/admin/oauth/callback?code=auth-code&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "platform.example.com"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)

	require.Equalf(t, http.StatusFound, cbW.Code, "expected redirect; body: %s", cbW.Body.String())
	loc := cbW.Header().Get("Location")
	assert.NotContains(t, loc, "evil.example",
		"the callback must never bounce to the operator-supplied off-origin URL")
	assert.Equal(t, "/portal/admin/connections", loc,
		"safeReturnURL must fall back to the constant when the return_url is rewritten")
}

// TestAPIGatewayOAuthCallback_UpstreamError drives the upstream-error
// branch of the API gateway callback: when the IdP redirects back with
// an `error` query parameter for a valid pending state, the handler
// must render the HTML error page (HTTP 400) echoing the upstream error
// code, without attempting a token exchange.
func TestAPIGatewayOAuthCallback_UpstreamError(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "vendor",
			Config: apiAuthCodeConfig("https://idp.example.com/authorize", "https://idp.example.com/token"),
		},
	}
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, store)

	// Step 1: oauth-start to seed a valid pending PKCE state.
	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/vendor/oauth-start", http.NoBody)
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code, "start body=%s", startW.Body.String())
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	// Step 2: IdP redirects back with an error instead of a code.
	cbURL := "/api/v1/admin/api-gateway/oauth/callback?error=access_denied&error_description=nope&state=" +
		url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "platform.example.com"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)

	assert.Equal(t, http.StatusBadRequest, cbW.Code)
	assert.Contains(t, cbW.Body.String(), "upstream OAuth error: access_denied",
		"an upstream error callback must render the error page, not attempt a token exchange")
}
