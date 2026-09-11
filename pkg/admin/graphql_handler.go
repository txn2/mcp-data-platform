package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/graphqlapi"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// registerGraphQLRoutes mounts the GraphQL connection schema surface,
// implemented in the graphqlapi subpackage: reading the schema state,
// re-reading it from the endpoint, and accepting one an operator
// supplies for an endpoint that disables introspection.
func (h *Handler) registerGraphQLRoutes() {
	graphqlapi.Register(h.mux, graphqlapi.Config{
		Toolkits:     h.liveToolkits,
		Mutable:      h.isMutable(),
		SchemaStored: h.publishSchemaReload,
	})
}

// publishSchemaReload announces a replaced stored schema to peer replicas,
// which install it from the store (#1676). Nil-safe on a deployment with no
// reload bus.
func (h *Handler) publishSchemaReload(kind, name string) {
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishConnectionReload(kind, name, platform.ReloadSchema)
	}
}

// liveToolkits returns the registered toolkits, or nil when no registry
// was wired. The seam takes a function rather than a snapshot so a
// connection added after startup is reachable without a restart.
func (h *Handler) liveToolkits() []registry.Toolkit {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	return h.deps.ToolkitRegistry.All()
}
