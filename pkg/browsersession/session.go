// Package browsersession provides browser-based OIDC authentication
// and cookie-managed sessions for the portal UI. It implements:
//   - HMAC-SHA256 signed JWT session cookies (stateless)
//   - OIDC authorization code flow with PKCE
//   - Cookie-based authenticator for the HTTP auth chain
package browsersession

import (
	"net/http"
	"time"
)

// Default configuration values.
const (
	DefaultCookieName = "mcp_session"
	DefaultCookiePath = "/"
	DefaultTTL        = 8 * time.Hour
)

// SessionClaims holds the claims stored in the session JWT cookie.
type SessionClaims struct {
	UserID string   `json:"sub"`
	Email  string   `json:"email,omitempty"`
	Roles  []string `json:"roles"`
}

// CookieConfig controls session cookie behavior.
type CookieConfig struct {
	// Name is the cookie name (default: "mcp_session").
	Name string

	// Domain restricts the cookie to a specific domain.
	Domain string

	// Path restricts the cookie to a specific path (default: "/").
	Path string

	// Secure marks the cookie as HTTPS-only (default: true).
	Secure bool

	// SameSite controls cross-site cookie behavior (default: Lax).
	SameSite http.SameSite

	// TTL is the session lifetime (default: 8h).
	TTL time.Duration

	// Key is the HMAC-SHA256 signing key. Must be at least 32 bytes.
	Key []byte
}

// effectiveName returns the cookie name, applying the default if empty.
func (c *CookieConfig) effectiveName() string {
	if c.Name == "" {
		return DefaultCookieName
	}
	return c.Name
}

// effectivePath returns the cookie path, applying the default if empty.
func (c *CookieConfig) effectivePath() string {
	if c.Path == "" {
		return DefaultCookiePath
	}
	return c.Path
}

// effectiveTTL returns the session TTL, applying the default if zero.
func (c *CookieConfig) effectiveTTL() time.Duration {
	if c.TTL == 0 {
		return DefaultTTL
	}
	return c.TTL
}

// effectiveSameSite returns the SameSite mode, defaulting to Lax.
func (c *CookieConfig) effectiveSameSite() http.SameSite {
	if c.SameSite == 0 {
		return http.SameSiteLaxMode
	}
	return c.SameSite
}
