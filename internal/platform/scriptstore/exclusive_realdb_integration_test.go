//go:build integration

package scriptstore

// The real-schema proof for #1986. A script set to run one at a time is held
// to one open run by a partial unique index, which no fake can stand in for:
// the claim is that two inserts at the same instant, from any trigger and any
// replica, cannot both land.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/openrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// setExclusive saves the script's exclusive setting through the store's
// ordinary update, the path manage_script and the portal take.
func setExclusive(ctx context.Context, s *Store, sc *script.Script, on bool) error {
	live, err := s.GetByID(ctx, sc.ID)
	if err != nil {
		return err
	}
	live.Exclusive = on
	return s.Update(ctx, live)
}

// toolRun is a run_script run of v with a caller-minted id.
func toolRun(sc *script.Script, v *script.Version, id string) *script.Run {
	return &script.Run{
		ID: id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
		Trigger: script.TriggerTool, RequestedBy: "jane@example.com",
	}
}

// openRuns counts the script's pending and running runs.
func openRuns(ctx context.Context, t *testing.T, s *Store, scriptID string) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM script_runs WHERE script_id = $1 AND status IN ('pending', 'running')`,
		scriptID).Scan(&n))
	return n
}

// TestRealDB_AnExclusiveScriptSkipsAFireWhileAToolRunIsOpen is criterion 1:
// the schedule fires while a run_script run is open, the fire is recorded as
// skipped_overlap, and the reason names the open run and what started it.
func TestRealDB_AnExclusiveScriptSkipsAFireWhileAToolRunIsOpen(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "backfill")
	require.NoError(t, setExclusive(ctx, s, sc, true))
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)

	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_backfill")))
	_, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)

	skip := fireRun(sc, v, sched, "dpx_hourly", fire)
	outcome, err := s.MaterializeRun(ctx, skip)
	require.NoError(t, err)
	require.Equal(t, script.MaterializedSkippedOverlap, outcome)

	recorded, err := s.GetRun(ctx, skip.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusSkippedOverlap, recorded.Status)
	assert.Contains(t, recorded.Error, "run dpx_backfill (started by run_script, running since")
	assert.Equal(t, 1, openRuns(ctx, t, s, sc.ID))
}

// TestRealDB_AnExclusiveScriptRefusesAToolRunWhileAScheduledRunIsOpen is
// criterion 2: run_script while a scheduled run is open is refused naming it,
// and nothing is queued. Once the open run ends, a run is queued again.
func TestRealDB_AnExclusiveScriptRefusesAToolRunWhileAScheduledRunIsOpen(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "hourly")
	require.NoError(t, setExclusive(ctx, s, sc, true))
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)
	outcome, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_fire", fire))
	require.NoError(t, err)
	require.Equal(t, script.MaterializedRun, outcome)

	err = s.Enqueue(ctx, toolRun(sc, v, "dpx_manual"))
	var openErr *openrun.Error
	require.ErrorAs(t, err, &openErr)
	assert.Equal(t, "dpx_fire", openErr.Open.ID)
	assert.Contains(t, err.Error(), "run dpx_fire (started by its schedule, queued since")
	_, err = s.GetRun(ctx, "dpx_manual")
	require.ErrorIs(t, err, script.ErrRunNotFound, "nothing was queued")

	claimed, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	err = s.Enqueue(ctx, toolRun(sc, v, "dpx_manual"))
	require.ErrorAs(t, err, &openErr)
	assert.Contains(t, err.Error(), "running since", "a running run is named as running")

	require.NoError(t, s.Finish(ctx, claimed.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_manual")), "the script runs again once the open run ends")
}

// TestRealDB_ANonExclusiveScriptBehavesAsBefore is criterion 3: without the
// setting, two run_script runs and a fire beside them are all queued.
func TestRealDB_ANonExclusiveScriptBehavesAsBefore(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "loose")
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)

	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_one")))
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_two")))
	outcome, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_fire", fire))
	require.NoError(t, err)
	assert.Equal(t, script.MaterializedRun, outcome)
	assert.Equal(t, 3, openRuns(ctx, t, s, sc.ID))
}

// TestRealDB_RacingRunsOfAnExclusiveScriptLeaveExactlyOneOpen is the
// enforcement claim: run_script calls and schedule fires arriving at the same
// instant on several replicas leave one open run, and every other caller is
// either refused naming a run or recorded as a skip.
func TestRealDB_RacingRunsOfAnExclusiveScriptLeaveExactlyOneOpen(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "racy")
	require.NoError(t, setExclusive(ctx, s, sc, true))
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)

	const racers = 8
	errs := make([]error, racers)
	outcomes := make([]script.Materialization, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				errs[i] = s.Enqueue(ctx, toolRun(sc, v, fmt.Sprintf("dpx_tool_%d", i)))
				return
			}
			outcomes[i], errs[i] = s.MaterializeRun(ctx,
				fireRun(sc, v, sched, fmt.Sprintf("dpx_fire_%d", i), fire.Add(time.Duration(i)*time.Minute)))
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if i%2 == 0 && err != nil {
			require.ErrorIs(t, err, openrun.ErrOpen, "racer %d", i)
		}
		if i%2 == 1 {
			require.NoError(t, err, "racer %d", i)
		}
	}
	assert.Equal(t, 1, openRuns(ctx, t, s, sc.ID), "one open run, whoever won")
}

// TestRealDB_TurningExclusiveOnStampsTheOpenRun covers changing the setting
// while runs are open: with two open it is refused and nothing changes; with
// one open, that run carries the setting and a second is refused naming it.
func TestRealDB_TurningExclusiveOnStampsTheOpenRun(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "toggle")
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_a")))
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_b")))

	err := setExclusive(ctx, s, sc, true)
	require.ErrorIs(t, err, openrun.ErrOpen)
	assert.Contains(t, err.Error(), openrun.BlockedMessage)
	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	assert.False(t, live.Exclusive, "a refused save changes nothing")

	_, _, err = s.CancelRun(ctx, "dpx_b", "jane@example.com")
	require.NoError(t, err)
	require.NoError(t, setExclusive(ctx, s, sc, true))

	err = s.Enqueue(ctx, toolRun(sc, v, "dpx_c"))
	var openErr *openrun.Error
	require.ErrorAs(t, err, &openErr)
	assert.Equal(t, "dpx_a", openErr.Open.ID, "the run open when the setting changed carries it")

	require.NoError(t, setExclusive(ctx, s, sc, false))
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_c")), "turning it off lets runs overlap again")
}

// TestRealDB_AReclaimedRunIsStillTheOneOpenRun pins the lease case: a run
// whose worker died is claimed again as the same row, so it neither counts as
// a second run nor stops counting as the first.
func TestRealDB_AReclaimedRunIsStillTheOneOpenRun(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "reclaim")
	require.NoError(t, setExclusive(ctx, s, sc, true))
	require.NoError(t, s.Enqueue(ctx, toolRun(sc, v, "dpx_lost")))

	crashed, err := s.Claim(ctx, "worker-a", 0, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	reclaimed, err := s.Claim(ctx, "worker-b", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	require.Equal(t, crashed.ID, reclaimed.ID)

	assert.Equal(t, 1, openRuns(ctx, t, s, sc.ID))
	err = s.Enqueue(ctx, toolRun(sc, v, "dpx_next"))
	var openErr *openrun.Error
	require.ErrorAs(t, err, &openErr)
	assert.Equal(t, "dpx_lost", openErr.Open.ID)
}

// TestRealDB_AScheduleSlowerThanItsIntervalNeverOverlaps is the regression
// test the ticket asks for, on both kinds of script: a schedule that fires
// every minute and a run that takes two. Every fire is materialized by
// several replicas at once; the fires that come due while the run is open
// are each recorded as one skip, and the next run starts at the first fire
// after the open run ends.
func TestRealDB_AScheduleSlowerThanItsIntervalNeverOverlaps(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		t.Run(fmt.Sprintf("exclusive=%v", exclusive), func(t *testing.T) {
			db := testdb.New(t)
			s := New(db)
			ctx := context.Background()

			sc, v := savedScript(ctx, t, s, "minutely")
			require.NoError(t, setExclusive(ctx, s, sc, exclusive))
			t0 := pastFire()
			sched := scheduledFor(ctx, t, s, sc, t0)

			fireAll := func(n int) []script.Materialization {
				fire := t0.Add(time.Duration(n) * time.Minute)
				const replicas = 4
				got := make([]script.Materialization, replicas)
				var wg sync.WaitGroup
				for r := range replicas {
					wg.Go(func() {
						var err error
						got[r], err = s.MaterializeRun(ctx, fireRun(sc, v, sched, fmt.Sprintf("dpx_%d_%d", n, r), fire))
						require.NoError(t, err)
					})
				}
				wg.Wait()
				return got
			}
			count := func(got []script.Materialization, want script.Materialization) int {
				n := 0
				for _, g := range got {
					if g == want {
						n++
					}
				}
				return n
			}

			require.Equal(t, 1, count(fireAll(0), script.MaterializedRun), "the first fire runs once")
			running, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
			require.NoError(t, err)

			for n := 1; n <= 2; n++ {
				got := fireAll(n)
				assert.Equal(t, 0, count(got, script.MaterializedRun), "fire %d starts nothing while the run is open", n)
				assert.Equal(t, 1, count(got, script.MaterializedSkippedOverlap), "fire %d is recorded once as a skip", n)
				assert.Equal(t, 1, openRuns(ctx, t, s, sc.ID))
			}

			require.NoError(t, s.Finish(ctx, running.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))
			assert.Equal(t, 1, count(fireAll(3), script.MaterializedRun), "the first fire after the run ends starts the next")

			runs, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID})
			require.NoError(t, err)
			skips := 0
			for _, r := range runs {
				if r.Status == script.RunStatusSkippedOverlap {
					skips++
					assert.Contains(t, r.Error, running.ID, "a skip names the run it waited on")
				}
			}
			assert.Equal(t, 2, skips, "the skips are visible in the run history")
		})
	}
}
