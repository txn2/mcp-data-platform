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
// be observed via the store.
func gatewayOAuthHandlerWithToolkit(t *testing.T, store ConnectionStore) (*Handler, gatewaykit.TokenStore) {
	t.Helper()
	tk := gatewaykit.New("primary")
	tokenStore := gatewaykit.NewMemoryTokenStore()
	tk.SetTokenStore(tokenStore)
	t.Cleanup(func() { _ = tk.Close() })

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
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

func TestGatewayOAuthCallback_UpstreamError(t *testing.T) {
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

	cbURL := "/api/v1/admin/oauth/callback?error=access_denied&error_description=denied&state=" +
		url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)
	assert.Equal(t, http.StatusBadRequest, cbW.Code)
	assert.Contains(t, cbW.Body.String(), "access_denied")
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
	assert.True(t, strings.Contains(got, "app=foo&"), "should keep existing query: %s", got)
	assert.True(t, strings.Contains(got, "state=state-x"))
	assert.True(t, strings.Contains(got, "code_challenge_method=S256"))
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
		"javascript:alert(1)":            "/portal/admin/connections", //nolint:gosec // G203 false positive: test data string, not executed
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
