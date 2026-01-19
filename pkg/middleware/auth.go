package middleware

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// Authenticator validates authentication credentials.
type Authenticator interface {
	// Authenticate validates credentials and returns user info.
	Authenticate(ctx context.Context) (*UserInfo, error)
}

// UserInfo holds authenticated user information.
type UserInfo struct {
	UserID   string
	Email    string
	Claims   map[string]any
	Roles    []string
	AuthType string // "oidc", "apikey", etc.
}

// AuthMiddleware creates authentication middleware.
func AuthMiddleware(authenticator Authenticator) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, request)
			}

			// Authenticate
			userInfo, err := authenticator.Authenticate(ctx)
			if err != nil {
				return mcp.NewToolResultError("authentication failed: " + err.Error()), nil
			}

			if userInfo != nil {
				pc.UserID = userInfo.UserID
				pc.UserEmail = userInfo.Email
				pc.UserClaims = userInfo.Claims
				pc.Roles = userInfo.Roles
			}

			return next(ctx, request)
		}
	}
}

// NoopAuthenticator always succeeds authentication.
type NoopAuthenticator struct {
	DefaultUserID string
	DefaultRoles  []string
}

// Authenticate always returns a default user.
func (n *NoopAuthenticator) Authenticate(_ context.Context) (*UserInfo, error) {
	userID := n.DefaultUserID
	if userID == "" {
		userID = "anonymous"
	}
	return &UserInfo{
		UserID:   userID,
		Email:    userID + "@localhost",
		Claims:   make(map[string]any),
		Roles:    n.DefaultRoles,
		AuthType: "noop",
	}, nil
}
