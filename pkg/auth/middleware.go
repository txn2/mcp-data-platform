package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ChainedAuthenticator tries multiple authenticators in order.
type ChainedAuthenticator struct {
	authenticators []middleware.Authenticator
	allowAnonymous bool
}

// ChainedAuthConfig configures the chained authenticator.
type ChainedAuthConfig struct {
	AllowAnonymous bool
}

// NewChainedAuthenticator creates a new chained authenticator.
func NewChainedAuthenticator(cfg ChainedAuthConfig, authenticators ...middleware.Authenticator) *ChainedAuthenticator {
	return &ChainedAuthenticator{
		authenticators: authenticators,
		allowAnonymous: cfg.AllowAnonymous,
	}
}

// Authenticate tries each authenticator in order.
func (c *ChainedAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	var lastErr error

	for _, auth := range c.authenticators {
		userInfo, err := auth.Authenticate(ctx)
		if err == nil && userInfo != nil {
			return userInfo, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	if c.allowAnonymous {
		return &middleware.UserInfo{
			UserID:   "anonymous",
			AuthType: "anonymous",
			Claims:   make(map[string]any),
		}, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("authentication failed")
}

// Verify interface compliance.
var _ middleware.Authenticator = (*ChainedAuthenticator)(nil)

// TokenExtractor extracts tokens from various sources.
type TokenExtractor interface {
	Extract(ctx context.Context) (string, error)
}

// BearerTokenExtractor extracts Bearer tokens from Authorization header.
type BearerTokenExtractor struct {
	HeaderName string // Default: "Authorization"
}

// Extract extracts a bearer token from the context.
func (*BearerTokenExtractor) Extract(ctx context.Context) (string, error) {
	// In MCP, we typically get auth info from the request metadata
	// This is a placeholder - actual extraction depends on transport
	token := GetToken(ctx)
	if token == "" {
		return "", fmt.Errorf("no bearer token found")
	}

	// Strip "Bearer " prefix if present
	if after, ok := strings.CutPrefix(token, "Bearer "); ok {
		return after, nil
	}

	return token, nil
}

// APIKeyExtractor extracts API keys from headers or query params.
type APIKeyExtractor struct {
	HeaderName string // e.g., "X-API-Key"
	QueryParam string // e.g., "api_key"
}

// Extract extracts an API key from the context.
func (*APIKeyExtractor) Extract(ctx context.Context) (string, error) {
	// This is a placeholder - actual extraction depends on transport
	token := GetToken(ctx)
	if token == "" {
		return "", fmt.Errorf("no API key found")
	}
	return token, nil
}
