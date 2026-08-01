package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/connoauthapi"
)

// OAuthKindHandler adapts a kind-specific connection config to the shared
// connoauth flow. The platform registers one per kind (MCP gateway, HTTP API
// gateway, future kinds) at startup. Aliased to the seam's declaration rather
// than restated so the two cannot drift.
//
// New connection kinds add support by implementing this interface (typically
// in their toolkit package) and registering it in Deps.OAuthKinds — they do
// NOT add a parallel handler file or a parallel token store.
type OAuthKindHandler = connoauthapi.OAuthKindHandler

// OAuthKindHandlers is the registry shape passed via Deps, keyed by the kind
// string (e.g. connoauth.KindMCP). Keep keys aligned with the
// connection_kind values stored in connection_instances and
// connection_oauth_tokens.
type OAuthKindHandlers = connoauthapi.OAuthKindHandlers

// registerConnectionOAuthRoutes mounts the connection OAuth surface,
// implemented in the connoauthapi subpackage. The seam owns the choice
// between the unified per-kind surface and the two legacy per-kind ones,
// because that choice is a property of those routes rather than of the admin
// API as a whole.
func (h *Handler) registerConnectionOAuthRoutes() {
	connoauthapi.Register(h.mux, h.publicMux, connoauthapi.Config{
		Connections:    h.deps.ConnectionStore,
		Tokens:         h.deps.ConnOAuthStore,
		Kinds:          h.deps.OAuthKinds,
		PKCEStore:      h.deps.PKCEStore,
		AuthEvents:     h.deps.AuthEvents,
		AuthEventStore: h.deps.AuthEventStore,
		GatewayToolkit: h.findGatewayToolkit,
		Mutable:        h.isMutable(),
		Author:         authorEmailOrID,
		Decode:         decodeStrict,
		DecodeOptional: decodeStrictOptional,
	})
}
