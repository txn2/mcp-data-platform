// Package connsource maps connections to their DataHub URN components (platform
// name and catalog mapping), with forward and reverse lookups. It is extracted
// from the platform facade so that package stays within its size budget; the
// platform re-exports Source and Map as type aliases for its existing callers.
package connsource

import "strings"

// Source holds the DataHub mapping for a single connection.
type Source struct {
	// Kind is the toolkit kind (trino, s3).
	Kind string `json:"kind"`

	// Name is the connection name.
	Name string `json:"name"`

	// DataHubSourceName is the platform identifier in DataHub URNs
	// (e.g. "trino", "postgres", "s3"). Multiple connections can share the same
	// source name.
	DataHubSourceName string `json:"datahub_source_name"`

	// CatalogMapping maps connection catalog names to DataHub catalog names.
	// For example: {"rdbms": "postgres"} means the connection's "rdbms" catalog
	// corresponds to "postgres" in DataHub URNs.
	CatalogMapping map[string]string `json:"catalog_mapping,omitempty"`

	// Description is the human-readable connection description.
	Description string `json:"description,omitempty"`
}

// Map provides forward and reverse lookups between connections and DataHub URN
// components.
type Map struct {
	// byConnection maps "kind/name" to its DataHub source info.
	byConnection map[string]*Source

	// bySourceName maps DataHub source name to all connections that use it.
	bySourceName map[string][]*Source
}

// NewMap creates an empty source map.
func NewMap() *Map {
	return &Map{
		byConnection: make(map[string]*Source),
		bySourceName: make(map[string][]*Source),
	}
}

// Add registers a connection's DataHub source mapping.
// If the same connection (kind+name) already exists, the old entry is
// replaced so that bySourceName never contains duplicates.
func (m *Map) Add(src Source) {
	key := src.Kind + "/" + src.Name
	if _, exists := m.byConnection[key]; exists {
		m.Remove(src.Kind, src.Name)
	}
	m.byConnection[key] = &src
	m.bySourceName[src.DataHubSourceName] = append(m.bySourceName[src.DataHubSourceName], &src)
}

// Remove deletes a connection's DataHub source mapping.
func (m *Map) Remove(kind, name string) {
	key := kind + "/" + name
	src, ok := m.byConnection[key]
	if !ok {
		return
	}

	delete(m.byConnection, key)

	// Remove from the bySourceName slice.
	dsn := src.DataHubSourceName
	entries := m.bySourceName[dsn]
	for i, e := range entries {
		if e.Kind == kind && e.Name == name {
			m.bySourceName[dsn] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(m.bySourceName[dsn]) == 0 {
		delete(m.bySourceName, dsn)
	}
}

// ForConnection returns the DataHub source info for a connection.
// Returns nil if the connection has no mapping.
//
// Kind and name together identify a connection, and both are required because a
// deployment may legitimately carry one name across several kinds. A lookup by
// name alone used to answer with whichever entry the map iteration reached
// first, which put an s3 platform on a Trino warehouse table's URN and returned
// a different platform on the next call (#1384). Every caller reads the kind off
// the audit event or PlatformContext it reads the name from, so this map is not
// the place to guess one.
func (m *Map) ForConnection(kind, name string) *Source {
	if m == nil {
		return nil
	}
	return m.byConnection[kind+"/"+name]
}

// DataHubSourceName returns the DataHub source name mapped to a connection, or ""
// when none; it adapts the map to the connview.SourceResolver capability.
func (m *Map) DataHubSourceName(kind, name string) string {
	if s := m.ForConnection(kind, name); s != nil {
		return s.DataHubSourceName
	}
	return ""
}

// ConnectionsForSource returns all connections that map to the given DataHub
// source name (e.g. "trino" returns all Trino connections).
func (m *Map) ConnectionsForSource(datahubSourceName string) []*Source {
	if m == nil {
		return nil
	}
	return m.bySourceName[datahubSourceName]
}

// ConnectionsForURN parses a DataHub URN and returns all connections whose
// source name matches the URN's platform. Returns nil if the URN can't be parsed.
func (m *Map) ConnectionsForURN(urn string) []*Source {
	if m == nil {
		return nil
	}
	platform := PlatformFromURN(urn)
	if platform == "" {
		return nil
	}
	return m.bySourceName[platform]
}

// PlatformFromURN extracts the platform name from a DataHub URN.
// Example: "urn:li:dataset:(urn:li:dataPlatform:trino,...)" returns "trino".
// Exported so callers that resolve a URN against the live connection set (rather
// than this map's entries) parse the platform the same way.
func PlatformFromURN(urn string) string {
	const prefix = "urn:li:dataPlatform:"
	_, after, found := strings.Cut(urn, prefix)
	if !found {
		return ""
	}
	rest := after
	// Platform name ends at comma or closing paren
	for i, c := range rest {
		if c == ',' || c == ')' {
			return rest[:i]
		}
	}
	return rest
}
