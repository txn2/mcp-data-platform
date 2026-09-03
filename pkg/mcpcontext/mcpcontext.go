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
	sourceKey
)

// SourceScript labels a call arriving from a managed script's host
// bindings. It is defined here, rather than in pkg/middleware alongside
// the other source labels, so a toolkit can recognize it without
// importing middleware (which would form an import cycle); middleware's
// SourceScript is this constant.
const SourceScript = "script"

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

// WithSource records how a call arrived, using the labels pkg/middleware
// defines. It lives here so a toolkit can vary on the caller without
// importing middleware. The tool-call middleware writes it from the
// PlatformContext it has just resolved; the api-gateway toolkit reads it
// to tell a managed script's call from a model's.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey, source)
}

// GetSource returns how the call arrived, or "" when nothing recorded it.
func GetSource(ctx context.Context) string {
	source, _ := ctx.Value(sourceKey).(string)
	return source
}
