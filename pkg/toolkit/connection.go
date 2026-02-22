// Package toolkit provides shared types for toolkit implementations and the
// platform layer. This package has zero internal dependencies to avoid import
// cycles between pkg/registry (which imports toolkit implementations) and the
// toolkit implementations themselves.
package toolkit

// ConnectionDetail provides information about a single connection within a toolkit.
type ConnectionDetail struct {
	Name        string
	Description string
	IsDefault   bool
}

// ConnectionLister is an optional interface for toolkits that manage multiple
// connections internally. Toolkits implementing this interface expose all their
// connections for discovery via the list_connections tool.
type ConnectionLister interface {
	ListConnections() []ConnectionDetail
}
