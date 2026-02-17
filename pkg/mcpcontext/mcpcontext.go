// Package mcpcontext provides context helpers for MCP session state.
// These are in a separate package to avoid import cycles between
// middleware and toolkit packages.
package mcpcontext

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// contextKey is a private type for context keys.
type contextKey int

const (
	serverSessionKey contextKey = iota
	progressTokenKey
	readOnlyEnforcedKey
)

// WithServerSession adds a ServerSession to the context.
func WithServerSession(ctx context.Context, ss *mcp.ServerSession) context.Context {
	return context.WithValue(ctx, serverSessionKey, ss)
}

// GetServerSession retrieves the ServerSession from the context.
func GetServerSession(ctx context.Context) *mcp.ServerSession {
	ss, _ := ctx.Value(serverSessionKey).(*mcp.ServerSession)
	return ss
}

// WithProgressToken adds a progress token to the context.
func WithProgressToken(ctx context.Context, token any) context.Context {
	return context.WithValue(ctx, progressTokenKey, token)
}

// GetProgressToken retrieves the progress token from the context.
func GetProgressToken(ctx context.Context) any {
	return ctx.Value(progressTokenKey)
}

// WithReadOnlyEnforced marks the context as read-only enforced.
// Set by MCPToolCallMiddleware when the persona's read_only rules match
// the tool; read by toolkit interceptors to block write operations.
func WithReadOnlyEnforced(ctx context.Context, enforced bool) context.Context {
	return context.WithValue(ctx, readOnlyEnforcedKey, enforced)
}

// IsReadOnlyEnforced returns true if the persona's read_only rules matched.
func IsReadOnlyEnforced(ctx context.Context) bool {
	v, _ := ctx.Value(readOnlyEnforcedKey).(bool)
	return v
}
