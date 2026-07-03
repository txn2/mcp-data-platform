// Package browsersession provides browser-based OIDC authentication
// and cookie-managed sessions for the portal UI. It implements:
//   - HMAC-SHA256 signed JWT session cookies (stateless)
//   - OIDC authorization code flow with PKCE
//   - Cookie-based authenticator for the HTTP auth chain
package browsersession

import (
	"net/http"
	"strings"
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
	UserID  string   `json:"sub"`
	Email   string   `json:"email,omitempty"`
	Roles   []string `json:"roles"`
	IDToken string   `json:"idt,omitempty"` // raw id_token for logout id_token_hint

	// FirstName and LastName are derived from the id_token at login and used to
	// record the person in the known-users directory (#614). They are NOT
	// persisted in the session cookie (json:"-"); they exist only in-memory for
	// the duration of the callback, so the cookie stays small and these are
	// absent on subsequent cookie-authenticated requests.
	FirstName string `json:"-"`
	LastName  string `json:"-"`
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

// ParseSameSite maps a configuration string ("lax", "strict", "none") to an
// http.SameSite mode. An empty or unrecognized value yields the zero value,
// which effectiveSameSite treats as Lax, the safe default. The match is
// case-insensitive.
func ParseSameSite(s string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	default:
		return 0 // unset resolves to the Lax default via effectiveSameSite
	}
}

// IsValidSameSite reports whether s is a recognized same_site config value
// (case-insensitive): "" (defaults to Lax), "lax", "strict", or "none".
// Config validation uses this to reject a typo at startup rather than let
// ParseSameSite silently downgrade an unrecognized value to Lax.
func IsValidSameSite(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "lax", "strict", "none":
		return true
	default:
		return false
	}
}
