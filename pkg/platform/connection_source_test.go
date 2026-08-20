package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connsource"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

func TestConnectionSourceMap_AddAndForConnection(t *testing.T) {
	m := NewConnectionSourceMap()

	src := ConnectionSource{
		Kind:              "trino",
		Name:              "prod",
		DataHubSourceName: "trino",
		CatalogMapping:    map[string]string{"rdbms": "postgres"},
	}
	m.Add(src)

	got := m.ForConnection("trino", "prod")
	require.NotNil(t, got)
	assert.Equal(t, "trino", got.DataHubSourceName)
	assert.Equal(t, "postgres", got.CatalogMapping["rdbms"])

	// Non-existent connection returns nil.
	assert.Nil(t, m.ForConnection("trino", "missing"))
	assert.Nil(t, m.ForConnection("s3", "prod"))
}

// TestConnectionSourceMap_SharedNameResolvesByKind proves that one name carried
// by three kinds resolves to the connection meant, repeatably (#1384). The
// alias is re-exported to platform callers, so the guarantee is asserted on the
// surface they use.
func TestConnectionSourceMap_SharedNameResolvesByKind(t *testing.T) {
	m := NewConnectionSourceMap()
	for _, kind := range []string{"trino", "datahub", "s3"} {
		m.Add(ConnectionSource{Kind: kind, Name: "acme", DataHubSourceName: kind})
	}

	for range 20 {
		for _, kind := range []string{"trino", "datahub", "s3"} {
			got := m.ForConnection(kind, "acme")
			require.NotNil(t, got)
			assert.Equal(t, kind, got.DataHubSourceName)
		}
	}
	assert.Nil(t, m.ForConnection("trino", "missing"))
}

func TestConnectionSourceMap_ConnectionsForSource(t *testing.T) {
	m := NewConnectionSourceMap()
	m.Add(ConnectionSource{Kind: "trino", Name: "prod", DataHubSourceName: "trino"})
	m.Add(ConnectionSource{Kind: "trino", Name: "staging", DataHubSourceName: "trino"})
	m.Add(ConnectionSource{Kind: "s3", Name: "lake", DataHubSourceName: "s3"})

	trinoCons := m.ConnectionsForSource("trino")
	assert.Len(t, trinoCons, 2)
	assert.Equal(t, "prod", trinoCons[0].Name)
	assert.Equal(t, "staging", trinoCons[1].Name)

	s3Cons := m.ConnectionsForSource("s3")
	assert.Len(t, s3Cons, 1)

	// Non-existent source returns nil.
	assert.Nil(t, m.ConnectionsForSource("unknown"))
}

func TestConnectionSourceMap_ConnectionsForURN(t *testing.T) {
	m := NewConnectionSourceMap()
	m.Add(ConnectionSource{Kind: "trino", Name: "prod", DataHubSourceName: "trino"})

	conns := m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)")
	assert.Len(t, conns, 1)
	assert.Equal(t, "prod", conns[0].Name)

	// Non-matching platform returns nil.
	assert.Nil(t, m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:postgres,catalog.schema.table,PROD)"))

	// Invalid URN returns nil.
	assert.Nil(t, m.ConnectionsForURN("not-a-urn"))
}

func TestConnectionSourceFromInstance(t *testing.T) {
	t.Run("with datahub_source_name and catalog_mapping", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:        "trino",
			Name:        "prod",
			Description: "Production Trino",
			Config: map[string]any{
				"host":                "trino.local",
				"datahub_source_name": "custom_trino",
				"catalog_mapping": map[string]any{
					"rdbms": "postgres",
					"hive":  "hdfs",
				},
			},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Equal(t, "trino", src.Kind)
		assert.Equal(t, "prod", src.Name)
		assert.Equal(t, "custom_trino", src.DataHubSourceName)
		assert.Equal(t, "Production Trino", src.Description)
		assert.Equal(t, "postgres", src.CatalogMapping["rdbms"])
		assert.Equal(t, "hdfs", src.CatalogMapping["hive"])
	})

	// A row that states no source name reports none, so Overlay can tell it
	// apart from one that states the kind default and must not out-rank the
	// file config the registry arm resolved (#1396).
	t.Run("without datahub_source_name states none", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:   "s3",
			Name:   "lake",
			Config: map[string]any{"bucket": "my-bucket"},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Empty(t, src.DataHubSourceName)
		assert.Nil(t, src.CatalogMapping)
	})

	t.Run("empty config states nothing", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:   "trino",
			Name:   "dev",
			Config: map[string]any{},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Empty(t, src.DataHubSourceName)
	})
}

// TestBuildConnectionSourceMap_BackfillRowKeepsConfiguredMapping is the
// regression for the second boot of a DB-backed deployment: connbackfill seeds
// a config-less connection_instances row for every file-configured connection,
// and reading those rows back must not replace the deployment's urn_mapping
// with a kind default (#1396).
func TestBuildConnectionSourceMap_BackfillRowKeepsConfiguredMapping(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse"}, {Name: "staging"}},
	}))

	p := &Platform{
		toolkitRegistry: reg,
		config:          &Config{},
		connectionStore: &mockConnectionStoreForTest{instances: []ConnectionInstance{
			// What the backfill writes: a credential-free, config-less row.
			{Kind: "trino", Name: "warehouse", Config: map[string]any{}},
			{Kind: "trino", Name: "staging", Config: map[string]any{}},
		}},
	}
	p.config.Semantic.URNMapping = URNMappingConfig{
		Platform:       "hive",
		CatalogMapping: map[string]string{"rdbms": "postgres"},
	}
	m := p.buildConnectionSourceMap()

	for _, name := range []string{"warehouse", "staging"} {
		src := m.ForConnection("trino", name)
		require.NotNil(t, src, "connection %q resolves", name)
		assert.Equal(t, "hive", src.DataHubSourceName,
			"a row that states no source name leaves the configured urn_mapping standing")
		assert.Equal(t, "postgres", src.CatalogMapping["rdbms"])
	}
}

// TestBuildConnectionSourceMap_StoredMappingStillWins guards the other
// direction: a row that does state a mapping overrides the file.
func TestBuildConnectionSourceMap_StoredMappingStillWins(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse"}},
	}))

	p := &Platform{
		toolkitRegistry: reg,
		config:          &Config{},
		connectionStore: &mockConnectionStoreForTest{instances: []ConnectionInstance{{
			Kind: "trino",
			Name: "warehouse",
			Config: map[string]any{
				"datahub_source_name": "postgres",
				"catalog_mapping":     map[string]any{"rdbms": "warehouse"},
			},
		}}},
	}
	p.config.Semantic.URNMapping = URNMappingConfig{
		Platform:       "hive",
		CatalogMapping: map[string]string{"rdbms": "postgres"},
	}

	src := p.buildConnectionSourceMap().ForConnection("trino", "warehouse")
	require.NotNil(t, src)
	assert.Equal(t, "postgres", src.DataHubSourceName)
	assert.Equal(t, "warehouse", src.CatalogMapping["rdbms"])
}

func TestDefaultSourceName(t *testing.T) {
	assert.Equal(t, "trino", connsource.DefaultSourceName("trino"))
	assert.Equal(t, "s3", connsource.DefaultSourceName("s3"))
	assert.Equal(t, "datahub", connsource.DefaultSourceName("datahub"))
	assert.Equal(t, "", connsource.DefaultSourceName("unknown"))
	assert.Equal(t, "", connsource.DefaultSourceName(""))
}

func TestConnectionSourceMap_AddDeduplicates(t *testing.T) {
	m := NewConnectionSourceMap()
	src := ConnectionSource{Kind: "trino", Name: "prod", DataHubSourceName: "trino"}
	m.Add(src)
	m.Add(src) // add same connection again

	conns := m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)")
	assert.Len(t, conns, 1, "duplicate Add should not create duplicate entries")
	assert.Equal(t, "prod", conns[0].Name)
}

func TestConnectionSourceMap_Nil(t *testing.T) {
	var m *ConnectionSourceMap

	assert.Nil(t, m.ForConnection("trino", "prod"))
	assert.Nil(t, m.ConnectionsForSource("trino"))
	assert.Nil(t, m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,c.s.t,PROD)"))
}

// TestBuildConnectionSourceMap_KeysByConnectionName is the regression for
// #1396: a toolkit configured with a connection_name is keyed in the source map
// by the name a lookup arrives with, not by its instances: key. Every lookup
// (enrichment's catalog mapping, the URN builder's platform) reads the
// connection name off PlatformContext, so keying by the instance meant the
// documented multi-provider shape resolved nothing and silently fell back to
// the query-provider mapping.
func TestBuildConnectionSourceMap_KeysByConnectionName(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "datahub", name: "primary", connection: "Primary Catalog", tools: []string{"datahub_browse"},
	}))
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "data_lake", connection: "Data Lake", tools: []string{"s3_list_buckets"},
	}))

	p := &Platform{toolkitRegistry: reg, config: &Config{}}
	m := p.buildConnectionSourceMap()

	dh := m.ForConnection("datahub", "Primary Catalog")
	require.NotNil(t, dh, "the connection resolves under the name a tool call carries")
	assert.Equal(t, "datahub", dh.DataHubSourceName)

	s3 := m.ForConnection("s3", "Data Lake")
	require.NotNil(t, s3)
	assert.Equal(t, "s3", s3.DataHubSourceName)

	assert.Nil(t, m.ForConnection("datahub", "primary"),
		"the instances: key is not a name any lookup arrives with")
	assert.Nil(t, m.ForConnection("s3", "data_lake"))
}

// TestBuildConnectionSourceMap_EveryServedConnection proves a multi-connection
// toolkit contributes one entry per connection it serves, not a single entry
// under the toolkit instance: a call naming any of them resolves the mapping.
func TestBuildConnectionSourceMap_EveryServedConnection(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse"}, {Name: "staging"}},
	}))

	p := &Platform{toolkitRegistry: reg, config: &Config{}}
	p.config.Semantic.URNMapping = URNMappingConfig{
		Platform:       "hive",
		CatalogMapping: map[string]string{"rdbms": "postgres"},
	}
	m := p.buildConnectionSourceMap()

	for _, name := range []string{"warehouse", "staging"} {
		src := m.ForConnection("trino", name)
		require.NotNil(t, src, "connection %q resolves", name)
		assert.Equal(t, "hive", src.DataHubSourceName)
		assert.Equal(t, "postgres", src.CatalogMapping["rdbms"])
	}
}

// TestDatasetURNFor_UsesConnectionMapping is the end-to-end assertion behind
// #1396: the map built from the live registry is read by the platform's one
// answer to "which catalog entity is this table", keyed by the connection name
// the middleware puts on PlatformContext. Before the fix the lookup missed and
// the URN carried the query-provider fallback instead of the connection's own
// platform and catalog mapping.
func TestDatasetURNFor_UsesConnectionMapping(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "lake", connection: "Data Lake", tools: []string{"s3_list_buckets"},
	}))

	p := &Platform{toolkitRegistry: reg, config: &Config{}}
	p.config.Query.URNMapping = URNMappingConfig{Platform: "fallback-platform"}
	p.connectionSources = p.buildConnectionSourceMap()

	urn := p.datasetURNFor("s3", "Data Lake", "bucket", "raw", "events")
	assert.Contains(t, urn, "urn:li:dataPlatform:s3",
		"the connection's own source name builds the URN")
	assert.NotContains(t, urn, "fallback-platform")

	assert.Contains(t, p.datasetURNFor("s3", "unknown", "bucket", "raw", "events"), "fallback-platform",
		"an unknown connection still falls back, so the fix narrows nothing")
}

// TestBuildConnectionSourceMap_StoredOverrideReachesTheBoundName proves the DB
// arm files its entry under the same name the registry arm does. A stored row
// is keyed by the toolkit instance it configures; a call binds the connection
// name. Filing the override under the instance left it unreachable by every
// lookup while the registry default answered in its place (#1396).
func TestBuildConnectionSourceMap_StoredOverrideReachesTheBoundName(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "data_lake", connection: "Data Lake", tools: []string{"s3_list_buckets"},
	}))

	p := &Platform{
		toolkitRegistry: reg,
		config:          &Config{},
		connectionStore: &mockConnectionStoreForTest{instances: []ConnectionInstance{{
			Kind:   "s3",
			Name:   "data_lake",
			Config: map[string]any{"datahub_source_name": "minio"},
		}}},
	}
	m := p.buildConnectionSourceMap()

	src := m.ForConnection("s3", "Data Lake")
	require.NotNil(t, src, "the stored connection resolves under the name a call binds")
	assert.Equal(t, "minio", src.DataHubSourceName,
		"the operator's stored source name overrides the kind default")
	assert.Nil(t, m.ForConnection("s3", "data_lake"),
		"the instance key is not a second entry for the same connection")
}
