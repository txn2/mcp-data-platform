// Package toolkit provides shared types for toolkit implementations and the
// platform layer. This package has zero internal dependencies to avoid import
// cycles between pkg/registry (which imports toolkit implementations) and the
// toolkit implementations themselves.
package toolkit

// ConnectionDetail provides information about a single connection within a toolkit.
//
// CatalogID and OperationCount are optional and only populated by toolkits
// where they have meaning (today: apigateway). They exist on this shared
// struct so list_connections can surface what's actually bound at runtime
// rather than only what's stored in the DB. The two disagreed in the
// past when a config update reached the store but never reached the
// in-memory toolkit, and the missing fields here made the divergence
// invisible from the MCP surface.
type ConnectionDetail struct {
	Name           string
	Description    string
	IsDefault      bool
	CatalogID      string
	OperationCount int
	// Health is optional per-connection reachability, populated by gateway
	// kinds that hold a live upstream session (so an evicted or dead upstream
	// is observable from list_connections instead of only when a downstream
	// tool call fails). Nil for toolkits that do not track reachability.
	Health *ConnectionHealth
}

// ConnectionHealth is the runtime reachability of a gateway upstream.
type ConnectionHealth struct {
	// Reachable is true when the connection has a live session and its most
	// recent interaction did not end in an unrecovered error.
	Reachable bool
	// LastSuccessUnix is the unix-seconds time of the last successful
	// forwarded call (or the initial connect). Zero when none has succeeded.
	LastSuccessUnix int64
	// LastError is the most recent call or connect failure, empty when healthy.
	LastError string
}

// ConnectionLister is an optional interface for toolkits that manage multiple
// connections internally. Toolkits implementing this interface expose all their
// connections for discovery via the list_connections tool.
type ConnectionLister interface {
	ListConnections() []ConnectionDetail
}

// ConnectionManager is an optional interface for toolkits that support adding
// and removing backend connections at runtime without restart. Used by the admin
// API to make DB-managed connections live immediately.
type ConnectionManager interface {
	AddConnection(name string, config map[string]any) error
	RemoveConnection(name string) error
	HasConnection(name string) bool
}
