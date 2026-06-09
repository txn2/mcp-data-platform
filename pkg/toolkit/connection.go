// Package toolkit provides shared types for toolkit implementations and the
// platform layer. This package has zero internal dependencies to avoid import
// cycles between pkg/registry (which imports toolkit implementations) and the
// toolkit implementations themselves.
package toolkit

import "time"

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
//
// Reachability is observed passively from forwarded traffic, not from an active
// background probe: a transport error or timeout on a forwarded call marks the
// connection unreachable, and it is cleared by the next successful call (or a
// re-dial). So an idle connection that saw one transient failure can read
// unreachable until traffic resumes, even though its session is alive. A
// tool-level error (e.g. bad arguments) is NOT a transport failure and does not
// affect reachability.
type ConnectionHealth struct {
	// Reachable is true when the connection has a live session and its most
	// recent forwarded call did not end in an unrecovered transport error.
	Reachable bool
	// LastSuccessUnix is the unix-seconds time of the last successful
	// forwarded call (or the initial connect). Zero when none has succeeded.
	LastSuccessUnix int64
	// LastError is the most recent call or connect failure, empty when healthy.
	LastError string
}

// ConnectionHealthWire is the JSON wire shape for ConnectionHealth, shared by
// every operator surface (the list_connections MCP tool and the admin
// connections API) so they report identical reachability for the same
// connection by construction rather than by two copies happening to agree.
type ConnectionHealthWire struct {
	Reachable   bool   `json:"reachable"`
	LastSuccess string `json:"last_success,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// Wire renders runtime health into its JSON wire shape, formatting the last
// success time as RFC3339 UTC (omitted when no call has ever succeeded).
// Returns nil for a nil receiver so connections that do not track
// reachability omit the field entirely.
func (h *ConnectionHealth) Wire() *ConnectionHealthWire {
	if h == nil {
		return nil
	}
	w := &ConnectionHealthWire{
		Reachable: h.Reachable,
		LastError: h.LastError,
	}
	if h.LastSuccessUnix > 0 {
		w.LastSuccess = time.Unix(h.LastSuccessUnix, 0).UTC().Format(time.RFC3339)
	}
	return w
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
