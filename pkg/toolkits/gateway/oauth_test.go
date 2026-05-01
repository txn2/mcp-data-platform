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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestClearStaleStateLocked_NilStoreIsSafe covers the defensive
// store==nil branch in clearStaleStateLocked. Used by client_credentials
// grants which legitimately run with a nil store; clearing in-memory
// state must not panic when there's nothing to delete.
func TestClearStaleStateLocked_NilStoreIsSafe(t *testing.T) {
	src := newOAuthTokenSource(OAuthConfig{
		Grant: OAuthGrantClientCredentials,
	}, "test", nil)
	src.mu.Lock()
	defer src.mu.Unlock()
	src.state.AccessToken = "x"
	src.state.RefreshToken = "y"
	src.clearStaleStateLocked(context.Background())
	assert.Empty(t, src.state.AccessToken)
	assert.Empty(t, src.state.RefreshToken)
}

// TestClearStaleStateLocked_DeleteErrorPreservesCallerLastError verifies
// the contract that clearStaleStateLocked never overwrites the
// caller's lastError on Delete failure. The caller (Token / Reacquire)
// has already recorded the IdP rejection error — that's what operators
// need to see in Status. Cleanup-side failures go to slog.Warn only,
// so they're visible in logs without clobbering the diagnostic chain.
func TestClearStaleStateLocked_DeleteErrorPreservesCallerLastError(t *testing.T) {
	src := newOAuthTokenSource(OAuthConfig{
		Grant: OAuthGrantAuthorizationCode,
	}, "test", &erroringDeleteStore{})
	src.mu.Lock()
	defer src.mu.Unlock()
	const callerErr = "oauth: 400 invalid_grant: revoked (oauth: refresh token revoked by issuer)"
	src.lastError = callerErr
	src.state.RefreshToken = "doomed"
	src.clearStaleStateLocked(context.Background())
	assert.Equal(t, callerErr, src.lastError,
		"clearStaleStateLocked must preserve caller's lastError so Status "+
			"shows the IdP rejection, not the cleanup-side Delete failure")
	assert.True(t, src.refreshTokenRevoked,
		"refreshTokenRevoked must still be set to true even when Delete fails")
	assert.Empty(t, src.state.RefreshToken,
		"in-memory refresh token must still be cleared even when Delete fails")
}

// erroringDeleteStore is a minimal TokenStore that returns a non-
// ErrTokenNotFound error on Delete. Used to exercise the failure
// branch of clearStaleStateLocked.
type erroringDeleteStore struct{}

func (*erroringDeleteStore) Get(_ context.Context, _ string) (*PersistedToken, error) {
	return nil, ErrTokenNotFound
}
func (*erroringDeleteStore) Set(_ context.Context, _ PersistedToken) error { return nil }
func (*erroringDeleteStore) Delete(_ context.Context, _ string) error {
	return errors.New("simulated delete failure")
}

// TestExchangeLocked_TransportError covers the diagnostic-log branch
// on transport-level token-request failures (server closed, DNS, TLS,
// etc.). Uses httptest.NewServer().Close() rather than a raw listener
// because httptest.Server tracks active connections and ensures the
// closed address is not handed back out by the OS during the test —
// avoiding the rare port-reuse race that bare `net.Listen + Close`
// has on systems with aggressive port recycling.
func TestExchangeLocked_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	closedURL := srv.URL
	srv.Close()

	src := newOAuthTokenSource(OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     closedURL + "/token",
		ClientID:     "id",
		ClientSecret: "sec",
	}, "test", nil)
	src.client = &http.Client{Timeout: 500 * time.Millisecond}
	_, err := src.Token(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth: token request",
		"transport-level errors must be wrapped as 'oauth: token request: ...'")
}

// TestURLHost_UnparseableFallsBack covers the fallback branch in
// URLHost: when url.Parse can't extract a host (e.g. raw scheme-less
// string), the helper returns the original input so log fields never
// go empty.
func TestURLHost_UnparseableFallsBack(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"happy path returns host only", "https://idp.example.com/realms/x", "idp.example.com"},
		{"scheme-less falls back to raw", "not-a-url", "not-a-url"},
		{"broken scheme falls back to raw", "://broken", "://broken"},
		{"empty input returns empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, URLHost(tc.in))
		})
	}
}

// TestIngestOAuthToken_ConnectionNotFound covers the not-found
// branch (fresh slog.Warn added for diagnostic visibility). When the
// admin OAuth callback handler races a RemoveConnection (or the
// connection was deleted between oauth-start and oauth-callback),
// IngestOAuthToken must surface a structured error rather than panic.
func TestIngestOAuthToken_ConnectionNotFound(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{
		Name:        "missing",
		AccessToken: "x",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConnectionNotFound)
}

// TestIngestOAuthToken_TokenStoreSetFailsSurfaces covers the
// IngestTokenResponse-error branch of IngestOAuthToken: when the
// underlying token store rejects Set (DB unreachable, encryption
// error, etc.), the error must be wrapped with the connection name
// and surfaced to the caller — not swallowed.
func TestIngestOAuthToken_TokenStoreSetFailsSurfaces(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetTokenStore(&erroringSetStore{})

	cfg := map[string]any{
		"endpoint":                "https://upstream.example.com/mcp",
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_authorization_url": "https://idp.example.com/auth",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	require.NoError(t, tk.AddConnection("vendor", cfg))

	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{
		Name:         "vendor",
		AccessToken:  "fresh",
		RefreshToken: "fresh-r",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ingest token",
		"failure inside IngestTokenResponse must wrap the underlying error "+
			"with the 'gateway: <name>: ingest token: …' prefix")
}

// erroringSetStore returns an error from Set so IngestOAuthToken's
// IngestTokenResponse → persistLocked path surfaces a Set failure
// to the caller.
type erroringSetStore struct{}

func (*erroringSetStore) Get(_ context.Context, _ string) (*PersistedToken, error) {
	return nil, ErrTokenNotFound
}

func (*erroringSetStore) Set(_ context.Context, _ PersistedToken) error {
	return errors.New("simulated set failure")
}
func (*erroringSetStore) Delete(_ context.Context, _ string) error { return nil }

// TestIngestOAuthToken_PersistsAndRebuilds covers the happy path:
// IngestTokenResponse persists the new tokens, RemoveConnection drops
// the placeholder, and the subsequent AddConnection re-dials with the
// fresh credentials. Uses an upstream MCP server so the dial actually
// succeeds and we can verify the connection becomes live.
func TestIngestOAuthToken_PersistsAndRebuilds(t *testing.T) {
	upstreamURL := upstreamServer(t)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	store := NewMemoryTokenStore()
	tk.SetTokenStore(store)

	// Pre-seed a placeholder authcode connection (no token yet).
	cfg := map[string]any{
		"endpoint":                upstreamURL,
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         "https://idp.example.com/token",
		"oauth_authorization_url": "https://idp.example.com/auth",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "3s",
		"call_timeout":            "3s",
	}
	require.NoError(t, tk.AddConnection("vendor", cfg))

	// Ingest a fresh token set as the OAuth callback handler would.
	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{
		Name:            "vendor",
		AccessToken:     "fresh-access",
		RefreshToken:    "fresh-refresh",
		ExpiresIn:       3600,
		Scope:           "openid profile",
		AuthenticatedBy: "admin@example.com",
	})
	require.NoError(t, err, "ingestion + rebuild must succeed against a healthy upstream")

	// Persisted token row must reflect the ingestion.
	rec, getErr := store.Get(context.Background(), "vendor")
	require.NoError(t, getErr)
	assert.Equal(t, "fresh-access", rec.AccessToken)
	assert.Equal(t, "fresh-refresh", rec.RefreshToken)
	assert.Equal(t, "admin@example.com", rec.AuthenticatedBy)

	// Connection must be live (post-rebuild) — Status reports Healthy.
	status := tk.Status("vendor")
	require.NotNil(t, status)
	assert.True(t, status.Healthy, "connection must be promoted to live after IngestOAuthToken's rebuild")
}

// TestDialContext_AppliesConfiguredTimeout proves the fix for the
// dial-timeout bug: every discover() call now goes through dialContext,
// which bounds the dial by cfg.ConnectTimeout. Pre-fix, addParsedConnection
// used context.Background() with no deadline, so a hung upstream
// MCP-protocol handshake held the OAuth callback's HTTP response open
// until the SDK's internal timeout fired (minutes, in production) —
// operators saw this as "Loading..." for over a minute on the admin
// page after clicking Connect.
//
// We unit-test the helper directly because the integration path
// involves the MCP SDK's session establishment, which doesn't release
// hung-server connections cleanly during test teardown.
func TestDialContext_AppliesConfiguredTimeout(t *testing.T) {
	cfg := Config{ConnectTimeout: 750 * time.Millisecond}
	before := time.Now()
	ctx, cancel := dialContext(cfg)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "dialContext must produce a context with a deadline")
	assert.WithinDuration(t, before.Add(750*time.Millisecond), deadline, 50*time.Millisecond,
		"deadline must equal now + cfg.ConnectTimeout (within scheduling slack)")
}

// TestDialContext_FallsBackToDefault covers the zero-value case: when
// an operator constructs a Config without setting ConnectTimeout (or
// when the YAML omits the field), dialContext must fall back to the
// package default rather than producing an immediately-canceled
// context (which would make every dial fail before it even started).
func TestDialContext_FallsBackToDefault(t *testing.T) {
	cfg := Config{} // ConnectTimeout zero — exercises fallback
	before := time.Now()
	ctx, cancel := dialContext(cfg)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, before.Add(DefaultConnectTimeout), deadline, 50*time.Millisecond,
		"dialContext must use DefaultConnectTimeout when cfg.ConnectTimeout is zero")
}

// TestDialContext_NegativeTimeoutFallsBackToDefault covers a defensive
// edge: a negative ConnectTimeout (parse oddity, hand-rolled config)
// must NOT produce a context that's already past its deadline.
func TestDialContext_NegativeTimeoutFallsBackToDefault(t *testing.T) {
	cfg := Config{ConnectTimeout: -1 * time.Second}
	before := time.Now()
	ctx, cancel := dialContext(cfg)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.True(t, deadline.After(before),
		"dialContext must produce a future deadline even when cfg.ConnectTimeout is negative")
}

// TestInterpretTokenError_RecognizesDeadRefresh proves
// interpretTokenError wraps errRefreshTokenRevoked when the IdP
// returns 400 + invalid_grant FOR a refresh_token grant only (RFC
// 6749 §5.2). Other grants, other status codes, and other error
// codes pass through verbatim — they may be transient or carry a
// different meaning (bad authorization_code, bad client_secret) that
// the caller must NOT respond to by clearing stored credentials.
func TestInterpretTokenError_RecognizesDeadRefresh(t *testing.T) {
	cases := []struct {
		name      string
		grantType string
		status    int
		errCode   string
		wantDead  bool
	}{
		// Refresh-grant cases: invalid_grant is the only definitive
		// "refresh token dead" signal.
		{"refresh+400 invalid_grant — dead", "refresh_token", 400, "invalid_grant", true},
		{"refresh+400 invalid_token — not RFC 6749, not dead", "refresh_token", 400, "invalid_token", false},
		{"refresh+400 invalid_request — transient", "refresh_token", 400, "invalid_request", false},
		{"refresh+401 invalid_grant — wrong status, not dead", "refresh_token", 401, "invalid_grant", false},
		{"refresh+500 invalid_grant — 5xx is transient", "refresh_token", 500, "invalid_grant", false},

		// Non-refresh grants: invalid_grant means something different
		// (e.g. authorization_code is bad/expired) — must NEVER wrap
		// the refresh-revoked sentinel.
		{"authorization_code+400 invalid_grant — bad code, not refresh-dead", "authorization_code", 400, "invalid_grant", false},
		{"client_credentials+400 invalid_grant — bad secret, not refresh-dead", "client_credentials", 400, "invalid_grant", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"error":"` + tc.errCode + `","error_description":"x"}`)
			err := interpretTokenError(tc.grantType, tc.status, body)
			if errors.Is(err, errRefreshTokenRevoked) != tc.wantDead {
				t.Errorf("grant=%q status=%d code=%q: errors.Is(errRefreshTokenRevoked) = %v, want %v",
					tc.grantType, tc.status, tc.errCode,
					errors.Is(err, errRefreshTokenRevoked), tc.wantDead)
			}
		})
	}
}

// TestToken_RefreshDeadClearsState proves the stale-token-noise fix:
// when the IdP definitively rejects a refresh token, the source clears
// in-memory state AND deletes the persisted row, so the next attempt
// doesn't replay the same dead credential against the IdP.
func TestToken_RefreshDeadClearsState(t *testing.T) {
	// Token endpoint returns 400 invalid_grant — the canonical
	// "your refresh token is dead, stop asking" response.
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token is not active"}`))
	})

	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "old-access",
		RefreshToken:   "stale-refresh",
		ExpiresAt:      time.Now().Add(-time.Hour), // expired so refresh path fires
	}))

	cfg := OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     tokenURL,
		ClientID:     "id",
		ClientSecret: "sec",
	}
	src := newOAuthTokenSource(cfg, "vendor", store)

	_, err := src.Token(context.Background())
	require.Error(t, err, "Token must propagate the reauth-required error")

	// Persisted row must be gone — proves clearStaleStateLocked ran.
	_, getErr := store.Get(context.Background(), "vendor")
	assert.ErrorIs(t, getErr, ErrTokenNotFound,
		"persisted token row must be deleted after IdP signals invalid_grant")

	// In-memory state must also be clear so a subsequent call doesn't
	// replay the same dead refresh.
	src.mu.Lock()
	defer src.mu.Unlock()
	assert.Empty(t, src.state.RefreshToken,
		"in-memory refresh_token must be cleared after definitive rejection")
	assert.Empty(t, src.state.AccessToken,
		"in-memory access_token must be cleared after definitive rejection")
}

// TestToken_RefreshTransientErrorKeepsState ensures transient failures
// do NOT clear the persisted token. Only RFC 6749 §5.2 invalid_grant
// at 400 (in the refresh path) triggers clearStaleStateLocked. Every
// other status / non-RFC error code is treated as transient — the
// persisted refresh must survive so the next attempt can succeed.
func TestToken_RefreshTransientErrorKeepsState(t *testing.T) {
	// Each subtest spins a fake token endpoint returning a different
	// transient signal. The contract is the same in every case: the
	// persisted refresh token must remain after Token() fails.
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "503 Service Unavailable (temporary IdP outage)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("upstream busy"))
			},
		},
		{
			name: "500 Internal Server Error (IdP bug)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("kaboom"))
			},
		},
		{
			name: "401 invalid_grant (status mismatch — 6749 says 400)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			},
		},
		{
			name: "400 invalid_request (RFC code, not refresh-dead)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"missing param"}`))
			},
		},
		{
			name: "400 invalid_token (RFC 6750 not RFC 6749 — not refresh-dead)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			},
		},
		{
			name: "400 with non-JSON body (mangled IdP response)",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("not json"))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokenURL := fakeTokenServer(t, tc.handler)
			store := NewMemoryTokenStore()
			require.NoError(t, store.Set(context.Background(), PersistedToken{
				ConnectionName: "vendor",
				AccessToken:    "still-valid",
				RefreshToken:   "still-valid-r",
				ExpiresAt:      time.Now().Add(-time.Hour),
			}))

			cfg := OAuthConfig{
				Grant:        OAuthGrantAuthorizationCode,
				TokenURL:     tokenURL,
				ClientID:     "id",
				ClientSecret: "sec",
			}
			src := newOAuthTokenSource(cfg, "vendor", store)

			_, err := src.Token(context.Background())
			require.Error(t, err)

			// Persisted row MUST remain — the IdP didn't say "dead",
			// it just signaled something the platform must not act on
			// by deleting credentials.
			rec, getErr := store.Get(context.Background(), "vendor")
			require.NoError(t, getErr,
				"transient errors must not delete the persisted token row")
			assert.Equal(t, "still-valid-r", rec.RefreshToken,
				"transient IdP errors must NOT clear the persisted refresh token")
		})
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
	err := interpretTokenError("refresh_token", http.StatusInternalServerError, []byte(big))
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

// TestSetTokenStore_PlaceholderRetainedWhenUpstreamDown covers the
// "upstream still unreachable" path: even after the token store is
// wired and the placeholder is retried, if the upstream MCP itself is
// down the placeholder must be retained — a "Connect" UI is wrong here
// because the user already authorized; the upstream is just sick.
func TestSetTokenStore_PlaceholderRetainedWhenUpstreamDown(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "valid", RefreshToken: "valid-r", ExpiresIn: 3600,
		})
	})
	// Upstream MCP that immediately 503s (simulating a sick vendor).
	deadUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(deadUpstream.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	cfg := map[string]any{
		"endpoint":                deadUpstream.URL,
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/auth",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	require.NoError(t, tk.AddConnection("vendor", cfg))

	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor", AccessToken: "valid", RefreshToken: "valid-r",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	tk.SetTokenStore(store) // retry runs; upstream still down

	// Connection should still be registered (placeholder preserved) but
	// have zero tools — UI should keep showing "Connect".
	statuses := tk.ListConnections()
	assert.Len(t, statuses, 1, "placeholder must be preserved when retry fails")
	assert.Empty(t, tk.Tools(), "no tools should leak from a sick upstream")
}

// TestSetTokenStore_MultiplePlaceholdersRetriedIndependently asserts
// each authorization_code placeholder is retried on its own — one
// failure does not prevent another from succeeding.
func TestSetTokenStore_MultiplePlaceholdersRetriedIndependently(t *testing.T) {
	goodTokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "good", RefreshToken: "good-r", ExpiresIn: 3600,
		})
	})
	upstreamA := upstreamServer(t)
	deadUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(deadUpstream.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	mkCfg := func(endpoint, name string) map[string]any {
		return map[string]any{
			"endpoint":                endpoint,
			"connection_name":         name,
			"auth_mode":               AuthModeOAuth,
			"oauth_grant":             OAuthGrantAuthorizationCode,
			"oauth_token_url":         goodTokenURL,
			"oauth_authorization_url": goodTokenURL + "/auth",
			"oauth_client_id":         "id",
			"oauth_client_secret":     "sec",
			"connect_timeout":         "1s",
			"call_timeout":            "1s",
		}
	}
	require.NoError(t, tk.AddConnection("a", mkCfg(upstreamA, "a")))
	require.NoError(t, tk.AddConnection("b", mkCfg(deadUpstream.URL, "b")))

	store := NewMemoryTokenStore()
	for _, name := range []string{"a", "b"} {
		require.NoError(t, store.Set(context.Background(), PersistedToken{
			ConnectionName: name, AccessToken: "good", RefreshToken: "good-r",
			ExpiresAt: time.Now().Add(time.Hour),
		}))
	}

	tk.SetTokenStore(store)

	// "a" should be live (real upstream); "b" should still be a
	// placeholder (sick upstream) but retained.
	statuses := tk.ListConnections()
	assert.Len(t, statuses, 2)
	assert.NotEmpty(t, tk.Tools(), "live upstream a should contribute tools")
	for _, name := range tk.Tools() {
		assert.True(t, strings.HasPrefix(name, "a"+NamespaceSeparator),
			"tools must come only from the live placeholder, not the dead one: got %q", name)
	}
}

// TestSetTokenStore_NilStoreNoOp confirms passing nil leaves the
// toolkit in a sane state (placeholders unchanged, no panic).
func TestSetTokenStore_NilStoreNoOp(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetTokenStore(nil) // empty toolkit — does not panic
	assert.Empty(t, tk.ListConnections())
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

// TestStatus_PlaceholderReturnsOAuthNeedsReauth proves the admin UI fix
// for the "Connect button missing" bug: when an authorization_code OAuth
// connection has been saved but never authorized, AddConnection records
// a placeholder upstream with client == nil. Status() must still surface
// the OAuth field — populated as NeedsReauth=true — so the admin UI can
// render the Connect button.
func TestStatus_PlaceholderReturnsOAuthNeedsReauth(t *testing.T) {
	// Token endpoint that always 401s, simulating the "no refresh token
	// yet, browser sign-in required" state. AddConnection's discover()
	// fails, the toolkit creates a placeholder, and Status() must report
	// the placeholder as needs_reauth so the UI knows to prompt Connect.
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	cfg := map[string]any{
		"endpoint":                "https://upstream.example.com/mcp",
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	require.NoError(t, tk.AddConnection("vendor", cfg))

	st := tk.Status("vendor")
	require.NotNil(t, st, "Status must return a snapshot for the placeholder")
	assert.False(t, st.Healthy, "placeholder is not healthy (no live client)")
	assert.Equal(t, AuthModeOAuth, st.AuthMode)
	require.NotNil(t, st.OAuth,
		"OAuth field must be populated for placeholders so the admin UI can show Connect")
	assert.True(t, st.OAuth.NeedsReauth, "placeholder must report NeedsReauth=true")
	assert.False(t, st.OAuth.TokenAcquired)
	assert.Equal(t, OAuthGrantAuthorizationCode, st.OAuth.Grant)
	assert.NotEmpty(t, st.OAuth.LastError,
		"placeholder must surface the dial error via LastError so operators "+
			"can see WHY the upstream rejected — issue #349")
}

// TestAddConnection_PlaceholderRecordsLastError proves the fix for #349:
// when an authorization_code connection's dial fails, the placeholder
// upstream stores the discover() error string in its lastError field
// so Status() can surface the actual upstream rejection reason — not
// just the silent "awaiting reauth" warning operators were getting before.
func TestAddConnection_PlaceholderRecordsLastError(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	cfg := map[string]any{
		"endpoint":                "https://upstream.example.com/mcp",
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/authorize",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	require.NoError(t, tk.AddConnection("vendor", cfg))

	// Direct field access (same package) — proves the placeholder
	// captured the error string for later Status() retrieval.
	tk.mu.RLock()
	u := tk.connections["vendor"]
	tk.mu.RUnlock()
	require.NotNil(t, u, "placeholder must exist")
	require.Nil(t, u.client, "placeholder must have nil client")
	assert.NotEmpty(t, u.lastError, "placeholder must record the dial error")
}

// TestSetTokenStore_PlaceholderRetryUpdatesLastError proves the
// retry-after-store-wired path also keeps lastError fresh. Without this,
// Status() would surface a stale error from AddConnection time even
// after a retry produced a different (or the same) failure.
func TestSetTokenStore_PlaceholderRetryUpdatesLastError(t *testing.T) {
	// Token endpoint that succeeds — refresh works.
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse{ //nolint:gosec // G117 false positive: OAuth response shape, not a credential
			AccessToken: "valid", RefreshToken: "valid-r", ExpiresIn: 3600,
		})
	})
	// Upstream that always returns 503 — initialize / list-tools always fail
	// even when the token is present and refresh works.
	deadUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(deadUpstream.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	cfg := map[string]any{
		"endpoint":                deadUpstream.URL,
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/auth",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	// First Add — placeholder with the original (no-token) error.
	require.NoError(t, tk.AddConnection("vendor", cfg))
	tk.mu.RLock()
	originalErr := tk.connections["vendor"].lastError
	tk.mu.RUnlock()
	require.NotEmpty(t, originalErr)

	// Pre-seed token store and wire it. Retry will run, will succeed at
	// fetching a token, but will fail at dialing the dead upstream.
	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "valid",
		RefreshToken:   "valid-r",
		ExpiresAt:      time.Now().Add(time.Hour),
	}))
	tk.SetTokenStore(store)

	// Placeholder must remain (sick upstream) and lastError must have
	// CHANGED to reflect the most recent failure — the dead-upstream
	// 503 surfaced during retry, not the original "no token" error.
	// Asserting NotEqual (instead of just NotEmpty) is what actually
	// proves the retry path called recordPlaceholderError; a non-empty
	// check would pass even if recordPlaceholderError did nothing.
	tk.mu.RLock()
	u := tk.connections["vendor"]
	tk.mu.RUnlock()
	require.NotNil(t, u)
	require.Nil(t, u.client, "placeholder must remain when retry fails")
	require.NotEmpty(t, u.lastError, "lastError must remain populated after retry")
	assert.NotEqual(t, originalErr, u.lastError,
		"retry path must update lastError to the new failure (was %q, still %q)",
		originalErr, u.lastError)
}

// TestRecordPlaceholderError_DefensiveBranches covers the no-op paths
// of recordPlaceholderError: missing connection (concurrent removal)
// and already-promoted-to-live (concurrent retry success). Neither path
// should panic or mutate unrelated state.
func TestRecordPlaceholderError_DefensiveBranches(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	// (a) Missing connection — must be a silent no-op AND must NOT
	// create a new entry in the connections map. A buggy implementation
	// that fell through into "set lastError on a fresh upstream" would
	// have inserted a phantom placeholder; we explicitly guard against
	// that here.
	tk.recordPlaceholderError("does-not-exist", "ignored")
	tk.mu.RLock()
	_, exists := tk.connections["does-not-exist"]
	tk.mu.RUnlock()
	assert.False(t, exists,
		"recordPlaceholderError must not create entries for missing connections")

	// (b) Connection exists but is already live (client != nil). Pre-
	// existing lastError (if any) must NOT be overwritten — the live
	// client owns the connection's status now.
	tk.mu.Lock()
	tk.connections["live"] = &upstream{
		name:      "live",
		config:    Config{ConnectionName: "live", AuthMode: AuthModeOAuth},
		client:    &upstreamClient{cfg: Config{}},
		lastError: "stale",
	}
	tk.mu.Unlock()

	tk.recordPlaceholderError("live", "should be ignored")

	tk.mu.RLock()
	got := tk.connections["live"].lastError
	tk.mu.RUnlock()
	assert.Equal(t, "stale", got,
		"recordPlaceholderError must not overwrite a live connection's lastError")
}

// TestStatus_PlaceholderSurfaceLastErrorPrecedence verifies that the
// placeholder's lastError takes precedence over the token-source's
// Status().LastError when surfacing through ConnectionStatus.OAuth. The
// placeholder error is the one operators need to see.
func TestStatus_PlaceholderSurfaceLastErrorPrecedence(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	tk.mu.Lock()
	tk.connections["vendor"] = &upstream{
		name: "vendor",
		config: Config{
			ConnectionName: "vendor",
			AuthMode:       AuthModeOAuth,
			OAuth: OAuthConfig{
				Grant:        OAuthGrantAuthorizationCode,
				TokenURL:     "https://idp.example.com/token",
				ClientID:     "id",
				ClientSecret: "sec",
			},
		},
		desc:      "Awaiting OAuth authorization",
		lastError: "connect to https://upstream.example.com/mcp: HTTP 401 Unauthorized",
	}
	tk.mu.Unlock()

	st := tk.Status("vendor")
	require.NotNil(t, st)
	require.NotNil(t, st.OAuth)
	assert.Equal(t,
		"connect to https://upstream.example.com/mcp: HTTP 401 Unauthorized",
		st.OAuth.LastError,
		"Status() must surface the placeholder's lastError so operators "+
			"see WHY the upstream rejected")
}

// TestStatus_PlaceholderWithStoredTokenReportsAuthorized covers the post-
// restart case: the token store has a valid token, AddConnection's dial
// fails because the upstream is unreachable, so a placeholder is kept.
// Status() must reflect that the operator has already authorized (so the
// UI does NOT push them through Connect again) — only the upstream is
// sick.
func TestStatus_PlaceholderWithStoredTokenReportsAuthorized(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
	})
	deadUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(deadUpstream.Close)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })

	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName:  "vendor",
		AccessToken:     "stored-acc",
		RefreshToken:    "stored-ref",
		ExpiresAt:       time.Now().Add(time.Hour),
		AuthenticatedBy: "admin@example.com",
		AuthenticatedAt: time.Now().UTC(),
	}))
	tk.SetTokenStore(store)

	cfg := map[string]any{
		"endpoint":                deadUpstream.URL,
		"connection_name":         "vendor",
		"auth_mode":               AuthModeOAuth,
		"oauth_grant":             OAuthGrantAuthorizationCode,
		"oauth_token_url":         tokenURL,
		"oauth_authorization_url": tokenURL + "/auth",
		"oauth_client_id":         "id",
		"oauth_client_secret":     "sec",
		"connect_timeout":         "1s",
		"call_timeout":            "1s",
	}
	// Dial will fail because deadUpstream returns 503; placeholder retained.
	require.NoError(t, tk.AddConnection("vendor", cfg))

	st := tk.Status("vendor")
	require.NotNil(t, st)
	require.NotNil(t, st.OAuth, "OAuth status must surface even when upstream is unreachable")
	assert.False(t, st.OAuth.NeedsReauth,
		"already authorized — UI should not push Connect again")
	assert.True(t, st.OAuth.HasRefreshToken)
	assert.Equal(t, "admin@example.com", st.OAuth.AuthenticatedBy)
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

// TestReacquire_RefreshDeadClearsStaleState mirrors Token()'s
// dead-refresh handling on the manual Reacquire path. When an admin
// clicks Reacquire while the IdP has revoked the refresh token:
//   - the IdP rejection error must be returned to the caller,
//   - the persisted row must be deleted (so subsequent Token() calls
//     don't replay the dead credential and spam the IdP audit log),
//   - Status must surface BOTH RefreshTokenRevoked=true (so the UI can
//     show a distinct "click Connect" panel) AND NeedsReauth=true
//     (so the existing UI logic moves the operator past Reacquire),
//   - lastError must contain the IdP-rejection text (with the wrapped
//     sentinel suffix) so operators see the actual cause, not a
//     generic "needs reauth" message.
func TestReacquire_RefreshDeadClearsStaleState(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"session_idle_timeout"}`))
	})

	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "old",
		RefreshToken:   "stale",
		ExpiresAt:      time.Now().Add(-time.Hour),
	}))

	src := newOAuthTokenSource(OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     tokenURL,
		ClientID:     "id",
		ClientSecret: "sec",
	}, "vendor", store)

	err := src.Reacquire(context.Background())
	require.Error(t, err, "Reacquire must propagate the IdP rejection")
	assert.ErrorIs(t, err, errRefreshTokenRevoked,
		"returned error must wrap errRefreshTokenRevoked so admin handlers can react via errors.Is")

	// The persisted row must be gone — proves Reacquire ran the same
	// stale-state cleanup as Token() would have.
	_, getErr := store.Get(context.Background(), "vendor")
	assert.ErrorIs(t, getErr, ErrTokenNotFound,
		"Reacquire must delete the persisted row when refresh is definitively dead "+
			"so the same dead refresh doesn't replay on the next Token() call")

	// Status must report the FULL recovery posture so the UI can render
	// the right messaging without ambiguity.
	st := src.Status()
	assert.True(t, st.RefreshTokenRevoked,
		"RefreshTokenRevoked must be true so the UI can show 'IdP revoked your token' messaging")
	assert.True(t, st.NeedsReauth,
		"NeedsReauth must be true so the UI replaces the Reacquire affordance with Connect")
	assert.False(t, st.HasRefreshToken,
		"HasRefreshToken must be false after the cleanup — the row is gone")
	assert.False(t, st.TokenAcquired,
		"TokenAcquired must be false after the cleanup — in-memory state is empty")
	assert.Contains(t, st.LastError, "invalid_grant",
		"LastError must contain the IdP rejection text (operators need to see the actual cause)")
	assert.Contains(t, st.LastError, "session_idle_timeout",
		"LastError must include the IdP error_description for diagnostic context")
}

// TestStatus_RefreshTokenRevokedFullRecoveryCycle proves the full
// state-machine cycle the admin UI depends on:
//
//   - revoked refresh -> RefreshTokenRevoked flips true,
//     NeedsReauth flips true, store row is gone;
//   - operator runs Connect -> IngestTokenResponse with fresh tokens
//     -> RefreshTokenRevoked flips back to false, NeedsReauth flips
//     to false, TokenAcquired and HasRefreshToken flip to true.
//
// A sticky-true RefreshTokenRevoked would lock the UI into the
// revoked-panel forever even after recovery; a missed flip on the
// happy-path side would leave the panel stale across restarts.
func TestStatus_RefreshTokenRevokedFullRecoveryCycle(t *testing.T) {
	tokenURL := fakeTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})

	store := NewMemoryTokenStore()
	require.NoError(t, store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "old",
		RefreshToken:   "stale",
		ExpiresAt:      time.Now().Add(-time.Hour),
	}))

	src := newOAuthTokenSource(OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     tokenURL,
		ClientID:     "id",
		ClientSecret: "sec",
	}, "vendor", store)

	// Phase 1: trigger the dead-refresh cleanup via the real refresh
	// path (NOT by direct field mutation — we exercise the production
	// state machine, not its internals).
	require.Error(t, src.Reacquire(context.Background()))
	pre := src.Status()
	require.True(t, pre.RefreshTokenRevoked, "phase 1: RefreshTokenRevoked should be true")
	require.True(t, pre.NeedsReauth, "phase 1: NeedsReauth should be true")

	// Phase 2: simulate the operator completing a fresh Connect flow
	// through the real IngestTokenResponse path.
	require.NoError(t, src.IngestTokenResponse(context.Background(), IngestTokenResponseInput{
		AccessToken:     "fresh",
		RefreshToken:    "fresh-r",
		ExpiresIn:       3600,
		AuthenticatedBy: "ops@example.com",
	}))

	post := src.Status()
	assert.False(t, post.RefreshTokenRevoked,
		"phase 2: RefreshTokenRevoked must clear after a successful IngestTokenResponse")
	assert.False(t, post.NeedsReauth,
		"phase 2: NeedsReauth must clear — the connection is fully authorized again")
	assert.True(t, post.TokenAcquired, "phase 2: TokenAcquired must reflect the new access token")
	assert.True(t, post.HasRefreshToken, "phase 2: HasRefreshToken must reflect the new refresh token")
	assert.Empty(t, post.LastError,
		"phase 2: LastError must clear — the operator successfully recovered")
	assert.Equal(t, "ops@example.com", post.AuthenticatedBy,
		"phase 2: AuthenticatedBy must record who completed the fresh Connect flow")
}

// TestIngestTokenResponse_RejectsEmptyAccessToken proves the boundary
// guard added to IngestTokenResponse. An empty access token would
// silently flip TokenAcquired=true on the admin status panel, hiding
// upstream bugs in callers that produce no token but still call
// IngestTokenResponse.
func TestIngestTokenResponse_RejectsEmptyAccessToken(t *testing.T) {
	src := newOAuthTokenSource(OAuthConfig{
		Grant: OAuthGrantAuthorizationCode,
	}, "vendor", nil)

	err := src.IngestTokenResponse(context.Background(), IngestTokenResponseInput{
		AccessToken: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access_token is required")

	st := src.Status()
	assert.False(t, st.TokenAcquired,
		"failed IngestTokenResponse must NOT mutate state — "+
			"an empty access_token cannot flip TokenAcquired to true")
}

// TestEnsureLoadedLocked_TransientErrorRetries verifies the bug fix:
// a transient store error on first load must NOT mark the source
// loaded. Subsequent calls retry rather than locking the source into
// "no token" state when postgres has the row.
func TestEnsureLoadedLocked_TransientErrorRetries(t *testing.T) {
	store := &flakyGetStore{}
	src := newOAuthTokenSource(OAuthConfig{
		Grant: OAuthGrantAuthorizationCode,
	}, "vendor", store)

	// First load: store returns a transient error.
	src.mu.Lock()
	src.ensureLoadedLocked(context.Background())
	loadedAfterFailure := src.loaded
	lastErrAfterFailure := src.lastError
	src.mu.Unlock()

	assert.False(t, loadedAfterFailure,
		"transient store error must NOT mark source loaded — otherwise the source is "+
			"permanently locked into empty state until process restart")
	assert.Contains(t, lastErrAfterFailure, "load token",
		"transient load error must surface on lastError so Status shows the cause")

	// Second load: store now returns the row successfully.
	store.healed = true
	src.mu.Lock()
	src.ensureLoadedLocked(context.Background())
	state := src.state
	src.mu.Unlock()

	assert.Equal(t, "real-access", state.AccessToken,
		"after the store heals, the next ensureLoadedLocked call must succeed")
	assert.Equal(t, "real-refresh", state.RefreshToken,
		"after the store heals, the persisted refresh token must be loaded")
}

// flakyGetStore returns a transient error on the first Get and the
// real row on subsequent calls (after `healed` is set). Used to
// exercise ensureLoadedLocked's retry-on-transient-error contract.
type flakyGetStore struct{ healed bool }

func (s *flakyGetStore) Get(_ context.Context, _ string) (*PersistedToken, error) {
	if !s.healed {
		return nil, errors.New("simulated transient DB error")
	}
	return &PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "real-access",
		RefreshToken:   "real-refresh",
		ExpiresAt:      time.Now().Add(time.Hour),
	}, nil
}
func (*flakyGetStore) Set(_ context.Context, _ PersistedToken) error { return nil }
func (*flakyGetStore) Delete(_ context.Context, _ string) error      { return nil }
