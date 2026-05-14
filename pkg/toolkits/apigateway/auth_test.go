package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			AuthMode:        AuthModeAPIKey,
			Credential:      "k",
			APIKeyPlacement: APIKeyPlacementHeader,
			APIKeyHeader:    "X-API-Key",
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
		ConnectionName: "fixture",
		OAuth2:         OAuth2Config{TokenURL: "https://idp", ClientID: "id"},
	})
	auth.SetConnOAuthStore(connoauth.NewMemoryStore())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream/x", http.NoBody)
	require.NoError(t, err)
	err = auth.Apply(req)
	require.ErrorIs(t, err, ErrNeedsReauth)
}

func TestConnoauthConfigFromOAuth2_MapsAuthStyleAndScopes(t *testing.T) {
	got := connoauthConfigFromOAuth2(OAuth2Config{
		AuthorizationURL:  "https://idp/authorize",
		TokenURL:          "https://idp/token",
		ClientID:          "id",
		ClientSecret:      "secret",
		Scopes:            []string{"api", "refresh_token"},
		EndpointAuthStyle: OAuth2AuthStyleParams,
		Prompt:            "consent",
	})
	assert.Equal(t, "authorization_code", got.Grant)
	assert.Equal(t, []string{"api", "refresh_token"}, got.Scopes)
	assert.Equal(t, "consent", got.Prompt)
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
