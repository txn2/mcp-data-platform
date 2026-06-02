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
	authTokenKey
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

// WithAuthToken stores the inbound authentication token (the bearer or
// API key that authenticated the MCP session) on the context. It lives
// here, rather than in pkg/middleware, so toolkit packages can read it
// without importing middleware (which would form an import cycle). The
// auth middleware writes it; the api-gateway toolkit reads it to forward
// the acting caller's identity on identity-passthrough connections.
func WithAuthToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, authTokenKey, token)
}

// GetAuthToken retrieves the inbound authentication token from the
// context, or "" when none was set.
func GetAuthToken(ctx context.Context) string {
	token, _ := ctx.Value(authTokenKey).(string)
	return token
}
