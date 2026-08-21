package connreach_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// oneConnToolkit is a toolkit that serves a single connection, which is the
// fallback shape connview uses when a toolkit does not enumerate its own. It
// carries an instance name distinct from its connection name, because that gap
// is what the value rule exists for.
type oneConnToolkit struct {
	kind, name, conn string
}

func (c *oneConnToolkit) Kind() string                          { return c.kind }
func (c *oneConnToolkit) Name() string                          { return c.name }
func (c *oneConnToolkit) Connection() string                    { return c.conn }
func (*oneConnToolkit) RegisterTools(_ *mcp.Server)             {}
func (*oneConnToolkit) Tools() []string                         { return nil }
func (*oneConnToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*oneConnToolkit) SetQueryProvider(_ query.Provider)       {}
func (*oneConnToolkit) Close() error                            { return nil }

// fixture is two connections and one persona granted only the first, which is
// what makes the boundary observable rather than assumed.
func fixture(t *testing.T) connreach.Deps {
	t.Helper()
	toolkits := registry.NewRegistry()
	require.NoError(t, toolkits.Register(&oneConnToolkit{kind: "trino", name: "wh", conn: "warehouse"}))
	require.NoError(t, toolkits.Register(&oneConnToolkit{kind: "s3", name: "lk", conn: "lake"}))

	personas := persona.NewRegistry()
	require.NoError(t, personas.Register(&persona.Persona{
		Name: "analyst", Roles: []string{"dp_analyst"},
		Connections: persona.ConnectionRules{Allow: []string{"warehouse"}},
	}))
	return connreach.Deps{Toolkits: toolkits, Personas: personas}
}

// TestForPersona_AppliesThePersonaBoundary is why the enumeration is
// trustworthy: it lists what a call would be allowed to name, by the same
// predicate the authorizer applies.
func TestForPersona_AppliesThePersonaBoundary(t *testing.T) {
	got := connreach.New(fixture(t)).ForPersona(context.Background(), "analyst", false)

	require.Len(t, got, 1, "a persona granted one connection reaches one")
	assert.Equal(t, "warehouse", got[0].Name,
		"the connection name is what a persona's rules match, not the instance name")
	assert.Equal(t, "trino", got[0].Kind)
}

// TestForPersona_EnumeratesAnAdministratorUnrestricted keeps the admin surface
// unrestricted, which is what it is everywhere else.
func TestForPersona_EnumeratesAnAdministratorUnrestricted(t *testing.T) {
	got := connreach.New(fixture(t)).ForPersona(context.Background(), "admin", true)
	assert.Len(t, got, 2)
}

// TestForPersona_DeniesAnUnresolvedPersona is the fail-closed default the
// authorizer applies to a tool call, applied here too.
func TestForPersona_DeniesAnUnresolvedPersona(t *testing.T) {
	assert.Empty(t, connreach.New(fixture(t)).ForPersona(context.Background(), "nobody", false))
}

// TestForPersona_FallsBackToTheInstanceName covers a toolkit configured with no
// connection name of its own: there is nothing else for a caller to name it by,
// so the instance name is the value.
func TestForPersona_FallsBackToTheInstanceName(t *testing.T) {
	toolkits := registry.NewRegistry()
	require.NoError(t, toolkits.Register(&oneConnToolkit{kind: "s3", name: "lake"}))

	got := connreach.New(connreach.Deps{Toolkits: toolkits}).
		ForPersona(context.Background(), "admin", true)

	require.Len(t, got, 1)
	assert.Equal(t, "lake", got[0].Name)
}

// TestNilListerAnswersNothing keeps a deployment that cannot enumerate its
// connections from answering "reaches nothing", which a form renders as a
// refusal and an approval would read as one.
func TestNilListerAnswersNothing(t *testing.T) {
	l := connreach.New(connreach.Deps{Personas: persona.NewRegistry()})
	require.Nil(t, l, "no toolkit registry means no enumeration at all")
	assert.Nil(t, l.ForPersona(context.Background(), "analyst", false))
}
