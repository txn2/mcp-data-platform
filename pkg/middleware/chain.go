package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler is the type for tool handlers.
type Handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)

// Middleware wraps a handler with additional logic.
type Middleware func(Handler) Handler

// Chain holds an ordered list of middleware.
type Chain struct {
	before []Middleware // Run before handler
	after  []Middleware // Run after handler (for response processing)
}

// NewChain creates a new middleware chain.
func NewChain() *Chain {
	return &Chain{
		before: make([]Middleware, 0),
		after:  make([]Middleware, 0),
	}
}

// UseBefore adds middleware to run before the handler.
func (c *Chain) UseBefore(mw Middleware) {
	c.before = append(c.before, mw)
}

// UseAfter adds middleware to run after the handler.
func (c *Chain) UseAfter(mw Middleware) {
	c.after = append(c.after, mw)
}

// Wrap wraps a handler with the middleware chain.
func (c *Chain) Wrap(handler Handler) Handler {
	// Apply after middleware in reverse order (so first added runs last)
	wrapped := handler
	for i := len(c.after) - 1; i >= 0; i-- {
		wrapped = c.after[i](wrapped)
	}

	// Apply before middleware in reverse order (so first added runs first)
	for i := len(c.before) - 1; i >= 0; i-- {
		wrapped = c.before[i](wrapped)
	}

	return wrapped
}

// WrapWithContext creates a handler that initializes platform context.
func (c *Chain) WrapWithContext(handler Handler, toolName, toolkitKind, toolkitName string) Handler {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Create request ID
		requestID := generateRequestID()

		// Initialize platform context
		pc := NewPlatformContext(requestID)
		pc.ToolName = toolName
		pc.ToolkitKind = toolkitKind
		pc.ToolkitName = toolkitName

		ctx = WithPlatformContext(ctx, pc)

		// Run through chain
		return c.Wrap(handler)(ctx, request)
	}
}

// generateRequestID creates a cryptographically secure request ID.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req-%x", b)
}
