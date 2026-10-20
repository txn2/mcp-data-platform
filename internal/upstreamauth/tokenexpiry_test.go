package upstreamauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth/tokenexpiry"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// issue1737IDP is a client_credentials token endpoint that answers without
// expires_in, handing out a new access token on every request.
func issue1737IDP(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var fetches atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fmt.Appendf(nil, `{"access_token":"cc-token-%d","token_type":"Bearer"}`, n))
	}))
	t.Cleanup(idp.Close)
	return idp, &fetches
}

// issue1737Config is a client_credentials connection against tokenURL.
func issue1737Config(tokenURL string) Config {
	return Config{
		AuthMode: AuthModeOAuth,
		OAuth2: OAuth2Config{
			Grant:             connoauth.GrantClientCredentials,
			TokenURL:          tokenURL,
			ClientID:          "id",
			ClientSecret:      "secret",
			EndpointAuthStyle: OAuth2AuthStyleHeader,
		},
	}
}

// issue1737Apply runs one outbound request through the authenticator and
// returns the Authorization header it carried.
func issue1737Apply(t *testing.T, auth Authenticator) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/x", http.NoBody)
	require.NoError(t, err)
	require.NoError(t, auth.Apply(req))
	return req.Header.Get("Authorization")
}

// TestOAuth2ClientCredentials_ATokenWithNoExpiresInIsStillCached is the
// no-stampede half: bounding the window must not turn every outbound call
// into a token request.
func TestOAuth2ClientCredentials_ATokenWithNoExpiresInIsStillCached(t *testing.T) {
	idp, fetches := issue1737IDP(t)
	auth := newOAuth2ClientCredentialsAuth(issue1737Config(idp.URL), time.Now)

	for range 3 {
		assert.Equal(t, "Bearer cc-token-1", issue1737Apply(t, auth))
	}
	assert.Equal(t, int32(1), fetches.Load(), "a token inside its window must be reused")
}

// TestOAuth2ClientCredentials_ATokenWithNoExpiresInIsRefetched is #1737: once
// the fallback window has passed, the connection presents a new access token
// instead of the first one for the life of the process. The clock is stubbed
// one window in the past so the stamped expiry is already behind real time,
// which is what the cache compares against.
func TestOAuth2ClientCredentials_ATokenWithNoExpiresInIsRefetched(t *testing.T) {
	idp, fetches := issue1737IDP(t)
	elapsed := func() time.Time { return time.Now().Add(-tokenexpiry.DefaultLifetime) }
	auth := newOAuth2ClientCredentialsAuth(issue1737Config(idp.URL), elapsed)

	first := issue1737Apply(t, auth)
	second := issue1737Apply(t, auth)

	assert.Equal(t, "Bearer cc-token-1", first)
	assert.Equal(t, "Bearer cc-token-2", second, "the call after the window must carry a new token")
	assert.Equal(t, int32(2), fetches.Load())
}

// TestParse_OAuthTokenLifetime reads oauth_token_lifetime in both forms it
// admits, defaults it, and refuses a negative one at validation.
func TestParse_OAuthTokenLifetime(t *testing.T) {
	base := func(v any) map[string]any {
		cfg := map[string]any{
			"auth_mode": "oauth", "oauth_grant": "client_credentials",
			"oauth_token_url": "https://idp.example/token", "oauth_client_id": "id", "oauth_client_secret": "secret",
		}
		if v != nil {
			cfg["oauth_token_lifetime"] = v
		}
		return cfg
	}
	for name, tc := range map[string]struct {
		value any
		want  time.Duration
	}{
		"unset":           {nil, tokenexpiry.DefaultLifetime},
		"duration string": {"30s", 30 * time.Second},
		"seconds":         {45, 45 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", base(tc.value))
			require.NoError(t, err)
			assert.Equal(t, tc.want, c.OAuth2.UnstatedTokenLifetime)
			assert.NoError(t, c.ValidateAuth())
		})
	}

	c, err := Parse(connoauth.KindAPI, "apigateway", "https://upstream.example", base("-1m"))
	require.NoError(t, err)
	assert.EqualError(t, c.ValidateAuth(), "apigateway: oauth_token_lifetime must not be negative")
}
