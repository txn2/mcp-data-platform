package platform

import (
	"context"
	"log/slog"
	"strings"
)

// ConnectionSource holds the DataHub mapping for a single connection.
type ConnectionSource struct {
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

// ConnectionSourceMap provides forward and reverse lookups between connections
// and DataHub URN components.
type ConnectionSourceMap struct {
	// byConnection maps "kind/name" to its DataHub source info.
	byConnection map[string]*ConnectionSource

	// bySourceName maps DataHub source name to all connections that use it.
	bySourceName map[string][]*ConnectionSource
}

// NewConnectionSourceMap creates an empty source map.
func NewConnectionSourceMap() *ConnectionSourceMap {
	return &ConnectionSourceMap{
		byConnection: make(map[string]*ConnectionSource),
		bySourceName: make(map[string][]*ConnectionSource),
	}
}

// Add registers a connection's DataHub source mapping.
// If the same connection (kind+name) already exists, the old entry is
// replaced so that bySourceName never contains duplicates.
func (m *ConnectionSourceMap) Add(src ConnectionSource) {
	key := src.Kind + "/" + src.Name
	if _, exists := m.byConnection[key]; exists {
		m.Remove(src.Kind, src.Name)
	}
	m.byConnection[key] = &src
	m.bySourceName[src.DataHubSourceName] = append(m.bySourceName[src.DataHubSourceName], &src)
}

// Remove deletes a connection's DataHub source mapping.
func (m *ConnectionSourceMap) Remove(kind, name string) {
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
func (m *ConnectionSourceMap) ForConnection(kind, name string) *ConnectionSource {
	if m == nil {
		return nil
	}
	return m.byConnection[kind+"/"+name]
}

// ForConnectionName returns the DataHub source info by connection name only.
// Searches all kinds. Returns nil if not found.
func (m *ConnectionSourceMap) ForConnectionName(name string) *ConnectionSource {
	if m == nil {
		return nil
	}
	for _, src := range m.byConnection {
		if src.Name == name {
			return src
		}
	}
	return nil
}

// ConnectionsForSource returns all connections that map to the given DataHub
// source name (e.g. "trino" returns all Trino connections).
func (m *ConnectionSourceMap) ConnectionsForSource(datahubSourceName string) []*ConnectionSource {
	if m == nil {
		return nil
	}
	return m.bySourceName[datahubSourceName]
}

// ConnectionsForURN parses a DataHub URN and returns all connections whose
// source name matches the URN's platform. Returns nil if the URN can't be parsed.
func (m *ConnectionSourceMap) ConnectionsForURN(urn string) []*ConnectionSource {
	if m == nil {
		return nil
	}
	platform := extractPlatformFromURN(urn)
	if platform == "" {
		return nil
	}
	return m.bySourceName[platform]
}

// extractPlatformFromURN extracts the platform name from a DataHub URN.
// Example: "urn:li:dataset:(urn:li:dataPlatform:trino,...)" returns "trino".
func extractPlatformFromURN(urn string) string {
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

// buildConnectionSourceMap constructs the source map from the toolkit registry
// and DB connection instances.
func (p *Platform) buildConnectionSourceMap() *ConnectionSourceMap {
	m := NewConnectionSourceMap()
	p.addRegistryConnections(m)
	p.addDBConnections(m)
	return m
}

// addRegistryConnections populates the source map from the live toolkit registry.
func (p *Platform) addRegistryConnections(m *ConnectionSourceMap) {
	for _, tk := range p.toolkitRegistry.All() {
		kind := tk.Kind()
		src := ConnectionSource{
			Kind: kind,
			Name: tk.Name(),
		}

		switch kind {
		case kindTrino:
			src.DataHubSourceName = p.config.Semantic.URNMapping.Platform
			if src.DataHubSourceName == "" {
				src.DataHubSourceName = kindTrino
			}
			src.CatalogMapping = p.config.Semantic.URNMapping.CatalogMapping
		case kindS3:
			src.DataHubSourceName = kindS3
		case kindDataHub:
			src.DataHubSourceName = kindDataHub
		default:
			continue
		}

		m.Add(src)
	}
}

// addDBConnections loads connection instances from the database and adds them
// to the source map, overriding any registry entries with the same key.
func (p *Platform) addDBConnections(m *ConnectionSourceMap) {
	if p.connectionStore == nil {
		return
	}

	instances, err := p.connectionStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB connections for source map", "error", err)
		return
	}

	for _, inst := range instances {
		m.Add(ConnectionSourceFromInstance(inst))
	}
}

// ConnectionSourceFromInstance builds a ConnectionSource from a DB instance.
func ConnectionSourceFromInstance(inst ConnectionInstance) ConnectionSource {
	src := ConnectionSource{
		Kind:        inst.Kind,
		Name:        inst.Name,
		Description: inst.Description,
	}

	if dsn, ok := inst.Config["datahub_source_name"].(string); ok && dsn != "" {
		src.DataHubSourceName = dsn
	} else {
		src.DataHubSourceName = defaultSourceNameForKind(inst.Kind)
	}

	if cm, ok := inst.Config["catalog_mapping"].(map[string]any); ok {
		src.CatalogMapping = make(map[string]string, len(cm))
		for k, v := range cm {
			if vs, ok := v.(string); ok {
				src.CatalogMapping[k] = vs
			}
		}
	}

	return src
}

// defaultSourceNameForKind returns the default DataHub source name for a toolkit kind.
func defaultSourceNameForKind(kind string) string {
	switch kind {
	case kindTrino:
		return kindTrino
	case kindS3:
		return kindS3
	default:
		return ""
	}
}
