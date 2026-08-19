package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/pkg/connview"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// TestScriptPortalIdentity covers the identity the portal script routes read
// their two entitlements from: the persona a listing is scoped to, and the
// admin membership that makes the surface unrestricted.
func TestScriptPortalIdentity(t *testing.T) {
	resolver := scriptPortalIdentity([]string{"admin"}, func(roles []string) *portal.PersonaInfo {
		if rolesIntersect(roles, []string{"analyst_role"}) {
			return &portal.PersonaInfo{Name: "analyst"}
		}
		return nil
	})

	// No user in context: nil identity, which the handlers answer as a 401.
	assert.Nil(t, resolver(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		UserID: "u1", Email: "sarah@example.com", Roles: []string{"analyst_role"},
	}))
	id := resolver(req)
	require.NotNil(t, id)
	assert.Equal(t, "u1", id.UserID)
	assert.Equal(t, "sarah@example.com", id.Email)
	assert.Equal(t, "analyst", id.Persona)
	assert.False(t, id.IsAdmin)

	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
		Email: "root@example.com", Roles: []string{"admin"},
	}))
	id = resolver(req)
	require.NotNil(t, id)
	assert.True(t, id.IsAdmin)
}

// A deployment with no persona registry resolves an identity with no persona
// rather than panicking inside the closure, which is what the nil guard in the
// mount is for.
func TestScriptPortalIdentity_NoPersonaResolver(t *testing.T) {
	resolver := scriptPortalIdentity(nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", http.NoBody)
	req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{Email: "sarah@example.com"}))
	id := resolver(req)
	require.NotNil(t, id)
	assert.Empty(t, id.Persona)
	assert.False(t, id.IsAdmin)
}

// choiceToolkit is a single-connection toolkit for the enumerator tests: it is
// the fallback shape connview uses when a toolkit does not enumerate its own
// connections, and it is enough to prove which names the persona boundary lets
// through.
type choiceToolkit struct {
	kind, name, conn string
}

func (c *choiceToolkit) Kind() string                          { return c.kind }
func (c *choiceToolkit) Name() string                          { return c.name }
func (c *choiceToolkit) Connection() string                    { return c.conn }
func (*choiceToolkit) RegisterTools(_ *mcp.Server)             {}
func (*choiceToolkit) Tools() []string                         { return nil }
func (*choiceToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*choiceToolkit) SetQueryProvider(_ query.Provider)       {}
func (*choiceToolkit) Close() error                            { return nil }

// enumeratorFixture is two connections and one persona granted only the first,
// which is what makes the boundary observable rather than assumed.
func enumeratorFixture(t *testing.T) (toolkits *registry.Registry, personas *persona.Registry) {
	t.Helper()
	toolkits = registry.NewRegistry()
	require.NoError(t, toolkits.Register(&choiceToolkit{kind: "trino", name: "wh", conn: "warehouse"}))
	require.NoError(t, toolkits.Register(&choiceToolkit{kind: "s3", name: "lk", conn: "lake"}))

	personas = persona.NewRegistry()
	require.NoError(t, personas.Register(&persona.Persona{
		Name: "analyst", Roles: []string{"dp_analyst"},
		Connections: persona.ConnectionRules{Allow: []string{"warehouse"}},
	}))
	return toolkits, personas
}

// TestScriptConnectionEnumerator_AppliesThePersonaBoundary is the reason the
// picker is trustworthy: it offers what a tool call would be allowed to name,
// by the same predicate list_connections applies.
func TestScriptConnectionEnumerator_AppliesThePersonaBoundary(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	enumerate := scriptConnectionEnumerator(tr, pr)
	require.NotNil(t, enumerate)

	got := enumerate(context.Background(), scripthttp.ConnectionScope{Persona: "analyst"})
	require.Len(t, got, 1, "a persona granted one connection sees one")
	assert.Equal(t, "warehouse", got[0].Name)
	assert.Equal(t, "trino", got[0].Kind)
}

// TestScriptConnectionEnumerator_EnumeratesAnAdministratorUnrestricted keeps
// the admin surface unrestricted, which is what it is everywhere else.
func TestScriptConnectionEnumerator_EnumeratesAnAdministratorUnrestricted(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	got := scriptConnectionEnumerator(tr, pr)(context.Background(),
		scripthttp.ConnectionScope{Persona: "admin", Unrestricted: true})

	require.Len(t, got, 2)
}

// TestScriptConnectionEnumerator_DeniesAnUnresolvedPersona is the fail-closed
// default the authorizer applies to a tool call, applied here too: a caller the
// registry cannot place reaches nothing.
func TestScriptConnectionEnumerator_DeniesAnUnresolvedPersona(t *testing.T) {
	tr, pr := enumeratorFixture(t)
	got := scriptConnectionEnumerator(tr, pr)(context.Background(),
		scripthttp.ConnectionScope{Persona: "nobody"})

	assert.Empty(t, got)
}

// TestScriptConnectionEnumerator_IsAbsentWithoutAToolkitRegistry leaves the
// choices route unmounted rather than serving an empty set a form would render
// as "this script may reach nothing".
func TestScriptConnectionEnumerator_IsAbsentWithoutAToolkitRegistry(t *testing.T) {
	assert.Nil(t, scriptConnectionEnumerator(nil, persona.NewRegistry()))
}

// TestConnectionValue pins which name a picker offers, which is not always the
// one the enumeration leads with: a single-connection toolkit's entry carries
// its INSTANCE name in Name and its connection name in Connection, and the
// connection name is what a persona's rules match and a grant lists. Offering
// the instance name would produce a picker whose every value the run refuses.
func TestConnectionValue(t *testing.T) {
	assert.Equal(t, "warehouse",
		connectionValue(connview.Entry{Name: "wh", Connection: "warehouse"}))
	assert.Equal(t, "wh",
		connectionValue(connview.Entry{Name: "wh"}))
}
