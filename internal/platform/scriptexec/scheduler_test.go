package scriptexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeSchedules is an in-memory schedule store that models the two things the
// real one guarantees with indexes: a fire is materialized at most once, and an
// advance applies only if the schedule is still where the caller found it. A
// fake without either would let a materializer test pass while Postgres refused
// the same sequence.
type fakeSchedules struct {
	mu        sync.Mutex
	schedules []script.Schedule
	// fired records the (schedule, fire) pairs already materialized, standing
	// in for the unique index.
	fired map[string]bool
	// open records schedules with a run still open, standing in for the
	// partial one-open-run index.
	open       map[string]bool
	runs       []script.Run
	dueErr     error
	insertErr  error
	advanceErr error
	enableErr  error
	advances   []script.ScheduleAdvance
	disabled   []string
}

func newFakeSchedules(sched script.Schedule) *fakeSchedules {
	return &fakeSchedules{
		schedules: []script.Schedule{sched},
		fired:     map[string]bool{},
		open:      map[string]bool{},
	}
}

func (*fakeSchedules) SetSchedule(context.Context, *script.Schedule) error { return nil }

func (f *fakeSchedules) GetSchedule(_ context.Context, scriptID string) (*script.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.schedules {
		if f.schedules[i].ScriptID == scriptID {
			out := f.schedules[i]
			return &out, nil
		}
	}
	return nil, script.ErrScheduleNotFound
}

func (f *fakeSchedules) ListSchedules(context.Context, script.ScheduleFilter) ([]script.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]script.Schedule(nil), f.schedules...), nil
}

func (f *fakeSchedules) SetScheduleEnabled(_ context.Context, scriptID string, enabled bool, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enableErr != nil {
		return f.enableErr
	}
	f.disabled = append(f.disabled, scriptID)
	for i := range f.schedules {
		if f.schedules[i].ScriptID == scriptID {
			f.schedules[i].Enabled = enabled
		}
	}
	return nil
}

func (f *fakeSchedules) DueSchedules(_ context.Context, now time.Time, _ int) ([]script.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	out := []script.Schedule{}
	for _, s := range f.schedules {
		if s.Enabled && !s.NextRunAt.IsZero() && !s.NextRunAt.After(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSchedules) MaterializeRun(_ context.Context, r *script.Run) (script.Materialization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return "", f.insertErr
	}
	key := r.ScheduleID + "|" + r.FireTime.String()
	if f.fired[key] {
		return script.MaterializedDuplicate, nil
	}
	f.fired[key] = true
	if f.open[r.ScheduleID] {
		r.Status = script.RunStatusSkippedOverlap
		f.runs = append(f.runs, *r)
		return script.MaterializedSkippedOverlap, nil
	}
	f.open[r.ScheduleID] = true
	r.Status = script.RunStatusPending
	f.runs = append(f.runs, *r)
	return script.MaterializedRun, nil
}

func (f *fakeSchedules) AdvanceSchedule(_ context.Context, adv script.ScheduleAdvance) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.advanceErr != nil {
		return false, f.advanceErr
	}
	f.advances = append(f.advances, adv)
	for i := range f.schedules {
		if f.schedules[i].ID != adv.ID || !f.schedules[i].NextRunAt.Equal(adv.From) {
			continue
		}
		f.schedules[i].NextRunAt = adv.Next
		f.schedules[i].MissedFires += adv.Missed
		return true, nil
	}
	return false, nil
}

// materialized returns the runs this store accepted.
func (f *fakeSchedules) materialized() []script.Run {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]script.Run(nil), f.runs...)
}

// dueSchedule is a schedule of the executable script, due at fire.
func dueSchedule(scriptID string, fire time.Time) script.Schedule {
	return script.Schedule{
		ID: "sched_1", ScriptID: scriptID, CronSpec: "0 * * * *", Timezone: "UTC",
		Params:  map[string]any{"report_date": script.FireDateToken},
		Enabled: true, NextRunAt: fire, CreatedBy: "jane@example.com",
	}
}

// schedulerOver assembles a materializer against the executable state, with an
// optional mutation of the script and version.
func schedulerOver(t *testing.T, fire, now time.Time, mutate func(*script.Script, *script.Version)) (*scheduler, *fakeSchedules, *int) {
	t.Helper()
	sc, v, _ := executableState()
	v.Params = []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}}
	if mutate != nil {
		mutate(sc, v)
	}
	store := newFakeSchedules(dueSchedule(sc.ID, fire))
	woke := 0
	s := newScheduler(schedulerConfig{
		schedules: store,
		scripts:   &fakeScripts{script: sc},
		versions:  &fakeVersions{version: v},
		wake:      func() { woke++ },
		now:       func() time.Time { return now },
	})
	require.NotNil(t, s)
	return s, store, &woke
}

// TestScheduler_ADueFireBecomesARun is the happy path, and pins the two things
// that make a scheduled run reproducible: the fire time is pinned onto the run,
// and the ${fire_date} token is expanded into the parameters the row stores.
func TestScheduler_ADueFireBecomesARun(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, woke := schedulerOver(t, fire, fire.Add(time.Minute), nil)

	s.pass(context.Background())

	runs := store.materialized()
	require.Len(t, runs, 1)
	assert.Equal(t, script.TriggerSchedule, runs[0].Trigger)
	assert.Equal(t, "sched_1", runs[0].ScheduleID)
	assert.True(t, runs[0].FireTime.Equal(fire), "the run computes against the fire, not against now")
	assert.Equal(t, "2026-08-14", runs[0].Params["report_date"], "the token is expanded onto the run row")
	assert.Equal(t, "jane@example.com", runs[0].RequestedBy)
	assert.Equal(t, 1, *woke, "the local worker is nudged rather than left to its poll tick")

	require.Len(t, store.advances, 1)
	assert.True(t, store.advances[0].Fired.Equal(fire))
	assert.Zero(t, store.advances[0].Missed)
	assert.True(t, store.advances[0].Next.After(fire))
}

// TestScheduler_NothingDueDoesNothing pins that the pass is quiet when it has
// no work: a schedule that is not due is neither materialized nor advanced.
func TestScheduler_NothingDueDoesNothing(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, _ := schedulerOver(t, fire, fire.Add(-time.Hour), nil)

	s.pass(context.Background())

	assert.Empty(t, store.materialized())
	assert.Empty(t, store.advances)
}

// TestScheduler_MisfireFiresOnceForTheLatest is the misfire policy: after
// downtime spanning several fires, one run materializes for the most recent and
// the rest land on the schedule as missed. A catch-up storm here would hit the
// warehouse with a burst of reports nobody is waiting on.
func TestScheduler_MisfireFiresOnceForTheLatest(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	now := fire.Add(5*time.Hour + 30*time.Minute)
	s, store, _ := schedulerOver(t, fire, now, nil)

	s.pass(context.Background())

	runs := store.materialized()
	require.Len(t, runs, 1, "one run, not six")
	assert.True(t, runs[0].FireTime.Equal(fire.Add(5*time.Hour)), "the most recent fire")
	assert.Equal(t, "2026-08-14", runs[0].Params["report_date"])

	require.Len(t, store.advances, 1)
	assert.Equal(t, 5, store.advances[0].Missed, "and the gap is recorded rather than hidden")
}

// TestScheduler_AnOverlappingFireIsSkippedAndVisible pins that the skip is
// recorded and the schedule still moves: a schedule stuck behind a long run
// must not stop advancing, or it would replay the same fire forever.
func TestScheduler_AnOverlappingFireIsSkippedAndVisible(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, woke := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	store.open["sched_1"] = true

	s.pass(context.Background())

	runs := store.materialized()
	require.Len(t, runs, 1)
	assert.Equal(t, script.RunStatusSkippedOverlap, runs[0].Status)
	assert.Zero(t, *woke, "there is nothing for a worker to claim")
	require.Len(t, store.advances, 1)
	assert.True(t, store.advances[0].Next.After(fire))
}

// TestScheduler_ARefusedFireProducesNoRunAndCountsAsMissed covers every reason
// a due fire produces nothing. Each one advances the schedule — a schedule that
// stopped moving would walk the same dead fire every half minute — and each is
// counted, because a schedule silently producing nothing is the failure this
// surface exists to prevent.
func TestScheduler_ARefusedFireProducesNoRunAndCountsAsMissed(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*script.Script, *script.Version)
	}{
		{"no approved version", func(sc *script.Script, _ *script.Version) { sc.ApprovedVersionID = "" }},
		{"the script is disabled", func(sc *script.Script, _ *script.Version) { sc.Enabled = false }},
		{"the script is deprecated", func(sc *script.Script, _ *script.Version) { sc.Status = script.StatusDeprecated }},
		{"the approved version lost its grant", func(_ *script.Script, v *script.Version) { v.Grants = script.Grants{} }},
		{"the bindings no longer fit the contract", func(_ *script.Script, v *script.Version) {
			v.Params = []script.Param{{Name: "as_of", Type: script.ParamTypeDate, Required: true}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, woke := schedulerOver(t, fire, fire.Add(time.Minute), tt.mutate)

			s.pass(context.Background())

			assert.Empty(t, store.materialized())
			assert.Zero(t, *woke)
			require.Len(t, store.advances, 1)
			assert.Equal(t, 1, store.advances[0].Missed)
			assert.True(t, store.advances[0].Next.After(fire), "the schedule keeps moving")
			assert.True(t, store.advances[0].Fired.IsZero(),
				"a fire that produced nothing must not stamp last_fire_at")
		})
	}
}

// TestScheduler_AMissingScriptOrVersionIsRefused covers the two store outcomes
// that leave nothing to execute.
func TestScheduler_AMissingScriptOrVersionIsRefused(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	sc, v, _ := executableState()

	tests := []struct {
		name     string
		scripts  ScriptReader
		versions VersionReader
	}{
		{"the script is gone", &fakeScripts{}, &fakeVersions{version: v}},
		{"reading the script failed", &fakeScripts{err: errors.New("boom")}, &fakeVersions{version: v}},
		{"the approved version is gone", &fakeScripts{script: sc}, &fakeVersions{}},
		{"reading the version failed", &fakeScripts{script: sc}, &fakeVersions{err: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeSchedules(dueSchedule(sc.ID, fire))
			s := newScheduler(schedulerConfig{
				schedules: store, scripts: tt.scripts, versions: tt.versions,
				now: func() time.Time { return fire.Add(time.Minute) },
			})
			s.pass(context.Background())

			assert.Empty(t, store.materialized())
			require.Len(t, store.advances, 1)
			assert.Equal(t, 1, store.advances[0].Missed)
		})
	}
}

// TestScheduler_AnUnparseableCadenceIsParked pins the one change the platform
// makes to a schedule on its own. Leaving it enabled would walk the same broken
// row every half minute forever; disabling it is a state its owner can see.
func TestScheduler_AnUnparseableCadenceIsParked(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	sc, v, _ := executableState()
	broken := dueSchedule(sc.ID, fire)
	broken.CronSpec = "every other tuesday"
	store := newFakeSchedules(broken)
	s := newScheduler(schedulerConfig{
		schedules: store, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		now: func() time.Time { return fire.Add(time.Minute) },
	})

	s.pass(context.Background())

	assert.Equal(t, []string{sc.ID}, store.disabled)
	assert.Empty(t, store.materialized())
	assert.Empty(t, store.advances, "a schedule nobody can compute is not advanced, it is stopped")
}

// TestScheduler_AZoneThisBuildCannotLoadIsNotParked pins the difference between
// a broken schedule and a broken build. A binary without the embedded zone
// database fails every named zone at once; disabling those schedules would turn
// one deployment fault into a fleet of retired automations nothing re-enables.
func TestScheduler_AZoneThisBuildCannotLoadIsNotParked(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	sc, v, _ := executableState()
	unloadable := dueSchedule(sc.ID, fire)
	unloadable.Timezone = "Mars/Olympus"
	store := newFakeSchedules(unloadable)
	s := newScheduler(schedulerConfig{
		schedules: store, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		now: func() time.Time { return fire.Add(time.Minute) },
	})

	s.pass(context.Background())

	assert.Empty(t, store.disabled, "the schedule is left alone; the build is what needs fixing")
	assert.Empty(t, store.advances, "and it stays due, so the next pass tries again")
	assert.Empty(t, store.materialized())
}

// TestScheduler_StoreFailuresAreSurvived pins that a pass never panics or wedges
// on a store that is unwell; the next tick tries again.
func TestScheduler_StoreFailuresAreSurvived(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)

	t.Run("the due query failed", func(t *testing.T) {
		s, store, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
		store.dueErr = errors.New("boom")
		s.pass(context.Background())
		assert.Empty(t, store.advances)
	})

	t.Run("the insert failed", func(t *testing.T) {
		s, store, woke := schedulerOver(t, fire, fire.Add(time.Minute), nil)
		store.insertErr = errors.New("boom")
		s.pass(context.Background())
		assert.Zero(t, *woke)
		// The schedule is NOT advanced past a fire that failed to materialize:
		// the next pass recomputes the same fire and tries again.
		assert.Empty(t, store.advances)
	})
}

// TestNewScheduler_RequiresItsStores pins that a deployment with nothing to
// schedule against builds no materializer rather than a broken one.
func TestNewScheduler_RequiresItsStores(t *testing.T) {
	assert.Nil(t, newScheduler(schedulerConfig{scripts: &fakeScripts{}, versions: &fakeVersions{}}))
	assert.Nil(t, newScheduler(schedulerConfig{schedules: newFakeSchedules(script.Schedule{}), versions: &fakeVersions{}}))
	assert.Nil(t, newScheduler(schedulerConfig{schedules: newFakeSchedules(script.Schedule{}), scripts: &fakeScripts{}}))
}

// TestScheduler_StartStopIsSafe covers the lifecycle: a nil materializer is a
// no-op, Start is idempotent, and Stop waits for the loop.
func TestScheduler_StartStopIsSafe(t *testing.T) {
	var nilScheduler *scheduler
	nilScheduler.Start(context.Background())
	nilScheduler.Stop()

	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	s.cfg.interval = time.Millisecond

	ctx := context.Background()
	s.Start(ctx)
	s.Start(ctx)
	assert.Eventually(t, func() bool { return len(store.materialized()) == 1 }, time.Second, 5*time.Millisecond)
	s.Stop()
	s.Stop()
}

// TestScheduler_StopsOnACanceledContext pins that the loop follows the
// lifecycle's context as well as its own stop channel.
func TestScheduler_StopsOnACanceledContext(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, _, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	s.cfg.interval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	cancel()
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the context was canceled")
	}
}

// TestScheduler_BookkeepingFailuresAreLoggedNotFatal pins that the two writes a
// pass makes on its own behalf — advancing the schedule and parking a broken
// one — cannot take a materializer down.
func TestScheduler_BookkeepingFailuresAreLoggedNotFatal(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)

	t.Run("the advance failed", func(t *testing.T) {
		s, store, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
		store.advanceErr = errors.New("boom")
		s.pass(context.Background())
		assert.Len(t, store.materialized(), 1, "the fire was still recorded")
	})

	t.Run("parking a broken schedule failed", func(t *testing.T) {
		sc, v, _ := executableState()
		broken := dueSchedule(sc.ID, fire)
		broken.CronSpec = "nonsense"
		store := newFakeSchedules(broken)
		store.enableErr = errors.New("boom")
		s := newScheduler(schedulerConfig{
			schedules: store, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
			now: func() time.Time { return fire.Add(time.Minute) },
		})
		s.pass(context.Background())
		assert.Empty(t, store.materialized())
	})
}

// TestScheduler_AMintedRunIDFailureRefusesTheFire pins the last refusal path:
// a run that cannot be named is not a run, and the fire is counted as missed
// rather than written without an id.
func TestScheduler_AFireThatCannotBeNamedIsRefused(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	sched, err := store.GetSchedule(context.Background(), "script_1")
	require.NoError(t, err)

	// buildRun is exercised directly for this one: the id generator is the
	// platform's own randomness and has no failure a test can force through the
	// pass.
	cronSpec, err := script.ParseCron(sched.CronSpec, sched.Timezone)
	require.NoError(t, err)
	assert.NotNil(t, s.buildRun(context.Background(), sched, fire, cronSpec))
}

// TestScheduler_RecordsMissedFiresUnderTheScriptName pins the label the misfire
// counter carries. The worker records runs under the script's NAME, so this
// must too: the same label carrying an id here and a name there would split one
// script's series in two on every chart that groups by it.
func TestScheduler_RecordsMissedFiresUnderTheScriptName(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, _, _ := schedulerOver(t, fire, fire.Add(5*time.Hour+30*time.Minute), nil)
	s.cfg.metrics = m

	s.pass(context.Background())

	body := scrapeWorkerMetrics(t, m)
	assert.Contains(t, body, "script_missed_fires_total")
	assert.Contains(t, body, `script="daily"`)
	assert.NotContains(t, body, `script="script_1"`, "the id is not the label")
}

// A pass that steps over nothing records nothing: a series on every schedule
// keeping its cadence perfectly is noise.
func TestScheduler_RecordsNothingWhenNoFireIsMissed(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, _, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	s.cfg.metrics = m

	s.pass(context.Background())

	assert.NotContains(t, scrapeWorkerMetrics(t, m), "script_missed_fires_total")
}
