package middleware

import (
	"context"
)

// Authorizer checks if a user is authorized for a tool.
type Authorizer interface {
	// IsAuthorized checks if the user can use the tool.
	// Returns:
	//   - authorized: whether the user is authorized
	//   - personaName: the resolved persona name (for audit logging)
	//   - reason: reason for denial (empty if authorized)
	IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (authorized bool, personaName string, reason string)
}

// NoopAuthorizer always authorizes.
type NoopAuthorizer struct{}

// IsAuthorized always returns true with empty persona name.
func (*NoopAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (authorized bool, personaName, reason string) {
	return true, "", ""
}

// AllowAllAuthorizer authorizes all requests.
func AllowAllAuthorizer() Authorizer {
	return &NoopAuthorizer{}
}
