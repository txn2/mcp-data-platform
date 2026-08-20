package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/platform/connsource"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
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
// the admin API is a candidate for the URNs it can serve without a restart.
func connectionScopeFor(reg *persona.Registry, sources *ConnectionSourceMap, toolkits *registry.Registry) knowledge.ConnectionScope {
	if reg == nil {
		return nil
	}
	return connscope.New(connscope.Deps{
		Registry: reg,
		URNConnections: func(urn string) []string {
			if toolkits == nil {
				return nil
			}
			return connsource.ConnectionNamesForURN(sources, toolkits.All(), urn)
		},
	})
}
