//go:build integration

// Real-Postgres round-trip tests for the user directory store. These exercise
// the actual ON CONFLICT name-merge logic in Observe, which sqlmock cannot
// verify: the rule that admin-entered names survive a later login (claims only
// fill blank fields) is pure SQL and only provable against a real database.

package user

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestUserStore_Observe_FillsBlankNames_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	// First login: no name known yet.
	require.NoError(t, store.Observe(ctx, "marcus@example.com", "", ""))
	u, err := store.Get(ctx, "marcus@example.com")
	require.NoError(t, err)
	assert.True(t, u.Confirmed)
	assert.Equal(t, SourceAuth, u.Source)
	assert.Empty(t, u.FirstName)
	assert.NotNil(t, u.LastSeenAt)

	// Later login carries claims: blank names get filled.
	require.NoError(t, store.Observe(ctx, "marcus@example.com", "Marcus", "Johnson"))
	u, err = store.Get(ctx, "marcus@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Marcus", u.FirstName)
	assert.Equal(t, "Johnson", u.LastName)
}

func TestUserStore_Observe_DoesNotOverwriteAdminName_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	// Admin pre-adds the person with a name, unconfirmed.
	require.NoError(t, store.Insert(ctx, User{
		Email: "dana@example.com", FirstName: "Dana", LastName: "Lee",
		Source: SourceAdmin, AddedBy: "admin@example.com",
	}))

	// The person logs in; their claims carry a different spelling. Admin wins.
	require.NoError(t, store.Observe(ctx, "dana@example.com", "Daniela", "Leigh"))

	u, err := store.Get(ctx, "dana@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Dana", u.FirstName, "admin-entered first name must stick")
	assert.Equal(t, "Lee", u.LastName, "admin-entered last name must stick")
	assert.True(t, u.Confirmed, "login should still confirm the row")
}

func TestUserStore_Search_EscapesLikeMetachars_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, User{Email: "plain@example.com", FirstName: "Plain", LastName: "Name"}))
	require.NoError(t, store.Insert(ctx, User{Email: "odd@example.com", FirstName: "a_b%c", LastName: `back\slash`}))

	// A query with LIKE metacharacters and a trailing backslash must not error
	// (a dangling escape would make Postgres reject the pattern) and must match
	// literally rather than treating % / _ / \ as wildcards.
	for _, q := range []string{`back\`, "a_b%c", `\`, "%"} {
		got, _, err := store.List(ctx, Filter{Query: q})
		require.NoErrorf(t, err, "query %q must not error", q)
		_ = got
	}

	// The literal underscore/percent name matches only its own row, not via
	// wildcard expansion.
	got, total, err := store.List(ctx, Filter{Query: "a_b%c"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, "odd@example.com", got[0].Email)
}

func TestUserStore_Insert_Duplicate_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, User{Email: "x@example.com"}))
	err := store.Insert(ctx, User{Email: "x@example.com"})
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestUserStore_ListUpdateDelete_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, User{Email: "amy@example.com", FirstName: "Amy", LastName: "Adams"}))
	require.NoError(t, store.Insert(ctx, User{Email: "bob@example.com", FirstName: "Bob", LastName: "Brown"}))

	// Search by name.
	got, total, err := store.List(ctx, Filter{Query: "ada"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, "amy@example.com", got[0].Email)

	// Update.
	newLast := "Adamson"
	require.NoError(t, store.Update(ctx, "amy@example.com", Update{LastName: &newLast}))
	u, err := store.Get(ctx, "amy@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Adamson", u.LastName)
	assert.Equal(t, "Amy", u.FirstName, "unspecified field unchanged")

	// Delete.
	require.NoError(t, store.Delete(ctx, "amy@example.com"))
	_, err = store.Get(ctx, "amy@example.com")
	assert.ErrorIs(t, err, ErrNotFound)
}
