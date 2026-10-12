//go:build integration

package scriptstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRealDB_AVersionSavedBeforeTheStatementCapKeepsTheMark is #2085 against
// the migrated schema: a version written now is not marked, a version marked
// as the migration marks every version that existed when it ran reads back
// marked, a version that moves only the description keeps its source's mark,
// and a version with new source is not marked.
func TestRealDB_AVersionSavedBeforeTheStatementCapKeepsTheMark(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("ingest", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))
	fresh, err := s.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	assert.False(t, fresh.PredatesStatementCap, "a version saved now is held to the ceiling")

	// What migration 000188 does to every row that exists when it runs.
	_, err = db.ExecContext(ctx, `UPDATE script_versions SET predates_statement_cap = TRUE WHERE script_id = $1`, sc.ID)
	require.NoError(t, err)
	marked, err := s.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	assert.True(t, marked.PredatesStatementCap)

	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	live.Description = "Only the description changed."
	require.NoError(t, s.UpdateWithVersion(ctx, live, testAuthor))
	same, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	require.Greater(t, same.Version, marked.Version)
	assert.True(t, same.PredatesStatementCap, "the same source keeps the mark it was saved with")

	live.Source = "def main():\n    return 2\n"
	require.NoError(t, s.UpdateWithVersion(ctx, live, testAuthor))
	edited, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	assert.False(t, edited.PredatesStatementCap, "new source is held to the ceiling")
}
