//go:build integration

package whimap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRepairsRealDB: a source's repair is owed until recorded, the record
// keeps what it did, a second record replaces the first, and the row goes
// with its source.
func TestRepairsRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('inbox', 'scratch')`)
	require.NoError(t, err)
	r := NewRepairs(db)

	done, err := r.Repaired(ctx, "inbox")
	require.NoError(t, err)
	assert.False(t, done)
	none, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Nil(t, none)

	rep := RepairReport{
		Moved:   []FolderMove{{From: "a/inbox-7-3", To: "a/" + MessageFolder("<m@x>")}},
		Removed: []RemovedFolder{{Folder: "a/inbox-9-3", Files: 2}, {Folder: "a/inbox-9-4", Files: 1}},
		Kept:    []string{"a/inbox-9-5"},
	}
	require.NoError(t, r.RecordRepair(ctx, "inbox", rep))
	done, err = r.Repaired(ctx, "inbox")
	require.NoError(t, err)
	assert.True(t, done)
	got, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Moved)
	assert.Equal(t, 2, got.Removed)
	assert.Equal(t, 3, got.RemovedFiles)
	assert.Equal(t, 1, got.Kept)
	assert.Equal(t, rep, got.Report)
	assert.False(t, got.RepairedAt.IsZero())

	require.NoError(t, r.RecordRepair(ctx, "inbox", RepairReport{Moved: []FolderMove{}, Removed: []RemovedFolder{}, Kept: []string{}}))
	got, err = r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Zero(t, got.Moved+got.Removed+got.Kept)

	_, err = db.ExecContext(ctx, `DELETE FROM webhook_sources WHERE name = 'inbox'`)
	require.NoError(t, err)
	done, err = r.Repaired(ctx, "inbox")
	require.NoError(t, err)
	assert.False(t, done, "the record goes with its source")
}
