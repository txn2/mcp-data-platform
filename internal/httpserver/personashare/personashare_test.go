package personashare

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// pagedUsers serves a directory a page at a time, as the Postgres store does.
type pagedUsers struct {
	user.Store
	all []user.User
}

func (p pagedUsers) List(_ context.Context, f user.Filter) ([]user.User, int, error) {
	end := min(f.Offset+f.Limit, len(p.all))
	if f.Offset >= len(p.all) {
		return nil, len(p.all), nil
	}
	return p.all[f.Offset:end], len(p.all), nil
}

// A persona's members are the people whose recorded roles resolve to it,
// across every page of the directory (#2089).
func TestPersonaMembersReadsEveryPage(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{Name: "finance", DisplayName: "Finance", Roles: []string{"fin"}}))
	require.NoError(t, pr.Register(&persona.Persona{Name: "analyst", DisplayName: "Analyst", Roles: []string{"an"}}))
	all := make([]user.User, 0, user.MaxListLimit+5)
	for i := range user.MaxListLimit + 5 {
		roles := []string{"an"}
		if i%2 == 0 {
			roles = []string{"fin"}
		}
		all = append(all, user.User{Email: "p" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + "@example.com", Roles: roles})
	}
	members := members(context.Background(), pr, pagedUsers{all: all}, "finance")
	assert.Len(t, members, (user.MaxListLimit+5+1)/2)

	var deps portal.Deps
	Wire(&deps, pr, pagedUsers{all: all})
	require.NotNil(t, deps.Personas)
	assert.Equal(t, []portal.SharePersona{{Name: "analyst", DisplayName: "Analyst"}, {Name: "finance", DisplayName: "Finance"}}, deps.Personas())
	assert.Len(t, deps.PersonaMembers(context.Background(), "analyst"), (user.MaxListLimit+5)/2)
}
