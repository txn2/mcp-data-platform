//go:build integration

package knowledge

// Real-Postgres round-trip test for the changeset store. postgresChangesetStore
// is the apply_knowledge persistence path: it marshals map/slice fields to JSONB
// columns, scans them back, and toggles rollback state with a conditional
// UPDATE. None of that is exercised against a real engine by the in-memory
// store tests, so a JSONB/enum/constraint mismatch or a broken rollback guard
// would ship green. This asserts write -> read-back -> list -> rollback against
// the actual schema (migration 000008).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestChangesetStore_RealDB_RoundTripAndRollback(t *testing.T) {
	store := NewPostgresChangesetStore(testdb.New(t))
	ctx := context.Background()

	const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.e2e_test.test_orders,PROD)"
	cs := Changeset{
		ID:               "cs_realdb_1",
		TargetURN:        urn,
		ChangeType:       "update_description",
		PreviousValue:    map[string]any{"description": "old"},
		NewValue:         map[string]any{"description": "new"},
		SourceInsightIDs: []string{"ins_a", "ins_b"},
		ApprovedBy:       "admin@example.com",
		AppliedBy:        "admin@example.com",
	}
	require.NoError(t, store.InsertChangeset(ctx, cs), "insert changeset")

	// Read-back: every JSONB column must round-trip to the written value.
	got, err := store.GetChangeset(ctx, cs.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, cs.ID, got.ID)
	assert.Equal(t, cs.TargetURN, got.TargetURN)
	assert.Equal(t, cs.ChangeType, got.ChangeType)
	assert.Equal(t, "old", got.PreviousValue["description"])
	assert.Equal(t, "new", got.NewValue["description"])
	assert.Equal(t, []string{"ins_a", "ins_b"}, got.SourceInsightIDs)
	assert.Equal(t, cs.ApprovedBy, got.ApprovedBy)
	assert.Equal(t, cs.AppliedBy, got.AppliedBy)
	assert.False(t, got.RolledBack, "freshly inserted changeset is not rolled back")
	assert.False(t, got.CreatedAt.IsZero(), "created_at default must populate")

	// List by the target URN finds it; the not-rolled-back filter includes it.
	notRolledBack := false
	list, total, err := store.ListChangesets(ctx, ChangesetFilter{EntityURN: urn, RolledBack: &notRolledBack})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, cs.ID, list[0].ID)

	// The rolled-back filter excludes it while it is still active.
	rolledBack := true
	_, total, err = store.ListChangesets(ctx, ChangesetFilter{EntityURN: urn, RolledBack: &rolledBack})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "active changeset must not appear under a rolled_back=true filter")

	// Rollback flips the state columns.
	require.NoError(t, store.RollbackChangeset(ctx, cs.ID, "operator@example.com"))
	got, err = store.GetChangeset(ctx, cs.ID)
	require.NoError(t, err)
	assert.True(t, got.RolledBack, "rolled_back column must be set")
	assert.Equal(t, "operator@example.com", got.RolledBackBy)
	require.NotNil(t, got.RolledBackAt, "rolled_back_at must populate")

	// After rollback it appears under the rolled_back=true filter.
	rolled, total, err := store.ListChangesets(ctx, ChangesetFilter{EntityURN: urn, RolledBack: &rolledBack})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, rolled, 1)
	assert.Equal(t, cs.ID, rolled[0].ID)

	// A second rollback is a no-op guarded by the WHERE rolled_back = FALSE clause.
	err = store.RollbackChangeset(ctx, cs.ID, "operator@example.com")
	require.Error(t, err, "double rollback must report not-found/already-rolled-back")
}
