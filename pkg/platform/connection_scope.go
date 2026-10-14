package platform

import (
	"context"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// connectionScopeFor builds the persona connection boundary the discovery
// surfaces enforce (#1108): the predicate behind search, fetch, and
// list_connections that keeps a persona from seeing the inventory of connections
// it may not reach.
//
// It returns a nil interface when no persona registry is configured, which
// leaves discovery unfiltered rather than denying everything: with no personas
// there is no boundary to honor, and a deployment that never restricted anything
// must not have its catalog blanked.
// The toolkit registry is held, not snapshotted, so a connection added through
// the admin API is resolved for the catalog references it serves without a
// restart.
func connectionScopeFor(reg *persona.Registry, toolkits *registry.Registry) knowledge.ConnectionScope {
	if reg == nil {
		return nil
	}
	return connscope.New(connscope.Deps{
		Registry: reg,
		URNConnections: func(reference string) []string {
			if toolkits == nil {
				return nil
			}
			return connscope.RefConnections(toolkits.All(), reference)
		},
	})
}

// connectionResolver is the one place the platform crosses between a stored
// record's instance name and the name a call binds. A free function so it does
// not become part of Platform's surface.
func connectionResolver(p *Platform) *connid.Resolver {
	return connid.NewResolver(p.toolkitRegistry.All(), p.config)
}

// catalogReach is the connection boundary apply_knowledge's catalog writes
// respect: the persona the call is authorized under must reach the connection
// a dataset or column reference names, the rule the portal's catalog routes
// apply to their caller. An administrator, a call with no platform context
// (the platform's own work) and a reference naming no connection pass. The
// registries are read at each call, so a persona or connection changed through
// the admin API applies to the next write.
func catalogReach(p *Platform) knowledgekit.CatalogReach {
	return func(ctx context.Context, reference string) bool {
		pc := middleware.GetPlatformContext(ctx)
		if pc == nil || pc.IsAdmin || p.personaRegistry == nil {
			return true
		}
		names := connscope.RefConnections(p.toolkitRegistry.All(), reference)
		if len(names) == 0 {
			return true
		}
		scope := connscope.New(connscope.Deps{Registry: p.personaRegistry})
		return slices.ContainsFunc(names, func(n string) bool { return scope.AllowConnection(pc.PersonaName, n) })
	}
}
