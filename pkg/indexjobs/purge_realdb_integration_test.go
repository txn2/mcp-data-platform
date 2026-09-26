//go:build integration

package indexjobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestPurgeUnresolvedFailed_RealDB covers #1904: a failure nobody resolved is
// removed once it finished before the cutoff; a recent one, a resolved one
// (PurgeTerminal's to remove) and a pending job are kept.
func TestPurgeUnresolvedFailed_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -100)
	_, err := db.ExecContext(ctx, `INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, completed_at, resolved_at) VALUES
		('k', 'old_unresolved', 'write', 'failed', $1, NULL),
		('k', 'recent_unresolved', 'write', 'failed', NOW(), NULL),
		('k', 'old_resolved', 'write', 'failed', $1, $1),
		('k', 'pending', 'write', 'pending', NULL, NULL)`, old)
	require.NoError(t, err)

	n, err := NewPostgresStore(db).PurgeUnresolvedFailed(ctx, 90)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rows, err := db.QueryContext(ctx, `SELECT source_id FROM index_jobs`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var left []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		left = append(left, id)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{"recent_unresolved", "old_resolved", "pending"}, left)
}
