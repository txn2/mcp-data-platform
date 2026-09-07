package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// TestNewAuthenticator_DispatchesByAuthMode locks the small dispatch
// table in NewAuthenticator. Each branch returns the concrete type
// the caller expects so that downstream type assertions (e.g.
// SetConnOAuthStore on the authorization_code variant) compile.
func TestNewAuthenticator_DispatchesByAuthMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "none", cfg: Config{AuthMode: AuthModeNone}},
		{name: "bearer", cfg: Config{AuthMode: AuthModeBearer, Credential: "tok"}},
		{name: "api_key_header", cfg: Config{
			AuthMode:            AuthModeAPIKey,
			Credential:          "k",
			CredentialPlacement: CredentialPlacementHeader,
			APIKeyHeader:        "X-API-Key",
		}},
		{name: "basic", cfg: Config{
			AuthMode: AuthModeBasic,
			Username: "alice",
			Password: "s3cret",
		}},
		{name: "oauth2_client_credentials", cfg: Config{
			AuthMode: AuthModeOAuth2ClientCredentials,
			OAuth2: OAuth2Config{
				TokenURL: "https://idp.example/token",
				ClientID: "id",
			},
		}},
		{name: "oauth2_authorization_code", cfg: Config{
			AuthMode: AuthModeOAuth2AuthorizationCode,
			OAuth2: OAuth2Config{
				TokenURL:         "https://idp.example/token",
				AuthorizationURL: "https://idp.example/authorize",
				ClientID:         "id",
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := NewAuthenticator(tc.cfg)
			require.NoError(t, err)
			require.NotNil(t, auth)
		})
	}
}

func TestNewAuthenticator_RejectsUnknownAuthMode(t *testing.T) {
	_, err := NewAuthenticator(Config{AuthMode: "future-mode"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authenticator")
}

// TestOAuth2AuthCode_ApplyRefreshesStaleAccessToken proves the new
// authorization_code authenticator round-trips through connoauth.Source
// on every Apply: when the persisted access token is expired, Apply
// refreshes against the IdP, persists the rotation, and attaches the
// fresh access token to the request.
//
// This is the structural test for the bug that motivated this refactor
// — the authenticator must NOT cache the refresh token in memory.
// Verified here by rotating the persisted refresh token between two
// Apply calls and asserting the second call exchanges the rotated
// value (the fake IdP increments its counter, and the persisted row
// rolls forward).
func TestOAuth2AuthCode_ApplyRefreshesStaleAccessToken(t *testing.T) {
	var refreshCount atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seq := refreshCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"access-` + intToString(seq) + `",
			"refresh_token":"refresh-` + intToString(seq) + `",
			"token_type":"Bearer",
			"expires_in":3600
		}`))
	}))
	defer idp.Close()

	store := connoauth.NewMemoryStore()
	key := connoauth.Key{Kind: connoauth.KindAPI, Name: "fixture"}
	require.NoError(t, store.Set(context.Background(), connoauth.PersistedToken{
		Key:          key,
		AccessToken:  "stale-access",
		RefreshToken: "stale-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}))

	auth := newOAuth2AuthorizationCodeAuth(Config{
		Kind:           connoauth.KindAPI,
		ConnectionName: "fixture",
		OAuth2: OAuth2Config{
			TokenURL:     idp.URL,
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
	})
	auth.SetConnOAuthStore(store)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	assert.Equal(t, "Bearer access-1", req.Header.Get("Authorization"))
	assert.Equal(t, int32(1), refreshCount.Load())

	// Simulate an external rotation (background refresher).
	persisted, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	persisted.RefreshToken = "refresh-99"
	persisted.ExpiresAt = time.Now().Add(-time.Hour)
	require.NoError(t, store.Set(context.Background(), *persisted))

	req2, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/y", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req2))
	assert.Equal(t, "Bearer access-2", req2.Header.Get("Authorization"))

	after, err := store.Get(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, "refresh-2", after.RefreshToken,
		"second Apply must have refreshed using the externally-rotated refresh-99 and persisted the IdP's rotation back")
}

func TestOAuth2AuthCode_ApplyWithoutStoreErrors(t *testing.T) {
	auth := newOAuth2AuthorizationCodeAuth(Config{
		Kind:           connoauth.KindAPI,
		ConnectionName: "fixture",
		OAuth2:         OAuth2Config{TokenURL: "https://idp", ClientID: "id"},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream/x", http.NoBody)
	require.NoError(t, err)
	err = auth.Apply(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token store not wired")
}

func TestOAuth2AuthCode_ApplyWithoutPersistedTokenReturnsNeedsReauth(t *testing.T) {
	auth := newOAuth2AuthorizationCodeAuth(Config{
		Kind:           connoauth.KindAPI,
		ConnectionName: "fixture",
		OAuth2:         OAuth2Config{TokenURL: "https://idp", ClientID: "id"},
	})
	auth.SetConnOAuthStore(connoauth.NewMemoryStore())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream/x", http.NoBody)
	require.NoError(t, err)
	err = auth.Apply(req)
	require.ErrorIs(t, err, ErrNeedsReauth)
}

// TestBasicAuth_ApplyEncodesRFC7617 pins the exact Authorization
// header value the basic authenticator produces. The base64 of
// "alice:s3cret" is "YWxpY2U6czNjcmV0", locked literally so a future
// refactor that swaps the encoding library or accidentally URL-encodes
// the userinfo will fail loudly here rather than at an integration
// boundary.
func TestBasicAuth_ApplyEncodesRFC7617(t *testing.T) {
	auth, err := NewAuthenticator(Config{
		AuthMode: AuthModeBasic,
		Username: "alice",
		Password: "s3cret",
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream/x", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	assert.Equal(t, "Basic YWxpY2U6czNjcmV0", req.Header.Get("Authorization"))
}

// TestBasicAuth_EmptyPasswordAllowed covers the legacy "token-in-userid"
// pattern. Some APIs accept `Authorization: Basic base64(<token>:)`
// where the password is intentionally empty; validation must permit it.
// base64("token:") = "dG9rZW46".
func TestBasicAuth_EmptyPasswordAllowed(t *testing.T) {
	auth, err := NewAuthenticator(Config{
		AuthMode: AuthModeBasic,
		Username: "token",
		Password: "",
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream/x", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	assert.Equal(t, "Basic dG9rZW46", req.Header.Get("Authorization"))
}

// TestNewBasicAuth_DefenseInDepth confirms the authenticator's own
// guards reject malformed credentials even when called through paths
// that bypass Config.Validate (e.g. a direct construction in a test).
func TestNewBasicAuth_DefenseInDepth(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		wantMsg  string
	}{
		{name: "empty username", username: "", password: "p", wantMsg: "requires a username"},
		{name: "colon in username", username: "a:b", password: "p", wantMsg: "must not contain"},
		{name: "CRLF in username", username: "a\r\nb", password: "p", wantMsg: "CR/LF/NUL"},
		{name: "NUL in password", username: "a", password: "p\x00q", wantMsg: "CR/LF/NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newBasicAuth(Config{
				AuthMode: AuthModeBasic,
				Username: tc.username,
				Password: tc.password,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestConnOAuthConfig_MapsAuthStyleAndScopes(t *testing.T) {
	got := Config{
		OAuth2: OAuth2Config{
			Grant:             "authorization_code",
			AuthorizationURL:  "https://idp/authorize",
			TokenURL:          "https://idp/token",
			ClientID:          "id",
			ClientSecret:      "secret",
			Scopes:            []string{"api", "refresh_token"},
			EndpointAuthStyle: OAuth2AuthStyleParams,
			Prompt:            "consent",
		},
		TLSCABundlePEM: "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n",
	}.ConnOAuthConfig()
	assert.Equal(t, "authorization_code", got.Grant)
	assert.Equal(t, []string{"api", "refresh_token"}, got.Scopes)
	assert.Equal(t, "consent", got.Prompt)
	// CABundlePEM must propagate so refresh paths against private-CA
	// IdPs work; without this assertion a regression that drops the
	// field from the translator would not be caught.
	assert.Equal(t, "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n", got.CABundlePEM)
}

// --- helpers --------------------------------------------------------

func intToString(n int32) string {
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

// TestOAuth2ClientCredentials_ApplyAttachesTheFetchedToken covers the
// server-to-server grant end to end against a fake IdP: the token is
// fetched once, attached as a bearer, and reused from the library's
// cache on the next call rather than re-fetched.
func TestOAuth2ClientCredentials_ApplyAttachesTheFetchedToken(t *testing.T) {
	var fetches atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cc-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer idp.Close()

	auth, err := NewAuthenticator(Config{
		AuthMode: AuthModeOAuth,
		OAuth2: OAuth2Config{
			Grant:             connoauth.GrantClientCredentials,
			TokenURL:          idp.URL,
			ClientID:          "id",
			ClientSecret:      "secret",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})
	require.NoError(t, err)

	for range 2 {
		req, rerr := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
		require.NoError(t, rerr)
		require.NoError(t, auth.Apply(req))
		assert.Equal(t, "Bearer cc-token", req.Header.Get("Authorization"))
	}
	assert.Equal(t, int32(1), fetches.Load(), "the cached token must be reused inside its lifetime")
}

// TestOAuth2ClientCredentials_ApplySurfacesAScrubbedFetchFailure is the
// security contract on the token-fetch error path: an IdP rejection
// reaches the model as a status code, never as the IdP's response body,
// which can carry material from a partial grant exchange.
func TestOAuth2ClientCredentials_ApplySurfacesAScrubbedFetchFailure(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","leaked_refresh_token":"do-not-echo"}`))
	}))
	defer idp.Close()

	auth, err := NewAuthenticator(Config{
		ErrPrefix: "apigateway",
		AuthMode:  AuthModeOAuth,
		OAuth2: OAuth2Config{
			Grant:             connoauth.GrantClientCredentials,
			TokenURL:          idp.URL,
			ClientID:          "id",
			ClientSecret:      "secret",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	require.NoError(t, err)
	err = auth.Apply(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apigateway: oauth2 token fetch failed: status=401")
	assert.NotContains(t, err.Error(), "do-not-echo")
	assert.Empty(t, req.Header.Get("Authorization"))
}

// TestTokenFetchError_ScrubsEveryShape walks the error types the oauth2
// library can hand back. Each branch exists to keep a credential out of
// the message: the IdP's body, userinfo or a query string embedded in
// the token URL, and anything URL-shaped from a future library version.
func TestTokenFetchError_ScrubsEveryShape(t *testing.T) {
	cfg := Config{ErrPrefix: "apigateway"}

	t.Run("RetrieveError keeps only the status", func(t *testing.T) {
		err := tokenFetchError(cfg, &oauth2.RetrieveError{
			Response: &http.Response{StatusCode: http.StatusForbidden},
			Body:     []byte(`{"secret":"leaked"}`),
		})
		assert.Equal(t, "apigateway: oauth2 token fetch failed: status=403", err.Error())
	})

	t.Run("url.Error drops userinfo and query", func(t *testing.T) {
		err := tokenFetchError(cfg, &url.Error{
			Op:  "Post",
			URL: "https://user:s3cret@idp.example/token?client_secret=alsosecret",
			Err: errors.New("dial tcp: connection refused"),
		})
		msg := err.Error()
		assert.Contains(t, msg, "https://idp.example/token")
		assert.NotContains(t, msg, "s3cret")
		assert.NotContains(t, msg, "alsosecret")
	})

	t.Run("unparseable url.Error keeps only the op", func(t *testing.T) {
		err := tokenFetchError(cfg, &url.Error{
			Op:  "Post",
			URL: "://not a url",
			Err: errors.New("boom"),
		})
		msg := err.Error()
		assert.Contains(t, msg, "oauth2 token fetch Post")
		assert.NotContains(t, msg, "not a url")
	})

	t.Run("unknown URL-shaped error is redacted wholesale", func(t *testing.T) {
		err := tokenFetchError(cfg, errors.New("boom talking to https://user:pw@idp.example/token"))
		assert.Equal(t, "apigateway: oauth2 token fetch failed (details redacted)", err.Error())
	})

	t.Run("unknown plain error passes through", func(t *testing.T) {
		err := tokenFetchError(cfg, errors.New("context deadline exceeded"))
		assert.Equal(t, "apigateway: oauth2 token fetch failed: context deadline exceeded", err.Error())
	})
}

// TestOAuth2ClientCredentials_ApplyRejectsAnEmptyAccessToken covers the
// last-resort guard on a token source that succeeds but yields nothing.
// The oauth2 library refuses an empty access_token itself, so this is
// reachable only by substituting the source — which is exactly the
// point: sending "Bearer " would produce an opaque 401 from the
// upstream instead of naming the real fault.
func TestOAuth2ClientCredentials_ApplyRejectsAnEmptyAccessToken(t *testing.T) {
	cases := []struct {
		name string
		src  oauth2.TokenSource
	}{
		{name: "empty access token", src: stubTokenSource{token: &oauth2.Token{}}},
		{name: "nil token", src: stubTokenSource{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := oauth2ClientCredentialsAuth{cfg: Config{ErrPrefix: "apigateway"}, src: tc.src}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
			require.NoError(t, err)
			err = auth.Apply(req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "apigateway: oauth2 token source returned no access token")
			assert.Empty(t, req.Header.Get("Authorization"))
		})
	}
}

// stubTokenSource hands back whatever the test configured, including
// nothing at all, so the authenticator's own guards are reachable
// without an IdP that violates the OAuth spec.
type stubTokenSource struct {
	token *oauth2.Token
}

func (s stubTokenSource) Token() (*oauth2.Token, error) { return s.token, nil }

// TestOAuth2ConfigFromConnoauth_MapsTheParamsAuthStyle covers the
// non-default endpoint auth style, which some IdPs require and which
// travels as an enum on one side and a string on the other.
func TestOAuth2ConfigFromConnoauth_MapsTheParamsAuthStyle(t *testing.T) {
	got := oauth2ConfigFromConnoauth(connoauth.Config{EndpointAuthStyle: oauth2.AuthStyleInParams})
	assert.Equal(t, OAuth2AuthStyleParams, got.EndpointAuthStyle)

	got = oauth2ConfigFromConnoauth(connoauth.Config{EndpointAuthStyle: oauth2.AuthStyleInHeader})
	assert.Equal(t, OAuth2AuthStyleHeader, got.EndpointAuthStyle)
}

// TestOAuth2AuthCode_ApplyKeepsATransientFailureOutOfReauth is the
// classification contract: only a definitively dead credential asks the
// operator to reconnect. A store that cannot be read is transient, and
// reporting it as ErrNeedsReauth would send an admin to redo a browser
// flow over what is probably a database blip.
func TestOAuth2AuthCode_ApplyKeepsATransientFailureOutOfReauth(t *testing.T) {
	auth := newOAuth2AuthorizationCodeAuth(Config{
		ErrPrefix:      "apigateway",
		Kind:           connoauth.KindAPI,
		ConnectionName: "fixture",
		OAuth2:         OAuth2Config{TokenURL: "https://idp", ClientID: "id"},
	})
	auth.SetConnOAuthStore(failingStore{err: errors.New("database unreachable")})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	require.NoError(t, err)
	err = auth.Apply(req)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNeedsReauth)
	assert.Contains(t, err.Error(), "apigateway: oauth token:")
	assert.Empty(t, req.Header.Get("Authorization"))
}

// failingStore fails every read so the authenticator's transient-error
// arm is reachable without a database.
type failingStore struct {
	err error
}

func (s failingStore) Get(context.Context, connoauth.Key) (*connoauth.PersistedToken, error) {
	return nil, s.err
}
func (s failingStore) Set(context.Context, connoauth.PersistedToken) error { return s.err }
func (s failingStore) Delete(context.Context, connoauth.Key) error         { return s.err }
func (s failingStore) List(context.Context) ([]connoauth.PersistedToken, error) {
	return nil, s.err
}

func (failingStore) Lock(context.Context, connoauth.Key) (func(), error) {
	return func() {}, nil
}
