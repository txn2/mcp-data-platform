package admin

import (
	"context"
	"net/http"
	"slices"
	"strings"
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
