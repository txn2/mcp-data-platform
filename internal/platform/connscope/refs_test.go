package connscope

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestRefConnections(t *testing.T) {
	assert.Nil(t, RefConnections(nil, "mcp:glossary_term:revenue"), "a definition names no connection")
	assert.Nil(t, RefConnections(nil, "not a reference"))
	assert.Equal(t, []string{"warehouse"}, RefConnections(nil, "mcp:dataset:(trino,warehouse,a.b.c)"),
		"an instance no toolkit serves is matched by its own name")
}

// TestForRegistry: no persona registry wires no boundary, which leaves
// discovery unfiltered; a registry with no toolkit registry attributes a
// reference by its own connection name.
func TestForRegistry(t *testing.T) {
	assert.Nil(t, ForRegistry(nil, nil), "a deployment with no personas has no boundary to honor")
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Connections: persona.ConnectionRules{Allow: []string{"warehouse"}}}))
	scope := ForRegistry(reg, nil)
	require.NotNil(t, scope)
	assert.True(t, scope.AllowConnection("analyst", "warehouse"))
	assert.Empty(t, scope.ConnectionsForURN("mcp:dataset:(trino,warehouse,a.b.c)"), "no toolkit registry, nothing attributed")
	scope = ForRegistry(reg, registry.NewRegistry())
	assert.Equal(t, []string{"warehouse"}, scope.ConnectionsForURN("mcp:dataset:(trino,warehouse,a.b.c)"))
}
