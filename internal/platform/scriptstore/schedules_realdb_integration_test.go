//go:build integration

package scriptstore

// The real-schema proof for #1286 scheduling. Every guarantee here is a
// PostgreSQL index, and none of them can be tested anywhere else: sqlmock
// returns whatever a test tells it to for an ON CONFLICT DO NOTHING, and an
// in-memory queue that "checks first" proves nothing about two processes
// inserting at the same instant. What makes a fire happen once is the unique
// index, so the unique index is what these tests exercise.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// scheduledFor stores a schedule for an approved script and returns it.
func scheduledFor(ctx context.Context, t *testing.T, s *Store, sc *script.Script, next time.Time) *script.Schedule {
	t.Helper()
	sched := &script.Schedule{
		ScriptID: sc.ID, CronSpec: "0 7 * * 1-5", Timezone: "America/Los_Angeles",
		Params:  map[string]any{"report_date": script.FireDateToken},
		Enabled: true, NextRunAt: next, CreatedBy: "jane@example.com", UpdatedBy: "jane@example.com",
	}
	require.NoError(t, s.SetSchedule(ctx, sched))
	require.NotEmpty(t, sched.ID)
	return sched
}

// pastFire is a fire time that has already come due, truncated to the second so
// it survives a round trip through a timestamptz unchanged. It is computed from
// the clock rather than written as a literal: a fixed future date would make
// every run these tests materialize unclaimable, since the queue's own
// predicate is scheduled_for <= NOW().
func pastFire() time.Time {
	return time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
}

// fireRun is the run one fire of sched produces, with a caller-minted id.
func fireRun(sc *script.Script, v *script.Version, sched *script.Schedule, id string, fire time.Time) *script.Run {
	return &script.Run{
		ID: id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
		ScheduleID: sched.ID, Trigger: script.TriggerSchedule,
		Params:   map[string]any{"report_date": "2026-08-14"},
		FireTime: fire, ScheduledFor: fire, RequestedBy: "jane@example.com",
	}
}

// TestRealDB_TwoRacingMaterializersProduceExactlyOneRun is the single-fire
// guarantee, which is the correctness claim the whole feature rests on: every
// worker replica materializes, so several notice the same fire at the same
// moment, and the unique index on (schedule_id, fire_time) is what collapses
// them to one run.
func TestRealDB_TwoRacingMaterializersProduceExactlyOneRun(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "daily")
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)

	const racers = 8
	outcomes := make([]script.Materialization, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcomes[i], errs[i] = s.MaterializeRun(ctx,
				fireRun(sc, v, sched, mintRunID(i), fire))
		}()
	}
	close(start)
	wg.Wait()

	materialized := 0
	for i := range racers {
		require.NoError(t, errs[i], "a lost race is a normal outcome, not an error")
		if outcomes[i] == script.MaterializedRun {
			materialized++
		} else {
			assert.Equal(t, script.MaterializedDuplicate, outcomes[i])
		}
	}
	assert.Equal(t, 1, materialized, "exactly one materializer creates the run")

	runs, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID})
	require.NoError(t, err)
	assert.Len(t, runs, 1, "and exactly one run row exists for the fire")
	assert.Equal(t, script.TriggerSchedule, runs[0].Trigger)
	assert.Equal(t, sched.ID, runs[0].ScheduleID)
}

// mintRunID returns a distinct run id per racer, since each materializer mints
// its own before it knows whether it will win.
func mintRunID(i int) string {
	return "dpx_race_" + string(rune('a'+i))
}

// TestRealDB_ARetryCannotUnpinTheFire is why the unique index is keyed on
// fire_time and not on scheduled_for. A run returned to the queue by an
// infrastructure retry gets a new scheduled_for; if the fire's identity moved
// with it, the next materializer would insert a second run for the same fire.
func TestRealDB_ARetryCannotUnpinTheFire(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "daily")
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)

	run := fireRun(sc, v, sched, "dpx_retry", fire)
	outcome, err := s.MaterializeRun(ctx, run)
	require.NoError(t, err)
	require.Equal(t, script.MaterializedRun, outcome)

	claimed, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	require.NoError(t, s.Retry(ctx, claimed.Lease(), runstate.AttemptRetried, "the warehouse was unreachable", time.Hour))

	moved, err := s.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.True(t, moved.ScheduledFor.After(fire), "the retry moved the due time")
	assert.True(t, moved.FireTime.Equal(fire), "and left the fire time exactly where it was")

	again, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_retry_2", fire))
	require.NoError(t, err)
	assert.Equal(t, script.MaterializedDuplicate, again,
		"the fire is still taken, however far its retry pushed the due time out")
}

// TestRealDB_AnOverlappingFireIsRecordedAsASkip is the overlap policy: the
// partial unique index on one open run per schedule refuses the second run, and
// the skip is written as its own terminal row so a reader of the history sees
// the fire that produced nothing.
func TestRealDB_AnOverlappingFireIsRecordedAsASkip(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "daily")
	first := pastFire()
	sched := scheduledFor(ctx, t, s, sc, first)

	outcome, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_first", first))
	require.NoError(t, err)
	require.Equal(t, script.MaterializedRun, outcome)

	// The next fire arrives while the first is still pending.
	second := first.Add(time.Minute)
	skipRun := fireRun(sc, v, sched, "dpx_second", second)
	outcome, err = s.MaterializeRun(ctx, skipRun)
	require.NoError(t, err)
	assert.Equal(t, script.MaterializedSkippedOverlap, outcome)

	recorded, err := s.GetRun(ctx, skipRun.ID)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusSkippedOverlap, recorded.Status)
	assert.NotNil(t, recorded.FinishedAt, "a skip is finished on arrival, so retention can sweep it")
	assert.Contains(t, recorded.Error, "run dpx_first (started by its schedule", "the skip names the run it waited on (#1986)")

	// And the skipped row does not itself hold the schedule open: once the
	// first run finishes, the fire after it materializes normally.
	claimed, err := s.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	require.Equal(t, "dpx_first", claimed.ID, "a skipped row is never claimed")
	require.NoError(t, s.Finish(ctx, claimed.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))

	third := second.Add(time.Minute)
	outcome, err = s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_third", third))
	require.NoError(t, err)
	assert.Equal(t, script.MaterializedRun, outcome)
}

// TestRealDB_ScheduleLifecycleAgainstTheRealSchema covers the writes sqlmock
// cannot vouch for: the replace keyed on the schedule's id and held to its
// script, a second schedule on one script (#2434), the delete, the
// conditional advance, and the due predicate reading a NULL next fire as
// parked.
func TestRealDB_ScheduleLifecycleAgainstTheRealSchema(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, _ := savedScript(ctx, t, s, "daily")
	due := time.Now().Add(-time.Minute)
	sched := scheduledFor(ctx, t, s, sc, due)

	t.Run("setting with its id replaces in place", func(t *testing.T) {
		replacement := &script.Schedule{
			ID: sched.ID, ScriptID: sc.ID, CronSpec: "@daily", Timezone: "UTC",
			Enabled: true, NextRunAt: due, UpdatedBy: "admin@example.com",
		}
		require.NoError(t, s.SetSchedule(ctx, replacement))
		assert.Equal(t, sched.ID, replacement.ID, "a replaced schedule keeps its identity")
		assert.Equal(t, "jane@example.com", replacement.CreatedBy, "and who created it")

		all, err := s.ListSchedules(ctx, script.ScheduleFilter{ScriptID: sc.ID})
		require.NoError(t, err)
		assert.Len(t, all, 1)
		assert.Equal(t, "@daily", all[0].CronSpec)
	})

	t.Run("an id read from another script's schedule changes nothing", func(t *testing.T) {
		other, _ := savedScript(ctx, t, s, "weekly")
		err := s.SetSchedule(ctx, &script.Schedule{
			ID: sched.ID, ScriptID: other.ID, CronSpec: "@hourly", Timezone: "UTC", UpdatedBy: "mallory@example.com",
		})
		require.ErrorIs(t, err, script.ErrScheduleNotFound)

		kept, err := s.ScheduleByID(ctx, sched.ID)
		require.NoError(t, err)
		assert.Equal(t, sc.ID, kept.ScriptID)
		assert.Equal(t, "@daily", kept.CronSpec)
	})

	t.Run("setting with no id adds a second schedule, and one with no runs deletes", func(t *testing.T) {
		evening := &script.Schedule{
			ScriptID: sc.ID, CronSpec: "0 19 * * *", Timezone: "UTC",
			Enabled: true, NextRunAt: time.Now().Add(30 * 24 * time.Hour), UpdatedBy: "jane@example.com",
		}
		require.NoError(t, s.SetSchedule(ctx, evening))
		require.NotEqual(t, sched.ID, evening.ID, "a script may hold several schedules")

		all, err := s.ListSchedules(ctx, script.ScheduleFilter{ScriptID: sc.ID})
		require.NoError(t, err)
		assert.Len(t, all, 2)

		require.NoError(t, s.DeleteSchedule(ctx, evening.ID))
		_, err = s.ScheduleByID(ctx, evening.ID)
		require.ErrorIs(t, err, script.ErrScheduleNotFound)
		require.ErrorIs(t, s.DeleteSchedule(ctx, evening.ID), script.ErrScheduleNotFound)
	})

	t.Run("the due query finds it", func(t *testing.T) {
		out, err := s.DueSchedules(ctx, time.Now(), 0)
		require.NoError(t, err)
		require.Len(t, out, 1)
		assert.Equal(t, sched.ID, out[0].ID)
	})

	t.Run("the advance is conditional on where the caller found it", func(t *testing.T) {
		next := time.Now().Add(time.Hour)
		moved, err := s.AdvanceSchedule(ctx, script.ScheduleAdvance{
			ID: sched.ID, From: due, Next: next, Fired: due, Missed: 3,
		})
		require.NoError(t, err)
		require.True(t, moved)

		// A second replica that walked the same fire finds the row moved.
		moved, err = s.AdvanceSchedule(ctx, script.ScheduleAdvance{
			ID: sched.ID, From: due, Next: next, Fired: due, Missed: 3,
		})
		require.NoError(t, err)
		assert.False(t, moved, "the misses are counted once, not once per replica")

		after, err := s.ScheduleByID(ctx, sched.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, after.MissedFires)
		require.NotNil(t, after.LastFireAt)
	})

	t.Run("disabling takes it out of the due set and keeps the row", func(t *testing.T) {
		require.NoError(t, s.EnableSchedule(ctx, sched.ID, false, "admin@example.com"))
		out, err := s.DueSchedules(ctx, time.Now().Add(48*time.Hour), 0)
		require.NoError(t, err)
		assert.Empty(t, out)

		still, err := s.ScheduleByID(ctx, sched.ID)
		require.NoError(t, err)
		assert.False(t, still.Enabled, "the schedule that explains past runs is still there")
	})

	t.Run("a parked schedule is never due", func(t *testing.T) {
		require.NoError(t, s.EnableSchedule(ctx, sched.ID, true, "admin@example.com"))
		moved, err := s.AdvanceSchedule(ctx, script.ScheduleAdvance{
			ID: sched.ID, From: mustNextRunAt(ctx, t, s, sched.ID),
		})
		require.NoError(t, err)
		require.True(t, moved)

		out, err := s.DueSchedules(ctx, time.Now().Add(365*24*time.Hour), 0)
		require.NoError(t, err)
		assert.Empty(t, out, "a NULL next fire is parked, not perpetually overdue")
	})
}

// mustNextRunAt reads a schedule's current next fire.
func mustNextRunAt(ctx context.Context, t *testing.T, s *Store, scheduleID string) time.Time {
	t.Helper()
	sched, err := s.ScheduleByID(ctx, scheduleID)
	require.NoError(t, err)
	return sched.NextRunAt
}

// TestRealDB_DeletingAScriptRemovesItsScheduleAndRuns pins the foreign-key
// choice: schedule_id is NO ACTION so a schedule that produced runs cannot be
// deleted on its own, while deleting the SCRIPT — whose cascade removes the
// schedule and the runs in one statement — still works. RESTRICT would have
// blocked it.
func TestRealDB_DeletingAScriptRemovesItsScheduleAndRuns(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "daily")
	fire := pastFire()
	sched := scheduledFor(ctx, t, s, sc, fire)
	_, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, "dpx_1", fire))
	require.NoError(t, err)

	require.ErrorIs(t, s.DeleteSchedule(ctx, sched.ID), ErrScheduleHasRuns,
		"a schedule its runs name is refused on its own (#2434)")
	kept, err := s.ScheduleByID(ctx, sched.ID)
	require.NoError(t, err, "and the row stays")
	assert.Equal(t, sched.ID, kept.ID)

	removed, delErr := s.Delete(ctx, sc.ID)
	require.NoError(t, delErr, "a script with a schedule and run history is still deletable")
	assert.True(t, removed.Handlers, "the delete reports the handlers it took (#1593, #2441)")
	assert.True(t, removed.Runs, "the delete reports the run history it took (#1593)")

	_, err = s.ScheduleByID(ctx, sched.ID)
	assert.ErrorIs(t, err, script.ErrScheduleNotFound)
	runs, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID})
	require.NoError(t, err)
	assert.Empty(t, runs)
}

// TestRealDB_TwoSchedulesOfOneScriptFireOnTheirOwn is the several-schedules
// guarantee (#2434): each schedule of a script fires on its own, so two fires
// at the same instant are two runs, each carrying the id of the schedule
// that produced it, and neither is taken for the other's duplicate.
func TestRealDB_TwoSchedulesOfOneScriptFireOnTheirOwn(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc, v := savedScript(ctx, t, s, "daily")
	fire := pastFire()
	morning := scheduledFor(ctx, t, s, sc, fire)
	evening := &script.Schedule{
		ScriptID: sc.ID, CronSpec: "0 19 * * *", Timezone: "UTC",
		Enabled: true, NextRunAt: fire, UpdatedBy: "jane@example.com",
	}
	require.NoError(t, s.SetSchedule(ctx, evening))

	due, err := s.DueSchedules(ctx, time.Now(), 0)
	require.NoError(t, err)
	assert.Len(t, due, 2, "both schedules of the script are due")

	for i, sched := range []*script.Schedule{morning, evening} {
		outcome, err := s.MaterializeRun(ctx, fireRun(sc, v, sched, mintRunID(i), fire))
		require.NoError(t, err)
		assert.Equal(t, script.MaterializedRun, outcome, "schedule %s fires on its own", sched.ID)
	}

	runs, err := s.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID})
	require.NoError(t, err)
	require.Len(t, runs, 2)
	got := []string{runs[0].ScheduleID, runs[1].ScheduleID}
	assert.ElementsMatch(t, []string{morning.ID, evening.ID}, got, "each run keeps its schedule's id")
}
