package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Log-field keys kept local to this package so we don't add a cross-
// package import just for two string constants. Must match the keys
// already used by pkg/middleware so operators can grep across the
// stack consistently.
const (
	logKeyRequestID = "request_id"
	logKeyTool      = "tool"
)

// correlationFields extracts request_id and tool name from any
// PlatformContext present in ctx so chained-auth log lines can be
// correlated with the upstream MCP request. Returns empty strings when
// no PlatformContext is set (e.g. tools/list visibility filtering,
// admin REST paths). The slog JSON handler renders empty values as
// `"request_id":""` rather than omitting the key, which is acceptable
// for the rare no-PC paths and keeps the call site branch-free.
func correlationFields(ctx context.Context) (requestID, tool string) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return "", ""
	}
	return pc.RequestID, pc.ToolName
}

// ErrNotAJWT is the sentinel returned by JWT-based authenticators when
// the supplied credential does not have the structural shape of a JWT
// (three non-empty dot-separated segments). It is a fallthrough signal,
// not a security event: the chained authenticator recognizes it and
// silently advances to the next authenticator without logging.
var ErrNotAJWT = errors.New("not a JWT")

// LooksLikeJWT returns true when s has the structural shape of a JWT
// (three non-empty dot-separated segments). This is a cheap pre-check
// used by JWT-based authenticators to short-circuit non-JWT
// credentials (e.g. API keys) without invoking the JWT library or
// surfacing a low-level parse error to operators.
func LooksLikeJWT(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != jwtPartCount {
		return false
	}
	return !slices.Contains(parts, "")
}

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
//
// An authenticator returning ErrNotAJWT is a structural fallthrough
// (e.g. JWT authenticator handed an API key) and is NOT logged: it is
// the chain's normal "this authenticator can't handle this credential
// type, try the next one" path. Real verification failures (bad
// signature, wrong issuer, expired token, no matching API key) are
// logged at DEBUG with correlation context.
//
// lastErr tracks the most recent real (non-sentinel) error so a
// failed-everywhere chain surfaces a meaningful reason instead of the
// noisy ErrNotAJWT sentinel.
func (c *ChainedAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	var lastErr error
	reqID, tool := correlationFields(ctx)

	for i, auth := range c.authenticators {
		userInfo, err := auth.Authenticate(ctx)
		if err == nil && userInfo != nil {
			return userInfo, nil
		}
		if err == nil {
			continue
		}
		if errors.Is(err, ErrNotAJWT) {
			continue
		}
		slog.Debug("chained auth: verification failed for this authenticator, trying next",
			"index", i,
			"type", fmt.Sprintf("%T", auth),
			"error", err.Error(),
			logKeyRequestID, reqID,
			logKeyTool, tool,
		)
		lastErr = err
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
