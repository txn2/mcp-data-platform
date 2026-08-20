// Package connsource maps connections to their DataHub URN components (platform
// name and catalog mapping), with forward and reverse lookups. It is extracted
// from the platform facade so that package stays within its size budget; the
// platform re-exports Source and Map as type aliases for its existing callers.
package connsource

import (
	"strings"
	"sync"
)

// keySep joins a connection's kind and name into its map key.
const keySep = "/"

// The toolkit kinds whose connections carry a DataHub platform.
const (
	kindTrino   = "trino"
	kindS3      = "s3"
	kindDataHub = "datahub"
)

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
//
// One map is shared for the life of the process: tool-call goroutines read it
// on every enrichment and URN build, while the admin connection routes add,
// overlay and remove entries from HTTP goroutines. Go maps are not safe for
// concurrent read and write — that pair is a fatal runtime error, not a
// recoverable one — so every accessor takes the lock, and the readers that
// answer with a slice answer with their own copy of it.
type Map struct {
	mu sync.RWMutex

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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addLocked(src)
}

// addLocked registers a mapping. The caller holds the write lock.
func (m *Map) addLocked(src Source) {
	k := key(src.Kind, src.Name)
	if _, exists := m.byConnection[k]; exists {
		m.removeLocked(src.Kind, src.Name)
	}
	m.byConnection[k] = &src
	m.bySourceName[src.DataHubSourceName] = append(m.bySourceName[src.DataHubSourceName], &src)
}

// key is the map key a connection is filed under.
func key(kind, name string) string { return kind + keySep + name }

// Overlay files a stored connection's mapping over the entry the map holds for
// the same connection: a field the stored config leaves unset keeps the value
// already there, and the kind's default answers only when nothing else did.
//
// The entry it overlays MUST be the one the running configuration derives —
// call Seed first, or overlay onto a map the registry arm has just populated.
// Two behaviors depend on that. Replacing the entry outright would lose the
// deployment's urn_mapping on every boot after the first, because the
// connection backfill seeds a config-less row for each file-configured
// connection and a row with nothing to say must not out-rank the file. And
// overlaying onto a previous overlay would make a field unclearable: an
// operator who removes datahub_source_name from a stored connection would keep
// reading the value they deleted until the next restart (#1396).
func (m *Map) Overlay(src Source) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if base := m.byConnection[key(src.Kind, src.Name)]; base != nil {
		if src.DataHubSourceName == "" {
			src.DataHubSourceName = base.DataHubSourceName
		}
		if src.CatalogMapping == nil {
			src.CatalogMapping = base.CatalogMapping
		}
		if src.Description == "" {
			src.Description = base.Description
		}
	}
	if src.DataHubSourceName == "" {
		src.DataHubSourceName = DefaultSourceName(src.Kind)
	}
	m.addLocked(src)
}

// Seed files the entry the running configuration derives for one connection,
// replacing whatever the map held. It is what an Overlay must land on and what
// a deleted stored override falls back to, so a connection is never left
// carrying a value no configuration states.
func (m *Map) Seed(entries []Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, src := range entries {
		m.addLocked(src)
	}
}

// DefaultSourceName returns the DataHub platform a kind's datasets carry when
// nothing overrides it. An unmapped kind returns "", which leaves its
// connections out of every URN's candidate set rather than in the wrong one.
func DefaultSourceName(kind string) string {
	switch kind {
	case kindTrino, kindS3, kindDataHub:
		return kind
	default:
		return ""
	}
}

// RegistryEntries returns the source entries a live toolkit of the given kind
// contributes: one per connection name it serves, all sharing the kind's
// mapping. urnPlatform and catalogMapping are the deployment's semantic
// urn_mapping, which only the query engine's kind carries. A kind that names no
// DataHub platform contributes nothing.
//
// It is the one derivation of "what does the running configuration say about
// this connection", so the startup build and the admin path that has to restore
// it after a stored override is deleted cannot answer differently (#1396).
func RegistryEntries(kind string, names []string, urnPlatform string, catalogMapping map[string]string) []Source {
	src := Source{Kind: kind, DataHubSourceName: DefaultSourceName(kind)}
	if src.DataHubSourceName == "" {
		return nil
	}
	if kind == kindTrino {
		if urnPlatform != "" {
			src.DataHubSourceName = urnPlatform
		}
		src.CatalogMapping = catalogMapping
	}

	out := make([]Source, 0, len(names))
	for _, name := range names {
		src.Name = name
		out = append(out, src)
	}
	return out
}

// Remove deletes a connection's DataHub source mapping.
func (m *Map) Remove(kind, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(kind, name)
}

// removeLocked deletes a mapping. The caller holds the write lock.
func (m *Map) removeLocked(kind, name string) {
	k := key(kind, name)
	src, ok := m.byConnection[k]
	if !ok {
		return
	}

	delete(m.byConnection, k)

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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byConnection[key(kind, name)]
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sourcesFor(datahubSourceName)
}

// sourcesFor copies the entries filed under a source name, so a caller iterating
// the result cannot be racing an Add that appends to the stored slice. The
// caller holds the read lock. A source name with no entries answers nil.
func (m *Map) sourcesFor(datahubSourceName string) []*Source {
	entries := m.bySourceName[datahubSourceName]
	if len(entries) == 0 {
		return nil
	}
	out := make([]*Source, len(entries))
	copy(out, entries)
	return out
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sourcesFor(platform)
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
