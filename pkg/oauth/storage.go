// Package oauth provides OAuth 2.1 server capabilities.
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"time"
)

// HashToken returns the hex-encoded SHA-256 digest of a token value.
// Opaque bearer credentials (refresh tokens, authorization codes) are
// stored only as digests so a database read never yields a usable
// credential; lookups hash the presented value and compare digests.
func HashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// Storage defines the interface for OAuth data persistence.
type Storage interface {
	// Client management
	CreateClient(ctx context.Context, client *Client) error
	GetClient(ctx context.Context, clientID string) (*Client, error)
	UpdateClient(ctx context.Context, client *Client) error
	DeleteClient(ctx context.Context, clientID string) error
	ListClients(ctx context.Context) ([]*Client, error)

	// Authorization code management. Implementations persist only the
	// SHA-256 digest of the code value (HashToken); the get, delete, and
	// consume methods take the raw value and hash it before comparison.
	SaveAuthorizationCode(ctx context.Context, code *AuthorizationCode) error
	GetAuthorizationCode(ctx context.Context, code string) (*AuthorizationCode, error)
	DeleteAuthorizationCode(ctx context.Context, code string) error
	// ConsumeAuthorizationCode atomically deletes the authorization code
	// and returns it, so single-use invalidation and retrieval cannot
	// diverge: a replayed or concurrently exchanged code fails because
	// the record is already gone. Returns an error when the code does
	// not exist.
	ConsumeAuthorizationCode(ctx context.Context, code string) (*AuthorizationCode, error)
	CleanupExpiredCodes(ctx context.Context) error

	// Token management. Implementations persist only the SHA-256 digest
	// of the token value (HashToken); the get, delete, and consume
	// methods take the raw value and hash it before comparison.
	SaveRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, token string) error
	// ConsumeRefreshToken atomically deletes the refresh token and
	// returns it, backing rotation: the old token is provably invalid
	// before new tokens are issued. Returns an error when the token
	// does not exist.
	ConsumeRefreshToken(ctx context.Context, token string) (*RefreshToken, error)
	DeleteRefreshTokensForClient(ctx context.Context, clientID string) error
	CleanupExpiredTokens(ctx context.Context) error
}

// Client represents an OAuth 2.1 client.
type Client struct {
	ID           string    `json:"id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"` // #nosec G117 -- bcrypt hashed, required for OAuth client storage
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirect_uris"`
	GrantTypes   []string  `json:"grant_types"`
	RequirePKCE  bool      `json:"require_pkce"`
	CreatedAt    time.Time `json:"created_at"`
	Active       bool      `json:"active"`
	// DynamicallyRegistered marks clients created through the unauthenticated
	// DCR /register endpoint (RFC 7591), as opposed to pre-registered
	// config-file clients. Only DCR clients are eligible for TTL-based cleanup
	// of unused registrations.
	DynamicallyRegistered bool `json:"dynamically_registered"`
}

// AuthorizationCode represents an OAuth authorization code.
type AuthorizationCode struct {
	ID string `json:"id"`
	// Code carries the raw value when issuing; Storage implementations
	// persist only its SHA-256 digest (HashToken), so records read back
	// from storage hold the digest, never a usable credential.
	Code          string         `json:"code"`
	ClientID      string         `json:"client_id"`
	UserID        string         `json:"user_id"`
	UserClaims    map[string]any `json:"user_claims"`
	CodeChallenge string         `json:"code_challenge"`
	RedirectURI   string         `json:"redirect_uri"`
	Scope         string         `json:"scope"`
	ExpiresAt     time.Time      `json:"expires_at"`
	Used          bool           `json:"used"`
	CreatedAt     time.Time      `json:"created_at"`
}

// RefreshToken represents an OAuth refresh token.
type RefreshToken struct {
	ID string `json:"id"`
	// Token carries the raw value when issuing; Storage implementations
	// persist only its SHA-256 digest (HashToken), so records read back
	// from storage hold the digest, never a usable credential.
	Token      string         `json:"token"`
	ClientID   string         `json:"client_id"`
	UserID     string         `json:"user_id"`
	UserClaims map[string]any `json:"user_claims"`
	Scope      string         `json:"scope"`
	ExpiresAt  time.Time      `json:"expires_at"`
	CreatedAt  time.Time      `json:"created_at"`
}

// IsExpired checks if the authorization code has expired.
func (c *AuthorizationCode) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// IsExpired checks if the refresh token has expired.
func (t *RefreshToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// ValidRedirectURI checks if a redirect URI is valid for this client.
// For loopback redirect URIs (127.0.0.1, [::1], localhost), matching follows
// RFC 8252 Section 7.3: scheme and host must match, but port and path are ignored.
func (c *Client) ValidRedirectURI(uri string) bool {
	for _, registered := range c.RedirectURIs {
		if matchesRedirectURI(registered, uri) {
			return true
		}
	}
	return false
}

// isLoopbackURI checks if a URI is a loopback redirect URI per RFC 8252 Section 7.3.
// Loopback URIs use the http scheme with host 127.0.0.1, [::1], or localhost.
func isLoopbackURI(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	if u.Scheme != schemeHTTP {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// matchesRedirectURI checks if a requested redirect URI matches a registered one.
// For loopback URIs (RFC 8252 Section 7.3), only scheme and host must match;
// port and path are ignored because native apps use dynamic ports.
// Non-loopback URIs require an exact string match.
func matchesRedirectURI(registered, requested string) bool {
	if registered == requested {
		return true
	}
	if !isLoopbackURI(registered) || !isLoopbackURI(requested) {
		return false
	}
	regURL, err := url.Parse(registered)
	if err != nil {
		return false
	}
	reqURL, err := url.Parse(requested)
	if err != nil {
		return false
	}
	return regURL.Scheme == reqURL.Scheme && regURL.Hostname() == reqURL.Hostname()
}

// SupportsGrantType checks if the client supports a grant type.
func (c *Client) SupportsGrantType(grantType string) bool {
	return slices.Contains(c.GrantTypes, grantType)
}
