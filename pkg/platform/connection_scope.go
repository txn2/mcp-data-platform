package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/platform/connsource"
	"github.com/txn2/mcp-data-platform/pkg/connview"
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
			return connectionNamesForURN(sources, toolkits.All(), urn)
		},
	})
}

// connectionNamesForURN returns the names of the connections that could serve a
// catalog URN, empty when none can be determined. It is the one resolution
// shared by the enrichment middleware (which tells an agent which connections
// can query a dataset) and the discovery connection boundary (which decides
// whether the caller's persona reaches any of them).
//
// It walks the LIVE connections rather than the source map's entries, because
// the two are not the same set: the map keys a file-configured multi-connection
// toolkit by its instance, while a persona's rules and a tool call's
// `connection` argument name each connection that toolkit serves. Resolving
// through the map alone would attribute a dataset to a name no persona grants —
// and would miss a connection added after startup. Each connection's platform
// comes from its own map entry when it has one (the DB-backed case, where a
// connection may override its DataHub source name) and otherwise from its
// toolkit's; a connection with neither is not a candidate, which leaves the URN
// unattributable rather than attributed by guess.
func connectionNamesForURN(sources *ConnectionSourceMap, toolkits []registry.Toolkit, urn string) []string {
	platform := connsource.PlatformFromURN(urn)
	if platform == "" || sources == nil {
		return nil
	}
	var names []string
	for _, tk := range toolkits {
		for _, name := range connview.ConnectionNames(tk) {
			if platformForConnection(sources, tk, name) == platform {
				names = append(names, name)
			}
		}
	}
	return names
}

// platformForConnection resolves the DataHub platform a connection's datasets
// carry: its own mapping when the deployment records one, else its toolkit's.
// Empty means unknown, which keeps the connection out of every URN's candidate
// set.
func platformForConnection(sources *ConnectionSourceMap, tk registry.Toolkit, connection string) string {
	if s := sources.ForConnection(tk.Kind(), connection); s != nil {
		return s.DataHubSourceName
	}
	if s := sources.ForConnection(tk.Kind(), tk.Name()); s != nil {
		return s.DataHubSourceName
	}
	return ""
}
