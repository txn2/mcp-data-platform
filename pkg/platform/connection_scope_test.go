package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,db.public.orders,PROD)"

// scopeFixture builds a deployment shaped like the one the boundary must get
// right: a multi-connection Trino toolkit (whose connections a persona names
// individually, while the source map keys the toolkit instance), a single
// S3 toolkit on its own platform, and a persona granted one Trino connection.
func scopeFixture(t *testing.T) (personas *persona.Registry, sources *ConnectionSourceMap, toolkits *registry.Registry) {
	t.Helper()

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse-a"}, {Name: "warehouse-b"}},
	}))
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "lake", connection: "prod-lake", tools: []string{"s3_list_buckets"},
	}))

	sources = NewConnectionSourceMap()
	sources.Add(ConnectionSource{Kind: "trino", Name: "warehouse", DataHubSourceName: "trino"})
	sources.Add(ConnectionSource{Kind: "s3", Name: "lake", DataHubSourceName: "s3"})

	personas = persona.NewRegistry()
	require.NoError(t, personas.Register(&persona.Persona{
		Name:        "analyst",
		Connections: persona.ConnectionRules{Allow: []string{"warehouse-a"}},
	}))
	return personas, sources, reg
}

func TestConnectionScopeFor(t *testing.T) {
	t.Run("no persona registry leaves discovery unfiltered", func(t *testing.T) {
		assert.Nil(t, connectionScopeFor(nil, nil, nil),
			"a deployment with no personas has no boundary to honor, so none is wired")
	})

	t.Run("resolves connections through the persona rules and the live connection set", func(t *testing.T) {
		personas, sources, reg := scopeFixture(t)
		scope := connectionScopeFor(personas, sources, reg)
		require.NotNil(t, scope)

		assert.True(t, scope.AllowConnection("analyst", "warehouse-a"))
		assert.False(t, scope.AllowConnection("analyst", "warehouse-b"))
		assert.False(t, scope.AllowConnection("analyst", "prod-lake"))

		// A Trino URN is attributed to the connections the toolkit serves, not to
		// the toolkit instance the source map is keyed by — the persona grants one
		// of those connection names, so the dataset stays visible.
		assert.Equal(t, []string{"warehouse-a", "warehouse-b"}, scope.ConnectionsForURN(ordersURN))
		assert.Empty(t, scope.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:mystery,db.public.x,PROD)"),
			"a URN whose platform no connection claims is unattributable")
	})

	t.Run("a nil toolkit registry attributes nothing", func(t *testing.T) {
		personas, sources, _ := scopeFixture(t)
		scope := connectionScopeFor(personas, sources, nil)
		require.NotNil(t, scope)
		assert.Empty(t, scope.ConnectionsForURN(ordersURN))
	})
}

func TestConnectionNamesForURN(t *testing.T) {
	_, sources, reg := scopeFixture(t)

	t.Run("expands a toolkit to the connections a persona actually names", func(t *testing.T) {
		assert.Equal(t, []string{"warehouse-a", "warehouse-b"},
			connectionNamesForURN(sources, reg.All(), ordersURN))
	})

	t.Run("a single-connection toolkit resolves to its connection name", func(t *testing.T) {
		assert.Equal(t, []string{"prod-lake"},
			connectionNamesForURN(sources, reg.All(), "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/raw,PROD)"))
	})

	t.Run("a connection added after startup is a candidate", func(t *testing.T) {
		// The source map is built once at startup; the toolkit's connection set is
		// live. A connection added later must not be treated as belonging to no
		// connection (which would hide its datasets from the persona granted it).
		added := &mockConnectionListerToolkit{
			mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
			connections: []toolkit.ConnectionDetail{{Name: "warehouse-a"}, {Name: "warehouse-c"}},
		}
		assert.Equal(t, []string{"warehouse-a", "warehouse-c"},
			connectionNamesForURN(sources, []registry.Toolkit{added}, ordersURN))
	})

	t.Run("a connection with its own mapping overrides its toolkit's", func(t *testing.T) {
		own := NewConnectionSourceMap()
		own.Add(ConnectionSource{Kind: "trino", Name: "warehouse", DataHubSourceName: "trino"})
		own.Add(ConnectionSource{Kind: "trino", Name: "warehouse-b", DataHubSourceName: "postgres"})
		assert.Equal(t, []string{"warehouse-a"}, connectionNamesForURN(own, reg.All(), ordersURN),
			"warehouse-b's datasets live under a different platform")
		assert.Equal(t, []string{"warehouse-b"},
			connectionNamesForURN(own, reg.All(), "urn:li:dataset:(urn:li:dataPlatform:postgres,db.public.orders,PROD)"))
	})

	t.Run("no mapping attributes nothing", func(t *testing.T) {
		assert.Empty(t, connectionNamesForURN(nil, reg.All(), ordersURN), "no source map means no attribution")
		assert.Empty(t, connectionNamesForURN(sources, reg.All(), "not-a-urn"))
		assert.Empty(t, connectionNamesForURN(sources, nil, ordersURN),
			"no live connection can serve the URN")
	})
}
