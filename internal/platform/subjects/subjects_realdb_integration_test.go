//go:build integration

package subjects

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
	store := NewPostgresStore(db)
	ctx := context.Background()

	got, err := store.Lookup(ctx, "nobody@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got)

	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1"))
	got, err = store.Lookup(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-1", got)

	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-2"))
	got, err = store.Lookup(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-2", got, "the most recent subject wins")

	var nilStore *PostgresStore
	require.NoError(t, nilStore.Record(ctx, "a@example.com", "s"))
	got, err = nilStore.Lookup(ctx, "a@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got)
}
