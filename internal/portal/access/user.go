package access

import "context"

// contextKey is a private type for portal context keys.
type contextKey string

const portalUserKey contextKey = "portal_user"

// User holds information about the authenticated portal user.
type User struct {
	UserID string
	Email  string
	Roles  []string
	// FromCookie is true for browser-session (cookie) auth; only such requests
	// are CSRF-enforced (API-key / Bearer auth is exempt).
	FromCookie bool
	// AuthType is HOW this caller was authenticated, as the authenticator that
	// admitted them reported it. It is carried rather than re-derived because a
	// surface that opens a session ON THIS CALLER'S BEHALF — the portal's
	// script dry run (#1364) — must present the authentication the request
	// actually arrived with, and FromCookie alone cannot tell an API key from a
	// bearer token.
	AuthType string
}

// GetUser returns the User from context, or nil if not set.
func GetUser(ctx context.Context) *User {
	u, _ := ctx.Value(portalUserKey).(*User)
	return u
}

// ContextWithUser returns a copy of ctx carrying the authenticated user, the
// value GetUser reads. The key is unexported and lives here with the subject of
// every permission check, so the parent package and every handler seam read the
// identity the portal authenticator wrote rather than each keying its own copy.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, portalUserKey, user)
}
