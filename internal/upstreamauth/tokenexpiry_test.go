package upstreamauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// staticSource is a token source that answers with what the test set,
// recording how often it was asked.
type staticSource struct {
	tok   *oauth2.Token
	err   error
	calls int
}

func (s *staticSource) Token() (*oauth2.Token, error) {
	s.calls++
	return s.tok, s.err
}

// TestBoundedExpiry_StampsATokenWithNoExpiry is the core of #1737: a token
// endpoint that omits expires_in leaves Expiry zero, which oauth2 treats as
// valid forever. The wrapper gives it the bounded window instead.
func TestBoundedExpiry_StampsATokenWithNoExpiry(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	src := &staticSource{tok: &oauth2.Token{AccessToken: "at-forever", TokenType: "Bearer"}}

	tok, err := withBoundedExpiry(src, func() time.Time { return now }).Token()

	require.NoError(t, err)
	assert.Equal(t, now.Add(DefaultUpstreamAccessTokenLifetime), tok.Expiry)
	assert.Equal(t, "at-forever", tok.AccessToken)
	assert.True(t, src.tok.Expiry.IsZero(), "the source's own token must not be mutated")
}

// TestBoundedExpiry_LeavesAnUpstreamExpiryAlone covers the other half of the
// rule: the upstream's own lifetime always wins, including one far shorter
// and one far longer than the fallback window.
func TestBoundedExpiry_LeavesAnUpstreamExpiryAlone(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	for name, expiry := range map[string]time.Time{
		"shorter than the window": now.Add(30 * time.Second),
		"longer than the window":  now.Add(24 * time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			src := &staticSource{tok: &oauth2.Token{AccessToken: "at-1", Expiry: expiry}}
			tok, err := withBoundedExpiry(src, func() time.Time { return now }).Token()
			require.NoError(t, err)
			assert.Equal(t, expiry, tok.Expiry)
		})
	}
}

// TestBoundedExpiry_PassesAFetchFailureThrough keeps the wrapper out of the
// error path: Apply's scrubbing classifies the library's error, so the
// wrapper must not replace or wrap it.
func TestBoundedExpiry_PassesAFetchFailureThrough(t *testing.T) {
	sentinel := errors.New("token endpoint refused")
	src := &staticSource{err: sentinel}

	tok, err := withBoundedExpiry(src, time.Now).Token()

	assert.Nil(t, tok)
	assert.ErrorIs(t, err, sentinel)
}

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
	elapsed := func() time.Time { return time.Now().Add(-DefaultUpstreamAccessTokenLifetime) }
	auth := newOAuth2ClientCredentialsAuth(issue1737Config(idp.URL), elapsed)

	first := issue1737Apply(t, auth)
	second := issue1737Apply(t, auth)

	assert.Equal(t, "Bearer cc-token-1", first)
	assert.Equal(t, "Bearer cc-token-2", second, "the call after the window must carry a new token")
	assert.Equal(t, int32(2), fetches.Load())
}
