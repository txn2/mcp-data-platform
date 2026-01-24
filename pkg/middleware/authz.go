package middleware

import (
	"context"
)

// Authorizer checks if a user is authorized for a tool.
type Authorizer interface {
	// IsAuthorized checks if the user can use the tool.
	IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (bool, string)
}

// NoopAuthorizer always authorizes.
type NoopAuthorizer struct{}

// IsAuthorized always returns true.
func (n *NoopAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (bool, string) {
	return true, ""
}

// AllowAllAuthorizer authorizes all requests.
func AllowAllAuthorizer() Authorizer {
	return &NoopAuthorizer{}
}
