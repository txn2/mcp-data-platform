package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const ordersURN = "mcp:dataset:(trino,warehouse-a,db.public.orders)"

// scopeFixture builds a deployment shaped like the one the boundary must get
// right: a multi-connection Trino toolkit (whose connections a persona names
// individually), a single S3 toolkit, and a persona granted one Trino
// connection.
func scopeFixture(t *testing.T) (personas *persona.Registry, toolkits *registry.Registry) {
	t.Helper()

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockConnectionListerToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse", tools: []string{"trino_query"}},
		connections: []toolkit.ConnectionDetail{{Name: "warehouse-a"}, {Name: "warehouse-b"}},
	}))
	require.NoError(t, reg.Register(&mockToolkit{
		kind: "s3", name: "lake", connection: "prod-lake", tools: []string{"s3_list"},
	}))

	personas = persona.NewRegistry()
	require.NoError(t, personas.Register(&persona.Persona{
		Name:        "analyst",
		Connections: persona.ConnectionRules{Allow: []string{"warehouse-a"}},
	}))
	return personas, reg
}

func TestConnectionScopeFor(t *testing.T) {
	t.Run("no persona registry leaves discovery unfiltered", func(t *testing.T) {
		assert.Nil(t, connectionScopeFor(nil, nil),
			"a deployment with no personas has no boundary to honor, so none is wired")
	})

	t.Run("resolves connections through the persona rules and the live connection set", func(t *testing.T) {
		personas, reg := scopeFixture(t)
		scope := connectionScopeFor(personas, reg)
		require.NotNil(t, scope)

		assert.True(t, scope.AllowConnection("analyst", "warehouse-a"))
		assert.False(t, scope.AllowConnection("analyst", "warehouse-b"))
		assert.False(t, scope.AllowConnection("analyst", "prod-lake"))

		// A catalog reference names its connection outright.
		assert.Equal(t, []string{"warehouse-a"}, scope.ConnectionsForURN(ordersURN))
		assert.Empty(t, scope.ConnectionsForURN("mcp:glossary_term:revenue"),
			"a definition names no connection")
	})

	t.Run("a nil toolkit registry attributes nothing", func(t *testing.T) {
		personas, _ := scopeFixture(t)
		scope := connectionScopeFor(personas, nil)
		require.NotNil(t, scope)
		assert.Empty(t, scope.ConnectionsForURN(ordersURN))
	})
}
