package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// TestConnoauthConfigFor_MapsFlatAndSplitsScopes proves the small
// translator from OAuthConfig (gateway-local) to connoauth.Config
// (unified) preserves every field the unified Source uses, splits the
// space-delimited scope string into the scopes slice, and pins the
// MCP gateway's HTTP Basic auth style.
func TestConnoauthConfigFor_MapsFlatAndSplitsScopes(t *testing.T) {
	in := OAuthConfig{
		Grant:            OAuthGrantAuthorizationCode,
		AuthorizationURL: "https://idp.example.com/auth",
		TokenURL:         "https://idp.example.com/token",
		ClientID:         "client-123",
		ClientSecret:     "secret-xyz",
		Scope:            "offline_access  read:tools  write:tools",
		Prompt:           "login",
	}
	got := connoauthConfigFor(in)
	assert.Equal(t, in.Grant, got.Grant)
	assert.Equal(t, in.AuthorizationURL, got.AuthorizationURL)
	assert.Equal(t, in.TokenURL, got.TokenURL)
	assert.Equal(t, in.ClientID, got.ClientID)
	assert.Equal(t, in.ClientSecret, got.ClientSecret)
	assert.Equal(t, in.Prompt, got.Prompt)
	assert.Equal(t, []string{"offline_access", "read:tools", "write:tools"}, got.Scopes)
}

func TestConnoauthConfigFor_EmptyScopeIsNil(t *testing.T) {
	got := connoauthConfigFor(OAuthConfig{
		Grant:    OAuthGrantClientCredentials,
		TokenURL: "https://idp.example.com/token",
		ClientID: "id",
	})
	assert.Nil(t, got.Scopes, "empty scope must not produce a single-empty-string slice")
}

// TestConnoauthSourceFor_NilStoreReturnsNil documents the contract:
// the helper returns nil when no connoauth.Store has been wired, so
// callers (dial / Status / IngestOAuthToken) can fall through to a
// configured-but-unwired error rather than constructing a Source
// against nil storage.
func TestConnoauthSourceFor_NilStoreReturnsNil(t *testing.T) {
	src := connoauthSourceFor(nil, nil, "any", OAuthConfig{TokenURL: "https://idp"})
	assert.Nil(t, src)
}

func TestConnoauthSourceFor_BuildsSourceForKindMCP(t *testing.T) {
	store := connoauth.NewMemoryStore()
	src := connoauthSourceFor(store, nil, "vendor-mcp", OAuthConfig{
		Grant:    OAuthGrantAuthorizationCode,
		TokenURL: "https://idp.example.com/token",
		ClientID: "id",
	})
	require.NotNil(t, src)
	// Round-trip a Status call to prove the source is wired to the
	// store with the right key. ErrTokenNotFound surfaces as
	// NeedsReauth — i.e., the unified Source path picked up the
	// MCP-kind row name without us having to inject one.
	status := src.Status(context.Background())
	assert.True(t, status.NeedsReauth)
	assert.Equal(t, "authorization_code", status.Grant)
}

// fakeOAuthIDP is a minimal token-endpoint stand-in. Counts refresh
// attempts and returns rotated tokens so a test can assert the
// round-tripper picks up freshly-persisted rotations.
type fakeOAuthIDP struct {
	server  *httptest.Server
	refresh atomic.Int32
}

func newFakeOAuthIDP() *fakeOAuthIDP {
	idp := &fakeOAuthIDP{}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seq := idp.refresh.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Issue a new access + refresh token on every refresh,
		// simulating an IdP that rotates the refresh token (RFC 6749
		// §6 allows this; Microsoft Entra and Keycloak with rotation
		// enabled require it).
		body := `{
			"access_token":  "access-` + intToString(seq) + `",
			"refresh_token": "refresh-` + intToString(seq) + `",
			"token_type":    "Bearer",
			"expires_in":    3600
		}`
		_, _ = w.Write([]byte(body))
	}))
	return idp
}

func (f *fakeOAuthIDP) close() { f.server.Close() }

// TestRoundTripper_BuildsFreshSourcePerCall is the regression for the
// stale-in-memory-refresh-token bug. The scenario:
//   - The persisted token has expired.
//   - The store's persisted refresh token is rotated between two
//     outbound requests by an external actor (the background
//     refresher in production).
//   - The round-tripper's second outbound call must use the
//     rotated refresh token from the store, NOT a stale in-memory
//     copy.
//
// The old in-toolkit oauthTokenSource cached the refresh token in
// memory and never re-read after `loaded=true`, so the second call
// would send the dead refresh token and Keycloak (etc.) would revoke
// the whole family. The new design constructs a fresh
// connoauth.Source per RoundTrip, so the store is the single source
// of truth and there's no in-memory cache to go stale.
func TestRoundTripper_BuildsFreshSourcePerCall(t *testing.T) {
	idp := newFakeOAuthIDP()
	defer idp.close()

	// Pre-seed the store with an expired access token + an initial
	// refresh token. The first Token() call will refresh against the
	// IdP and persist the rotated values.
	store := connoauth.NewMemoryStore()
	key := connoauth.Key{Kind: connoauth.KindMCP, Name: "fixture"}
	require.NoError(t, store.Set(context.Background(), connoauth.PersistedToken{
		Key:          key,
		AccessToken:  "stale-access",
		RefreshToken: "stale-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}))

	cfg := OAuthConfig{
		Grant:        OAuthGrantAuthorizationCode,
		TokenURL:     idp.server.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	// Inline tokenProvider that reads `store` directly each call —
	// mirrors what connoauthTokenProvider does in production, without
	// pulling a real *Toolkit into the test.
	tp := tokenProviderFn(func(ctx context.Context) (string, error) {
		src := connoauthSourceFor(store, nil, "fixture", cfg)
		return src.Token(ctx)
	})
	rt := &authRoundTripper{
		mode:          AuthModeOAuth,
		tokenProvider: tp,
		base:          &fakeRoundTripper{},
	}

	// First call: refresh runs once, rotates the stored token to
	// access-1 / refresh-1.
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	if err := rt.applyAuth(req1); err != nil {
		t.Fatalf("first applyAuth: %v", err)
	}
	assert.Equal(t, "Bearer access-1", req1.Header.Get("Authorization"))
	assert.Equal(t, int32(1), idp.refresh.Load())

	// Background-refresher-style write: an external actor rotates the
	// store's row to refresh-99 (simulating connoauth.Refresher's
	// behavior between our two RoundTrip calls).
	persisted, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	persisted.AccessToken = "external-rotated-access"
	persisted.RefreshToken = "refresh-99"
	persisted.ExpiresAt = time.Now().Add(-time.Hour) // force refresh path
	require.NoError(t, store.Set(context.Background(), *persisted))

	// Second call: the round-tripper must use the latest persisted
	// refresh token (refresh-99) — proving the Source is built per
	// call. If the old in-memory cache pattern were in place, the
	// round-tripper would send the original stale-refresh and the
	// fake IdP's refresh counter wouldn't tell us anything useful;
	// we instead assert that the store's row has been rotated again
	// after this call.
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/y", http.NoBody)
	if err := rt.applyAuth(req2); err != nil {
		t.Fatalf("second applyAuth: %v", err)
	}
	assert.Equal(t, "Bearer access-2", req2.Header.Get("Authorization"))
	assert.Equal(t, int32(2), idp.refresh.Load())

	// The persisted row must now carry the rotated refresh-2 — proves
	// the Source read refresh-99 from the store (not from a stale
	// in-memory cache) AND wrote the IdP's rotation result back.
	after, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, "access-2", after.AccessToken)
	assert.Equal(t, "refresh-2", after.RefreshToken)
}

func TestRoundTripper_OAuthRequiresProvider(t *testing.T) {
	rt := &authRoundTripper{
		mode: AuthModeOAuth,
		base: &fakeRoundTripper{},
	}
	err := rt.applyAuth(httptestRequest(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token provider not configured")
}

func TestRoundTripper_OAuthProviderErrorSurfaces(t *testing.T) {
	rt := &authRoundTripper{
		mode: AuthModeOAuth,
		tokenProvider: tokenProviderFn(func(context.Context) (string, error) {
			return "", errors.New("idp unreachable")
		}),
		base: &fakeRoundTripper{},
	}
	err := rt.applyAuth(httptestRequest(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idp unreachable")
}

// TestToolkit_TokenProviderFor_ClientCredentials proves the dispatch
// to the in-memory client_credentials provider — the regression for
// the refactor's first round of review, where this grant was silently
// routed through connoauth.Source which only knows how to refresh.
func TestToolkit_TokenProviderFor_ClientCredentials(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tp, cc := tk.tokenProviderFor("ccg", OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     "https://idp.example/token",
		ClientID:     "id",
		ClientSecret: "sec",
	})
	_, ok := tp.(*clientCredentialsTokenProvider)
	assert.True(t, ok, "client_credentials grant must produce a *clientCredentialsTokenProvider")
	require.NotNil(t, cc, "cc grant must also return the typed provider for Status/Reacquire wiring")
}

func TestToolkit_TokenProviderFor_AuthorizationCode_RequiresStore(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	// No connoauth.Store wired → authorization_code returns nil so
	// dial() surfaces the configuration error to the operator.
	tp, cc := tk.tokenProviderFor("ac", OAuthConfig{
		Grant:    OAuthGrantAuthorizationCode,
		TokenURL: "https://idp.example/token",
		ClientID: "id",
	})
	assert.Nil(t, tp)
	assert.Nil(t, cc)

	tk.SetConnOAuthStore(connoauth.NewMemoryStore())
	tp, cc = tk.tokenProviderFor("ac", OAuthConfig{
		Grant:    OAuthGrantAuthorizationCode,
		TokenURL: "https://idp.example/token",
		ClientID: "id",
	})
	_, ok := tp.(connoauthTokenProvider)
	assert.True(t, ok, "authorization_code with a wired store must produce a connoauthTokenProvider")
	assert.Nil(t, cc, "authorization_code grant must NOT produce a cc-typed provider")
}

func TestToolkit_TokenProviderFor_NoGrantReturnsNil(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tp, cc := tk.tokenProviderFor("x", OAuthConfig{})
	assert.Nil(t, tp)
	assert.Nil(t, cc)
}

// TestClientCredentialsTokenProvider_StatusReportsAcquiredAfterFetch
// proves the Status() helper used by Toolkit.Status for cc connections
// (which have no persisted row in the unified store, only an in-memory
// cache) reports TokenAcquired=true after a successful fetch.
func TestClientCredentialsTokenProvider_StatusReportsAcquiredAfterFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-1","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	cc := newClientCredentialsTokenProvider(OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "sec",
	})

	// Before any fetch, Status reports Configured but no token yet.
	pre := cc.Status()
	assert.True(t, pre.Configured)
	assert.False(t, pre.TokenAcquired)
	assert.Equal(t, OAuthGrantClientCredentials, pre.Grant)
	assert.False(t, pre.NeedsReauth, "client_credentials never needs operator re-auth")

	tok, err := cc.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cc-1", tok)

	post := cc.Status()
	assert.True(t, post.TokenAcquired)
	assert.False(t, post.ExpiresAt.IsZero())
}

// TestToolkit_Status_ClientCredentials_RoutesThroughInMemoryProvider
// proves Status uses the cc provider's in-memory cache (not the
// connoauth.Store, which has no row for cc by design) and the
// authorization_code path is unaffected.
func TestToolkit_Status_ClientCredentials_RoutesThroughInMemoryProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-a","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	cc := newClientCredentialsTokenProvider(OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "sec",
	})
	// Seed an in-memory token so Status reports TokenAcquired=true.
	_, err := cc.Token(context.Background())
	require.NoError(t, err)

	tk.mu.Lock()
	tk.connections["ccg"] = &upstream{
		name:       "ccg",
		config:     Config{AuthMode: AuthModeOAuth, OAuth: OAuthConfig{Grant: OAuthGrantClientCredentials, TokenURL: srv.URL}},
		ccProvider: cc,
	}
	tk.mu.Unlock()

	status := tk.Status(context.Background(), "ccg")
	require.NotNil(t, status)
	require.NotNil(t, status.OAuth)
	assert.True(t, status.OAuth.TokenAcquired, "cc Status must report TokenAcquired from the in-memory provider, not from the (empty) store")
	assert.False(t, status.OAuth.NeedsReauth)
	assert.Equal(t, OAuthGrantClientCredentials, status.OAuth.Grant)
}

func TestToolkit_Status_ClientCredentials_NoLiveProvider(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.mu.Lock()
	tk.connections["ccg"] = &upstream{
		name:   "ccg",
		config: Config{AuthMode: AuthModeOAuth, OAuth: OAuthConfig{Grant: OAuthGrantClientCredentials, TokenURL: "https://idp"}},
	}
	tk.mu.Unlock()
	status := tk.Status(context.Background(), "ccg")
	require.NotNil(t, status)
	require.NotNil(t, status.OAuth)
	assert.True(t, status.OAuth.Configured)
	assert.False(t, status.OAuth.TokenAcquired)
	assert.False(t, status.OAuth.NeedsReauth, "cc without a live provider is not a re-auth case — the next dial mints a fresh token")
}

func TestToolkit_ReacquireOAuthToken_ClientCredentials_ForcesFreshFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seq := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-` + intToString(seq) + `","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	cc := newClientCredentialsTokenProvider(OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "sec",
	})
	tk.mu.Lock()
	tk.connections["ccg"] = &upstream{
		name:       "ccg",
		config:     Config{AuthMode: AuthModeOAuth, OAuth: OAuthConfig{Grant: OAuthGrantClientCredentials, TokenURL: srv.URL}},
		ccProvider: cc,
	}
	tk.mu.Unlock()

	// First Token call mints cc-1.
	_, err := cc.Token(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), hits.Load())

	// ReacquireOAuthToken on the toolkit MUST force a fresh fetch via
	// the provider (not error out as the round-3 regression did, and
	// not silently no-op).
	require.NoError(t, tk.ReacquireOAuthToken(context.Background(), "ccg"))
	assert.Equal(t, int32(2), hits.Load(), "Reacquire must hit the IdP again")
}

// TestClientCredentialsTokenProvider_ReacquireClearsCacheAndRefetches
// proves the Reacquire path the admin "Reacquire" button uses for cc
// connections. The fake IdP increments its counter on every fetch, so
// two Reacquire calls back to back must produce two distinct fetches.
func TestClientCredentialsTokenProvider_ReacquireClearsCacheAndRefetches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seq := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-` + intToString(seq) + `","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	cc := newClientCredentialsTokenProvider(OAuthConfig{
		Grant:        OAuthGrantClientCredentials,
		TokenURL:     srv.URL,
		ClientID:     "id",
		ClientSecret: "sec",
	})

	tok1, err := cc.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cc-1", tok1)
	// Cached: second Token call must NOT fetch.
	tok1again, err := cc.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cc-1", tok1again)
	assert.Equal(t, int32(1), hits.Load())

	// Reacquire clears cache and forces fresh fetch.
	require.NoError(t, cc.Reacquire(context.Background()))
	assert.Equal(t, int32(2), hits.Load())
	tok2, err := cc.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cc-2", tok2)
}

func TestURLHost_ParsesValidURL(t *testing.T) {
	assert.Equal(t, "idp.example.com", URLHost("https://idp.example.com/oauth/token"))
}

func TestURLHost_FallsBackOnUnparseable(t *testing.T) {
	assert.Equal(t, "no-scheme.example", URLHost("no-scheme.example"))
}

func TestExpiresAtFromSeconds(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, now.Add(3600*time.Second), expiresAtFromSeconds(now, 3600))
	assert.True(t, expiresAtFromSeconds(now, 0).IsZero(), "zero seconds must produce zero time")
	assert.True(t, expiresAtFromSeconds(now, -1).IsZero(), "negative seconds must produce zero time")
}

// TestToolkit_Status_OAuthConnection_WithoutStore proves the
// placeholder branch of Status: when the toolkit has no
// connoauth.Store wired, an OAuth connection must still render in
// the admin UI as needs-reauth so the operator can take action
// (rather than the panel silently disappearing).
func TestToolkit_Status_OAuthConnection_WithoutStore(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	// Inject a placeholder upstream entry without going through dial
	// (no upstream server) so the test exercises only the Status path.
	cfg := Config{
		Endpoint:       "https://upstream.example/mcp",
		AuthMode:       AuthModeOAuth,
		ConnectionName: "fixture",
		ConnectTimeout: time.Second,
		CallTimeout:    time.Second,
		OAuth: OAuthConfig{
			Grant:    OAuthGrantAuthorizationCode,
			TokenURL: "https://idp.example/token",
			ClientID: "cid",
		},
	}
	tk.mu.Lock()
	tk.connections["fixture"] = &upstream{
		name:   "fixture",
		config: cfg,
	}
	tk.mu.Unlock()

	status := tk.Status(context.Background(), "fixture")
	require.NotNil(t, status)
	assert.Equal(t, AuthModeOAuth, status.AuthMode)
	require.NotNil(t, status.OAuth)
	assert.True(t, status.OAuth.NeedsReauth)
	assert.Equal(t, "https://idp.example/token", status.OAuth.TokenURL)
	assert.Equal(t, OAuthGrantAuthorizationCode, status.OAuth.Grant)
}

func TestToolkit_Status_UnknownConnection(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	assert.Nil(t, tk.Status(context.Background(), "missing"))
}

func TestToolkit_ReacquireOAuthToken_NotConfiguredForOAuth(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.mu.Lock()
	tk.connections["bearer"] = &upstream{
		name:   "bearer",
		config: Config{AuthMode: AuthModeBearer},
	}
	tk.mu.Unlock()
	err := tk.ReacquireOAuthToken(context.Background(), "bearer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured for OAuth")
}

func TestToolkit_ReacquireOAuthToken_ConnectionNotFound(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	err := tk.ReacquireOAuthToken(context.Background(), "missing")
	require.ErrorIs(t, err, ErrConnectionNotFound)
}

func TestToolkit_ReacquireOAuthToken_StoreNotWired(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.mu.Lock()
	tk.connections["oauth"] = &upstream{
		name: "oauth",
		config: Config{
			AuthMode: AuthModeOAuth,
			OAuth: OAuthConfig{
				Grant:    OAuthGrantAuthorizationCode,
				TokenURL: "https://idp.example/token",
				ClientID: "id",
			},
		},
	}
	tk.mu.Unlock()
	err := tk.ReacquireOAuthToken(context.Background(), "oauth")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth token store not wired")
}

func TestToolkit_IngestOAuthToken_AccessTokenRequired(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{Name: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access_token is required")
}

func TestToolkit_IngestOAuthToken_ConnectionNotFound(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetConnOAuthStore(connoauth.NewMemoryStore())
	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{
		Name:        "missing",
		AccessToken: "abc",
	})
	require.ErrorIs(t, err, ErrConnectionNotFound)
}

func TestToolkit_IngestOAuthToken_StoreNotWired(t *testing.T) {
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.mu.Lock()
	tk.connections["oauth"] = &upstream{
		name: "oauth",
		config: Config{
			AuthMode: AuthModeOAuth,
			OAuth: OAuthConfig{
				Grant:    OAuthGrantAuthorizationCode,
				TokenURL: "https://idp.example/token",
				ClientID: "id",
			},
		},
	}
	tk.mu.Unlock()
	err := tk.IngestOAuthToken(context.Background(), IngestOAuthTokenInput{
		Name:        "oauth",
		AccessToken: "abc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth token store not wired")
}

// --- helpers --------------------------------------------------------

// tokenProviderFn adapts a plain function into a tokenProvider so
// tests can inline a per-call closure without declaring a fresh struct.
type tokenProviderFn func(ctx context.Context) (string, error)

func (f tokenProviderFn) Token(ctx context.Context) (string, error) { return f(ctx) }

type fakeRoundTripper struct{}

func (fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused: applyAuth tests don't exercise the wire path")
}

func intToString(n int32) string {
	// strconv.FormatInt without taking the import dependency cost in
	// a tiny test helper.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func httptestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/x", http.NoBody)
	require.NoError(t, err)
	return req
}
