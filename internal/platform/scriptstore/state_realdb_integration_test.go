//go:build integration

package scriptstore

// The real-schema proof for script state (#1537). The compare-and-set is a row
// predicate the database evaluates, and sqlmock returns whatever rows a test
// hands it for the guarded upsert; only a real Postgres shows that two runs
// which both read revision N cannot both write N+1, that a failed run leaves
// the state alone, that a reset moves the revision under a run in flight, and
// that the state row follows the script through delete and transfer.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// queued enqueues one run of the script and returns it as the worker would
// claim it: the state read pinned on the row.
func queued(ctx context.Context, t *testing.T, s *Store, sc *script.Script, v *script.Version, id string) *script.Run {
	t.Helper()
	run := &script.Run{ID: id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version, Trigger: script.TriggerTool}
	require.NoError(t, s.Enqueue(ctx, run))
	return run
}

// claimed claims the next due run for worker.
func claimed(ctx context.Context, t *testing.T, s *Store, worker string) *script.Run {
	t.Helper()
	run, err := s.Claim(ctx, worker, time.Minute)
	require.NoError(t, err)
	return run
}

func TestRealDB_StateIsReadAtCreationAndWrittenOnSuccess(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, v := savedScript(ctx, t, s, "sync")

	first := queued(ctx, t, s, sc, v, "dpx_state_1")
	assert.Zero(t, first.StateRevision, "a script that never saved state is read at revision 0")
	assert.Equal(t, map[string]any{}, first.StateRead)

	run := claimed(ctx, t, s, "worker-a")
	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded,
		State:  &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-28"}},
	}))

	done, err := s.GetRun(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusSucceeded, done.Status)
	assert.Equal(t, int64(1), done.StateRevisionWritten, "the run row records the revision it wrote")
	assert.Equal(t, "2026-08-28", done.StateWritten["synced_through"])

	st, err := s.GetState(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), st.Revision)
	assert.Equal(t, first.ID, st.RunID, "the state names the run that wrote it")

	second := queued(ctx, t, s, sc, v, "dpx_state_2")
	assert.Equal(t, int64(1), second.StateRevision, "the next run reads what the first saved")
	assert.Equal(t, "2026-08-28", second.StateRead["synced_through"])
}

func TestRealDB_AFailedRunLeavesTheStateWhereItWas(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, v := savedScript(ctx, t, s, "sync")
	_, err := s.SetState(ctx, sc.ID, map[string]any{"synced_through": "2026-08-27"}, "jane@example.com")
	require.NoError(t, err)

	queued(ctx, t, s, sc, v, "dpx_state_fail")
	run := claimed(ctx, t, s, "worker-a")
	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusFailed, Error: "export refused",
		State: &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-28"}},
	}))

	st, err := s.GetState(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-27", st.Value["synced_through"], "a watermark never moves past work that did not happen")
	assert.Equal(t, int64(1), st.Revision)
	next := queued(ctx, t, s, sc, v, "dpx_state_next")
	assert.Equal(t, "2026-08-27", next.StateRead["synced_through"])
}

// TestRealDB_TwoRunsThatReadOneRevisionCannotBothWrite is the interleaving the
// compare-and-set exists for: a run_script call during a scheduled run, or a
// reclaimed run whose predecessor is still winding down. One writes N+1, the
// other fails naming it, and the loser's outputs stand.
func TestRealDB_TwoRunsThatReadOneRevisionCannotBothWrite(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, v := savedScript(ctx, t, s, "sync")

	a := queued(ctx, t, s, sc, v, "dpx_state_a")
	b := queued(ctx, t, s, sc, v, "dpx_state_b")
	claimedA := claimed(ctx, t, s, "worker-a")
	claimedB := claimed(ctx, t, s, "worker-b")
	require.Equal(t, a.ID, claimedA.ID)
	require.Equal(t, b.ID, claimedB.ID)
	require.Equal(t, claimedA.StateRevision, claimedB.StateRevision, "both read the same revision")
	require.NoError(t, s.RecordOutput(ctx, claimedB.Lease(), script.RunOutput{Name: "delta", AssetID: "asset_b", AssetVersion: 1}))

	require.NoError(t, s.Finish(ctx, claimedA.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{"mark": "a"}},
	}))
	require.NoError(t, s.Finish(ctx, claimedB.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{"mark": "b"}},
	}))

	doneA, err := s.GetRun(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusSucceeded, doneA.Status)
	assert.Equal(t, int64(1), doneA.StateRevisionWritten)

	doneB, err := s.GetRun(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusFailed, doneB.Status, "the second write is refused, not silently lost")
	assert.Contains(t, doneB.Error, "run "+a.ID, "the failure names the run that wrote")
	assert.Nil(t, doneB.StateWritten)
	assert.Zero(t, doneB.StateRevisionWritten)
	require.Len(t, doneB.Outputs, 1, "the loser's outputs stand: they were produced from the state it read")

	st, err := s.GetState(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", st.Value["mark"])
	assert.Equal(t, int64(1), st.Revision)
}

// TestRealDB_AResetUnderARunInFlightFailsItsWrite: the reset was after the
// run's premise, so the run fails at its write and the next run starts from
// the reset value.
func TestRealDB_AResetUnderARunInFlightFailsItsWrite(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, v := savedScript(ctx, t, s, "sync")
	_, err := s.SetState(ctx, sc.ID, map[string]any{"synced_through": "2026-08-01"}, "jane@example.com")
	require.NoError(t, err)

	inFlight := queued(ctx, t, s, sc, v, "dpx_state_inflight")
	run := claimed(ctx, t, s, "worker-a")
	require.Equal(t, int64(1), run.StateRevision)

	cleared, err := s.SetState(ctx, sc.ID, nil, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, int64(2), cleared.Revision)
	assert.Equal(t, "admin@example.com", cleared.WrittenBy())

	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-29"}},
	}))
	done, err := s.GetRun(ctx, inFlight.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusFailed, done.Status)
	assert.Contains(t, done.Error, "admin@example.com wrote revision 2")

	next := queued(ctx, t, s, sc, v, "dpx_state_after")
	assert.Equal(t, int64(2), next.StateRevision)
	assert.Equal(t, map[string]any{}, next.StateRead, "the next run starts from {}")
}

func TestRealDB_StateFollowsTheScript(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	sc, _ := savedScript(ctx, t, s, "sync")
	_, err := s.SetState(ctx, sc.ID, map[string]any{"synced_through": "2026-08-01"}, "jane@example.com")
	require.NoError(t, err)

	_, err = s.Transfer(ctx, script.TransferRequest{ID: sc.ID, NewOwnerEmail: "admin@example.com"}, script.Author{Email: "admin@example.com", Roles: []string{"admin"}})
	require.NoError(t, err)
	st, err := s.GetState(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), st.Revision, "a transfer keeps the state")

	require.NoError(t, s.Delete(ctx, sc.ID))
	var rows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM script_state WHERE script_id = $1`, sc.ID).Scan(&rows))
	assert.Zero(t, rows, "deleting the script deletes its state")
}
