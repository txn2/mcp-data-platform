package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// contextKey is a private type for context keys in admin package.
type contextKey string

const adminUserKey contextKey = "admin_user"

// User holds information about the authenticated admin user.
type User struct {
	UserID string
	Email  string
	Roles  []string
	// FromCookie is true when the user authenticated via a browser session
	// cookie (as opposed to an API key / Bearer token). Cookie-authenticated
	// requests are the only ones subject to CSRF enforcement, since the
	// browser attaches the cookie automatically on cross-site requests.
	FromCookie bool
}

// GetUser returns the User from context, or nil if not set.
func GetUser(ctx context.Context) *User {
	u, _ := ctx.Value(adminUserKey).(*User)
	return u
}

// Authenticator validates admin credentials.
type Authenticator interface {
	Authenticate(r *http.Request) (*User, error)
}

// APIKeyAuthenticator validates admin access via API keys.
type APIKeyAuthenticator struct {
	Keys map[string]User // key -> user info
}

// Authenticate checks the X-API-Key or Authorization header.
func (a *APIKeyAuthenticator) Authenticate(r *http.Request) (*User, error) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		auth := r.Header.Get("Authorization")
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			key = token
		}
	}
	if key == "" {
		return nil, nil //nolint:nilnil // nil user with nil error means no credentials provided
	}

	if user, ok := a.Keys[key]; ok {
		return &user, nil
	}
	return nil, nil //nolint:nilnil // nil user with nil error means invalid key (unauthenticated)
}

// RequireAdmin creates middleware that enforces admin authentication.
func RequireAdmin(auth Authenticator) func(http.Handler) http.Handler {
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

			if !hasAdminRole(user.Roles) {
				writeError(w, http.StatusForbidden, "admin role required")
				return
			}

			ctx := context.WithValue(r.Context(), adminUserKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hasAdminRole checks whether the roles list contains "admin".
func hasAdminRole(roles []string) bool {
	return slices.Contains(roles, roleAdmin)
}

// PlatformAuthenticator wraps the platform's middleware.Authenticator chain
// for HTTP admin requests, validating that the resolved persona matches
// the configured admin persona.
type PlatformAuthenticator struct {
	authenticator middleware.Authenticator
	adminPersona  string
	registry      *persona.Registry
	browserAuth   *browsersession.Authenticator
}

// NewPlatformAuthenticator creates a PlatformAuthenticator that bridges
// the platform's MCP auth chain to HTTP admin requests.
func NewPlatformAuthenticator(
	auth middleware.Authenticator,
	adminPersona string,
	registry *persona.Registry,
	opts ...PlatformAuthOption,
) *PlatformAuthenticator {
	pa := &PlatformAuthenticator{
		authenticator: auth,
		adminPersona:  adminPersona,
		registry:      registry,
	}
	for _, opt := range opts {
		opt(pa)
	}
	return pa
}

// PlatformAuthOption configures the PlatformAuthenticator.
type PlatformAuthOption func(*PlatformAuthenticator)

// WithBrowserSessionAuth adds cookie-based authentication.
func WithBrowserSessionAuth(ba *browsersession.Authenticator) PlatformAuthOption {
	return func(pa *PlatformAuthenticator) {
		pa.browserAuth = ba
	}
}

// Authenticate extracts credentials from the HTTP request, delegates to the
// platform authenticator, then checks that the resolved persona matches
// the admin persona. It checks browser session cookies first, then falls
// back to token-based authentication.
func (pa *PlatformAuthenticator) Authenticate(r *http.Request) (*User, error) {
	// Try cookie-based auth first (browser sessions). A CSRF failure on an
	// otherwise-valid admin cookie is deferred, not fatal: a CSRF-exempt
	// token (which a cross-site attacker cannot supply) may still legitimately
	// authenticate the request.
	u, csrfErr := pa.authenticateViaCookie(r)
	if u != nil {
		return u, nil
	}

	// Fall back to token-based auth.
	tokenUser, tokenErr := pa.authenticateViaToken(r)
	if tokenUser != nil {
		return tokenUser, nil
	}
	// No token authenticated the request. If a cookie was present but failed
	// CSRF, surface that rejection (→ 403) rather than a bare 401.
	if csrfErr != nil {
		return nil, csrfErr
	}
	return tokenUser, tokenErr
}

// authenticateViaCookie tries browser session cookie auth and verifies the
// user has the admin persona. It returns (nil, nil) when there is no valid
// admin cookie, and (nil, ErrCSRFInvalid) when a valid admin cookie is
// present on a state-changing request that lacks a valid CSRF token.
func (pa *PlatformAuthenticator) authenticateViaCookie(r *http.Request) (*User, error) {
	if pa.browserAuth == nil {
		return nil, nil //nolint:nilnil // no cookie authenticator configured
	}
	info, err := pa.browserAuth.AuthenticateHTTP(r)
	if err != nil || info == nil {
		return nil, nil //nolint:nilnil // no valid cookie → fall back to token
	}
	resolved, ok := pa.registry.GetForRoles(info.Roles)
	if !ok || resolved.Name != pa.adminPersona {
		return nil, nil //nolint:nilnil // cookie user is not admin persona
	}
	// The request is authenticated by a cookie the browser attached
	// automatically; enforce CSRF on state-changing methods.
	if err := pa.browserAuth.ValidateCSRFRequest(r, info.UserID); err != nil {
		return nil, err
	}
	return &User{UserID: info.UserID, Email: info.Email, Roles: info.Roles, FromCookie: true}, nil
}

// authenticateViaToken extracts a token from headers, validates it, and
// verifies the user has the admin persona.
func (pa *PlatformAuthenticator) authenticateViaToken(r *http.Request) (*User, error) {
	token := extractToken(r)
	if token == "" {
		return nil, nil //nolint:nilnil // no credentials
	}

	ctx := middleware.WithToken(r.Context(), token)
	info, err := pa.authenticator.Authenticate(ctx)
	if err != nil {
		// Auth failures (invalid keys, expired tokens) are not internal
		// errors — treat them as "no valid credentials" so the middleware
		// returns 401 instead of 500.
		slog.Debug("admin auth rejected", "error", err)
		return nil, nil //nolint:nilnil // auth failure → unauthenticated
	}
	if info == nil {
		return nil, nil //nolint:nilnil // authenticator rejected
	}

	// Resolve persona from roles
	resolved, ok := pa.registry.GetForRoles(info.Roles)
	if !ok || resolved.Name != pa.adminPersona {
		return nil, nil //nolint:nilnil // user authenticated but not admin persona
	}

	return &User{
		UserID: info.UserID,
		Email:  info.Email,
		Roles:  info.Roles,
	}, nil
}

// extractToken extracts an authentication token from X-API-Key or Authorization headers.
func extractToken(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return token
	}
	return ""
}

// RequirePersona creates middleware that enforces authentication via an
// Authenticator (which already includes persona validation).
func RequirePersona(auth Authenticator) func(http.Handler) http.Handler {
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

			ctx := context.WithValue(r.Context(), adminUserKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
