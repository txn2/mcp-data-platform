package platform

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/connsource"
)

// ConnectionSource is re-exported from the connsource package (extracted for the
// package-size budget) so existing platform callers keep the name.
type ConnectionSource = connsource.Source

// ConnectionSourceMap is re-exported from the connsource package; existing
// callers keep the name and NewConnectionSourceMap constructs one.
type ConnectionSourceMap = connsource.Map

// NewConnectionSourceMap creates an empty connection source map.
func NewConnectionSourceMap() *ConnectionSourceMap { return connsource.NewMap() }

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
