package portal

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// contextKey is a private type for portal context keys.
type contextKey string

const portalUserKey contextKey = "portal_user"

// User holds information about the authenticated portal user.
type User struct {
	UserID string
	Roles  []string
}

// GetUser returns the User from context, or nil if not set.
func GetUser(ctx context.Context) *User {
	u, _ := ctx.Value(portalUserKey).(*User)
	return u
}

// Authenticator wraps the platform's middleware.Authenticator chain
// for HTTP portal requests. Unlike the admin authenticator, it does not
// require a specific persona — any authenticated user can access the portal.
type Authenticator struct {
	authenticator middleware.Authenticator
	browserAuth   *browsersession.Authenticator
}

// NewAuthenticator creates a Authenticator.
func NewAuthenticator(auth middleware.Authenticator, opts ...AuthenticatorOption) *Authenticator {
	a := &Authenticator{authenticator: auth}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// AuthenticatorOption configures the portal authenticator.
type AuthenticatorOption func(*Authenticator)

// WithBrowserAuth adds cookie-based authentication.
func WithBrowserAuth(ba *browsersession.Authenticator) AuthenticatorOption {
	return func(a *Authenticator) {
		a.browserAuth = ba
	}
}

// Authenticate extracts credentials from the HTTP request and delegates
// to the platform authenticator. It checks browser session cookies first,
// then falls back to token-based authentication.
func (pa *Authenticator) Authenticate(r *http.Request) (*User, error) {
	// Try cookie-based auth first (browser sessions).
	if pa.browserAuth != nil {
		if info, err := pa.browserAuth.AuthenticateHTTP(r); err == nil && info != nil {
			return &User{UserID: info.UserID, Roles: info.Roles}, nil
		}
	}

	// Fall back to token-based auth (API key or Bearer token).
	token := extractPortalToken(r)
	if token == "" {
		return nil, nil //nolint:nilnil // no credentials
	}

	ctx := middleware.WithToken(r.Context(), token)
	info, err := pa.authenticator.Authenticate(ctx)
	if err != nil {
		slog.Warn("portal auth failed", "error", err)
		return nil, nil //nolint:nilnil // auth failure → unauthenticated
	}
	if info == nil {
		return nil, nil //nolint:nilnil // authenticator rejected
	}

	return &User{
		UserID: info.UserID,
		Roles:  info.Roles,
	}, nil
}

// extractPortalToken extracts an authentication token from X-API-Key or Authorization headers.
func extractPortalToken(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return token
	}
	return ""
}

// RequirePortalAuth creates middleware that enforces portal authentication.
func RequirePortalAuth(auth *Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := auth.Authenticate(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "authentication error")
				return
			}
			if user == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			ctx := context.WithValue(r.Context(), portalUserKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
