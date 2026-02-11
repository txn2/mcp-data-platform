package admin

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// contextKey is a private type for context keys in admin package.
type contextKey string

const adminUserKey contextKey = "admin_user"

// User holds information about the authenticated admin user.
type User struct {
	UserID string
	Roles  []string
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
	return slices.Contains(roles, "admin")
}

// PlatformAuthenticator wraps the platform's middleware.Authenticator chain
// for HTTP admin requests, validating that the resolved persona matches
// the configured admin persona.
type PlatformAuthenticator struct {
	authenticator middleware.Authenticator
	adminPersona  string
	registry      *persona.Registry
}

// NewPlatformAuthenticator creates a PlatformAuthenticator that bridges
// the platform's MCP auth chain to HTTP admin requests.
func NewPlatformAuthenticator(
	auth middleware.Authenticator,
	adminPersona string,
	registry *persona.Registry,
) *PlatformAuthenticator {
	return &PlatformAuthenticator{
		authenticator: auth,
		adminPersona:  adminPersona,
		registry:      registry,
	}
}

// Authenticate extracts credentials from the HTTP request, delegates to the
// platform authenticator, then checks that the resolved persona matches
// the admin persona.
func (pa *PlatformAuthenticator) Authenticate(r *http.Request) (*User, error) {
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
