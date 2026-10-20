package tokenexpiry

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
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

// TestBounded_StampsATokenWithNoExpiry is the core of #1737: a token endpoint
// that omits expires_in leaves Expiry zero, which oauth2 treats as valid
// forever. The wrapper gives it the bounded window instead.
func TestBounded_StampsATokenWithNoExpiry(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	src := &staticSource{tok: &oauth2.Token{AccessToken: "at-forever", TokenType: "Bearer"}}

	tok, err := Bounded(src, func() time.Time { return now }, 0).Token()

	require.NoError(t, err)
	assert.Equal(t, now.Add(DefaultLifetime), tok.Expiry)
	assert.Equal(t, "at-forever", tok.AccessToken)
	assert.True(t, src.tok.Expiry.IsZero(), "the source's own token must not be mutated")
}

// TestBounded_UsesTheConnectionsLifetime: a connection that sets
// oauth_token_lifetime holds a token with no expires_in for that long (#2218).
func TestBounded_UsesTheConnectionsLifetime(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	src := &staticSource{tok: &oauth2.Token{AccessToken: "at-short"}}

	tok, err := Bounded(src, func() time.Time { return now }, 20*time.Second).Token()

	require.NoError(t, err)
	assert.Equal(t, now.Add(20*time.Second), tok.Expiry)
}

// TestBounded_LeavesAnUpstreamExpiryAlone covers the other half of the rule:
// the upstream's own lifetime always wins, including one far shorter and one
// far longer than the fallback window.
func TestBounded_LeavesAnUpstreamExpiryAlone(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	for name, expiry := range map[string]time.Time{
		"shorter than the window": now.Add(30 * time.Second),
		"longer than the window":  now.Add(24 * time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			src := &staticSource{tok: &oauth2.Token{AccessToken: "at-1", Expiry: expiry}}
			tok, err := Bounded(src, func() time.Time { return now }, 0).Token()
			require.NoError(t, err)
			assert.Equal(t, expiry, tok.Expiry)
		})
	}
}

// TestBounded_PassesAFetchFailureThrough keeps the wrapper out of the error
// path: the caller's scrubbing classifies the library's error, so the wrapper
// must not replace or wrap it.
func TestBounded_PassesAFetchFailureThrough(t *testing.T) {
	sentinel := errors.New("token endpoint refused")
	src := &staticSource{err: sentinel}

	tok, err := Bounded(src, time.Now, 0).Token()

	assert.Nil(t, tok)
	assert.ErrorIs(t, err, sentinel)
}

// TestFunc_CallsTheFetch: Func is the fetch itself, with nothing cached.
func TestFunc_CallsTheFetch(t *testing.T) {
	calls := 0
	f := Func(func() (*oauth2.Token, error) {
		calls++
		return &oauth2.Token{AccessToken: "at"}, nil
	})
	for range 2 {
		tok, err := f.Token()
		require.NoError(t, err)
		assert.Equal(t, "at", tok.AccessToken)
	}
	assert.Equal(t, 2, calls)
}
