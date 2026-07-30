// Package connoauthapi serves the admin OAuth-to-upstream surface: starting
// an authorization-code flow for a connection, receiving the IdP redirect,
// reporting per-connection token health, and listing the durable auth-event
// history. It is a decomposition seam of pkg/admin (which sat at the package
// size budget); the parent registers it on the admin mux and injects the
// request-scoped helpers it shares with the other admin routes.
//
// Three route families live here. The unified surface
// (/api/v1/admin/connections/{kind}/...) dispatches on the kind path value
// through a registry of OAuthKindHandler, and is the path every new
// connection kind takes. The two legacy per-kind surfaces (MCP gateway and
// HTTP API gateway) remain for deployments whose IdP client configuration
// still names their callback URLs; Register picks between them exactly as the
// parent did before the move.
package connoauthapi

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// Structured log field names, matching the keys the parent admin package
// emits so both sides of the split produce one field vocabulary.
const (
	logKeyError = "error"
	logKeyName  = "name"
	logKeyKind  = "kind"
)

// pathKeyName is the {name} path placeholder on the per-connection routes.
const pathKeyName = "name"

// connectionKindMCP is the kind discriminator for MCP gateway connections,
// the default the legacy callback attributes a pending flow to.
const connectionKindMCP = "mcp"

// ConnectionReader is the read side of the connection store. These routes
// never write a connection instance — they persist tokens through Tokens
// instead — so they take the narrower interface.
type ConnectionReader interface {
	List(ctx context.Context) ([]platform.ConnectionInstance, error)
	Get(ctx context.Context, kind, name string) (*platform.ConnectionInstance, error)
}

// Config carries the stores and parent-owned helpers these routes need.
type Config struct {
	// Connections reads the connection instance whose OAuth settings drive
	// the flow. nil disables every route in this package.
	Connections ConnectionReader
	// Tokens persists OAuth tokens for every connection kind in one shared
	// table. nil selects the legacy per-kind routes below.
	Tokens connoauth.Store
	// Kinds maps connection kind to its config extractor and post-auth
	// hook. Empty selects the legacy per-kind routes below.
	Kinds OAuthKindHandlers
	// PKCEStore holds in-flight authorization_code+PKCE state between
	// oauth-start and the callback. Required: oauth-start answers 503 when
	// nil.
	PKCEStore pkcestore.Store
	// AuthEvents writes the durable per-connection OAuth-lifecycle audit
	// trail. nil disables event writes; the endpoints still work.
	AuthEvents *authevents.Writer
	// AuthEventStore is the read surface behind the History endpoint. Kept
	// distinct from AuthEvents because writes are best-effort while reads
	// need a real implementation to return 200 rather than an empty list.
	AuthEventStore authevents.Store
	// GatewayToolkit returns the live MCP gateway toolkit, so a completed
	// legacy flow can re-add the connection and register its tools. Returns
	// nil when no gateway toolkit is registered.
	GatewayToolkit func() *gatewaykit.Toolkit
	// Mutable reports database config mode; false registers nothing, since
	// every route here mutates stored credentials.
	Mutable bool
	// Author resolves the acting admin from the request context for audit
	// attribution and auth-event actors.
	Author func(ctx context.Context) string
	// Decode is the parent's strict JSON body decoder (unknown fields
	// rejected, size-capped).
	Decode func(w http.ResponseWriter, r *http.Request, dst any) error
	// DecodeOptional is Decode for endpoints whose body is optional (an
	// OAuth start with an optional return_url), treating an empty body as
	// success.
	DecodeOptional func(w http.ResponseWriter, r *http.Request, dst any) error
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the OAuth routes on mux, and the IdP redirect targets on
// publicMux — the callback carries PKCE state rather than an admin session,
// which is what authenticates it, so it must not sit behind the admin auth
// middleware.
//
// The unified surface activates once the platform has wired the shared token
// store and at least one kind handler; otherwise the legacy per-kind routes
// register so deployments mid-rollout keep working. This is the branch the
// parent used to make in registerRoutes.
func Register(mux, publicMux *http.ServeMux, cfg Config) {
	if !cfg.Mutable || cfg.Connections == nil {
		return
	}
	h := &handler{cfg: cfg}
	if cfg.Tokens != nil && len(cfg.Kinds) > 0 {
		h.registerConnectionOAuthRoutes(mux, publicMux)
		return
	}
	h.registerGatewayOAuthRoutes(mux, publicMux)
	h.registerAPIGatewayOAuthRoutes(mux, publicMux)
}
