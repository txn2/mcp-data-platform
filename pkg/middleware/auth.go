package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Authenticator validates authentication credentials.
type Authenticator interface {
	// Authenticate validates credentials and returns user info.
	Authenticate(ctx context.Context) (*UserInfo, error)
}

// ToolkitLookup provides toolkit metadata for a given tool name.
type ToolkitLookup interface {
	// GetToolkitForTool returns toolkit info (kind, name, connection) for a tool.
	// Returns Found=false if the tool is not found in any registered toolkit.
	GetToolkitForTool(toolName string) registry.ToolkitMatch
}

// UserInfo holds authenticated user information.
type UserInfo struct {
	UserID   string
	Email    string
	Claims   map[string]any
	Roles    []string
	AuthType string // "oidc", "apikey", etc.
}

// NewToolResultError creates an error result.
func NewToolResultError(errMsg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: errMsg},
		},
	}
}

// NewToolResultText creates a text result.
func NewToolResultText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
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
