//go:build integration

package scriptstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_RunQueueStateCountsTheQueueByState: a due run, a claimed run and
// a run queued for later are counted apart, and the oldest due run's age is
// the database's (#1897).
func TestRealDB_RunQueueStateCountsTheQueueByState(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, version := savedScript(ctx, t, s, "daily")
	for _, id := range []string{"dpx_due", "dpx_claimed"} {
		require.NoError(t, s.Enqueue(ctx, &script.Run{
			ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version, Trigger: script.TriggerTool,
		}))
	}
	_, err := db.ExecContext(ctx, `UPDATE script_runs SET scheduled_for = NOW() - INTERVAL '90 seconds' WHERE id = 'dpx_due'`)
	require.NoError(t, err)
	_, err = s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)

	other, otherVersion := savedScript(ctx, t, s, "weekly")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_later", ScriptID: other.ID, VersionID: otherVersion.ID, Version: otherVersion.Version, Trigger: script.TriggerTool,
	}))
	_, err = db.ExecContext(ctx, `UPDATE script_runs SET scheduled_for = NOW() + INTERVAL '1 hour' WHERE id = 'dpx_later'`)
	require.NoError(t, err)

	st, err := s.RunQueueState(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), st.Running, "the claim took the oldest due run")
	assert.Equal(t, int64(1), st.Pending, "the other run is due and unclaimed")
	assert.Equal(t, int64(1), st.Waiting, "a run queued for later is waiting, not due")
	assert.Less(t, st.OldestDue, 90*time.Second, "the run claimed is no longer the oldest due")
}

// TestRealDB_DueScheduleStateCountsSchedulesNoPassMaterialized: a schedule
// whose next fire has passed is due until a scheduler pass materializes it,
// which is what grows when no worker runs (#1897).
func TestRealDB_DueScheduleStateCountsSchedulesNoPassMaterialized(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, _ := savedScript(ctx, t, s, "daily")
	scheduledFor(ctx, t, s, sc, time.Now().Add(-10*time.Minute))
	later, _ := savedScript(ctx, t, s, "weekly")
	scheduledFor(ctx, t, s, later, time.Now().Add(time.Hour))

	due, oldest, err := s.DueScheduleState(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), due)
	assert.GreaterOrEqual(t, oldest, 9*time.Minute)
}
