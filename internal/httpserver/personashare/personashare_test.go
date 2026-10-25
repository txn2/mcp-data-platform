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

// A person is external when the roles the directory recorded for them
// resolve to a persona marked external, and the persona list marks such a
// persona (#2290).
func TestExternal(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{Name: "client-a", DisplayName: "Client A", Roles: []string{"ca"}, External: true}))
	require.NoError(t, pr.Register(&persona.Persona{Name: "staff", Roles: []string{"st"}}))
	users := pagedUsers{all: []user.User{
		{Email: "dana@client-a.example.com", Roles: []string{"ca"}},
		{Email: "ann@example.com", Roles: []string{"st"}},
	}}
	var deps portal.Deps
	Wire(&deps, pr, directoryOf(users))
	require.NotNil(t, deps.ExternalAddress)
	ctx := context.Background()
	assert.True(t, deps.ExternalAddress(ctx, "dana@client-a.example.com"))
	assert.False(t, deps.ExternalAddress(ctx, "ann@example.com"))
	assert.False(t, deps.ExternalAddress(ctx, "nobody@example.com"), "an address the directory does not hold")
	assert.Equal(t, []portal.SharePersona{
		{Name: "client-a", DisplayName: "Client A", External: true}, {Name: "staff"},
	}, deps.Personas())
}

// directoryOf answers Get from the pages it holds.
type directoryOf pagedUsers

func (d directoryOf) List(ctx context.Context, f user.Filter) ([]user.User, int, error) {
	return pagedUsers(d).List(ctx, f)
}

func (d directoryOf) Get(_ context.Context, email string) (*user.User, error) {
	for i := range d.all {
		if d.all[i].Email == email {
			return &d.all[i], nil
		}
	}
	return nil, user.ErrNotFound
}

// Each persona's count is how many people PersonaMembers lists for it, read
// in one walk; HasAccount is whether the directory holds the address (#2291).
func TestMemberCountsAndAccounts(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{Name: "finance", Roles: []string{"fin"}}))
	require.NoError(t, pr.Register(&persona.Persona{Name: "analyst", Roles: []string{"an"}}))
	users := directoryOf{all: []user.User{
		{Email: "a@example.com", Roles: []string{"fin"}},
		{Email: "b@example.com", Roles: []string{"fin"}},
		{Email: "c@example.com", Roles: []string{"an"}},
		{Email: "d@example.com", Roles: []string{"none"}},
	}}
	var deps portal.Deps
	Wire(&deps, pr, users)
	require.NotNil(t, deps.PersonaMemberCounts)
	ctx := context.Background()
	counts := deps.PersonaMemberCounts(ctx)
	assert.Equal(t, map[string]int{"finance": 2, "analyst": 1}, counts)
	assert.Len(t, deps.PersonaMembers(ctx, "finance"), counts["finance"])

	assert.True(t, deps.HasAccount(ctx, "c@example.com"))
	assert.False(t, deps.HasAccount(ctx, "guest@example.org"))
}
