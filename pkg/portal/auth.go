package portal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

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
}

// GetUser returns the User from context, or nil if not set.
func GetUser(ctx context.Context) *User {
	u, _ := ctx.Value(portalUserKey).(*User)
	return u
}

// ContextWithUser returns a copy of ctx carrying the authenticated user, the
// value GetUser reads. Exported so handlers split into sibling packages (e.g.
// internal/httpserver/datahubapi) can be exercised with an authenticated principal.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, portalUserKey, user)
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

// Authenticate resolves the caller via session cookie (CSRF-enforced on
// state-changing methods) first, then falls back to token auth. A CSRF failure
// on a valid cookie is deferred, not fatal: a CSRF-exempt token (which a
// cross-site attacker cannot supply) may still authenticate the request, and
// only when none does is the rejection surfaced (→ 403).
func (pa *Authenticator) Authenticate(r *http.Request) (*User, error) {
	var csrfErr error
	if pa.browserAuth != nil {
		if info, err := pa.browserAuth.AuthenticateHTTP(r); err == nil && info != nil {
			verr := pa.browserAuth.ValidateCSRFRequest(r, info.UserID)
			if verr == nil {
				return &User{UserID: info.UserID, Email: info.Email, Roles: info.Roles, FromCookie: true}, nil
			}
			csrfErr = fmt.Errorf("portal cookie csrf: %w", verr)
		}
	}

	// Fall back to token-based auth (API key or Bearer token).
	token := extractPortalToken(r)
	if token == "" {
		return nil, csrfErr
	}
	info, err := pa.authenticator.Authenticate(middleware.WithToken(r.Context(), token))
	if err != nil {
		slog.Warn("portal auth failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil, csrfErr
	}
	if info == nil {
		return nil, csrfErr
	}
	return &User{UserID: info.UserID, Email: info.Email, Roles: info.Roles}, nil
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

// IssueCSRF returns a CSRF token bound to subject, or "" when cookie auth is
// not configured.
func (pa *Authenticator) IssueCSRF(subject string) string {
	if pa.browserAuth == nil {
		return ""
	}
	return pa.browserAuth.IssueCSRFToken(subject)
}

// RequirePortalAuth creates middleware that enforces portal authentication.
func RequirePortalAuth(auth *Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := auth.Authenticate(r)
			if err != nil {
				if errors.Is(err, browsersession.ErrCSRFInvalid) {
					writeError(w, http.StatusForbidden, "invalid or missing CSRF token")
					return
				}
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
