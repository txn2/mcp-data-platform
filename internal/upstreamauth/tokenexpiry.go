package upstreamauth

import (
	"time"

	"golang.org/x/oauth2"
)

// DefaultUpstreamAccessTokenLifetime is how long an access token from a token
// endpoint is reused when the response carries no expires_in. RFC 6749
// section 5.1 makes expires_in RECOMMENDED rather than required, and
// golang.org/x/oauth2 treats a token with a zero Expiry as valid forever
// (oauth2.Token.expired returns false when Expiry.IsZero), so an upstream that
// omits it would otherwise have its first access token presented for the life
// of the process: once the session behind that token ended, every call through
// the connection would fail with the upstream's 401 until the connection was
// saved again or the replica restarted.
//
// Obtaining a token is unattended and costs one request, so a window this
// short bounds how long an ended session can go unnoticed for little cost: a
// connection nobody calls fetches nothing, and a busy one fetches four times
// an hour per replica.
const DefaultUpstreamAccessTokenLifetime = 15 * time.Minute

// boundedExpiry is the one place a token with no expiry is given one. Both
// grants that obtain an access token here -- client_credentials and
// jwt_bearer -- wrap their token source in it, inside the
// oauth2.ReuseTokenSource that caches the result, so the cache fetches again
// at the window instead of holding the first token forever. The
// authorization_code grant needs none of this: it holds its token in the
// database through pkg/connoauth, whose accessTokenStillValid treats a zero
// expiry as a token to refresh rather than one to keep.
type boundedExpiry struct {
	src oauth2.TokenSource
	// now is time.Now in production and a stub in tests.
	now func() time.Time
}

// withBoundedExpiry wraps src so a token it returns with no expiry is reused
// for DefaultUpstreamAccessTokenLifetime rather than forever. A token that
// carries an expiry is passed through untouched: the upstream's own lifetime
// always wins, including one shorter than the window.
func withBoundedExpiry(src oauth2.TokenSource, now func() time.Time) oauth2.TokenSource {
	return boundedExpiry{src: src, now: now}
}

// Token returns the wrapped source's token, stamping an expiry onto a copy
// when it has none. The token is copied rather than mutated in place because
// a source is free to hand out a token it also retains.
func (b boundedExpiry) Token() (*oauth2.Token, error) {
	tok, err := b.src.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller classifies and scrubs the library's error
	}
	if tok == nil || !tok.Expiry.IsZero() {
		return tok, nil
	}
	stamped := *tok
	stamped.Expiry = b.now().Add(DefaultUpstreamAccessTokenLifetime)
	return &stamped, nil
}

// tokenFunc adapts a bare fetch into an oauth2.TokenSource, for a grant whose
// fetch is a function rather than a type. It caches nothing: the one cache is
// the oauth2.ReuseTokenSource wrapping the bounded-expiry source above it.
type tokenFunc func() (*oauth2.Token, error)

// Token calls the function.
func (f tokenFunc) Token() (*oauth2.Token, error) { return f() }
