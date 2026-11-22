package collectionsweepwire

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/threadreach"
	"github.com/txn2/mcp-data-platform/pkg/auth"
)

type fakePlatform struct {
	db    *sql.DB
	keys  *auth.APIKeyAuthenticator
	added int
}

func (f *fakePlatform) DB() *sql.DB { return f.db }

func (f *fakePlatform) APIKeyAuthenticator() *auth.APIKeyAuthenticator { return f.keys }

func (f *fakePlatform) AddCollectionItemSweep(func(context.Context) (int64, error)) { f.added++ }

type grantingShares struct{}

func (grantingShares) CollectionsGrantingAsset(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

func newReach() *threadreach.Reach {
	return threadreach.New(threadreach.Config{
		Access: access.New(access.Config{AdminRoles: []string{"admin"}}),
		PersonaName: func(roles []string) string {
			if len(roles) > 0 {
				return roles[0]
			}
			return ""
		},
	})
}

// The sweep is added only with a database, a decision and a share store that
// lists the collections granting an asset.
func TestWire(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	reach := newReach()
	keys := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{})

	Wire(nil, reach, grantingShares{})
	for name, tc := range map[string]struct {
		p      *fakePlatform
		reach  *threadreach.Reach
		shares any
		want   int
	}{
		"added":                                 {&fakePlatform{db: db, keys: keys}, reach, grantingShares{}, 1},
		"added with no API keys":                {&fakePlatform{db: db}, reach, grantingShares{}, 1},
		"no decision":                           {&fakePlatform{db: db}, nil, grantingShares{}, 0},
		"a share store that cannot list grants": {&fakePlatform{db: db}, reach, struct{}{}, 0},
		"no database":                           {&fakePlatform{}, reach, grantingShares{}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			Wire(tc.p, tc.reach, tc.shares)
			assert.Equal(t, tc.want, tc.p.added)
		})
	}
}

// A standalone key's identity is judged by the key's roles; a key bound to a
// person, a key no longer held and any other owner by the directory, which
// holds none of them here, so they are judged with no roles.
func TestOwnersPerson(t *testing.T) {
	keys := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{})
	keys.AddKey(auth.APIKey{Key: "k1", Name: "agent", Roles: []string{"admin"}})
	keys.AddKey(auth.APIKey{Key: "k2", Name: "bound", UserEmail: "p@example.com"})
	o := owners{reach: newReach(), keys: keys.ListKeys}
	ctx := context.Background()

	u, persona := o.Person(ctx, "apikey:agent", "agent@apikey.local")
	assert.Equal(t, []string{"admin"}, u.Roles)
	assert.Equal(t, "admin", persona)
	assert.Equal(t, "apikey:agent", u.UserID)

	for _, id := range []string{"apikey:bound", "apikey:gone", "sub-123"} {
		u, persona := o.Person(ctx, id, "")
		assert.Empty(t, u.Roles, id)
		assert.Empty(t, persona, id)
	}
	u, _ = owners{reach: newReach()}.Person(ctx, "apikey:agent", "")
	assert.Empty(t, u.Roles, "no key source")
}
