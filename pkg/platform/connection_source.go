package platform

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/connsource"
	"github.com/txn2/mcp-data-platform/pkg/connid"
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

// addRegistryConnections populates the source map from the live toolkit registry,
// one entry per connection the toolkit serves.
//
// The key is the name a lookup arrives with — the connection name a tool call
// carries, which enrichment and the URN builder read off PlatformContext — not
// the toolkit's `instances:` key. For a toolkit configured with a
// connection_name the two differ, and keying by the instance meant every lookup
// missed and the caller silently fell back to the query-provider mapping
// (#1396). The resolver's Bound is that name, and it is the same value the
// persona boundary matches and discovery advertises, so the three cannot drift.
func (p *Platform) addRegistryConnections(m *ConnectionSourceMap) {
	mapping := p.config.Semantic.URNMapping
	for _, c := range connectionResolver(p).All("") {
		for _, src := range connsource.RegistryEntries(
			c.Kind, []string{string(c.Bound)}, mapping.Platform, mapping.CatalogMapping) {
			m.Add(src)
		}
	}
}

// connectionResolver is the one place the platform crosses between a stored
// record's instance name and the name a call binds. A free function so it does
// not become part of Platform's surface.
func connectionResolver(p *Platform) *connid.Resolver {
	return connid.NewResolver(p.toolkitRegistry.All(), p.config)
}

// addDBConnections loads connection instances from the database and adds them
// to the source map, overriding the registry entry for the same connection.
//
// A stored row is keyed by the toolkit instance it configures; the map is keyed
// by the name a call binds. The resolver translates, so an operator's stored
// datahub_source_name reaches the lookup enrichment makes even when the
// instance carries a connection_name (#1396).
func (p *Platform) addDBConnections(m *ConnectionSourceMap) {
	if p.connectionStore == nil {
		return
	}

	instances, err := p.connectionStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB connections for source map", "error", err)
		return
	}

	res := connectionResolver(p)
	for _, inst := range instances {
		src := ConnectionSourceFromInstance(inst)
		src.Name = string(res.ByInstance(inst.Kind, connid.Instance(inst.Name)).Bound)
		m.Overlay(src)
	}
}

// ConnectionSourceFromInstance builds a ConnectionSource from a DB instance.
//
// It reports only what the stored config states: a field the row does not set
// is left zero rather than filled with a kind default, so Overlay can tell
// "this connection is mapped to s3" from "this row says nothing". The backfill
// seeds a config-less row for every file-configured connection, and those rows
// must not out-rank the file (#1396).
func ConnectionSourceFromInstance(inst ConnectionInstance) ConnectionSource {
	src := ConnectionSource{
		Kind:        inst.Kind,
		Name:        inst.Name,
		Description: inst.Description,
	}

	if dsn, ok := inst.Config["datahub_source_name"].(string); ok {
		src.DataHubSourceName = dsn
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
