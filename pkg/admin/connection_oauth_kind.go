package admin

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// OAuthKindHandler adapts a kind-specific connection config to the
// shared connoauth flow. The platform registers one per kind (MCP
// gateway, HTTP API gateway, future kinds) at startup. The unified
// connection OAuth handler dispatches on r.PathValue("kind") and
// invokes the registered OAuthKindHandler for ParseConfig and
// post-auth side effects.
//
// New connection kinds add support by implementing this interface
// (typically in their toolkit package) and registering it in
// HandlerDeps.OAuthKinds — they do NOT add a parallel handler file
// or a parallel token store.
type OAuthKindHandler interface {
	// ParseOAuthConfig validates the connection's stored config and
	// extracts the OAuth 2.1 settings (auth URL, token URL,
	// credentials, scopes, etc.) into a connoauth.Config. Returns an
	// error when the connection is not configured for the
	// authorization_code grant (the unified handler maps the error to
	// HTTP 409 Conflict with the operator-facing message).
	ParseOAuthConfig(connConfig map[string]any) (connoauth.Config, error)
	// AfterConnect runs after a successful Connect or Reacquire so
	// the kind can perform any per-kind side effect needed to make
	// the connection usable. The MCP gateway re-adds the connection
	// here so its tool surface is registered with the platform's MCP
	// server; the HTTP API gateway is a no-op because its
	// Authenticator reads the store lazily on every request.
	AfterConnect(ctx context.Context, name string, connConfig map[string]any) error
}

// OAuthKindHandlers is the registry shape passed via HandlerDeps. The
// key is the kind string (e.g. connoauth.KindMCP). Keep keys aligned
// with the connection_kind values stored in connection_instances and
// connection_oauth_tokens.
type OAuthKindHandlers map[string]OAuthKindHandler
