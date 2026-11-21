package connscope

import (
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// ForRegistry builds the persona connection boundary the discovery
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
func ForRegistry(reg *persona.Registry, toolkits *registry.Registry) knowledge.ConnectionScope {
	if reg == nil {
		return nil
	}
	return New(Deps{
		Registry: reg,
		URNConnections: func(reference string) []string {
			if toolkits == nil {
				return nil
			}
			return RefConnections(toolkits.All(), reference)
		},
	})
}
