package graphql

import (
	"io"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
)

// Authenticator applies a connection's authentication scheme to an
// outbound HTTP request. The implementations and every auth mode live
// in internal/upstreamauth, shared with the platform's other
// HTTP-based connection kinds; this alias keeps the toolkit's own
// vocabulary intact at its call sites.
type Authenticator = upstreamauth.Authenticator

// ErrNeedsReauth is the structured error a tool surfaces when an
// authorization_code connection's stored refresh token is missing,
// expired, or definitively rejected by the IdP. Transient failures
// (network, 5xx, cancellation) do not produce it.
var ErrNeedsReauth = upstreamauth.ErrNeedsReauth

// NewAuthenticator returns the Authenticator implementation for a
// validated Config.
func NewAuthenticator(c Config) (Authenticator, error) {
	//nolint:wrapcheck // the message already carries this toolkit's prefix; wrapping would state it twice
	return upstreamauth.NewAuthenticator(c.upstream())
}

// upstream projects this kind's Config onto the authentication and
// transport slice internal/upstreamauth owns. The toolkit keeps its own
// exported Config — its keys, its defaults, its public API — and this
// is the single place the two are related, so a field added to either
// side has exactly one place to be joined up.
func (c Config) upstream() upstreamauth.Config {
	return upstreamauth.Config{
		Kind:                Kind,
		ErrPrefix:           upstreamErrPrefix,
		ConnectionName:      c.ConnectionName,
		AuthMode:            c.AuthMode,
		Credential:          c.Credential,
		CredentialPlacement: c.CredentialPlacement,
		APIKeyHeader:        c.APIKeyHeader,
		APIKeyParam:         c.APIKeyParam,
		Username:            c.Username,
		Password:            c.Password,
		OAuth2: upstreamauth.OAuth2Config{
			Grant:             c.OAuth2.Grant,
			TokenURL:          c.OAuth2.TokenURL,
			ClientID:          c.OAuth2.ClientID,
			ClientSecret:      c.OAuth2.ClientSecret,
			Scopes:            c.OAuth2.Scopes,
			EndpointAuthStyle: c.OAuth2.EndpointAuthStyle,
			AuthorizationURL:  c.OAuth2.AuthorizationURL,
			Prompt:            c.OAuth2.Prompt,
		},
		ConnectTimeout:      c.ConnectTimeout,
		CallTimeout:         c.CallTimeout,
		MaxResponseBytes:    c.MaxResponseBytes,
		StaticHeaders:       c.StaticHeaders,
		MTLSClientCertPEM:   c.MTLSClientCertPEM,
		MTLSClientKeyPEM:    c.MTLSClientKeyPEM,
		TLSCABundlePEM:      c.TLSCABundlePEM,
		IdentityPassthrough: c.IdentityPassthrough,
	}
}

// configFromUpstream is the inverse of Config.upstream: it seeds a
// Config with the authentication and transport values Parse read, so
// ParseConfig only has to fill in the keys this kind owns. Kept beside
// upstream() because the two must be changed together.
func configFromUpstream(up upstreamauth.Config) Config {
	return Config{
		AuthMode:            up.AuthMode,
		Credential:          up.Credential,
		CredentialPlacement: up.CredentialPlacement,
		APIKeyHeader:        up.APIKeyHeader,
		APIKeyParam:         up.APIKeyParam,
		Username:            up.Username,
		Password:            up.Password,
		OAuth2: OAuth2Config{
			Grant:             up.OAuth2.Grant,
			TokenURL:          up.OAuth2.TokenURL,
			ClientID:          up.OAuth2.ClientID,
			ClientSecret:      up.OAuth2.ClientSecret,
			Scopes:            up.OAuth2.Scopes,
			EndpointAuthStyle: up.OAuth2.EndpointAuthStyle,
			AuthorizationURL:  up.OAuth2.AuthorizationURL,
			Prompt:            up.OAuth2.Prompt,
		},
		ConnectTimeout:      up.ConnectTimeout,
		CallTimeout:         up.CallTimeout,
		MaxResponseBytes:    up.MaxResponseBytes,
		StaticHeaders:       up.StaticHeaders,
		MTLSClientCertPEM:   up.MTLSClientCertPEM,
		MTLSClientKeyPEM:    up.MTLSClientKeyPEM,
		TLSCABundlePEM:      up.TLSCABundlePEM,
		IdentityPassthrough: up.IdentityPassthrough,
	}
}

// newHTTPClient builds the per-connection HTTP client from the shared
// transport policy: the call timeout, the connection's TLS material,
// and a CheckRedirect that refuses 3xx.
func newHTTPClient(cfg Config) *http.Client {
	return upstreamauth.NewHTTPClient(cfg.upstream())
}

// readLimit is the most of a response to read given a configured limit,
// falling back to the shared default when there is none.
func readLimit(limit int64) int64 { return upstreamauth.ReadLimit(limit) }

// readBody reads at most maxBytes of an upstream response, reporting
// whether it was cut short.
func readBody(r io.Reader, maxBytes int64) (body []byte, truncated bool, err error) {
	//nolint:wrapcheck // the message already carries this toolkit's prefix
	return upstreamauth.ReadBody(upstreamErrPrefix, r, maxBytes)
}

// reserveBodyBudget reserves the worst-case buffer for one response
// against the shared in-flight budget, returning the amount to release
// and whether the reservation was granted.
func reserveBodyBudget(b *membudget.Budget, contentLength, readCap int64) (reserved int64, ok bool) {
	return upstreamauth.ReserveBodyBudget(b, contentLength, readCap)
}
