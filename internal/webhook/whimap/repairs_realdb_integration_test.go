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
// keeps what it did and by which version, a second pass by a later version
// adds to what the first did and replaces what is kept (#2842), and the row
// goes with its source.
func TestRepairsRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('inbox', 'scratch')`)
	require.NoError(t, err)
	r := NewRepairs(db)

	version, err := r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Zero(t, version)
	none, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Nil(t, none)

	// The first version's pass, as 000270 recorded it: no collapsed list.
	_, err = db.ExecContext(ctx, `INSERT INTO webhook_imap_repairs (source, moved, removed, removed_files, kept, report)
		VALUES ('inbox', 1, 2, 3, 2, '{"moved":[{"from":"a/inbox-7-3","to":"a/m"}],"removed":[{"folder":"a/inbox-9-3","files":2},{"folder":"a/inbox-9-4","files":1}],"kept":["a/inbox-1-5","a/inbox-2-5"]}')`)
	require.NoError(t, err)
	version, err = r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, 1, version, "a row the first version wrote")
	got, err := r.Get(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, []RemovedFolder{}, got.Report.Collapsed)

	second := RepairReport{
		Moved: []FolderMove{}, Removed: []RemovedFolder{},
		Collapsed: []RemovedFolder{{Folder: "a/inbox-2-5", Files: 2}},
		Kept:      []string{"a/inbox-1-5"},
	}
	require.NoError(t, r.RecordRepair(ctx, "inbox", 2, second))
	version, err = r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Equal(t, 2, version)
	got, err = r.Get(ctx, "inbox")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Moved, "the first pass's moves are kept")
	assert.Equal(t, 2, got.Removed)
	assert.Equal(t, 3, got.RemovedFiles)
	assert.Equal(t, 1, got.Collapsed)
	assert.Equal(t, 2, got.CollapsedFiles)
	assert.Equal(t, 1, got.Kept, "kept is what is left")
	assert.Equal(t, RepairReport{
		Moved:     []FolderMove{{From: "a/inbox-7-3", To: "a/m"}},
		Removed:   []RemovedFolder{{Folder: "a/inbox-9-3", Files: 2}, {Folder: "a/inbox-9-4", Files: 1}},
		Collapsed: []RemovedFolder{{Folder: "a/inbox-2-5", Files: 2}},
		Kept:      []string{"a/inbox-1-5"},
	}, got.Report)
	assert.False(t, got.RepairedAt.IsZero())

	// A source repaired first by this version.
	_, err = db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('fresh', 'scratch')`)
	require.NoError(t, err)
	require.NoError(t, r.RecordRepair(ctx, "fresh", 2, second))
	fresh, err := r.Get(ctx, "fresh")
	require.NoError(t, err)
	assert.Equal(t, second, fresh.Report)

	_, err = db.ExecContext(ctx, `DELETE FROM webhook_sources WHERE name = 'inbox'`)
	require.NoError(t, err)
	version, err = r.RepairedVersion(ctx, "inbox")
	require.NoError(t, err)
	assert.Zero(t, version, "the record goes with its source")
}
