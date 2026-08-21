package scriptexec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// Materializer defaults.
const (
	// defaultMaterializeEvery is how often due schedules are looked for. It is
	// the resolution of the whole feature — a fire is materialized within this
	// of its due time — and cron's own granularity is a minute, so half a
	// minute keeps the delay under one tick of what a schedule can express.
	defaultMaterializeEvery = 30 * time.Second

	// schedulePrincipal is the actor recorded when the platform itself changes
	// a schedule, which it does in exactly one case: parking a schedule whose
	// cron expression no longer parses.
	schedulePrincipal = "platform"

	// maxDuePerPass bounds how many due schedules one pass walks, matching the
	// store's own cap so a full batch is recognizable as one.
	maxDuePerPass = 100
)

// logKeyScheduleID is the structured-logging key for a schedule id.
const logKeyScheduleID = "schedule_id"

// schedulerConfig is what materializing due schedules needs.
type schedulerConfig struct {
	schedules script.ScheduleStore
	scripts   ScriptReader
	versions  VersionReader
	// wake nudges the local run worker after a run is materialized, so a fire
	// starts executing now rather than on the worker's next poll.
	wake func()
	// metrics counts the fires the misfire policy steps over, so a schedule
	// that is quietly not keeping its cadence is visible without reading the
	// table (#1307). Nil is a no-op.
	metrics *observability.Metrics
	// interval overrides defaultMaterializeEvery, and now overrides the clock.
	// Both are testing hooks.
	interval time.Duration
	now      func() time.Time
}

// scheduler turns due schedules into runs.
//
// It is not a scheduler in the usual sense: it holds no cron timers, keeps no
// state between passes, and decides nothing about what executes. It computes
// which fire has come due and writes a run row for it; the queue's own claim
// predicate does the rest. That is why it can run on every worker replica at
// once with no leader election — the correctness of a fire is the unique index
// the insert conflicts against, not the process that noticed it.
//
// It runs where the worker runs. Materializing on a replica that never claims
// would put runs on the queue from a pod that cannot execute them, which is
// exactly the split a worker-off replica is asking not to be part of.
type scheduler struct {
	cfg      schedulerConfig
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  bool
}

// newScheduler builds a materializer, applying defaults for zero config
// values. It returns nil when there is no schedule store, which is how a
// deployment with no database expresses "no scheduling" without a second flag.
func newScheduler(cfg schedulerConfig) *scheduler {
	if cfg.schedules == nil || cfg.scripts == nil || cfg.versions == nil {
		return nil
	}
	if cfg.interval <= 0 {
		cfg.interval = defaultMaterializeEvery
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &scheduler{cfg: cfg, stopCh: make(chan struct{})}
}

// Start runs the materialization loop until Stop. Nil-safe and idempotent.
//
// The first pass runs one interval in rather than at startup. A replica coming
// up during a rolling deploy has nothing to add — the schedules it would walk
// are the same ones every other replica is walking — and waiting keeps the
// database work off the boot path.
func (s *scheduler) Start(ctx context.Context) {
	if s == nil || s.started {
		return
	}
	s.started = true
	s.wg.Go(func() {
		ticker := time.NewTicker(s.cfg.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.pass(ctx)
			}
		}
	})
}

// Stop ends the loop and waits for a pass in flight. Nil-safe and idempotent.
//
// The wait is unbounded by design and costs nothing: a pass is a handful of
// short statements against the schedule table, with no interpreter in it. What
// a pass produces is a queue row, and a queue row outlives the process that
// wrote it.
func (s *scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

// pass materializes every schedule that has come due.
func (s *scheduler) pass(ctx context.Context) {
	now := s.cfg.now()
	due, err := s.cfg.schedules.DueSchedules(ctx, now, 0)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("scripts: reading due schedules failed", logKeyError, err)
		}
		return
	}
	if len(due) >= maxDuePerPass {
		// The batch is full, so there are probably more. Nothing is lost — the
		// due query is ordered by fire time, so the oldest are served first and
		// the rest arrive on the next tick — but a deployment whose schedules
		// no longer fit in one pass is running later than it thinks, and that
		// is worth saying rather than leaving to be inferred.
		slog.Info("scripts: the schedule pass filled its batch; the remainder waits for the next tick",
			"schedules", len(due), "interval", s.cfg.interval)
	}
	for i := range due {
		select {
		case <-s.stopCh:
			return
		default:
		}
		s.materialize(ctx, &due[i], now)
	}
}

// materialize walks one due schedule to its current fire and writes the run.
//
// The order is deliberate: the run is written FIRST and the schedule is moved
// forward afterwards. A process that dies between them leaves the schedule due,
// so the next pass recomputes the same fire and inserts it again — where the
// unique index answers "already materialized" and the pass moves on. The
// reverse order would lose the fire outright.
func (s *scheduler) materialize(ctx context.Context, sched *script.Schedule, now time.Time) {
	cronSpec, err := script.ParseCron(sched.CronSpec, sched.Timezone)
	if err != nil {
		s.refuseCadence(ctx, sched, err)
		return
	}
	fire := sched.NextFire(cronSpec, now)
	if !fire.Due {
		// Nothing to fire: either the walk is still catching up after a long
		// gap, or the expression has run out of fires. Either way the schedule
		// moves and the misses are recorded.
		s.advance(ctx, sched, script.ScheduleAdvance{
			ID: sched.ID, From: sched.NextRunAt, Next: fire.Next, Missed: fire.Missed,
		})
		return
	}
	adv := script.ScheduleAdvance{ID: sched.ID, From: sched.NextRunAt, Next: fire.Next, Missed: fire.Missed}
	if run := s.buildRun(ctx, sched, fire.At, cronSpec); run != nil {
		if !s.insert(ctx, sched, run) {
			// The write failed rather than conflicted, so this fire has not
			// been recorded anywhere. The schedule is deliberately NOT advanced
			// past it: leaving it due is what makes the next pass try again,
			// where advancing would discard a fire the platform simply failed
			// to write.
			return
		}
		// Only a fire that produced a row stamps last_fire_at. A refused fire
		// leaving that stamp behind would show a schedule as having last run at
		// a moment it ran nothing, which is the exact question the field exists
		// to answer.
		adv.Fired = fire.At
	} else {
		// The fire was not executable. It is counted as missed rather than
		// dropped silently, so a schedule attached to a script nothing may
		// execute reads as a schedule that is not producing anything.
		adv.Missed++
	}
	s.advance(ctx, sched, adv)
}

// buildRun assembles the run one fire produces, or nil when the fire must not
// produce one. Every refusal is logged, because a schedule that stops producing
// without saying why is the failure this whole surface exists to prevent.
func (s *scheduler) buildRun(ctx context.Context, sched *script.Schedule, fire time.Time, cronSpec script.Cron) *script.Run {
	sc, v, err := s.current(ctx, sched.ScriptID)
	if err != nil {
		refuse(sched, err)
		return nil
	}
	params, err := script.BindScheduleParams(v.Params, sched.Params, fire, cronSpec.Location())
	if err != nil {
		refuse(sched, fmt.Errorf("its bound parameters no longer satisfy the script's contract: %w", err))
		return nil
	}
	runID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		refuse(sched, fmt.Errorf("minting a run id failed: %w", err))
		return nil
	}
	run := &script.Run{
		ID: runID, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
		ScheduleID: sched.ID, Trigger: script.TriggerSchedule, Params: params,
		// The schedule's author is who asked for this cadence, and so is who
		// asked for every run of it. Attribution of the EXECUTION is separate:
		// the run authenticates as the script's own principal, presenting the
		// version author's captured roles.
		RequestedBy: sched.CreatedBy,
		FireTime:    fire, ScheduledFor: fire,
	}
	// The one gate, asked the same question the worker will ask it again at
	// execution. Asking here too keeps a schedule from filling the queue with
	// runs that are certain to be refused.
	if refusal := script.RefuseRun(sc); refusal != nil {
		refuse(sched, refusal)
		return nil
	}
	return run
}

// current resolves the script a schedule names and its latest saved version,
// which is the version a fire executes.
func (s *scheduler) current(ctx context.Context, scriptID string) (*script.Script, *script.Version, error) {
	sc, err := s.cfg.scripts.GetByID(ctx, scriptID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the script failed: %w", err)
	}
	if sc == nil {
		return nil, nil, errors.New("the script this schedule belongs to no longer exists")
	}
	v, err := s.cfg.versions.GetVersion(ctx, sc.ID, sc.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the script's current version failed: %w", err)
	}
	if v == nil {
		return nil, nil, errors.New("the script's current version is missing from its history")
	}
	return sc, v, nil
}

// insert writes the run and reports whether the fire was recorded — as a run,
// as a visible skip, or by the replica that got there first. Only a write that
// failed outright returns false.
func (s *scheduler) insert(ctx context.Context, sched *script.Schedule, run *script.Run) bool {
	outcome, err := s.cfg.schedules.MaterializeRun(ctx, run)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("scripts: materializing a scheduled run failed",
				logKeyScheduleID, logsan.SanitizeForLog(sched.ID), logKeyError, err)
		}
		return false
	}
	switch outcome {
	case script.MaterializedRun:
		slog.Info("scripts: schedule fired", logKeyScheduleID, logsan.SanitizeForLog(sched.ID),
			logKeyRunID, run.ID, "fire_time", run.FireTime)
		if s.cfg.wake != nil {
			s.cfg.wake()
		}
	case script.MaterializedSkippedOverlap:
		slog.Warn("scripts: schedule fire skipped; the previous run is still going",
			logKeyScheduleID, logsan.SanitizeForLog(sched.ID), "fire_time", run.FireTime)
	case script.MaterializedDuplicate:
		// Another replica materialized this fire. The expected outcome of
		// running a materializer everywhere, and not worth a log line.
	}
	return true
}

// advance moves the schedule forward, tolerating the loss of the race to do so.
func (s *scheduler) advance(ctx context.Context, sched *script.Schedule, adv script.ScheduleAdvance) {
	// Counted on the way past, whether or not the row moves: a missed fire is a
	// fire that did not happen either way, and the write below is bookkeeping.
	//
	// Labeled with the script's NAME, matching what the worker records — the
	// same label carrying an id here and a name there would split one script's
	// series in two on every chart that groups by it.
	if adv.Missed > 0 {
		s.cfg.metrics.RecordScriptMissedFires(ctx, s.scriptLabel(ctx, sched.ScriptID), adv.Missed)
	}
	if _, err := s.cfg.schedules.AdvanceSchedule(ctx, adv); err != nil && ctx.Err() == nil {
		slog.Warn("scripts: advancing a schedule failed",
			logKeyScheduleID, logsan.SanitizeForLog(sched.ID), logKeyError, err)
	}
}

// scriptLabel resolves a script's name for a metric label, falling back to its
// id when the read fails: a missed fire is worth counting under a less readable
// label rather than not counting it.
func (s *scheduler) scriptLabel(ctx context.Context, scriptID string) string {
	sc, err := s.cfg.scripts.GetByID(ctx, scriptID)
	if err != nil || sc == nil {
		return scriptID
	}
	return sc.Name
}

// refuse logs a fire that produced no run.
func refuse(sched *script.Schedule, reason error) {
	slog.Warn("scripts: a schedule came due and produced no run", // #nosec G706 -- structured slog call; ids sanitized
		logKeyScheduleID, logsan.SanitizeForLog(sched.ID),
		"script_id", logsan.SanitizeForLog(sched.ScriptID),
		"reason", logsan.SanitizeForLog(reason.Error()))
}

// refuseCadence handles a schedule whose timing cannot be computed, and decides
// which of the two causes it is — because the right response is opposite.
//
// A zone that will not load is a property of the BINARY, not of the schedule:
// the IANA database is compiled in, so a build that omits it fails every named
// zone at once. Disabling those schedules would turn one deployment fault into
// a fleet of silently retired automations that nothing re-enables when the
// build is fixed. It is logged and left alone, so the next pass tries again.
//
// An expression that will not parse is a property of the schedule, and is
// parked (below).
func (s *scheduler) refuseCadence(ctx context.Context, sched *script.Schedule, cause error) {
	if errors.Is(cause, script.ErrUnknownTimezone) {
		slog.Error("scripts: a schedule names a timezone this build cannot load; it is left alone until the deployment is fixed", // #nosec G706 -- structured slog call; ids sanitized
			logKeyScheduleID, logsan.SanitizeForLog(sched.ID),
			"timezone", logsan.SanitizeForLog(sched.Timezone),
			logKeyError, logsan.SanitizeForLog(cause.Error()))
		return
	}
	s.park(ctx, sched, cause)
}

// park disables a schedule whose cron expression no longer parses.
//
// It is the one change the platform makes to a schedule on its own, and it is
// preferable to both alternatives: leaving it enabled means walking the same
// unparseable row every half minute forever, and skipping it quietly means a
// schedule that reads as on while producing nothing. Disabled is a state the
// owner can see and correct.
func (s *scheduler) park(ctx context.Context, sched *script.Schedule, cause error) {
	slog.Error("scripts: disabling a schedule whose cron expression no longer parses", // #nosec G706 -- structured slog call; ids sanitized
		logKeyScheduleID, logsan.SanitizeForLog(sched.ID),
		"script_id", logsan.SanitizeForLog(sched.ScriptID),
		logKeyError, logsan.SanitizeForLog(cause.Error()))
	if err := s.cfg.schedules.SetScheduleEnabled(ctx, sched.ScriptID, false, schedulePrincipal); err != nil && ctx.Err() == nil {
		slog.Warn("scripts: disabling an unparseable schedule failed",
			logKeyScheduleID, logsan.SanitizeForLog(sched.ID), logKeyError, err)
	}
}
