//go:build integration

package scriptstore

// The real-schema proof for the run queue. The claim cannot be proved anywhere
// else: sqlmock will happily return whatever rows a test hands it for an
// UPDATE ... FOR UPDATE SKIP LOCKED, so a unit test of Claim asserts a string,
// not a behavior. Only a real Postgres shows that two concurrent workers take
// different runs, that a lease expiring makes a run claimable again, and that
// a worker whose run was reclaimed can no longer write to it.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// savedScript creates a script and returns it with its v1 snapshot — the state
// a run needs to exist against, since a saved script runs without review.
func savedScript(ctx context.Context, t *testing.T, s *Store, name string) (*script.Script, *script.Version) {
	t.Helper()
	sc := newScript(name, "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	version, err := s.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	require.NotNil(t, version)

	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	return live, version
}

// TestRealDB_SavedVersionCarriesTheRunIdentity pins what a run presents against
// the real schema: creating a script makes it active immediately, and its
// snapshot records the author's roles — the authority a run of that version
// executes with, which the persona filter then bounds at run time.
func TestRealDB_SavedVersionCarriesTheRunIdentity(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	live, version := savedScript(ctx, t, s, "daily")

	assert.Equal(t, script.StatusActive, live.Status, "a saved script is active with no review step")
	assert.NoError(t, script.RefuseRun(live), "and the run gate admits it")
	assert.Equal(t, script.VersionStatusApplied, version.Status)
	assert.Equal(t, testAuthor.Email, version.Author)
	assert.Equal(t, testAuthor.Roles, version.AuthorRoles,
		"the snapshot carries the author's roles, which a run presents")

	// The queue stores a version id, and only the id identifies one immutable
	// snapshot for the life of the script; the worker's read must resolve it.
	loaded, err := s.GetVersionByID(ctx, version.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, version.Version, loaded.Version)
}

// TestRealDB_ClaimIsExclusiveAcrossWorkers proves the property the whole
// multi-replica story rests on: two workers claiming at the same moment take
// different runs, and neither blocks on the other.
func TestRealDB_ClaimIsExclusiveAcrossWorkers(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	for _, id := range []string{"dpx_a", "dpx_b"} {
		require.NoError(t, s.Enqueue(ctx, &script.Run{
			ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
			Trigger: script.TriggerTool, RequestedBy: "jane@example.com",
		}))
	}

	first, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	second, err := s.Claim(ctx, "worker-b", time.Minute)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID, "two workers must not take the same run")

	_, err = s.Claim(ctx, "worker-c", time.Minute)
	assert.ErrorIs(t, err, script.ErrNoWork, "a leased run is not due for anyone else")
}

// TestRealDB_ExpiredLeaseIsReclaimedAndFencesTheOldWorker is crashed-worker
// recovery end to end: the run becomes claimable again on its own, and the
// worker that lost it can no longer write its result over the new holder's.
func TestRealDB_ExpiredLeaseIsReclaimedAndFencesTheOldWorker(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_a", ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool,
	}))

	// A zero lease expires the instant it is taken, which is the state a worker
	// that died mid-run leaves behind.
	crashed, err := s.Claim(ctx, "worker-a", 0)
	require.NoError(t, err)

	reclaimed, err := s.Claim(ctx, "worker-b", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, crashed.ID, reclaimed.ID)
	assert.Equal(t, crashed.Attempt+1, reclaimed.Attempt, "reclaiming counts as another attempt")

	err = s.Finish(ctx, crashed.Lease(), script.RunResult{Status: script.RunStatusSucceeded})
	assert.ErrorIs(t, err, script.ErrLeaseLost,
		"the worker that lost the lease must not overwrite the result of the one that took over")

	require.NoError(t, s.Finish(ctx, reclaimed.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded, Log: "done",
	}))
	final, err := s.GetRun(ctx, "dpx_a")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusSucceeded, final.Status)
	assert.Equal(t, "done", final.Log)
	require.NotNil(t, final.FinishedAt)
}

// TestRealDB_RecordOutputAppendsAndRetryRequeues covers the two writes a run
// makes on its way through: the output list it grows as it writes, and the
// return to the queue an infrastructure failure produces.
func TestRealDB_RecordOutputAppendsAndRetryRequeues(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_a", ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool,
	}))
	run, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)

	require.NoError(t, s.RecordOutput(ctx, run.Lease(), script.RunOutput{
		Name: "daily", Destination: script.DestinationPortal,
		AssetID: "asset_1", AssetVersion: 1, Format: "csv", RowCount: 2, Bytes: 40,
	}))
	require.NoError(t, s.RecordOutput(ctx, run.Lease(), script.RunOutput{
		Name: "weekly", Destination: script.DestinationPortal,
		AssetID: "asset_2", AssetVersion: 3, Format: "json",
	}))
	// The same name delivered to a bucket is a different write, and the row has
	// to carry both: this is the pair the reclaim guard reads.
	require.NoError(t, s.RecordOutput(ctx, run.Lease(), script.RunOutput{
		Name: "daily", Destination: "acme-drop",
		Bucket: "acme-exports", Key: "weekly/daily.csv", Format: "csv", Bytes: 40,
	}))

	stored, err := s.GetRun(ctx, "dpx_a")
	require.NoError(t, err)
	require.Len(t, stored.Outputs, 3, "each output is recorded as it lands, not all at the end")
	require.NotNil(t, stored.Output("daily", script.DestinationPortal))
	assert.Equal(t, "asset_1", stored.Output("daily", script.DestinationPortal).AssetID)
	require.NotNil(t, stored.Output("daily", "acme-drop"))
	assert.Equal(t, "weekly/daily.csv", stored.Output("daily", "acme-drop").Key)

	require.NoError(t, s.Retry(ctx, run.Lease(), "trino unreachable", 0))
	requeued, err := s.GetRun(ctx, "dpx_a")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusPending, requeued.Status)
	assert.Equal(t, "trino unreachable", requeued.Error)
	require.Len(t, requeued.Outputs, 3, "a requeued run remembers what it already wrote")
}

// TestRealDB_PurgeLeavesLiveWorkAlone pins the retention predicate against real
// rows: a finished run past retention goes, a pending one never does.
func TestRealDB_PurgeLeavesLiveWorkAlone(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	for _, id := range []string{"dpx_done", "dpx_pending"} {
		require.NoError(t, s.Enqueue(ctx, &script.Run{
			ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
			Trigger: script.TriggerTool,
		}))
	}
	done, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, s.Finish(ctx, done.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))

	purged, err := s.PurgeRuns(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), purged)

	_, err = s.GetRun(ctx, done.ID)
	assert.ErrorIs(t, err, script.ErrRunNotFound)

	survivors, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID})
	require.NoError(t, err)
	require.Len(t, survivors, 1, "a pending run is live work and is never swept")
	assert.Equal(t, script.RunStatusPending, survivors[0].Status)
}

// TestRealDB_LatestRunsIsOneRowPerScript is the portal listing's read against
// the real schema: DISTINCT ON is Postgres-specific and sqlmock validates no
// SQL, so only a real database shows that each script yields its newest run and
// that a script asked about but never run is simply absent.
func TestRealDB_LatestRunsIsOneRowPerScript(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	daily, dailyVersion := savedScript(ctx, t, s, "daily")
	weekly, _ := savedScript(ctx, t, s, "weekly")
	for _, id := range []string{"dpx_old", "dpx_new"} {
		require.NoError(t, s.Enqueue(ctx, &script.Run{
			ID: id, ScriptID: daily.ID, VersionID: dailyVersion.ID, Version: dailyVersion.Version,
			Trigger: script.TriggerTool,
		}))
	}

	latest, err := s.LatestRuns(ctx, []string{daily.ID, weekly.ID})
	require.NoError(t, err)
	require.Len(t, latest, 1, "a script that has never run contributes no row")
	assert.Equal(t, "dpx_new", latest[daily.ID].ID, "the newest run wins")
}

// TestRealDB_RunHoldsItsVersionInPlace exercises the ON DELETE RESTRICT foreign
// key from a run to the version it executed: run history that cannot name the
// code it ran is not history.
func TestRealDB_RunHoldsItsVersionInPlace(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_a", ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool,
	}))

	_, err := db.ExecContext(ctx, `DELETE FROM script_versions WHERE id = $1`, version.ID)
	assert.Error(t, err, "a version a run executed must not be deletable")
}

// TestRealDB_RetryMovesTheDueTimeAndNeverTheFireTime is the determinism
// property under a retry: a run pushed out by an infrastructure failure must
// still compute the report it was created for.
func TestRealDB_RetryMovesTheDueTimeAndNeverTheFireTime(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_a", ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool,
	}))
	run, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	fireTime := run.FireTime

	require.NoError(t, s.Retry(ctx, run.Lease(), "trino unreachable", time.Hour))
	requeued, err := s.GetRun(ctx, "dpx_a")
	require.NoError(t, err)

	assert.Equal(t, fireTime, requeued.FireTime, "the fire time is pinned at creation")
	assert.True(t, requeued.ScheduledFor.After(requeued.FireTime),
		"the due time moved out by the backoff")

	// A run that is not due is not claimable, which is what makes the backoff a
	// backoff rather than an immediate retry storm.
	_, err = s.Claim(ctx, "worker-b", time.Minute)
	assert.ErrorIs(t, err, script.ErrNoWork)
}

// TestRealDB_DeletingAScriptTakesItsRunsWithIt covers the interaction between
// two foreign keys that RESTRICT would have broken: run history holds a version
// in place, and deleting the whole script must still work because the same
// statement removes the runs that hold it.
func TestRealDB_DeletingAScriptTakesItsRunsWithIt(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, version := savedScript(ctx, t, s, "daily")
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: "dpx_a", ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool,
	}))

	require.NoError(t, s.Delete(ctx, sc.ID), "a script with run history must still be deletable")
	_, err := s.GetRun(ctx, "dpx_a")
	assert.ErrorIs(t, err, script.ErrRunNotFound)
}
