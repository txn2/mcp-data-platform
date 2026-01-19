// Package auth provides authentication support for the platform.
package auth

import "context"

// contextKey is a private type for context keys.
type contextKey int

const (
	userContextKey contextKey = iota
	tokenContextKey
)

// UserContext holds authenticated user information.
type UserContext struct {
	UserID    string         `json:"user_id"`
	Email     string         `json:"email,omitempty"`
	Name      string         `json:"name,omitempty"`
	Roles     []string       `json:"roles,omitempty"`
	Groups    []string       `json:"groups,omitempty"`
	Claims    map[string]any `json:"claims,omitempty"`
	AuthType  string         `json:"auth_type"` // "oidc", "apikey"
	TokenType string         `json:"token_type,omitempty"`
}

// WithUserContext adds user context to the context.
func WithUserContext(ctx context.Context, uc *UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, uc)
}

// GetUserContext retrieves user context from the context.
func GetUserContext(ctx context.Context) *UserContext {
	if uc, ok := ctx.Value(userContextKey).(*UserContext); ok {
		return uc
	}
	return nil
}

// WithToken adds a token to the context.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenContextKey, token)
}

// GetToken retrieves a token from the context.
func GetToken(ctx context.Context) string {
	if token, ok := ctx.Value(tokenContextKey).(string); ok {
		return token
	}
	return ""
}

// HasRole checks if the user has a specific role.
func (uc *UserContext) HasRole(role string) bool {
	for _, r := range uc.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if the user has any of the specified roles.
func (uc *UserContext) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if uc.HasRole(role) {
			return true
		}
	}
	return false
}

// InGroup checks if the user is in a specific group.
func (uc *UserContext) InGroup(group string) bool {
	for _, g := range uc.Groups {
		if g == group {
			return true
		}
	}
	return false
}
