// Package middleware provides the middleware chain for tool handlers.
package middleware

import (
	"context"
	"time"
)

// contextKey is a private type for context keys.
type contextKey int

const (
	platformContextKey contextKey = iota
)

// PlatformContext holds platform-specific context for a request.
type PlatformContext struct {
	// Request identification
	RequestID string
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
	AuthzError string

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
