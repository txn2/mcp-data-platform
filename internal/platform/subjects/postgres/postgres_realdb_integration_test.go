//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRealDB_RecordKeepsTheSubjectMostRecentlySeen runs the store's two
// statements against a migrated database: the upsert follows a person to a
// new subject, and an address never seen is unknown rather than an error.
func TestRealDB_RecordKeepsTheSubjectMostRecentlySeen(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()

	got, err := store.Lookup(ctx, "nobody@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got)

	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1", true))
	got, err = store.Lookup(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-1", got)

	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-2", true))
	got, err = store.Lookup(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-2", got, "the most recent subject wins")

	// A key's pair never displaces a person's, and a person's always wins
	// back: this is the statement's conflict clause, which sqlmock matches as
	// a string and cannot evaluate (#1759).
	require.NoError(t, store.Record(ctx, "jane@example.com", "apikey:ci", false))
	got, err = store.LookupPerson(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-2", got, "a key overwrote the subject a person authenticates as")

	require.NoError(t, store.Record(ctx, "keyed@example.com", "apikey:ci", false))
	got, err = store.LookupPerson(ctx, "keyed@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got, "a pair only a key wrote must not answer for the person")
	got, err = store.Lookup(ctx, "keyed@example.com")
	require.NoError(t, err)
	require.Equal(t, "apikey:ci", got, "the run path still reads it")

	require.NoError(t, store.Record(ctx, "keyed@example.com", "sub-keyed", true))
	got, err = store.LookupPerson(ctx, "keyed@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-keyed", got, "a person's own sign-in repairs the row")

	var nilStore *Store
	require.NoError(t, nilStore.Record(ctx, "a@example.com", "s", true))
	got, err = nilStore.Lookup(ctx, "a@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got)
}
