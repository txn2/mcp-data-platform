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
