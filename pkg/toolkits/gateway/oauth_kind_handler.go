package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// OAuthKindHandler adapts the MCP gateway toolkit to the unified
// connoauth flow. The admin layer's unified OAuth handler dispatches
// on the connection_kind path parameter ("mcp" for gateway
// connections) and invokes this implementation for config extraction
// and post-auth side effects.
//
// The MCP gateway's post-auth side effect is to re-add the connection
// to the running toolkit so its discovered tool surface is registered
// with the platform's MCP server. Without that step, the operator
// completes Connect, the token persists, but the platform's tool list
// still treats the connection as "needs auth" until the next process
// restart.
type OAuthKindHandler struct {
	toolkit *Toolkit
}

// NewOAuthKindHandler wires the MCP gateway toolkit into the unified
// OAuth dispatch. Returns nil when toolkit is nil so callers can
// register conditionally without a nil-check at every callsite.
func NewOAuthKindHandler(toolkit *Toolkit) *OAuthKindHandler {
	if toolkit == nil {
		return nil
	}
	return &OAuthKindHandler{toolkit: toolkit}
}

// ParseOAuthConfig validates the connection's stored config and maps
// the MCP gateway's per-kind OAuth shape into a connoauth.Config.
// Returns an error when the connection is not configured for the
// authorization_code grant — the unified handler maps that to HTTP
// 409 Conflict, matching the prior per-kind handler's response code.
func (*OAuthKindHandler) ParseOAuthConfig(connConfig map[string]any) (connoauth.Config, error) {
	cfg, err := ParseConfig(connConfig)
	if err != nil {
		return connoauth.Config{}, err
	}
	if cfg.AuthMode != AuthModeOAuth || cfg.OAuth.Grant != OAuthGrantAuthorizationCode {
		return connoauth.Config{}, errors.New("connection is not configured for authorization_code OAuth")
	}
	out := connoauth.Config{
		Grant:             cfg.OAuth.Grant,
		AuthorizationURL:  cfg.OAuth.AuthorizationURL,
		TokenURL:          cfg.OAuth.TokenURL,
		ClientID:          cfg.OAuth.ClientID,
		ClientSecret:      cfg.OAuth.ClientSecret,
		Prompt:            cfg.OAuth.Prompt,
		EndpointAuthStyle: oauth2AuthStyle(cfg.OAuth.EndpointAuthStyle),
	}
	if cfg.OAuth.Scope != "" {
		out.Scopes = splitScopeString(cfg.OAuth.Scope)
	}
	return out, nil
}

// AfterConnect rebuilds the connection on the live toolkit so tools
// register against the freshly-authorized upstream. Idempotent: if
// the connection already exists, RemoveConnection + AddConnection is
// safe (the in-memory state is replaced atomically). Errors are
// logged and returned so the admin layer can surface them — but the
// admin handler treats the error as non-fatal (token is already
// persisted; the toolkit's next reconciliation will retry).
func (h *OAuthKindHandler) AfterConnect(_ context.Context, name string, connConfig map[string]any) error { //nolint:revive // receiver h carries the toolkit reference; the kind-handler interface mandates this method
	// If the connection placeholder doesn't exist yet, seed it (the
	// platform startup adds known connections at boot; portal-created
	// connections arrive via AddConnection at edit time).
	if !h.toolkit.HasConnection(name) {
		if err := h.toolkit.AddConnection(name, connConfig); err != nil {
			return fmt.Errorf("seed connection placeholder: %w", err)
		}
		slog.Info("gateway: AfterConnect — seeded connection placeholder",
			logKeyConnection, name)
		return nil
	}
	// Rebuild: remove + re-add so the toolkit's auth round-tripper
	// picks up the freshly-persisted access token on its next call.
	// The MCP server's tool registry sees no churn — the toolkit
	// replaces the in-flight client transparently.
	if err := h.toolkit.RemoveConnection(name); err != nil {
		slog.Warn("gateway: AfterConnect — RemoveConnection failed (will still attempt AddConnection)",
			logKeyConnection, name, logKeyError, err)
	}
	if err := h.toolkit.AddConnection(name, connConfig); err != nil {
		return fmt.Errorf("rebuild connection: %w", err)
	}
	slog.Info("gateway: AfterConnect — connection rebuilt after Connect",
		logKeyConnection, name)
	return nil
}

// splitScopeString splits an OAuth scope string on whitespace. Empty
// inputs produce nil rather than a single empty element so the
// downstream authorize URL builder doesn't emit "scope=" for an
// unconfigured connection.
func splitScopeString(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' || s[i] == '\t' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
