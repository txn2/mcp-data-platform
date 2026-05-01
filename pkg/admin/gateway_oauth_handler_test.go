package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// authCodeConnectionConfig builds a stored connection_instances row for
// an authorization_code-grant gateway connection. Tests overlay the
// upstream's authorization/token URLs from spun-up httptest servers.
func authCodeConnectionConfig(authURL, tokenURL string) map[string]any {
	return map[string]any{
		"endpoint":                "https://upstream.example.com/mcp",
		"connection_name":         "vendor",
		"auth_mode":               gatewaykit.AuthModeOAuth,
		"oauth_grant":             gatewaykit.OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": authURL,
		"oauth_client_id":         "client-x",
		"oauth_client_secret":     "secret-x",
		"oauth_scope":             "api",
		"connect_timeout":         "3s",
		"call_timeout":            "3s",
	}
}

// gatewayOAuthHandlerWithToolkit builds a Handler whose live gateway
// toolkit shares a TokenStore with the test, so callback ingestion can
// be observed via the store. Each call gets its OWN in-memory PKCE
// store so test functions don't share state through a process global.
func gatewayOAuthHandlerWithToolkit(t *testing.T, store ConnectionStore) (*Handler, gatewaykit.TokenStore) {
	t.Helper()
	tk := gatewaykit.New("primary")
	tokenStore := gatewaykit.NewMemoryTokenStore()
	tk.SetTokenStore(tokenStore)
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
	}, nil)
	return h, tokenStore
}

func TestStartGatewayOAuth_NotFound(t *testing.T) {
	h, _ := gatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{getErr: platform.ErrConnectionNotFound})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/missing/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStartGatewayOAuth_RejectsClientCredentialsConnection(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "ccg",
			Config: map[string]any{
				"endpoint":            "https://x/mcp",
				"connection_name":     "ccg",
				"auth_mode":           gatewaykit.AuthModeOAuth,
				"oauth_grant":         gatewaykit.OAuthGrantClientCredentials,
				"oauth_token_url":     "https://t/",
				"oauth_client_id":     "id",
				"oauth_client_secret": "sec",
				"connect_timeout":     "3s",
				"call_timeout":        "3s",
			},
		},
	}
	h, _ := gatewayOAuthHandlerWithToolkit(t, store)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/ccg/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestStartGatewayOAuth_NoPKCEStoreReturns503 verifies the
// fail-fast guard for misconfigured handlers. Deps.PKCEStore is
// required; absence yields a clear 503 rather than a panic or a
// silent in-memory fallback.
func TestStartGatewayOAuth_NoPKCEStoreReturns503(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor",
			Config: authCodeConnectionConfig("https://auth/", "https://t/"),
		},
	}
	tk := gatewaykit.New("primary")
	tk.SetTokenStore(gatewaykit.NewMemoryTokenStore())
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
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", http.NoBody)
	req.Host = "platform.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "PKCE store not configured")
}

// TestGatewayOAuthCallback_NoPKCEStoreRendersError verifies the
// callback path behaves the same way: render an HTML error page
// rather than panic.
func TestGatewayOAuthCallback_NoPKCEStoreRendersError(t *testing.T) {
	tk := gatewaykit.New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		// PKCEStore intentionally nil
	}, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/oauth/callback?code=abc&state=anything", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code) // writeOAuthError uses 400
	assert.Contains(t, w.Body.String(), "PKCE store not configured")
}

func TestStartGatewayOAuth_ReturnsAuthorizationURL(t *testing.T) {
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor",
			Config: authCodeConnectionConfig("https://auth.example.com/authorize", "https://auth.example.com/token"),
		},
	}
	h, _ := gatewayOAuthHandlerWithToolkit(t, store)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", http.NoBody)
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
	assert.Contains(t, q.Get("redirect_uri"), "/api/v1/admin/oauth/callback")
}

func TestGatewayOAuthCallback_Success(t *testing.T) {
	// Token endpoint that echoes a valid token response when given
	// the expected code + verifier.
	var receivedForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedForm, _ = url.ParseQuery(string(body))
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
	h, tokenStore := gatewayOAuthHandlerWithToolkit(t, store)

	// Step 1: oauth-start to populate the PKCE store.
	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", http.NoBody)
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code)
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	// Step 2: provider redirects browser to /oauth/callback with
	// code + the state from step 1.
	cbURL := "/api/v1/admin/oauth/callback?code=auth-code&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbReq.Host = "platform.example.com"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)

	require.Equalf(t, http.StatusFound, cbW.Code, "expected redirect after successful exchange; body: %s", cbW.Body.String())
	assert.Contains(t, cbW.Header().Get("Location"), "/portal/admin/connections")

	// Verify the token store has the freshly-ingested tokens.
	stored, err := tokenStore.Get(context.Background(), "vendor")
	require.NoError(t, err)
	assert.Equal(t, "acc", stored.AccessToken)
	assert.Equal(t, "ref", stored.RefreshToken)

	// And the token endpoint received the code + PKCE verifier.
	assert.Equal(t, "auth-code", receivedForm.Get("code"))
	assert.NotEmpty(t, receivedForm.Get("code_verifier"))
	assert.Equal(t, "authorization_code", receivedForm.Get("grant_type"))
}

func TestGatewayOAuthCallback_MissingState(t *testing.T) {
	h, _ := gatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/oauth/callback?code=abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing state")
}

func TestGatewayOAuthCallback_UnknownState(t *testing.T) {
	h, _ := gatewayOAuthHandlerWithToolkit(t, &mockConnectionStore{})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/oauth/callback?code=abc&state=does-not-exist", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "expired or unknown")
}

// runOAuthCallbackErrorCase drives the start → callback flow against
// the real handler with the given callback query and asserts the
// response. Shared between TestGatewayOAuthCallback subtests so each
// case is a one-liner that documents what's being tested instead of
// duplicating ~25 lines of setup.
func runOAuthCallbackErrorCase(t *testing.T, callbackQuery, wantBodySubstr string) {
	t.Helper()
	store := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: gatewaykit.Kind, Name: "vendor",
			Config: authCodeConnectionConfig("https://auth/", "https://t/"),
		},
	}
	h, _ := gatewayOAuthHandlerWithToolkit(t, store)

	startReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/vendor/oauth-start", http.NoBody)
	startReq.Host = "platform.example.com"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code)
	var startResp startGatewayOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	cbURL := "/api/v1/admin/oauth/callback?" + callbackQuery +
		"&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)
	assert.Equal(t, http.StatusBadRequest, cbW.Code)
	assert.Contains(t, cbW.Body.String(), wantBodySubstr)
}

func TestGatewayOAuthCallback_UpstreamError(t *testing.T) {
	runOAuthCallbackErrorCase(t,
		"error=access_denied&error_description=denied",
		"access_denied")
}

func TestPKCEChallenge_Deterministic(t *testing.T) {
	// Same verifier → same challenge.
	a := pkceChallenge("verifier-x")
	b := pkceChallenge("verifier-x")
	assert.Equal(t, a, b)
	c := pkceChallenge("different")
	assert.NotEqual(t, a, c)
}

func TestBuildOAuthCallbackURL_HonorsForwardedHeaders(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/", http.NoBody)
	r.Host = "internal.svc"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "platform.example.com")
	got := buildOAuthCallbackURL(r)
	assert.Equal(t, "https://platform.example.com/api/v1/admin/oauth/callback", got)
}

func TestBuildAuthorizationURL_AppendsToExistingQuery(t *testing.T) {
	cfg := gatewaykit.OAuthConfig{
		ClientID:         "id",
		AuthorizationURL: "https://auth.example.com/o/authorize?app=foo",
		Scope:            "api",
	}
	got := buildAuthorizationURL(cfg, "state-x", "verifier-y", "https://platform.example.com/cb")
	assert.Contains(t, got, "app=foo&", "should keep existing query")
	assert.Contains(t, got, "state=state-x")
	assert.Contains(t, got, "code_challenge_method=S256")
}

// TestBuildAuthorizationURL_PromptParameter exercises the OIDC prompt
// parameter logic. Each subtest URL-parses the result so we verify the
// parameter shows up as a properly-encoded query value, not just as a
// substring (which could match accidental occurrences inside the path
// or scope).
//
// The configured Prompt value defeats the stale-Keycloak-form bug
// when set to "login" (every Reconnect click forces a fresh credential
// prompt rather than letting an active SSO session silently grant the
// code), while the empty default lets pure-OAuth providers — which
// reject unknown parameters — receive an unmodified authorize URL.
func TestBuildAuthorizationURL_PromptParameter(t *testing.T) {
	base := "https://auth.example.com/o/authorize"
	makeCfg := func(prompt string) gatewaykit.OAuthConfig {
		return gatewaykit.OAuthConfig{
			ClientID:         "id",
			AuthorizationURL: base,
			Scope:            "api",
			Prompt:           prompt,
		}
	}

	t.Run("Prompt=login is a properly-encoded query parameter", func(t *testing.T) {
		got := buildAuthorizationURL(makeCfg("login"), "state-x", "verifier-y", "https://platform.example.com/cb")
		u, err := url.Parse(got)
		require.NoError(t, err, "result must be a parseable URL: %q", got)
		assert.Equal(t, "login", u.Query().Get("prompt"),
			"prompt must be set as a query parameter, not embedded as substring")
	})

	t.Run("Prompt empty: parameter is omitted entirely", func(t *testing.T) {
		got := buildAuthorizationURL(makeCfg(""), "state-x", "verifier-y", "https://platform.example.com/cb")
		u, err := url.Parse(got)
		require.NoError(t, err)
		assert.False(t, u.Query().Has("prompt"),
			"empty Prompt config must produce no prompt query parameter — "+
				"some OAuth providers reject unknown parameters with invalid_request")
	})

	t.Run("Prompt=consent passes through unchanged", func(t *testing.T) {
		got := buildAuthorizationURL(makeCfg("consent"), "state-x", "verifier-y", "https://platform.example.com/cb")
		u, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, "consent", u.Query().Get("prompt"),
			"non-default Prompt values must reach the IdP verbatim")
	})

	t.Run("Existing query string in AuthorizationURL is preserved", func(t *testing.T) {
		cfg := gatewaykit.OAuthConfig{
			ClientID:         "id",
			AuthorizationURL: base + "?app=foo",
			Prompt:           "login",
		}
		got := buildAuthorizationURL(cfg, "state-x", "verifier-y", "https://platform.example.com/cb")
		u, err := url.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, "foo", u.Query().Get("app"))
		assert.Equal(t, "login", u.Query().Get("prompt"))
	})
}

func TestGenerateHelpers_ProduceUniqueValues(t *testing.T) {
	v1, _ := generatePKCEVerifier()
	v2, _ := generatePKCEVerifier()
	assert.NotEqual(t, v1, v2)
	s1, _ := generatePKCEState()
	s2, _ := generatePKCEState()
	assert.NotEqual(t, s1, s2)
}

func TestSafeReturnURL(t *testing.T) {
	cases := map[string]string{
		// rejected forms — CodeQL go/bad-redirect-check coverage
		"":                               "/portal/admin/connections",
		"//evil.example.com/x":           "/portal/admin/connections",
		`/\evil.example.com/x`:           "/portal/admin/connections", // backslash-protocol-relative
		`/\\evil.example.com/x`:          "/portal/admin/connections",
		"https://evil.example.com/x":     "/portal/admin/connections",
		"javascript:alert(1)":            "/portal/admin/connections",
		"portal/admin":                   "/portal/admin/connections",
		"/path?next=javascript:alert(1)": "/portal/admin/connections", // colon anywhere → reject
		// accepted forms
		"/portal/admin/connections/foo":          "/portal/admin/connections/foo",
		"/portal/admin/gateway/connections?ok=1": "/portal/admin/gateway/connections?ok=1",
		"/x":                                     "/x",
	}
	for input, want := range cases {
		assert.Equal(t, want, safeReturnURL(input), "input=%q", input)
	}
}

// TestWriteOAuthError_EscapesUpstreamControlledMsg covers the CodeQL
// go/reflected-xss alert: error_description from the OAuth provider
// is interpolated into the error page, so any markup-like content
// must be escaped by html/template before reaching the response.
func TestWriteOAuthError_EscapesUpstreamControlledMsg(t *testing.T) {
	w := httptest.NewRecorder()
	writeOAuthError(w, `<script>alert(1)</script> & "quote" 'tick'`)
	body := w.Body.String()
	assert.NotContains(t, body, "<script>", "raw <script> must not appear in body")
	assert.Contains(t, body, "&lt;script&gt;")
	assert.Contains(t, body, "&amp;")
	assert.Contains(t, body, "&#34;") // html/template uses numeric entity for "
}

func TestTrimOAuthBody(t *testing.T) {
	short := []byte("ok")
	assert.Equal(t, "ok", trimOAuthBody(short))

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	got := trimOAuthBody(long)
	assert.Len(t, got, 256+len("..."))
	assert.True(t, strings.HasSuffix(got, "..."), "expected ellipsis suffix, got %q", got[len(got)-5:])
}

// TestClientIP verifies clientIP honors X-Forwarded-For (first hop only)
// and falls back to RemoteAddr when no proxy header is present.
// Without this header awareness, every request behind an ingress logs
// the ingress controller's IP — useless for forensics.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name string
		xff  string
		ra   string
		want string
	}{
		{"no XFF falls back to RemoteAddr", "", "10.0.0.5:54321", "10.0.0.5:54321"},
		{"single-IP XFF returned trimmed", "  203.0.113.7  ", "10.0.0.1:1", "203.0.113.7"},
		{"multi-hop XFF returns first hop only", "203.0.113.7, 10.0.0.1, 10.0.0.2", "10.0.0.1:1", "203.0.113.7"},
		{"multi-hop XFF first hop trimmed", "  203.0.113.7  , 10.0.0.1", "10.0.0.1:1", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
			r.RemoteAddr = tc.ra
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			assert.Equal(t, tc.want, clientIP(r))
		})
	}
}

// TestGatewayOAuthCallback_MissingCode covers the callback path where
// the IdP redirects back without an `error` and without a `code` —
// observed in the wild when an operator manually replays a callback URL
// after the code has already been consumed.
func TestGatewayOAuthCallback_MissingCode(t *testing.T) {
	// Empty query (just state) — IdP returned neither error nor code,
	// observed in the wild when an operator manually replays a
	// callback URL after the code has already been consumed.
	runOAuthCallbackErrorCase(t, "", "missing code")
}
