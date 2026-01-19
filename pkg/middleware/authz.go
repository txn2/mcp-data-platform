package middleware

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// Authorizer checks if a user is authorized for a tool.
type Authorizer interface {
	// IsAuthorized checks if the user can use the tool.
	IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (bool, string)
}

// AuthzMiddleware creates authorization middleware.
func AuthzMiddleware(authorizer Authorizer) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, request)
			}

			// Check authorization
			authorized, reason := authorizer.IsAuthorized(ctx, pc.UserID, pc.Roles, pc.ToolName)
			pc.Authorized = authorized
			if !authorized {
				pc.AuthzError = reason
				return mcp.NewToolResultError("not authorized: " + reason), nil
			}

			return next(ctx, request)
		}
	}
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
