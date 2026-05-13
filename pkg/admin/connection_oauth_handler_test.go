package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// fakeOAuthKindHandler is a hand-rolled OAuthKindHandler so handler
// tests can drive ParseOAuthConfig success / failure and observe the
// AfterConnect hook invocation without dragging a real toolkit in.
type fakeOAuthKindHandler struct {
	parseCfg connoauth.Config
	parseErr error
	afterErr error
	// captured args from AfterConnect for assertions:
	afterCalled bool
	afterName   string
}

func (f *fakeOAuthKindHandler) ParseOAuthConfig(_ map[string]any) (connoauth.Config, error) {
	if f.parseErr != nil {
		return connoauth.Config{}, f.parseErr
	}
	return f.parseCfg, nil
}

func (f *fakeOAuthKindHandler) AfterConnect(_ context.Context, name string, _ map[string]any) error {
	f.afterCalled = true
	f.afterName = name
	return f.afterErr
}

// newOAuthTestHandler wires a minimal Handler suitable for exercising
// the unified connection OAuth routes. The PKCE store is in-memory;
// the connoauth store is in-memory; ConnectionStore is the same
// mock as the rest of the admin tests. The kinds map is populated
// per test.
func newOAuthTestHandler(t *testing.T, connStore *mockConnectionStore, kinds OAuthKindHandlers) (*Handler, connoauth.Store) {
	t.Helper()
	pkce := NewMemoryPKCEStore()
	t.Cleanup(func() { _ = pkce.Close() })
	store := connoauth.NewMemoryStore()
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
		PKCEStore:       pkce,
		ConnOAuthStore:  store,
		OAuthKinds:      kinds,
	}, nil)
	return h, store
}

func setupOAuthFixture(t *testing.T, tokenSrv *httptest.Server) (*Handler, connoauth.Store, *fakeOAuthKindHandler, *mockConnectionStore) {
	t.Helper()
	fake := &fakeOAuthKindHandler{
		parseCfg: connoauth.Config{
			AuthorizationURL:  "https://idp.example/authorize",
			TokenURL:          tokenSrv.URL + "/token",
			ClientID:          "test-client",
			ClientSecret:      "test-secret",
			Scopes:            []string{"openid", "offline_access"},
			EndpointAuthStyle: oauth2.AuthStyleInHeader,
		},
	}
	connStore := &mockConnectionStore{
		getResult: &platform.ConnectionInstance{
			Kind: connoauth.KindMCP,
			Name: "alpha",
			Config: map[string]any{
				"endpoint":                "http://upstream/mcp",
				"auth_mode":               "oauth",
				"oauth_grant":             "authorization_code",
				"oauth_authorization_url": "https://idp.example/authorize",
				"oauth_token_url":         tokenSrv.URL + "/token",
				"oauth_client_id":         "test-client",
				"oauth_client_secret":     "test-secret",
			},
		},
	}
	kinds := OAuthKindHandlers{connoauth.KindMCP: fake}
	h, store := newOAuthTestHandler(t, connStore, kinds)
	return h, store, fake, connStore
}

// fakeIdPServer is a minimal HTTP test double that issues tokens on
// /token. Each callback to the test can override the response.
func fakeIdPServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ────────────────────────────────────────────────────────────────────────
// startConnectionOAuth
// ────────────────────────────────────────────────────────────────────────

func TestStartConnectionOAuth_Success(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/oauth-start",
		strings.NewReader(`{"return_url":"/portal/admin/connections"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp startConnectionOAuthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp.AuthorizationURL, "https://idp.example/authorize")
	assert.Contains(t, resp.AuthorizationURL, "response_type=code")
	assert.Contains(t, resp.AuthorizationURL, "code_challenge=")
	assert.NotEmpty(t, resp.State)
	assert.NotEmpty(t, resp.RedirectURI)
	assert.NotEmpty(t, resp.ExpiresAt)
}

func TestStartConnectionOAuth_UnknownKind(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/unsupported/alpha/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported connection kind")
}

func TestStartConnectionOAuth_ConnectionNotFound(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, connStore := setupOAuthFixture(t, srv)
	connStore.getErr = platform.ErrConnectionNotFound

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/missing/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStartConnectionOAuth_NotConfiguredForAuthCode(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, fake, _ := setupOAuthFixture(t, srv)
	fake.parseErr = errors.New("connection is not configured for authorization_code OAuth")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/oauth-start", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// ────────────────────────────────────────────────────────────────────────
// connectionOAuthStatus
// ────────────────────────────────────────────────────────────────────────

func TestConnectionOAuthStatus_NoToken(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connections/mcp/alpha/oauth-status", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var status connoauth.OAuthStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&status))
	assert.True(t, status.Configured)
	assert.True(t, status.NeedsReauth)
	assert.False(t, status.TokenAcquired)
}

func TestConnectionOAuthStatus_WithToken(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, store, _, _ := setupOAuthFixture(t, srv)
	now := time.Now()
	_ = store.Set(context.Background(), connoauth.PersistedToken{
		Key:             connoauth.Key{Kind: connoauth.KindMCP, Name: "alpha"},
		AccessToken:     "at",
		RefreshToken:    "rt",
		ExpiresAt:       now.Add(time.Hour),
		AuthenticatedBy: "user@example.com",
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connections/mcp/alpha/oauth-status", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var status connoauth.OAuthStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&status))
	assert.True(t, status.TokenAcquired)
	assert.True(t, status.HasRefreshToken)
	assert.False(t, status.NeedsReauth)
	assert.Equal(t, "user@example.com", status.AuthenticatedBy)
}

// ────────────────────────────────────────────────────────────────────────
// reacquireConnectionOAuth
// ────────────────────────────────────────────────────────────────────────

func TestReacquireConnectionOAuth_Success(t *testing.T) {
	srv := fakeIdPServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at-fresh",
			"refresh_token": "rt-fresh",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	})
	h, store, _, _ := setupOAuthFixture(t, srv)
	now := time.Now()
	_ = store.Set(context.Background(), connoauth.PersistedToken{
		Key:          connoauth.Key{Kind: connoauth.KindMCP, Name: "alpha"},
		AccessToken:  "at-old",
		RefreshToken: "rt-old",
		ExpiresAt:    now.Add(time.Hour),
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/reacquire-oauth", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var status connoauth.OAuthStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&status))
	assert.True(t, status.TokenAcquired)
	// Confirm refresh actually rotated through the store.
	row, _ := store.Get(context.Background(), connoauth.Key{Kind: connoauth.KindMCP, Name: "alpha"})
	assert.Equal(t, "at-fresh", row.AccessToken)
	assert.Equal(t, "rt-fresh", row.RefreshToken)
}

func TestReacquireConnectionOAuth_NeedsReauth(t *testing.T) {
	srv := fakeIdPServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	h, store, _, _ := setupOAuthFixture(t, srv)
	_ = store.Set(context.Background(), connoauth.PersistedToken{
		Key:          connoauth.Key{Kind: connoauth.KindMCP, Name: "alpha"},
		AccessToken:  "at",
		RefreshToken: "rt-dead",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/reacquire-oauth", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// ────────────────────────────────────────────────────────────────────────
// connectionOAuthCallback — the full Start → callback → token persisted
// + AfterConnect hook fired round-trip.
// ────────────────────────────────────────────────────────────────────────

func TestConnectionOAuthCallback_RoundTrip(t *testing.T) {
	tokenSrv := fakeIdPServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"Bearer"}`))
	})
	h, store, fake, _ := setupOAuthFixture(t, tokenSrv)

	// Step 1: oauth-start
	startReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/oauth-start",
		strings.NewReader(`{"return_url":"/portal/admin/connections"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Host = "localhost:8080"
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	require.Equal(t, http.StatusOK, startW.Code, "start body=%s", startW.Body.String())
	var startResp startConnectionOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	// Step 2: callback with same state + the code the IdP would have issued
	callbackURL := "/api/v1/admin/oauth/callback?code=test-code&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, callbackURL, http.NoBody)
	cbReq.Host = "localhost:8080"
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)
	require.Equal(t, http.StatusFound, cbW.Code, "callback body=%s", cbW.Body.String())

	// Token must be persisted under (mcp, alpha)
	row, err := store.Get(context.Background(), connoauth.Key{Kind: connoauth.KindMCP, Name: "alpha"})
	require.NoError(t, err)
	assert.Equal(t, "at", row.AccessToken)
	assert.Equal(t, "rt", row.RefreshToken)

	// AfterConnect hook must have fired
	assert.True(t, fake.afterCalled)
	assert.Equal(t, "alpha", fake.afterName)

	// Redirect points to a safe path
	assert.Contains(t, cbW.Header().Get("Location"), "/portal/admin/connections")
}

func TestConnectionOAuthCallback_MissingState(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/oauth/callback?code=x", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(body), "missing state")
}

func TestConnectionOAuthCallback_UpstreamError(t *testing.T) {
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	// Need a valid PKCE state row first
	startReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connections/mcp/alpha/oauth-start", http.NoBody)
	startW := httptest.NewRecorder()
	h.ServeHTTP(startW, startReq)
	var startResp startConnectionOAuthResponse
	require.NoError(t, json.NewDecoder(startW.Body).Decode(&startResp))

	cbURL := "/api/v1/admin/oauth/callback?error=access_denied&error_description=user+cancelled&state=" + url.QueryEscape(startResp.State)
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, cbURL, http.NoBody)
	cbW := httptest.NewRecorder()
	h.ServeHTTP(cbW, cbReq)
	assert.Equal(t, http.StatusBadRequest, cbW.Code)
	assert.Contains(t, cbW.Body.String(), "access_denied")
}

func TestConnectionOAuthCallback_LegacyAPIGatewayURLAliased(t *testing.T) {
	// The legacy /api/v1/admin/api-gateway/oauth/callback URL must
	// still be handled (customer IdP configs registered it).
	srv := fakeIdPServer(t, func(http.ResponseWriter, *http.Request) {})
	h, _, _, _ := setupOAuthFixture(t, srv)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/api-gateway/oauth/callback?state=", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Should route into the unified handler (missing-state error
	// proves the route is bound, not 404).
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing state")
}

// ────────────────────────────────────────────────────────────────────────
// helper utility coverage
// ────────────────────────────────────────────────────────────────────────

func TestBuildConnectionAuthorizationURL(t *testing.T) {
	t.Parallel()
	cfg := connoauth.Config{
		AuthorizationURL: "https://idp/auth",
		ClientID:         "client-id",
		Scopes:           []string{"openid", "offline_access"},
		Prompt:           "login",
	}
	got := buildConnectionAuthorizationURL(cfg, "STATE", "VERIFIER", "https://platform/cb")
	assert.Contains(t, got, "response_type=code")
	assert.Contains(t, got, "client_id=client-id")
	assert.Contains(t, got, "state=STATE")
	assert.Contains(t, got, "code_challenge=")
	assert.Contains(t, got, "code_challenge_method=S256")
	assert.Contains(t, got, "scope=openid+offline_access")
	assert.Contains(t, got, "prompt=login")
}

func TestBuildConnectionAuthorizationURL_ExistingQuery(t *testing.T) {
	t.Parallel()
	cfg := connoauth.Config{AuthorizationURL: "https://idp/auth?tenant=acme", ClientID: "c"}
	got := buildConnectionAuthorizationURL(cfg, "S", "V", "https://x/cb")
	assert.Contains(t, got, "https://idp/auth?tenant=acme&")
}

func TestURLHostForLog(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "idp.example.com", urlHostForLog("https://idp.example.com/realms/x/token"))
	assert.Equal(t, "not-a-url", urlHostForLog("not-a-url"))
}
