package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTokenServer stands up an httptest server that mimics an OAuth 2.1
// token endpoint. The handler is programmable per-test.
func fakeTokenServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

func defaultOAuthConfig(tokenURL string) OAuthConfig {
	return OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     tokenURL,
		ClientID:     "client-x",
		ClientSecret: "secret-x",
		Scope:        "read",
	}
}

func TestOAuthTokenSource_AcquiresOnFirstCall(t *testing.T) {
	var seen url.Values
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = parseFormBytes(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "abc", TokenType: "Bearer", ExpiresIn: 3600,
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "abc" {
		t.Errorf("got %q, want %q", tok, "abc")
	}
	if seen.Get("grant_type") != "client_credentials" {
		t.Errorf("grant_type: got %q", seen.Get("grant_type"))
	}
	if seen.Get("client_id") != "client-x" {
		t.Errorf("client_id missing: %v", seen)
	}
	if seen.Get("client_secret") != "secret-x" {
		t.Errorf("client_secret missing")
	}
	if seen.Get("scope") != "read" {
		t.Errorf("scope: got %q", seen.Get("scope"))
	}
}

func TestOAuthTokenSource_CachesValidToken(t *testing.T) {
	calls := 0
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "cached", ExpiresIn: 3600,
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	for i := range 5 {
		_, err := src.Token(context.Background())
		if err != nil {
			t.Fatalf("Token call %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 token-endpoint call (caching), got %d", calls)
	}
}

func TestOAuthTokenSource_RefreshesUsingRefreshToken(t *testing.T) {
	var grantTypes []string
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f := parseFormBytes(body)
		grantTypes = append(grantTypes, f.Get("grant_type"))
		w.Header().Set("Content-Type", "application/json")
		// First call returns an immediately-expired token + refresh token.
		// Second call (refresh) returns a valid token.
		if len(grantTypes) == 1 {
			_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
				AccessToken: "expired", RefreshToken: "rt", ExpiresIn: 1,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "fresh", ExpiresIn: 3600,
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	// Force expiry into the past.
	src.mu.Lock()
	src.state.ExpiresAt = time.Now().Add(-time.Second)
	src.mu.Unlock()

	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if tok != "fresh" {
		t.Errorf("got %q, want %q", tok, "fresh")
	}
	if len(grantTypes) != 2 || grantTypes[1] != "refresh_token" {
		t.Errorf("expected second exchange to use refresh_token, got %v", grantTypes)
	}
}

func TestOAuthTokenSource_ReacquireBypassesCache(t *testing.T) {
	calls := 0
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "tok", ExpiresIn: 3600,
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if err := src.Reacquire(context.Background()); err != nil {
		t.Fatalf("Reacquire: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (cache + reacquire), got %d", calls)
	}
}

func TestOAuthTokenSource_RFCErrorResponse(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			Error: "invalid_client", ErrorDescription: "unknown client",
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("error %q missing structured code", err.Error())
	}
}

func TestOAuthTokenSource_NonJSONErrorResponse(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream is on fire"))
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "fire") {
		t.Errorf("error %q missing status/body", err.Error())
	}
}

func TestOAuthTokenSource_MissingAccessTokenIsError(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer", "expires_in": 3600})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	_, err := src.Token(context.Background())
	if !errors.Is(err, err) || err == nil || !strings.Contains(err.Error(), "missing access_token") {
		t.Errorf("expected missing-access-token error, got %v", err)
	}
}

func TestOAuthTokenSource_DefaultExpiryWhenAbsent(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		//nolint:gosec // G117 false positive: OAuth response shape, not a credential
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "no-expiry"})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	st := src.Status()
	if !st.TokenAcquired {
		t.Fatal("expected token acquired")
	}
	// Default is 1 hour; check we're somewhere in [55min, 65min] from now.
	delta := time.Until(st.ExpiresAt)
	if delta < 55*time.Minute || delta > 65*time.Minute {
		t.Errorf("expected ~1h default expiry, got %v", delta)
	}
}

func TestOAuthTokenSource_ConcurrentTokenCallsSerialize(t *testing.T) {
	calls := 0
	var callsMu sync.Mutex
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		// Simulate a slow upstream so concurrent callers must serialize.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "tok", ExpiresIn: 3600,
		})
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	const n = 8
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, _ = src.Token(context.Background())
		})
	}
	wg.Wait()
	if calls != 1 {
		t.Errorf("expected 1 token-endpoint call across %d concurrent Token() callers (serialized), got %d", n, calls)
	}
}

func TestOAuthTokenSource_StatusReportsLastError(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)

	_, _ = src.Token(context.Background())
	st := src.Status()
	if st.LastError == "" {
		t.Error("expected LastError populated")
	}
	if st.TokenAcquired {
		t.Error("did not expect TokenAcquired after failure")
	}
}

func TestInterpretTokenError_TruncatesLargeBody(t *testing.T) {
	big := strings.Repeat("x", 1024)
	err := interpretTokenError(http.StatusInternalServerError, []byte(big))
	if !strings.Contains(err.Error(), "...") {
		t.Errorf("expected truncated body marker, got %v", err)
	}
}

func TestParseOAuthConfig_NestedAndFlattened(t *testing.T) {
	nested := parseOAuthConfig(map[string]any{
		"oauth": map[string]any{
			"grant": "client_credentials", "token_url": "https://t/", "client_id": "id", "client_secret": "sec", "scope": "read",
		},
	})
	if nested.TokenURL != "https://t/" || nested.ClientID != "id" {
		t.Errorf("nested parse: %+v", nested)
	}
	flat := parseOAuthConfig(map[string]any{
		"oauth_token_url":     "https://flat/",
		"oauth_client_id":     "id2",
		"oauth_client_secret": "sec2",
		"oauth_scope":         "write",
	})
	if flat.TokenURL != "https://flat/" || flat.ClientID != "id2" || flat.Grant != OAuthGrantClientCredentials {
		t.Errorf("flat parse: %+v", flat)
	}
}

func TestValidateOAuth_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		o    OAuthConfig
	}{
		{"bad grant", OAuthConfig{Grant: "password", TokenURL: "x", ClientID: "x", ClientSecret: "x"}},
		{"no token_url", OAuthConfig{Grant: OAuthGrantClientCredentials, ClientID: "x", ClientSecret: "x"}},
		{"no client_id", OAuthConfig{Grant: OAuthGrantClientCredentials, TokenURL: "x", ClientSecret: "x"}},
		{"no client_secret", OAuthConfig{Grant: OAuthGrantClientCredentials, TokenURL: "x", ClientID: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateOAuth(tc.o); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestConfig_Validate_OAuth(t *testing.T) {
	cfg := Config{
		Endpoint:       "https://x/mcp",
		AuthMode:       AuthModeOAuth,
		TrustLevel:     TrustLevelUntrusted,
		ConnectTimeout: time.Second, CallTimeout: time.Second,
		OAuth: OAuthConfig{
			Grant: OAuthGrantClientCredentials, TokenURL: "https://t/",
			ClientID: "id", ClientSecret: "sec",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid OAuth config, got %v", err)
	}
}

// parseFormBytes decodes an application/x-www-form-urlencoded body into a
// url.Values map for assertions.
func parseFormBytes(b []byte) url.Values {
	v, _ := url.ParseQuery(string(b))
	return v
}

func TestOAuthTokenSource_ReacquireFailureCapturesError(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("nope"))
	})
	src := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)
	if err := src.Reacquire(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if src.Status().LastError == "" {
		t.Error("LastError should be populated after Reacquire failure")
	}
}

func TestAuthRoundTripper_AppliesBearerAndAPIKey(t *testing.T) {
	got := http.Header{}
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		maps.Copy(got, r.Header)
	}))
	t.Cleanup(ts.Close)

	client := &http.Client{
		Transport: &authRoundTripper{
			mode: AuthModeBearer, credential: "tok-1", base: http.DefaultTransport,
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, http.NoBody)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if got.Get("Authorization") != "Bearer tok-1" {
		t.Errorf("bearer header missing: %v", got)
	}

	got = http.Header{}
	client = &http.Client{
		Transport: &authRoundTripper{
			mode: AuthModeAPIKey, credential: "key-2", base: http.DefaultTransport,
		},
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, http.NoBody)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if got.Get("X-API-Key") != "key-2" {
		t.Errorf("X-API-Key header missing: %v", got)
	}
}

func TestAuthRoundTripper_OAuthFailureReturnsError(t *testing.T) {
	// No token source configured for oauth mode — applyAuth should error.
	client := &http.Client{
		Transport: &authRoundTripper{
			mode: AuthModeOAuth, base: http.DefaultTransport,
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/", http.NoBody)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error from missing token source")
	}
	if !strings.Contains(err.Error(), "token source not configured") {
		t.Errorf("got %v", err)
	}
}

func TestAuthRoundTripper_OAuthInjectsBearer(t *testing.T) {
	tokenServer := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "fresh-tok", ExpiresIn: 3600,
		})
	})
	source := newOAuthTokenSource(defaultOAuthConfig(tokenServer), "test", nil)

	got := http.Header{}
	echo := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		maps.Copy(got, r.Header)
	}))
	t.Cleanup(echo.Close)

	client := &http.Client{
		Transport: &authRoundTripper{
			mode: AuthModeOAuth, tokenSource: source, base: http.DefaultTransport,
		},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, echo.URL, http.NoBody)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if got.Get("Authorization") != "Bearer fresh-tok" {
		t.Errorf("bearer header missing: %v", got)
	}
}

func TestOAuthTokenSource_ReacquireAuthorizationCode_NoRefreshTokenReturnsReauthError(t *testing.T) {
	cfg := OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     "http://unused",
		ClientID:     "id",
		ClientSecret: "sec",
	}
	store := NewMemoryTokenStore()
	src := newOAuthTokenSource(cfg, "vendor", store)

	err := src.Reacquire(context.Background())
	if err == nil {
		t.Fatal("expected error when no refresh token, got nil")
	}
	if !strings.Contains(err.Error(), "no refresh token") {
		t.Errorf("error should mention missing refresh token: %v", err)
	}
	if !strings.Contains(err.Error(), "Connect") {
		t.Errorf("error should suggest Connect button: %v", err)
	}
}

func TestOAuthTokenSource_ReacquireAuthorizationCode_RefreshSucceeds(t *testing.T) {
	var seenGrant string
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenGrant = parseFormBytes(body).Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "fresh", RefreshToken: "newref", ExpiresIn: 3600,
		})
	})
	cfg := OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     tokenURL,
		ClientID:     "id",
		ClientSecret: "sec",
	}
	store := NewMemoryTokenStore()
	if err := store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor", AccessToken: "stale", RefreshToken: "oldref",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src := newOAuthTokenSource(cfg, "vendor", store)

	if err := src.Reacquire(context.Background()); err != nil {
		t.Fatalf("Reacquire: %v", err)
	}
	if seenGrant != "refresh_token" {
		t.Errorf("expected refresh_token grant, got %q", seenGrant)
	}
	persisted, err := store.Get(context.Background(), "vendor")
	if err != nil {
		t.Fatalf("Get persisted: %v", err)
	}
	if persisted.AccessToken != "fresh" || persisted.RefreshToken != "newref" {
		t.Errorf("persisted tokens not rotated: %+v", persisted)
	}
}

// TestSetTokenStore_RetriesAuthorizationCodePlaceholders proves the
// race-fix for the live-found bug: a toolkit that already has placeholder
// authorization_code connections (because the token store wasn't wired
// when AddConnection ran) MUST retry those placeholders when the store
// is finally attached, so persisted tokens survive process restarts.
func TestSetTokenStore_RetriesAuthorizationCodePlaceholders(t *testing.T) {
	// Token endpoint that always returns a valid token.
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "valid-acc", RefreshToken: "valid-ref", ExpiresIn: 3600,
		})
	})

	// Real MCP upstream that requires Bearer auth.
	upstreamURL := upstreamServer(t)

	// Step 1: AddConnection BEFORE token store is wired. Connection
	// should land as an "awaiting reauth" placeholder.
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	cfg := map[string]any{
		"endpoint":                upstreamURL,
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "3s",
		"call_timeout":            "3s",
	}
	if err := tk.AddConnection("vendor", cfg); err != nil {
		t.Fatalf("AddConnection (placeholder expected to be created without error): %v", err)
	}
	if got := len(tk.Tools()); got != 0 {
		t.Fatalf("placeholder should have zero tools, got %d", got)
	}

	// Step 2: Pre-seed token store and wire it. Placeholder should be
	// retried automatically.
	store := NewMemoryTokenStore()
	if err := store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "valid-acc",
		RefreshToken:   "valid-ref",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	tk.SetTokenStore(store)

	// Step 3: Connection should now be live with discovered tools.
	tools := tk.Tools()
	if len(tools) == 0 {
		t.Fatalf("expected SetTokenStore to retry placeholder and discover tools, got %d", len(tools))
	}
	for _, n := range tools {
		if !strings.HasPrefix(n, "vendor"+NamespaceSeparator) {
			t.Errorf("tool %q missing connection prefix", n)
		}
	}
}

func TestOAuthTokenSource_ReacquireAuthorizationCode_RefreshFailsCapturesError(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	cfg := OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     tokenURL,
		ClientID:     "id",
		ClientSecret: "sec",
	}
	store := NewMemoryTokenStore()
	_ = store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor", RefreshToken: "stillgood",
	})
	src := newOAuthTokenSource(cfg, "vendor", store)

	err := src.Reacquire(context.Background())
	if err == nil {
		t.Fatal("expected error on refresh failure")
	}
	st := src.Status()
	if st.LastError == "" {
		t.Errorf("expected lastError to be captured")
	}
}

func TestStatus_NotConfiguredReturnsNil(t *testing.T) {
	tk := New("primary")
	if tk.Status("missing") != nil {
		t.Error("expected nil for missing connection")
	}
}

func TestReacquireOAuthToken_NotFoundError(t *testing.T) {
	tk := New("primary")
	err := tk.ReacquireOAuthToken(context.Background(), "missing")
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("got %v, want ErrConnectionNotFound", err)
	}
}

func TestReacquireOAuthToken_NotConfiguredError(t *testing.T) {
	tk := New("primary")
	tk.connections["bearer"] = &upstream{
		config: Config{ConnectionName: "bearer", AuthMode: AuthModeBearer},
		client: &upstreamClient{cfg: Config{}}, // no oauth field
	}
	err := tk.ReacquireOAuthToken(context.Background(), "bearer")
	if err == nil || !strings.Contains(err.Error(), "not configured for OAuth") {
		t.Errorf("got %v", err)
	}
}

func TestReacquireOAuthToken_UnhealthyClientError(t *testing.T) {
	tk := New("primary")
	tk.connections["dead"] = &upstream{
		config: Config{ConnectionName: "dead", AuthMode: AuthModeOAuth},
		// nil client (unhealthy / unreached upstream)
	}
	err := tk.ReacquireOAuthToken(context.Background(), "dead")
	if err == nil || !strings.Contains(err.Error(), "not configured for OAuth") {
		t.Errorf("got %v", err)
	}
}

func TestStatus_OAuthFieldsPopulated(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "tok", ExpiresIn: 3600,
		})
	})
	source := newOAuthTokenSource(defaultOAuthConfig(tokenURL), "test", nil)
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatalf("seed Token: %v", err)
	}

	tk := New("primary")
	tk.connections["live"] = &upstream{
		config: Config{
			ConnectionName: "live", AuthMode: AuthModeOAuth,
			OAuth: defaultOAuthConfig(tokenURL),
		},
		toolNames: []string{"live__ping"},
		client:    &upstreamClient{cfg: Config{AuthMode: AuthModeOAuth}, oauth: source},
	}
	st := tk.Status("live")
	if st == nil {
		t.Fatal("expected status, got nil")
	}
	if !st.Healthy {
		t.Error("expected Healthy=true (client is non-nil)")
	}
	if st.AuthMode != AuthModeOAuth {
		t.Errorf("AuthMode: got %q", st.AuthMode)
	}
	if st.OAuth == nil {
		t.Fatal("expected OAuth status populated")
	}
	if !st.OAuth.TokenAcquired {
		t.Error("expected TokenAcquired=true")
	}
}
