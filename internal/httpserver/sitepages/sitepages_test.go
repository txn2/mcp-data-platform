package sitepages

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// people is a directory of bound principals by address; a missing address is
// nobody, and err fails every read.
type people struct {
	by  map[string]*auth.BoundPrincipal
	err error
}

func (p people) BoundPrincipal(_ context.Context, email string) (*auth.BoundPrincipal, error) {
	if p.err != nil {
		return nil, p.err
	}
	if found, ok := p.by[email]; ok {
		return found, nil
	}
	return nil, auth.ErrNoBoundPrincipal
}

func registry2747(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "admin", Roles: []string{"dp_admin"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"dp_analyst"}}))
	return reg
}

// TestTileOwner_ResolvesTheOwnerLive (#2747): the owner's subject, roles and
// persona come from the directory and the registry when the tile is drawn;
// the administrator persona is told apart; anyone the platform cannot speak
// for, or whose roles resolve to no persona, draws as nobody.
func TestTileOwner_ResolvesTheOwnerLive(t *testing.T) {
	dir := people{by: map[string]*auth.BoundPrincipal{
		"jane@example.com":  {Subject: "sub-jane", Email: "jane@example.com", Roles: []string{"dp_analyst"}},
		"root@example.com":  {Subject: "sub-root", Email: "root@example.com", Roles: []string{"dp_admin"}},
		"lost@example.com":  {Subject: "sub-lost", Email: "lost@example.com", Roles: []string{"dp_nothing"}},
		"never@example.com": {Email: "never@example.com", Roles: []string{"dp_analyst"}},
	}}
	owner := resolveOwner(dir, registry2747(t), "admin")
	require.NotNil(t, owner)

	assert.Equal(t, &epform.Viewer{UserID: "sub-jane", Email: "jane@example.com", Persona: "analyst", Roles: []string{"dp_analyst"}},
		owner(context.Background(), "jane@example.com"))
	root := owner(context.Background(), "root@example.com")
	require.NotNil(t, root)
	assert.True(t, root.Admin, "the administrator persona draws as an administrator")

	for _, email := range []string{"gone@example.com", "lost@example.com", "never@example.com"} {
		assert.Nil(t, owner(context.Background(), email), email)
	}
	failing := resolveOwner(people{err: errors.New("directory down")}, registry2747(t), "admin")
	assert.Nil(t, failing(context.Background(), "jane@example.com"), "a directory that cannot answer draws for nobody")

	assert.Nil(t, resolveOwner(nil, registry2747(t), "admin"), "no directory, no owner")
	assert.Nil(t, resolveOwner(dir, nil, "admin"), "no registry, no owner")
}

// TestWiring_ADeploymentWithNoDatabaseDrawsForNobody: with no database there
// are no sites and no directory, so a tile has no endpoint server and no
// owner. The wired path (sites served, the owner resolved) is the one
// #2747's acceptance criteria draw tiles through.
func TestWiring_ADeploymentWithNoDatabaseDrawsForNobody(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{Server: platform.ServerConfig{Name: "test"}}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	assert.Nil(t, ForTiles(p))
	assert.Nil(t, TileOwner(p))
}
