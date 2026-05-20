// Package middleware provides the middleware chain for tool handlers.
package middleware

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// EnrichmentModeFull is the enrichment mode value for full (non-dedup) enrichment.
const EnrichmentModeFull = "full"

// contextKey is a private type for context keys.
type contextKey int

const (
	platformContextKey contextKey = iota
	tokenContextKey
	preAuthUserKey
	sourceOverrideKey
)

// PlatformContext holds platform-specific context for a request.
type PlatformContext struct {
	// Request identification
	RequestID string
	SessionID string
	StartTime time.Time

	// User information
	UserID      string
	UserEmail   string
	UserClaims  map[string]any
	Roles       []string
	PersonaName string

	// Tool information
	ToolName    string
	ToolkitKind string
	ToolkitName string
	Connection  string

	// Authorization
	Authorized bool
	IsAdmin    bool // user belongs to the platform's admin persona
	AuthzError string

	// Transport metadata
	Transport string // "stdio" or "http"
	Source    string // "mcp", "admin", "inspector"

	// Enrichment tracking (set by enrichment middleware, read by audit)
	EnrichmentApplied     bool
	EnrichmentTokensFull  int    // estimated tokens for full enrichment
	EnrichmentTokensDedup int    // estimated tokens for deduped enrichment (0 if full sent)
	EnrichmentMode        string // "full", "summary", "reference", "none", or "" (not enriched)

	// Results (populated after handler)
	Success      bool
	ErrorMessage string
	Duration     time.Duration
}

// NewPlatformContext creates a new platform context.
func NewPlatformContext(requestID string) *PlatformContext {
	return &PlatformContext{
		RequestID:  requestID,
		StartTime:  time.Now(),
		UserClaims: make(map[string]any),
	}
}

// WithPlatformContext adds platform context to the context.
func WithPlatformContext(ctx context.Context, pc *PlatformContext) context.Context {
	return context.WithValue(ctx, platformContextKey, pc)
}

// GetPlatformContext retrieves platform context from the context.
func GetPlatformContext(ctx context.Context) *PlatformContext {
	if pc, ok := ctx.Value(platformContextKey).(*PlatformContext); ok {
		return pc
	}
	return nil
}

// MustGetPlatformContext retrieves platform context or panics.
func MustGetPlatformContext(ctx context.Context) *PlatformContext {
	pc := GetPlatformContext(ctx)
	if pc == nil {
		panic("platform context not found in context")
	}
	return pc
}

// WithToken adds an authentication token to the context.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenContextKey, token)
}

// GetToken retrieves an authentication token from the context.
func GetToken(ctx context.Context) string {
	if token, ok := ctx.Value(tokenContextKey).(string); ok {
		return token
	}
	return ""
}

// WithPreAuthenticatedUser adds a pre-authenticated user to the context.
// When present, the MCP auth middleware skips token validation and uses
// this user info directly. This is used by the admin API when the HTTP
// middleware has already authenticated the user (e.g. via browser session
// cookie) and the OIDC id_token may have expired.
func WithPreAuthenticatedUser(ctx context.Context, info *UserInfo) context.Context {
	return context.WithValue(ctx, preAuthUserKey, info)
}

// GetPreAuthenticatedUser retrieves a pre-authenticated user from the context.
func GetPreAuthenticatedUser(ctx context.Context) *UserInfo {
	if info, ok := ctx.Value(preAuthUserKey).(*UserInfo); ok {
		return info
	}
	return nil
}

// WithSource tags the context with an audit source override. The MCP tool
// call middleware honors this value when populating PlatformContext.Source,
// so REST shims (e.g. pkg/gatewayhttp) can mark tool calls they initiate as
// originating from the REST API rather than from a real MCP transport.
// When unset, the middleware defaults to "mcp".
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceOverrideKey, source)
}

// GetSource returns the audit source override stored on the context, or
// "" when no override has been set.
func GetSource(ctx context.Context) string {
	if s, ok := ctx.Value(sourceOverrideKey).(string); ok {
		return s
	}
	return ""
}

// WithServerSession adds a ServerSession to the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func WithServerSession(ctx context.Context, ss *mcp.ServerSession) context.Context {
	return mcpcontext.WithServerSession(ctx, ss)
}

// GetServerSession retrieves the ServerSession from the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func GetServerSession(ctx context.Context) *mcp.ServerSession {
	return mcpcontext.GetServerSession(ctx)
}

// WithProgressToken adds a progress token to the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func WithProgressToken(ctx context.Context, token any) context.Context {
	return mcpcontext.WithProgressToken(ctx, token)
}

// GetProgressToken retrieves the progress token from the context.
// Delegates to mcpcontext to share context keys with toolkit packages.
func GetProgressToken(ctx context.Context) any {
	return mcpcontext.GetProgressToken(ctx)
}
