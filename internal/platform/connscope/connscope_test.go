package connscope

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// registryWith builds a persona registry holding an analyst granted one
// connection glob and a deny rule, plus an admin granted everything — the two
// shapes an operator actually writes.
func registryWith(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{
		Name:  "analyst",
		Roles: []string{"analyst"},
		Connections: persona.ConnectionRules{
			Allow: []string{"warehouse-*"},
			Deny:  []string{"warehouse-secret"},
		},
	}))
	require.NoError(t, reg.Register(&persona.Persona{
		Name:        "admin",
		Roles:       []string{"admin"},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}))
	return reg
}

func TestScopeAllowConnection(t *testing.T) {
	s := New(Deps{Registry: registryWith(t)})

	assert.True(t, s.AllowConnection("analyst", "warehouse-a"), "an allow glob grants the connection")
	assert.False(t, s.AllowConnection("analyst", "warehouse-secret"), "deny takes precedence over allow")
	assert.False(t, s.AllowConnection("analyst", "payroll"), "connections are deny-by-default")
	assert.True(t, s.AllowConnection("admin", "payroll"), "an unrestricted persona reaches everything")
	assert.True(t, s.AllowConnection("analyst", ""), "an empty name is platform-level, not a connection")
}

func TestScopeAllowConnection_UnresolvablePersonaFailsClosed(t *testing.T) {
	s := New(Deps{Registry: registryWith(t)})

	assert.False(t, s.AllowConnection("", "warehouse-a"), "no persona denies, as it does on a tool call")
	assert.False(t, s.AllowConnection("ghost", "warehouse-a"), "an unknown persona denies")

	// No registry at all: nothing resolves, so nothing is allowed — including the
	// platform-level empty name, which IsConnectionAllowed admits only for a
	// persona that resolved. The wiring (connectionScopeFor) is what decides not
	// to build a scope for a deployment with no persona registry at all.
	none := New(Deps{})
	assert.False(t, none.AllowConnection("analyst", "warehouse-a"))
	assert.False(t, none.AllowConnection("analyst", ""))

	var nilScope *Scope
	assert.False(t, nilScope.AllowConnection("analyst", "warehouse-a"))
}

func TestScopeConnectionsForURN(t *testing.T) {
	s := New(Deps{
		Registry: registryWith(t),
		URNConnections: func(urn string) []string {
			if urn == "urn:known" {
				return []string{"warehouse-a"}
			}
			return nil
		},
	})

	assert.Equal(t, []string{"warehouse-a"}, s.ConnectionsForURN("urn:known"))
	assert.Empty(t, s.ConnectionsForURN("urn:other"), "an unmappable URN resolves to no connection")

	// No mapper wired: every URN is unattributable, which keeps catalog hits
	// visible rather than hiding them on a guess.
	assert.Empty(t, New(Deps{Registry: registryWith(t)}).ConnectionsForURN("urn:known"))

	var nilScope *Scope
	assert.Empty(t, nilScope.ConnectionsForURN("urn:known"))
}

// TestScopeSatisfiesKnowledgeContract is the compile-time proof that the adapter
// is what the knowledge router asks for; the platform composition root relies on
// it.
func TestScopeSatisfiesKnowledgeContract(t *testing.T) {
	var _ knowledge.ConnectionScope = New(Deps{Registry: registryWith(t)})
}
