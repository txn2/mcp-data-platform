package connsource

import (
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// ConnectionNamesForURN returns the names of the connections that could serve a
// catalog URN, empty when none can be determined. It is the one resolution
// shared by the enrichment middleware (which tells an agent which connections
// can query a dataset) and the discovery connection boundary (which decides
// whether the caller's persona reaches any of them).
//
// It walks the LIVE connections rather than the source map's entries, because
// the two are not the same set: the map is built once at startup, so it does
// not hold a connection added to a toolkit after it. Each connection's platform
// comes from its own map entry when it has one and otherwise from its toolkit's
// instance entry; a connection with neither is not a candidate, which leaves
// the URN unattributable rather than attributed by guess.
func ConnectionNamesForURN(sources *Map, toolkits []registry.Toolkit, urn string) []string {
	platform := PlatformFromURN(urn)
	if platform == "" || sources == nil {
		return nil
	}
	var names []string
	for _, c := range connid.NewResolver(toolkits, nil).All("") {
		if platformForConnection(sources, c) == platform {
			names = append(names, string(c.Bound))
		}
	}
	return names
}

// platformForConnection resolves the DataHub platform a connection's datasets
// carry: its own mapping when the deployment records one, else its toolkit's.
// Empty means unknown, which keeps the connection out of every URN's candidate
// set.
//
// The two lookups are into two different key spaces of the same map. The first
// is the name a call binds, which is what addRegistryConnections and the admin
// seed file. The second is the toolkit's own name, which names no connection at
// all for a multi-connection toolkit and is the per-kind entry a deployment
// configures once for all of them.
func platformForConnection(sources *Map, c connid.Connection) string {
	if s := sources.ForConnection(c.Kind, string(c.Bound)); s != nil {
		return s.DataHubSourceName
	}
	if s := sources.ForConnection(c.Kind, c.Toolkit); s != nil {
		return s.DataHubSourceName
	}
	return ""
}
