package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// apiAuthCodeConfig builds a connection_instances row for an API
// gateway connection running the authorization_code grant. Tests
// overlay the authorization_url + token_url from local httptest
// servers so the exchange path can be exercised end-to-end.
func apiAuthCodeConfig(authURL, tokenURL string) map[string]any {
	return map[string]any{
		"base_url":                 "https://api.example.com",
		"connection_name":          "vendor",
		"auth_mode":                apigatewaykit.AuthModeOAuth2AuthorizationCode,
		"oauth2_token_url":         tokenURL,
		"oauth2_authorization_url": authURL,
		"oauth2_client_id":         "client-x",
		"oauth2_client_secret":     "secret-x",
		"oauth2_scopes":            []any{"read:users"},
		"connect_timeout":          "3s",
		"call_timeout":             "3s",
	}
}

// apiGatewayOAuthHandlerWithToolkit builds a Handler wired to a
// shared connoauth.Store so callback ingestion can be observed by
// reading the store. Each call gets its OWN PKCE store — there is no
// process global to share.
func apiGatewayOAuthHandlerWithToolkit(t *testing.T, store ConnectionStore) (*Handler, connoauth.Store) {
	t.Helper()
	tk := apigatewaykit.New("primary")
	tokenStore := connoauth.NewMemoryStore()
	tk.SetConnOAuthStore(tokenStore)
	t.Cleanup(func() { _ = tk.Close() })

	pkceStore := NewMemoryPKCEStore()
	t.Cleanup(func() { _ = pkceStore.Close() })

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		PKCEStore:       pkceStore,
		ConnOAuthStore:  tokenStore,
	}, nil)
	return h, tokenStore
}

func TestStartAPIGatewayOAuth_NotFound(t *testing.T) {
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{getErr: platform.ErrConnectionNotFound})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/missing/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStartAPIGatewayOAuth_RejectsClientCredentialsConnection(t *testing.T) {
	// A client_credentials connection must not be accepted by the
	// authorization_code start endpoint — the user-flow only makes
	// sense for grants that involve a browser redirect.
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "ccg",
			Config: map[string]any{
				"base_url":             "https://api.example.com",
				"connection_name":      "ccg",
				"auth_mode":            apigatewaykit.AuthModeOAuth2ClientCredentials,
				"oauth2_token_url":     "https://idp/token",
				"oauth2_client_id":     "id",
				"oauth2_client_secret": "sec",
				"connect_timeout":      "3s",
				"call_timeout":         "3s",
			},
		},
	}
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, store)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/ccg/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestStartAPIGatewayOAuth_NoPKCEStoreReturns503 covers the
// fail-fast guard. A handler built without Deps.PKCEStore must
// return a clear 503 rather than panic or silently fall back.
func TestStartAPIGatewayOAuth_NoPKCEStoreReturns503(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "vendor",
			Config: apiAuthCodeConfig("https://idp/auth", "https://idp/token"),
		},
	}
	tk := apigatewaykit.New("primary")
	tk.SetConnOAuthStore(connoauth.NewMemoryStore())
	t.Cleanup(func() { _ = tk.Close() })

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		// PKCEStore intentionally nil
	}, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/vendor/oauth-start", http.NoBody)
	req.Host = "platform.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "PKCE store not configured")
}

func TestStartAPIGatewayOAuth_ReturnsAuthorizationURL(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "vendor",
			Config: apiAuthCodeConfig("https://idp.example.com/authorize", "https://idp.example.com/token"),
		},
	}
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, store)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/vendor/oauth-start", http.NoBody)
	req.Host = "platform.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	u, err := url.Parse(resp.AuthorizationURL)
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "client-x", q.Get("client_id"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.NotEmpty(t, q.Get("code_challenge"))
	assert.Equal(t, resp.State, q.Get("state"))
	assert.Contains(t, q.Get("redirect_uri"), "/api/v1/admin/api-gateway/oauth/callback")
}

// TestAPIGatewayOAuthCallback_Success exercises the full
// start → callback → token-persisted path. Distinct from the unit
// tests because it proves PKCE state plumbed from start is actually
// the verifier that lands in the token-exchange POST body, and that
// the toolkit's TokenStore receives the exchanged tokens.
func TestAPIGatewayOAuthCallback_Success(t *testing.T) {
	var receivedForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600,"scope":"read:users"}`))
	}))
	t.Cleanup(tokenSrv.Close)

	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "vendor",
			Config: apiAuthCodeConfig("https://idp.example.com/authorize", tokenSrv.URL),
		},
	}
	h, tokenStore := apiGatewayOAuthHandlerWithToolkit(t, store)

	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/vendor/oauth-start", http.NoBody)
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code)
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	cbURL := "/api/v1/admin/api-gateway/oauth/callback?code=auth-code&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "platform.example.com"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)

	require.Equalf(t, http.StatusFound, cbW.Code, "expected redirect after exchange; body: %s", cbW.Body.String())
	assert.Contains(t, cbW.Header().Get("Location"), "/portal/admin/connections")

	stored, err := tokenStore.Get(context.Background(), connoauth.Key{Kind: connoauth.KindAPI, Name: "vendor"})
	require.NoError(t, err)
	assert.Equal(t, "acc", stored.AccessToken)
	assert.Equal(t, "ref", stored.RefreshToken)
	assert.Equal(t, "read:users", stored.Scope)

	assert.Equal(t, "auth-code", receivedForm.Get("code"))
	assert.NotEmpty(t, receivedForm.Get("code_verifier"))
	assert.Equal(t, "authorization_code", receivedForm.Get("grant_type"))
}

func TestAPIGatewayOAuthCallback_MissingState(t *testing.T) {
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/api-gateway/oauth/callback?code=abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing state")
}

func TestAPIGatewayOAuthCallback_UnknownState(t *testing.T) {
	// State that was never registered (e.g. an attacker fabricating
	// a callback) must be rejected. The error message is deliberately
	// uniform with the "expired" case so the handler does not leak
	// which condition fired.
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/api-gateway/oauth/callback?code=abc&state=does-not-exist", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid or expired state")
}

func TestPkceS256Challenge_Deterministic(t *testing.T) {
	a := pkceS256Challenge("verifier-x")
	b := pkceS256Challenge("verifier-x")
	assert.Equal(t, a, b)
	c := pkceS256Challenge("different-verifier")
	assert.NotEqual(t, a, c)
}

func TestJoinScopes(t *testing.T) {
	assert.Equal(t, "", joinScopes(nil))
	assert.Equal(t, "", joinScopes([]string{}))
	assert.Equal(t, "read:users", joinScopes([]string{"read:users"}))
	assert.Equal(t, "read:users write:orders openid",
		joinScopes([]string{"read:users", "write:orders", "openid"}))
}

func TestBuildAPIGatewayCallbackURL_HonorsForwardedHeaders(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/", http.NoBody)
	r.Host = "internal.svc"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "platform.example.com")
	got := buildAPIGatewayCallbackURL(r)
	assert.Equal(t,
		"https://platform.example.com/api/v1/admin/api-gateway/oauth/callback",
		got)
}

func TestBuildAPIGatewayAuthorizationURL_AppendsToExistingQuery(t *testing.T) {
	cfg := apigatewaykit.OAuth2Config{
		ClientID:         "id",
		AuthorizationURL: "https://idp.example/authorize?app=foo",
		Scopes:           []string{"read:users"},
		Prompt:           "login",
	}
	got := buildAPIGatewayAuthorizationURL(cfg, "state-x", "verifier-y", "https://platform.example.com/cb")
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "foo", q.Get("app"), "should preserve existing query")
	assert.Equal(t, "state-x", q.Get("state"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "read:users", q.Get("scope"))
	assert.Equal(t, "login", q.Get("prompt"))
}

func TestBuildAPIGatewayAuthorizationURL_OmitsEmptyOptionals(t *testing.T) {
	// No scopes and no prompt — neither parameter should appear on
	// the resulting URL. Some IdPs reject unknown empty parameters
	// with invalid_request, so the build must omit them entirely.
	cfg := apigatewaykit.OAuth2Config{
		ClientID:         "id",
		AuthorizationURL: "https://idp.example/authorize",
	}
	got := buildAPIGatewayAuthorizationURL(cfg, "state-x", "verifier-y", "https://platform.example.com/cb")
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()
	assert.False(t, q.Has("scope"), "scope must be omitted when no scopes configured")
	assert.False(t, q.Has("prompt"), "prompt must be omitted when not configured")
}

func TestExchangeAPIGatewayCode_NonOKReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.Error(t, err)
	// Body content must not bleed through — only the status-derived
	// message. (Bodies on error responses can include sensitive
	// material from misbehaving IdPs.)
	assert.NotContains(t, err.Error(), "invalid_grant",
		"exchangeAPIGatewayCode must NOT echo upstream error body content — "+
			"a misbehaving IdP could include sensitive material there")
}

func TestExchangeAPIGatewayCode_MalformedJSONReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(srv.Close)
	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed JSON")
}

func TestExchangeAPIGatewayCode_MissingAccessTokenReturnsError(t *testing.T) {
	// IdP returns 200 + valid JSON but no access_token field. Must
	// be rejected so the TokenStore is not populated with an empty
	// access_token (which would silently fail on the next API call
	// instead of surfacing the misconfiguration here).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"refresh_token":"r","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no access_token")
}

// TestExchangeAPIGatewayCode_OversizeBodyDetected proves the
// admin-side counterpart to the gateway's same-named test. A
// malicious or misbehaving IdP that streams more than
// maxCodeExchangeBodyBytes must be rejected with an explicit
// cap-exceeded error rather than silently parsing a truncated JSON
// document (which an attacker could exploit to feed
// attacker-controlled fields into the freshly-stored token row).
func TestExchangeAPIGatewayCode_OversizeBodyDetected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 2 MiB — twice the 1 MiB cap.
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2<<20))
	}))
	t.Cleanup(srv.Close)

	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds",
		"oversize body must surface as an explicit cap-exceeded error — "+
			"silently parsing a truncated JSON response would let a "+
			"malicious IdP inject attacker-controlled fields into the "+
			"freshly-stored token row")
}

// TestExchangeAPIGatewayCode_DoesNotFollowRedirects proves the
// admin-side counterpart to the gateway's same-named test. The
// admin POST carries client_secret + authorization_code +
// code_verifier — a misconfigured or compromised IdP that
// 3xx-redirects must NOT cause the platform to forward those
// credentials to the redirect target.
func TestExchangeAPIGatewayCode_DoesNotFollowRedirects(t *testing.T) {
	var attackerHits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "stolen",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(attacker.Close)

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, attacker.URL+"/token", http.StatusFound)
	}))
	t.Cleanup(idp.Close)

	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          idp.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code-x", "verifier-x", "https://cb")
	require.Error(t, err,
		"a 3xx response must surface as an error — the redirect target must not be hit")
	assert.Equal(t, int32(0), attackerHits.Load(),
		"redirect target must NEVER receive the credential-bearing POST. "+
			"Following redirects on the token endpoint would leak the "+
			"client_secret + authorization_code + code_verifier to the redirect target")
}

// TestExchangeAPIGatewayCode_CapturesRefreshExpiresIn proves a
// Keycloak-style refresh_expires_in is parsed from the response
// and surfaces on apiGatewayExchangeResult.RefreshExpiresAt — so
// the callback handler can populate PersistedToken.RefreshExpiresAt
// and the auth.go refresh path can short-circuit dead-refresh
// attempts. Without this, the field stays zero and the auth-side
// expiry check is dead code.
func TestExchangeAPIGatewayCode_CapturesRefreshExpiresIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":300,"refresh_expires_in":1800}`))
	}))
	t.Cleanup(srv.Close)
	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "sec",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleHeader,
	}
	got, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.NoError(t, err)
	if got.RefreshExpiresAt.IsZero() {
		t.Fatal("RefreshExpiresAt is zero — refresh_expires_in dropped during decode")
	}
	// 30 min ± 5s window allows for test scheduling jitter.
	deltaSec := got.RefreshExpiresAt.Sub(got.Expiry).Seconds()
	assert.InDelta(t, 1500.0, deltaSec, 5.0,
		"RefreshExpiresAt should be ~1500s after Expiry (1800 refresh - 300 access)")
}

// TestStartAPIGatewayOAuth_OpenRedirectGuard verifies that a
// malicious return_url is rewritten by safeReturnURL on the
// callback path. Without this guard, an admin (or someone with
// admin-session XSRF) could register `return_url:
// "https://evil.example/x"` via oauth-start and the IdP redirect
// would bounce the operator's browser there.
func TestAPIGatewayOAuthCallback_RejectsExternalReturnURL(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: apigatewaykit.Kind, Name: "vendor",
			Config: apiAuthCodeConfig("https://idp.example.com/authorize", tokenSrv.URL),
		},
	}
	h, _ := apiGatewayOAuthHandlerWithToolkit(t, store)

	// Step 1: oauth-start with a malicious return_url.
	body := bytes.NewBufferString(`{"return_url":"https://evil.example/x"}`)
	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/api-gateway/connections/vendor/oauth-start", body)
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code)
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	// Step 2: callback. The redirect must NOT go to evil.example.
	cbURL := "/api/v1/admin/api-gateway/oauth/callback?code=auth-code&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "platform.example.com"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)

	require.Equal(t, http.StatusFound, cbW.Code)
	loc := cbW.Header().Get("Location")
	assert.NotContains(t, loc, "evil.example",
		"callback redirect must NOT bounce to operator-supplied external URL")
	assert.Equal(t, "/portal/admin/connections", loc,
		"safeReturnURL should fall back to the constant on rejection")
}

func TestExchangeAPIGatewayCode_AuthStyleParams(t *testing.T) {
	// EndpointAuthStyle "params" puts client_secret in the form body
	// instead of HTTP Basic auth. Verify both that the secret reaches
	// the form AND that no Authorization header is sent (some IdPs
	// reject having both).
	var sawAuthHeader bool
	var sawClientSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization") != ""
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		sawClientSecret = form.Get("client_secret")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	cfg := apigatewaykit.OAuth2Config{
		TokenURL:          srv.URL + "/token",
		ClientID:          "id",
		ClientSecret:      "in-body-secret",
		EndpointAuthStyle: apigatewaykit.OAuth2AuthStyleParams,
	}
	_, err := exchangeAPIGatewayCode(context.Background(), cfg, "code", "verifier", "https://cb")
	require.NoError(t, err)
	assert.False(t, sawAuthHeader, "Authorization header must not be sent in params style")
	assert.Equal(t, "in-body-secret", sawClientSecret)
}
