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

// enqueueOne queues one run of a saved script and returns its id.
func enqueueOne(ctx context.Context, t *testing.T, s *Store, id string) {
	t.Helper()
	sc, version := savedScript(ctx, t, s, "daily-"+id)
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version, Trigger: script.TriggerSchedule,
	}))
}

// TestRealDB_ReclaimsAreCappedAndTheAbandonedRunFails is #1860 against
// PostgreSQL: a run whose workers keep dying is taken over until its reclaims
// are spent, each dead attempt recorded, and then no claim takes it and the
// sweep fails it naming the last holder.
func TestRealDB_ReclaimsAreCappedAndTheAbandonedRunFails(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	enqueueOne(ctx, t, s, "dpx_loop")

	// A zero lease expires the instant it is taken: a worker that died.
	first, err := s.Claim(ctx, "worker-a", 0, 2)
	require.NoError(t, err)
	assert.False(t, first.Reclaimed)
	require.NotNil(t, first.ClaimedAt)
	require.NotNil(t, first.HeartbeatAt)

	second, err := s.Claim(ctx, "worker-b", 0, 2)
	require.NoError(t, err)
	assert.True(t, second.Reclaimed)
	assert.Equal(t, 1, second.Reclaims)
	require.Len(t, second.Attempts, 1)
	assert.Equal(t, runstate.AttemptLeaseExpired, second.Attempts[0].Outcome)
	assert.Equal(t, "worker-a", second.Attempts[0].Worker)

	third, err := s.Claim(ctx, "worker-c", 0, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, third.Reclaims)

	_, err = s.Claim(ctx, "worker-d", time.Minute, 2)
	require.ErrorIs(t, err, script.ErrNoWork, "a run whose reclaims are spent is not claimed again")

	failed, err := s.FailAbandoned(ctx, 2)
	require.NoError(t, err)
	require.Len(t, failed, 1)
	got := failed[0]
	assert.Equal(t, script.RunStatusFailed, got.Status)
	assert.Equal(t, runstate.CauseWorkerLost, got.Cause)
	assert.Contains(t, got.Error, "stopped without reporting a result 3 times")
	assert.Contains(t, got.Error, "last held by worker-c")
	assert.Empty(t, got.LockedBy, "the lease is cleared, fencing a holder that was only slow")
	require.Len(t, got.Attempts, 3)
	assert.Equal(t, "worker-c", got.Attempts[2].Worker)
	require.NotNil(t, got.FinishedAt)

	err = s.Finish(ctx, third.Lease(), script.RunResult{Status: script.RunStatusSucceeded})
	require.ErrorIs(t, err, script.ErrLeaseLost, "a late holder cannot overwrite the verdict")

	again, err := s.FailAbandoned(ctx, 2)
	require.NoError(t, err)
	assert.Empty(t, again)
}

// TestRealDB_EveryEndedAttemptIsRecorded: a requeued attempt is recorded with
// how it ended, and the attempt that records the verdict with the cause.
func TestRealDB_EveryEndedAttemptIsRecorded(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	enqueueOne(ctx, t, s, "dpx_attempts")

	run, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	require.NoError(t, s.Retry(ctx, run.Lease(),
		runstate.AttemptShed, "memory pressure", 0))

	run, err = s.Claim(ctx, "worker-b", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	assert.False(t, run.Reclaimed, "a requeued run is claimed, not taken over")
	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusFailed, Error: "upstream timed out", Cause: runstate.CauseUpstream,
	}))

	got, err := s.GetRun(ctx, "dpx_attempts")
	require.NoError(t, err)
	assert.Equal(t, runstate.CauseUpstream, got.Cause)
	require.Len(t, got.Attempts, 2)
	assert.Equal(t, runstate.AttemptShed, got.Attempts[0].Outcome)
	assert.Equal(t, "memory pressure", got.Attempts[0].Error)
	assert.Equal(t, runstate.AttemptFinished, got.Attempts[1].Outcome)
	assert.Equal(t, "upstream timed out", got.Attempts[1].Error)
}

// TestRealDB_ProgressIsAHeartbeatAndCancelEndsAnOrphan: a report stamps the
// holder's heartbeat; a cancel of a run whose holder stopped reporting ends it
// at once and fences the holder, and a cancel of one being executed marks it.
func TestRealDB_ProgressIsAHeartbeatAndCancelEndsAnOrphan(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	enqueueOne(ctx, t, s, "dpx_orphan")

	run, err := s.Claim(ctx, "worker-a", time.Hour, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE script_runs SET heartbeat_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, run.ID)
	require.NoError(t, err)
	_, _, err = s.RecordProgress(ctx, run.Lease(), script.RunLive{Unchanged: true})
	require.NoError(t, err)
	fresh, err := s.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, runstate.LivenessExecuting, fresh.Liveness(time.Now()), "an unchanged report is still a heartbeat")

	prior, now, err := s.CancelRun(ctx, run.ID, "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{script.RunStatusRunning, script.RunStatusRunning}, []string{prior, now},
		"a run being executed is marked for its worker")

	_, err = db.ExecContext(ctx, `UPDATE script_runs SET heartbeat_at = NOW() - INTERVAL '5 minutes' WHERE id = $1`, run.ID)
	require.NoError(t, err)
	prior, now, err = s.CancelRun(ctx, run.ID, "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{script.RunStatusRunning, script.RunStatusCanceled}, []string{prior, now})

	got, err := s.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Contains(t, got.Error, "had stopped reporting")
	assert.Empty(t, got.LockedBy)
	require.NotEmpty(t, got.Attempts)
	assert.Equal(t, runstate.AttemptUnresponsive, got.Attempts[len(got.Attempts)-1].Outcome)
	err = s.Finish(ctx, run.Lease(), script.RunResult{Status: script.RunStatusSucceeded})
	require.ErrorIs(t, err, script.ErrLeaseLost)
}

// TestRealDB_ListRunsLive lists only runs that have not ended.
func TestRealDB_ListRunsLive(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, version := savedScript(ctx, t, s, "live")
	for _, id := range []string{"dpx_done", "dpx_waiting"} {
		require.NoError(t, s.Enqueue(ctx, &script.Run{
			ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version, Trigger: script.TriggerTool,
		}))
	}
	done, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	require.NoError(t, s.Finish(ctx, done.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))

	live, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID, Live: true})
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.NotEqual(t, done.ID, live[0].ID)
}
