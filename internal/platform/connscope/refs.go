package connscope

import (
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// RefConnections returns the name a persona's connection rules match for the
// connection a platform catalog reference names (a dataset, a column, a
// schema document), or nil for a reference that names no connection. It is
// the one resolution shared by the enrichment middleware (which tells an agent
// which connection can query a dataset) and the discovery boundary (which
// decides whether the caller's persona reaches it).
//
// The live connections are what it resolves against, so a connection added
// through the admin API is resolved without a restart.
func RefConnections(toolkits []registry.Toolkit, reference string) []string {
	ref, err := catalog.ParseRef(reference)
	if err != nil || ref.Connection == "" {
		return nil
	}
	c := connid.NewResolver(toolkits, nil).ByInstance(ref.ConnectionKind, connid.Instance(ref.Connection))
	return []string{string(c.Bound)}
}
