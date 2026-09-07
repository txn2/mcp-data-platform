package graphql

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// OAuthKindHandler adapts this kind onto the platform's unified
// connection-OAuth flow, so a GraphQL connection using the
// authorization_code grant is connected, reconnected and revoked
// through the same admin surface and the same token store as every
// other kind. Registered at startup in the platform's HTTP wiring.
type OAuthKindHandler struct {
	tk *Toolkit
}

// NewOAuthKindHandler builds the handler for a toolkit.
func NewOAuthKindHandler(tk *Toolkit) *OAuthKindHandler {
	return &OAuthKindHandler{tk: tk}
}

// ParseOAuthConfig validates a connection's stored config and maps its
// OAuth settings into a connoauth.Config. It reports an error when the
// connection is not configured for the authorization_code grant, which
// the unified handler renders as HTTP 409.
//
// The mapping is delegated to the shared seam's ConnOAuthConfig so the
// initial code exchange here and the per-call silent refresh in the
// authenticator read every field through one translator.
func (*OAuthKindHandler) ParseOAuthConfig(connConfig map[string]any) (connoauth.Config, error) {
	cfg, err := ParseConfig(connConfig)
	if err != nil {
		return connoauth.Config{}, err
	}
	if !cfg.IsOAuthAuthorizationCode() {
		return connoauth.Config{}, errors.New("connection is not configured for authorization_code OAuth")
	}
	return cfg.upstream().ConnOAuthConfig(), nil
}

// AfterConnect reads the connection's schema now that a credential
// exists. A GraphQL endpoint behind an authorization_code grant refuses
// introspection until the flow completes, so the connection registered
// with no schema; this is the first moment it can have one.
func (h *OAuthKindHandler) AfterConnect(ctx context.Context, name string, _ map[string]any) error {
	if h.tk == nil || !h.tk.HasConnection(name) {
		return nil
	}
	//nolint:wrapcheck // already an operator-facing "graphql: ..." message
	return h.tk.RefreshSchema(ctx, name)
}
