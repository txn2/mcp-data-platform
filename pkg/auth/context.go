// Package auth provides authentication support for the platform.
package auth

import (
	"context"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// contextKey is a private type for context keys.
type contextKey int

const (
	userContextKey contextKey = iota
	// attemptCountedKey marks a context whose request the HTTP gate already
	// counted in auth_attempts_total (WithAttemptCounted).
	attemptCountedKey
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
// Delegates to middleware.WithToken so that both packages share the same context key.
func WithToken(ctx context.Context, token string) context.Context {
	return middleware.WithToken(ctx, token)
}

// GetToken retrieves a token from the context.
// Delegates to middleware.GetToken so that both packages share the same context key.
func GetToken(ctx context.Context) string {
	return middleware.GetToken(ctx)
}

// HasRole checks if the user has a specific role.
func (uc *UserContext) HasRole(role string) bool {
	return slices.Contains(uc.Roles, role)
}

// HasAnyRole checks if the user has any of the specified roles.
func (uc *UserContext) HasAnyRole(roles ...string) bool {
	return slices.ContainsFunc(roles, uc.HasRole)
}

// InGroup checks if the user is in a specific group.
func (uc *UserContext) InGroup(group string) bool {
	return slices.Contains(uc.Groups, group)
}

// WithAttemptCounted marks ctx as carrying a request the HTTP gate already
// validated and counted in auth_attempts_total (#1898). The protocol layer
// validates the same credential again for every tool call; under this mark
// that validation is not counted a second time, so the counter reads one
// attempt per request. A streamable-HTTP connection's later calls inherit the
// mark from the request it was built on, and each of their own requests is
// counted by the gate it passes through.
func WithAttemptCounted(ctx context.Context) context.Context {
	return context.WithValue(ctx, attemptCountedKey, true)
}

// attemptCounted reports whether ctx carries a request already counted.
func attemptCounted(ctx context.Context) bool {
	counted, _ := ctx.Value(attemptCountedKey).(bool)
	return counted
}
