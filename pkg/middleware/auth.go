package middleware

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Authenticator validates authentication credentials.
type Authenticator interface {
	// Authenticate validates credentials and returns user info.
	Authenticate(ctx context.Context) (*UserInfo, error)
}

// ErrValidationUnavailable is the sentinel an authenticator wraps when it could
// not COMPLETE validation because a dependency was transiently unavailable (e.g.
// the OIDC JWKS endpoint was unreachable and the key cache had expired) — as
// opposed to a definitive rejection (bad signature, expired token, unknown key
// in a fresh cache). It lets a caller distinguish "this token is invalid" from
// "I could not tell right now": the HTTP auth gate fails OPEN on it, passing the
// request to the protocol layer rather than dropping a possibly-valid client
// during an IdP blip, and the protocol layer reports a retryable outage rather
// than an identity failure. This never grants access — during the same outage
// the protocol layer cannot validate the token either, so a tool call is still
// rejected in-band; only auth-independent requests (initialize/list) proceed.
//
// It lives here (not in pkg/auth) so the protocol middleware can branch on it
// without importing pkg/auth, which would cycle.
var ErrValidationUnavailable = errors.New("token validation temporarily unavailable")

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
	Name     string // display name from claims (empty for API keys); may be a full name
	Claims   map[string]any
	Roles    []string
	AuthType string // one of the AuthType* constants below
}

// AuthType values set by the authenticators, identifying HOW a caller was
// authenticated. They are defined here (rather than as scattered string
// literals) so producers in pkg/auth and the distinct-identity guard in
// DiscoveryScopeKey (nonDistinctAuthTypes) reference one source of truth.
//
// IMPORTANT: any new AuthType that assigns every caller the SAME shared UserID
// (a guest / shared-token identity, like anonymous and noop) MUST be added to
// nonDistinctAuthTypes, or one such caller's search would open the search-first
// gate for all of them.
const (
	AuthTypeOIDC      = "oidc"
	AuthTypeOAuth     = "oauth"
	AuthTypeAPIKey    = "apikey"
	AuthTypeAnonymous = "anonymous" // shared fallback identity when auth is allowed-anonymous
	AuthTypeNoop      = "noop"      // shared identity from NoopAuthenticator (auth disabled)
)

// NewToolResultError creates an error result using the SDK's SetError method.
// The underlying error is retrievable via CallToolResult.GetError().
func NewToolResultError(errMsg string) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(errors.New(errMsg))
	return result
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
		AuthType: AuthTypeNoop,
	}, nil
}
