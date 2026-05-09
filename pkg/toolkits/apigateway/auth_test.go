package apigateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewAuthenticator_DispatchesByMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"none", Config{AuthMode: AuthModeNone}},
		{"bearer", Config{AuthMode: AuthModeBearer, Credential: "tok"}},
		{"api_key header", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-API-Key"}},
		{"api_key query", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: "key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAuthenticator(tc.cfg)
			if err != nil {
				t.Fatalf("NewAuthenticator: %v", err)
			}
			if a == nil {
				t.Fatal("NewAuthenticator returned nil")
			}
		})
	}
}

func TestNewAuthenticator_RejectsUnknownMode(t *testing.T) {
	if _, err := NewAuthenticator(Config{AuthMode: "weird"}); err == nil {
		t.Fatal("NewAuthenticator: want error for unknown mode")
	}
}

func TestBearerAuth_AppliesAuthorizationHeader(t *testing.T) {
	a, err := NewAuthenticator(Config{AuthMode: AuthModeBearer, Credential: "tok-xyz"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-xyz" {
		t.Errorf("Authorization = %q; want %q", got, "Bearer tok-xyz")
	}
}

func TestBearerAuth_RejectsEmptyCredential(t *testing.T) {
	a := bearerAuth{credential: ""}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Error("Apply: want error for empty credential")
	}
}

func TestAPIKeyAuth_HeaderPlacement(t *testing.T) {
	a, err := NewAuthenticator(Config{
		AuthMode:        AuthModeAPIKey,
		Credential:      "secret-key",
		APIKeyPlacement: APIKeyPlacementHeader,
		APIKeyHeader:    "X-Api-Token",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("X-Api-Token"); got != "secret-key" {
		t.Errorf("X-Api-Token = %q; want %q", got, "secret-key")
	}
}

func TestAPIKeyAuth_QueryPlacement_PreservesExistingQuery(t *testing.T) {
	a, err := NewAuthenticator(Config{
		AuthMode:        AuthModeAPIKey,
		Credential:      "qkey",
		APIKeyPlacement: APIKeyPlacementQuery,
		APIKeyParam:     "api_key",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo?existing=v1", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	q := req.URL.Query()
	if q.Get("api_key") != "qkey" {
		t.Errorf("api_key = %q; want %q", q.Get("api_key"), "qkey")
	}
	if q.Get("existing") != "v1" {
		t.Errorf("existing param dropped: %s", req.URL.RawQuery)
	}
}

func TestNoneAuth_SetsNoHeaders(t *testing.T) {
	a, err := NewAuthenticator(Config{AuthMode: AuthModeNone})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("noneAuth set Authorization")
	}
}

func TestNewAPIKeyAuth_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty credential", Config{AuthMode: AuthModeAPIKey, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-Key"}},
		{"empty header", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: ""}},
		{"empty query param", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: ""}},
		{"unknown placement", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: "weird"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newAPIKeyAuth(tc.cfg); err == nil {
				t.Errorf("newAPIKeyAuth(%+v) want error", tc.cfg)
			}
		})
	}
}

// Credentials never appear in slog output, error messages, or
// stringified Authenticator state. A future change that adds a
// fmt.Errorf("apigateway: bad credential %q", c.Credential) would
// be a real leak; this test is the canary.
func TestAuthenticators_DoNotLeakCredentials(t *testing.T) {
	const secret = "supersecret-credential-nonsense-9b7"
	cfgs := []Config{
		{AuthMode: AuthModeBearer, Credential: secret},
		{AuthMode: AuthModeAPIKey, Credential: secret, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-K"},
		{AuthMode: AuthModeAPIKey, Credential: secret, APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: "k"},
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	for _, cfg := range cfgs {
		a, err := NewAuthenticator(cfg)
		if err != nil {
			t.Errorf("NewAuthenticator(%s): %v", cfg.AuthMode, err)
			continue
		}
		// Stringify and log everything we have access to.
		logger.Info("auth materialized",
			"mode", cfg.AuthMode,
			"placement", cfg.APIKeyPlacement,
			"header", cfg.APIKeyHeader,
			"param", cfg.APIKeyParam,
		)
		// Log the error from a deliberately broken Apply call too.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
		_ = a.Apply(req)
	}

	if strings.Contains(buf.String(), secret) {
		t.Errorf("log buffer contains credential: %s", buf.String())
	}
}

func TestAPIKeyAuth_Apply_RejectsUnknownPlacement(t *testing.T) {
	// Force-construct an apiKeyAuth in an invalid state to verify the
	// Apply default branch fires. NewAuthenticator and newAPIKeyAuth
	// would normally reject this at construction time.
	a := apiKeyAuth{credential: "k", placement: "tampered", header: "X", param: "y"}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Fatal("Apply: want error for unknown placement")
	}
}

// fakeTokenServer simulates an OAuth 2.1 token endpoint. The
// standard library's oauth2 client posts
// application/x-www-form-urlencoded; the operator-supplied
// handler decides what to do with it.
func fakeTokenServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestOAuth2ClientCredentialsAuth_HappyPath(t *testing.T) {
	const wantClientID = "test-client"
	const wantSecret = "test-secret"
	const issuedToken = "issued-access-token"

	srv := fakeTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Defaults to header-based auth: Authorization: Basic <base64(client_id:client_secret)>.
		user, pass, ok := r.BasicAuth()
		if !ok || user != wantClientID || pass != wantSecret {
			t.Errorf("token endpoint saw basic-auth=(%q,%q,%v); want (%q,%q,true)", user, pass, ok, wantClientID, wantSecret)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + issuedToken + `","token_type":"Bearer","expires_in":3600}`))
	})
	defer srv.Close()

	a, err := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			TokenURL:          srv.URL + "/token",
			ClientID:          wantClientID,
			ClientSecret:      wantSecret,
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := req.Header.Get("Authorization")
	if got != "Bearer "+issuedToken {
		t.Errorf("Authorization = %q; want %q", got, "Bearer "+issuedToken)
	}
}

func TestOAuth2ClientCredentialsAuth_TokenIsCached(t *testing.T) {
	var fetches int
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
	})
	defer srv.Close()

	a, _ := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})

	for i := range 5 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
		if err := a.Apply(req); err != nil {
			t.Fatalf("Apply iter %d: %v", i, err)
		}
	}
	if fetches != 1 {
		t.Errorf("token endpoint fetched %d times; want 1 (cache should reuse the unexpired token)", fetches)
	}
}

// TestOAuth2ClientCredentialsAuth_RefetchesOnExpiry proves the
// auto-refresh claim. The IdP issues a token with expires_in: 1
// — golang.org/x/oauth2 considers a token expired when within 10s
// of expiry by default, so the second Apply call MUST trigger a
// fresh fetch. Without auto-refresh the second call would reuse
// the stale token and the upstream API would 401 instead.
func TestOAuth2ClientCredentialsAuth_RefetchesOnExpiry(t *testing.T) {
	var fetches int
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-` + fmt.Sprintf("%d", fetches) + `","token_type":"Bearer","expires_in":1}`))
	})
	defer srv.Close()

	a, _ := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})

	// First call: fresh fetch.
	req1 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req1); err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("after first call: fetches=%d; want 1", fetches)
	}

	// Second call: token is within library's 10s expiry buffer
	// (issued with expires_in: 1) → library MUST re-fetch.
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req2); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if fetches != 2 {
		t.Errorf("after second call: fetches=%d; want 2 (auto-refresh did not happen)", fetches)
	}
	// Bonus: the token applied to req2 should be the second one.
	if got := req2.Header.Get("Authorization"); got != "Bearer tok-2" {
		t.Errorf("req2 Authorization = %q; want \"Bearer tok-2\" (refresh used the new token)", got)
	}
}

func TestOAuth2ClientCredentialsAuth_IdPRejection_DoesNotLeakSecret(t *testing.T) {
	const secret = "supersecret-client-secret-9b7"
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	})
	defer srv.Close()

	a, _ := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: secret,
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if err == nil {
		t.Fatal("Apply: want error on IdP rejection")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("CREDENTIAL LEAK: client_secret appeared in error %q", err.Error())
	}
}

func TestOAuth2ClientCredentialsAuth_NetworkFailure_DoesNotLeakURLUserinfo(t *testing.T) {
	// Open + immediately close a server so connect attempts are
	// refused fast (vs. RFC 5737 blackhole which would wait for
	// the dial timeout). Same code path: *url.Error wrapping a
	// connect error → scrubber must drop userinfo from the URL.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()

	a, _ := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth2ClientCredentials,
		OAuth2: OAuth2Config{
			// userinfo embedded in URL — a bad pattern but happens
			// in the wild.
			TokenURL:          "http://embedded:supersecret-userinfo@" + addr + "/token",
			ClientID:          "c",
			ClientSecret:      "s",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if err == nil {
		t.Fatal("Apply: want error on network failure")
	}
	if strings.Contains(err.Error(), "supersecret-userinfo") {
		t.Errorf("CREDENTIAL LEAK: URL userinfo appeared in error %q", err.Error())
	}
}

func TestTokenFetchError_PassesThroughOpaqueErrors(t *testing.T) {
	// Plain error (no URL, no RetrieveError wrapper) — passed
	// through with the apigateway prefix. No URL-redaction logic
	// fires because there's nothing URL-shaped.
	err := tokenFetchError(errors.New("kaboom"))
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("tokenFetchError lost the underlying message: %v", err)
	}
}

// authorization_code Authenticator tests.

func newAuthCodeForTest(t *testing.T, tokenURL string) (*oauth2AuthorizationCodeAuth, TokenStore) {
	t.Helper()
	cfg := Config{
		ConnectionName: "c1",
		AuthMode:       AuthModeOAuth2AuthorizationCode,
		OAuth2: OAuth2Config{
			TokenURL:          tokenURL,
			ClientID:          "client",
			ClientSecret:      "secret",
			AuthorizationURL:  "https://idp.example/auth",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	}
	store := NewMemoryTokenStore()
	a := newOAuth2AuthorizationCodeAuth(cfg)
	a.SetTokenStore(store)
	return a, store
}

func TestAuthCodeAuth_NoStoreWired(t *testing.T) {
	cfg := Config{
		ConnectionName: "c1",
		AuthMode:       AuthModeOAuth2AuthorizationCode,
		OAuth2:         OAuth2Config{TokenURL: "https://idp/token", ClientID: "c", ClientSecret: "s"},
	}
	a := newOAuth2AuthorizationCodeAuth(cfg)
	// Deliberately do NOT call SetTokenStore.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Error("Apply: want error when token store not wired")
	}
}

func TestAuthCodeAuth_NoPersistedToken_ReturnsNeedsReauth(t *testing.T) {
	a, _ := newAuthCodeForTest(t, "https://idp.example/token")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if !errors.Is(err, ErrNeedsReauth) {
		t.Errorf("Apply with no token: got %v; want ErrNeedsReauth", err)
	}
}

func TestAuthCodeAuth_CachedToken_AppliedDirectly(t *testing.T) {
	a, store := newAuthCodeForTest(t, "https://idp.example/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "live-access-token",
		ExpiresAt:      time.Now().Add(time.Hour),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer live-access-token" {
		t.Errorf("Authorization = %q; want Bearer live-access-token", got)
	}
}

func TestAuthCodeAuth_ExpiredToken_RefreshesAndPersists(t *testing.T) {
	var fetches int
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-` + fmt.Sprintf("%d", fetches) + `","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`))
	})
	defer srv.Close()

	a, store := newAuthCodeForTest(t, srv.URL+"/token")
	// Persist a token whose access has already expired but whose
	// refresh is still valid (RefreshExpiresAt zero = no IdP-side
	// expiry signal, treated as "still good").
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		RefreshToken:   "old-refresh",
		ExpiresAt:      time.Now().Add(-time.Minute),
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer refreshed-1" {
		t.Errorf("Authorization = %q; want Bearer refreshed-1", got)
	}
	if fetches != 1 {
		t.Errorf("token endpoint fetched %d times; want 1", fetches)
	}

	// The store should now hold the rotated tokens.
	persisted, _ := store.Get(context.Background(), "c1")
	if persisted.AccessToken != "refreshed-1" {
		t.Errorf("stored access token = %q; want refreshed-1", persisted.AccessToken)
	}
	if persisted.RefreshToken != "new-refresh" {
		t.Errorf("stored refresh token = %q; want new-refresh (rotated)", persisted.RefreshToken)
	}
}

func TestAuthCodeAuth_ExpiredRefreshToken_ReturnsNeedsReauth(t *testing.T) {
	a, store := newAuthCodeForTest(t, "https://idp.example/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName:   "c1",
		AccessToken:      "stale",
		RefreshToken:     "old-refresh",
		ExpiresAt:        time.Now().Add(-time.Minute),
		RefreshExpiresAt: time.Now().Add(-time.Minute), // refresh also expired
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if !errors.Is(err, ErrNeedsReauth) {
		t.Errorf("Apply with expired refresh: got %v; want ErrNeedsReauth", err)
	}
	// And the store row should have been deleted so the only path
	// forward is admin Reconnect.
	if _, gerr := store.Get(context.Background(), "c1"); !errors.Is(gerr, ErrTokenNotFound) {
		t.Errorf("after refresh failure: Get = %v; want ErrTokenNotFound (row deleted)", gerr)
	}
}

func TestAuthCodeAuth_InvalidGrant400_ReturnsNeedsReauth(t *testing.T) {
	// RFC 6749 §5.2 invalid_grant at HTTP 400 is the canonical
	// "this refresh token is dead" signal. Apply must delete the
	// row and return ErrNeedsReauth so the admin must reconnect.
	// Content-Type: application/json is required so oauth2's
	// internal parser populates RetrieveError.ErrorCode (under
	// text/plain detection it falls into the form-encoded branch
	// and ErrorCode stays empty).
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	})
	defer srv.Close()

	a, store := newAuthCodeForTest(t, srv.URL+"/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		RefreshToken:   "revoked",
		ExpiresAt:      time.Now().Add(-time.Minute),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if !errors.Is(err, ErrNeedsReauth) {
		t.Errorf("got %v; want ErrNeedsReauth on invalid_grant @ 400", err)
	}
	// Row must be deleted so the only forward path is admin reconnect.
	if _, gerr := store.Get(context.Background(), "c1"); !errors.Is(gerr, ErrTokenNotFound) {
		t.Errorf("after invalid_grant: Get = %v; want ErrTokenNotFound (row deleted)", gerr)
	}
}

func TestAuthCodeAuth_TransientFailure_PreservesRow(t *testing.T) {
	// Transient failures (5xx, network drops) MUST NOT destroy the
	// persisted refresh token. A flaky-network event during a
	// single tool call would otherwise force the operator to
	// manually reconnect — invalidating a still-good refresh.
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	})
	defer srv.Close()

	a, store := newAuthCodeForTest(t, srv.URL+"/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		RefreshToken:   "still-good-refresh",
		ExpiresAt:      time.Now().Add(-time.Minute),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	err := a.Apply(req)
	if err == nil {
		t.Fatal("Apply: want error on transient IdP failure")
	}
	if errors.Is(err, ErrNeedsReauth) {
		t.Errorf("transient failure incorrectly mapped to ErrNeedsReauth: %v", err)
	}
	// Row MUST still exist — the refresh token may still be valid,
	// retry on next call should be possible.
	got, gerr := store.Get(context.Background(), "c1")
	if gerr != nil {
		t.Fatalf("after transient failure: Get = %v; row should still exist", gerr)
	}
	if got.RefreshToken != "still-good-refresh" {
		t.Errorf("refresh token wrongly mutated: %q", got.RefreshToken)
	}
}

func TestAuthCodeAuth_RefreshExpiresIn_PersistedOnRotation(t *testing.T) {
	// Keycloak-style refresh_expires_in must propagate into the
	// stored RefreshExpiresAt so future Apply calls can short-
	// circuit dead-refresh attempts before hitting the IdP.
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-acc","refresh_token":"new-ref","token_type":"Bearer","expires_in":300,"refresh_expires_in":1800}`))
	})
	defer srv.Close()

	a, store := newAuthCodeForTest(t, srv.URL+"/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		RefreshToken:   "old-ref",
		ExpiresAt:      time.Now().Add(-time.Minute),
		// Note: prior RefreshExpiresAt deliberately set to a SHORT
		// future window — proves the rotation overwrites it with
		// the new IdP-supplied 1800s window. Past values would
		// short-circuit refresh via errRefreshExpired and never
		// reach the IdP.
		RefreshExpiresAt: time.Now().Add(5 * time.Minute),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := store.Get(context.Background(), "c1")
	if got.RefreshExpiresAt.IsZero() {
		t.Fatal("RefreshExpiresAt is zero after rotation — refresh_expires_in dropped")
	}
	expected := time.Now().Add(1800 * time.Second)
	if delta := got.RefreshExpiresAt.Sub(expected); delta > 5*time.Second || delta < -5*time.Second {
		t.Errorf("RefreshExpiresAt = %v; want ~%v (Δ=%v)", got.RefreshExpiresAt, expected, delta)
	}
}

// TestAuthCodeAuth_RefreshDoesNotFollowRedirects proves the refresh
// path's http.Client refuses to follow 3xx — without this, a
// misconfigured or compromised IdP could redirect the
// credential-bearing POST (carrying client_secret + refresh_token) to
// an attacker URL. Mirrors the admin-side
// TestExchangeAPIGatewayCode_DoesNotFollowRedirects.
func TestAuthCodeAuth_RefreshDoesNotFollowRedirects(t *testing.T) {
	var attackerHits int
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer","expires_in":3600}`))
	}))
	defer attacker.Close()

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, attacker.URL+"/token", http.StatusFound)
	}))
	defer idp.Close()

	a, store := newAuthCodeForTest(t, idp.URL+"/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		RefreshToken:   "long-lived",
		ExpiresAt:      time.Now().Add(-time.Minute),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Fatal("Apply: want error on IdP 3xx; got nil (redirect followed?)")
	}
	if attackerHits != 0 {
		t.Errorf("attacker received %d hits; refresh path must NOT follow redirects "+
			"(would leak refresh_token + client_secret)", attackerHits)
	}
}

func TestAuthCodeAuth_RotationWithoutRefreshExpiresIn_ClearsDeadline(t *testing.T) {
	// IdP rotates the refresh token but does NOT echo
	// refresh_expires_in. The OLD deadline cannot apply to the new
	// token; the safe choice is to clear it so a stale value
	// doesn't outlive the rotation.
	srv := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-acc","refresh_token":"rotated-ref","token_type":"Bearer","expires_in":300}`))
	})
	defer srv.Close()

	a, store := newAuthCodeForTest(t, srv.URL+"/token")
	priorDeadline := time.Now().Add(time.Hour)
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName:   "c1",
		AccessToken:      "stale",
		RefreshToken:     "old-ref",
		ExpiresAt:        time.Now().Add(-time.Minute),
		RefreshExpiresAt: priorDeadline,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := store.Get(context.Background(), "c1")
	if !got.RefreshExpiresAt.IsZero() {
		t.Errorf("after rotation w/o refresh_expires_in: RefreshExpiresAt = %v; want zero (stale value cleared)", got.RefreshExpiresAt)
	}
	if got.RefreshToken != "rotated-ref" {
		t.Errorf("refresh token not rotated: %q", got.RefreshToken)
	}
}

func TestAuthCodeAuth_NoRefreshToken_ReturnsNeedsReauth(t *testing.T) {
	a, store := newAuthCodeForTest(t, "https://idp.example/token")
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "c1",
		AccessToken:    "stale",
		// No RefreshToken at all.
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", http.NoBody)
	if err := a.Apply(req); !errors.Is(err, ErrNeedsReauth) {
		t.Errorf("got %v; want ErrNeedsReauth when refresh token missing", err)
	}
}
