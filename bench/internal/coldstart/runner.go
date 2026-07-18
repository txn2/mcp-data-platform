package coldstart

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/curriculum"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// insightPollInterval is the delay between capture-verification polls (memory
// capture is synchronous, so an insight is usually visible on the first poll).
const insightPollInterval = 250 * time.Millisecond

// AdapterFactory builds the model adapter for one episode, keyed by unit (lesson
// or task id) and stage so the scripted adapter can play a per-episode script.
type AdapterFactory func(unitID, stage string) (llm.Adapter, error)

// Options configures a cold-start run.
type Options struct {
	Target        target.Target
	HTTPTimeout   time.Duration
	Arm           string // a3 (the lifecycle-and-search arm)
	K             int    // fresh evaluator identities per checkpoint (default 1)
	CurriculumDir string
	TasksDir      string
	TranscriptDir string
	Factory       AdapterFactory
	// ClaudeCLI, when non-nil, runs each episode through a real `claude -p`
	// client instead of the in-process agent loop. Factory is unused in this mode.
	ClaudeCLI     *claudecli.Runner
	ClientVersion string
	LLMProvider   string
	GitCommit     string
	AuditTimeout  time.Duration
	// SinkTimeout bounds the reviewer's post-apply sink read-back (zero uses the
	// promote package default); raise it for a store that serves reads slowly.
	SinkTimeout time.Duration
	// IdentityKeys is the identity-pool size the arm config defines. A run refuses
	// to start when the lessons + per-checkpoint evaluators exceed the pool. It
	// must be positive: the teacher and every checkpoint's evaluators must be
	// distinct identities so the curve measures promoted (shared) knowledge, never
	// an evaluator's own capture.
	IdentityKeys int
	// Settle is the pause between a successful promote and the following eval
	// checkpoint. The a3 arm caches table context with a 5m TTL, so a cache entry
	// populated by the PREVIOUS checkpoint's evaluators can serve the stale
	// pre-promotion description to the next checkpoint's evaluators,
	// nondeterministically attenuating the datahub-sink lift; waiting out the TTL
	// removes that pacing dependence. The scripted smoke sets it to zero.
	Settle time.Duration
	// SettleSleep overrides the settle pause's sleeper. Tests inject a recorder
	// so no test real-sleeps; nil uses a context-aware real sleep.
	SettleSleep func(ctx context.Context, d time.Duration) error
	// OnCheckpoint, if set, is called after every checkpoint with the aggregated
	// results so far. benchrun wires it to flush the results file, so a run that
	// spends real API budget always leaves every completed checkpoint on disk.
	OnCheckpoint func(*Results)
	Log          *slog.Logger
}

// Run drives the curriculum against an empty enrichment layer and returns the
// learning curve. A harness-level failure in any episode is recorded and
// surfaced in the returned error so the process exits nonzero.
func Run(ctx context.Context, opts Options) (*Results, error) {
	cur, evalTasks, err := load(opts)
	if err != nil {
		return nil, err
	}
	if opts.K <= 0 {
		opts.K = 1
	}
	if err := guardPool(cur, opts); err != nil {
		return nil, err
	}
	res := &Results{Manifest: Manifest{
		StartedAt: time.Now().UTC(), GitCommit: opts.GitCommit, Target: opts.Target.BaseURL,
		Arm: opts.Arm, LLMProvider: opts.LLMProvider, Seed: gen.Seed,
		CurriculumID: cur.ID, CurriculumHash: curriculum.Hash([]curriculum.Curriculum{cur}),
		EvalSuite: cur.EvalSuite, TaskSetHash: task.Hash(evalTasks), K: opts.K,
		Settle: settleLabel(opts.Settle),
	}}
	life := lifecycleapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout))
	env := &runEnv{
		opts:     opts,
		log:      opts.Log,
		audit:    auditapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout)),
		life:     life,
		reviewer: promote.Reviewer{Life: life, Log: opts.Log, SinkTimeout: opts.SinkTimeout},
	}

	// Refuse a contaminated baseline before any LLM episode is spent: a
	// non-empty starting state cannot be restored by re-seeding and would
	// publish a silently wrong curve (see preflight).
	if err := env.preflight(ctx, cur); err != nil {
		return nil, err
	}

	failures := env.run(ctx, cur, evalTasks, res)
	res.Manifest.FinishedAt = time.Now().UTC()
	res.Manifest.PlatformVersion = env.platformVersion
	res.Manifest.Model = env.model
	res.Manifest.ClientVersion = opts.ClientVersion
	res.Aggregate()
	if failures > 0 {
		return res, fmt.Errorf("%d cold-start episode(s) failed at the harness level; see lessons[].error and checkpoints[].attempts[].error", failures)
	}
	return res, nil
}

// load reads the single curriculum and the fixed eval task set.
func load(opts Options) (curriculum.Curriculum, []task.Task, error) {
	curricula, err := curriculum.Load(opts.CurriculumDir)
	if err != nil {
		return curriculum.Curriculum{}, nil, err
	}
	if len(curricula) != 1 {
		return curriculum.Curriculum{}, nil, fmt.Errorf("cold-start expects exactly one curriculum in %s, found %d", opts.CurriculumDir, len(curricula))
	}
	cur := curricula[0]
	evalTasks, err := loadEvalTasks(opts.TasksDir, cur.EvalSuite, opts.Arm)
	if err != nil {
		return curriculum.Curriculum{}, nil, err
	}
	return cur, evalTasks, nil
}

// loadEvalTasks loads the fixed eval set: the curriculum's suite, applicable to
// the arm, deterministically graded (exec_sql is rejected — the cold-start eval
// loop has no SQL executor, and the S3 suite it targets is numeric/entity only).
func loadEvalTasks(dir, suite, arm string) ([]task.Task, error) {
	all, err := task.Load(dir)
	if err != nil {
		return nil, err
	}
	var out []task.Task
	for _, t := range all {
		if t.Suite != suite || !t.AppliesTo(arm) {
			continue
		}
		if t.Grading.Kind == task.GradeExecSQL {
			return nil, fmt.Errorf("eval task %s uses exec_sql grading, unsupported by the cold-start eval loop", t.ID)
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no eval tasks for suite %q applicable to arm %q in %s", suite, arm, dir)
	}
	return out, nil
}

// guardPool refuses to start when the identities the run needs exceed the pool.
func guardPool(cur curriculum.Curriculum, opts Options) error {
	if opts.IdentityKeys <= 0 {
		return errors.New("cold-start requires an identity pool (-identity-keys > 0): the teacher and every checkpoint's evaluators must be distinct identities")
	}
	n := len(cur.Lessons)
	need := maxIdentitySeq(n, opts.K)
	if need > opts.IdentityKeys {
		return fmt.Errorf("%d identities needed (%d teachers + k=%d evaluators over %d checkpoints) exceed the pool of %d; raise -identity-keys and the config pool",
			need, n, opts.K, n+1, opts.IdentityKeys)
	}
	return nil
}

// teacherSeq is the pool sequence for lesson i's teacher (a distinct identity
// per lesson so capture verification is cleanly scoped to that lesson's insight).
func teacherSeq(lessonIndex int) int { return lessonIndex + 1 }

// evaluatorSeq is the pool sequence for checkpoint c's repeat r evaluator. The
// teachers occupy 1..n, so evaluators start at n+1 and never collide with a
// teacher or another checkpoint's evaluator.
func evaluatorSeq(checkpointIndex, repeat, lessonCount, k int) int {
	return lessonCount + checkpointIndex*k + repeat
}

// maxIdentitySeq is the highest pool sequence a run touches: the last repeat of
// the last checkpoint's evaluators (checkpoints are 0..n, so n+1 of them).
func maxIdentitySeq(lessonCount, k int) int {
	return evaluatorSeq(lessonCount, k, lessonCount, k)
}

// runEnv holds per-run clients and mutable manifest carry-overs.
type runEnv struct {
	opts     Options
	log      *slog.Logger
	audit    *auditapi.Client
	life     *lifecycleapi.Client
	reviewer promote.Reviewer

	platformVersion string
	model           string
}

// run executes the baseline checkpoint, then each lesson's teach+promote
// followed by a fresh eval checkpoint, returning the count of harness failures.
func (e *runEnv) run(ctx context.Context, cur curriculum.Curriculum, evalTasks []task.Task, res *Results) int {
	failures := 0
	n := len(cur.Lessons)

	base := e.evalCheckpoint(ctx, 0, curriculum.Lesson{}, evalTasks, n, 0)
	res.Checkpoints = append(res.Checkpoints, base)
	failures += base.HarnessFailures
	e.flush(res)

	promoted := 0
	for i, lesson := range cur.Lessons {
		lr := e.teachAndPromote(ctx, lesson, teacherSeq(i))
		if lr.Error != "" {
			failures++
		}
		if boolTrue(lr.Promoted) {
			promoted++
		}
		res.Lessons = append(res.Lessons, lr)
		// Flush the paid-for lesson record BEFORE the settle pause: an
		// interruption during the settle (or the following checkpoint) must never
		// discard a completed teach episode and its promote outcome.
		e.flush(res)
		e.settleAfterPromote(ctx, lr)

		cp := e.evalCheckpoint(ctx, i+1, lesson, evalTasks, n, promoted)
		res.Checkpoints = append(res.Checkpoints, cp)
		failures += cp.HarnessFailures
		e.flush(res)
	}
	return failures
}

// settleAfterPromote waits out the semantic-cache TTL between a successful
// promote and the following eval checkpoint, so a table-context entry cached by
// the previous checkpoint's evaluators can never serve the stale pre-promotion
// description to the next ones. Only a datahub-sink promote needs it: the cache
// holds DataHub table context, while knowledge-page hits are served live from
// the portal store with no TTL. A lesson that did not promote changed nothing.
// Every skip is logged so a paced run's timeline stays auditable.
func (e *runEnv) settleAfterPromote(ctx context.Context, lr LessonRecord) {
	if e.opts.Settle <= 0 {
		return
	}
	if !boolTrue(lr.Promoted) {
		e.log.Info("cold-start settle skipped: lesson did not promote, the enrichment layer is unchanged",
			"lesson", lr.LessonID, "settle", e.opts.Settle)
		return
	}
	if lr.Sink != protocol.SinkDataHub {
		e.log.Info("cold-start settle skipped: page-sink promotes are served live from the portal store, nothing is cached",
			"lesson", lr.LessonID, "settle", e.opts.Settle)
		return
	}
	e.log.Info("cold-start settle: waiting out the semantic-cache TTL before the next eval checkpoint",
		"lesson", lr.LessonID, "settle", e.opts.Settle)
	if err := e.sleep(ctx, e.opts.Settle); err != nil {
		e.log.Warn("cold-start settle interrupted", "error", err)
	}
}

// settleLabel renders the settle window for the manifest ("" when disabled).
func settleLabel(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// sleep pauses for d respecting ctx cancellation, via the injected test sleeper
// when set.
func (e *runEnv) sleep(ctx context.Context, d time.Duration) error {
	if e.opts.SettleSleep != nil {
		return e.opts.SettleSleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// teachAndPromote runs the lesson's teach episode, verifies capture through the
// insights API, and promotes the insight to its sink. A harness failure lands in
// Error; a missed capture or refused apply is a measured miss (Captured/Promoted
// false, no error).
func (e *runEnv) teachAndPromote(ctx context.Context, lesson curriculum.Lesson, seq int) LessonRecord {
	lr := LessonRecord{LessonID: lesson.ID, Title: lesson.Title, TrapClass: lesson.TrapClass, Sink: lesson.Sink}
	// The teach start (minus a skew margin) bounds capture verification to THIS
	// run: teacher identities and URNs are deterministic, so an unbounded read
	// could match a pending insight left by an interrupted prior run.
	teachStart := time.Now()
	rec := e.runEpisode(ctx, episodeSpec{
		stage: StageTeach, unitID: lesson.ID, seq: seq,
		prompt: lesson.Teach.Prompt, system: teachScaffold, budget: lesson.BudgetToolCalls,
	})
	lr.Episode = teachEpisode(rec)
	if rec.err != "" {
		lr.Error = rec.err
		return lr
	}
	since := teachStart.Add(-promote.CaptureSkewMargin)
	insight, err := promote.WaitForInsight(ctx, e.life, pool.Email(seq), lesson.EntityURN, since, e.opts.AuditTimeout, insightPollInterval)
	if err != nil {
		lr.Error = "capture verify: " + err.Error()
		return lr
	}
	captured := insight != nil
	lr.Captured = &captured
	if !captured {
		return lr
	}
	lr.InsightID = insight.ID

	session, handle, err := e.adminSession(ctx)
	if err != nil {
		lr.Error = "admin session: " + err.Error()
		return lr
	}
	defer func() { _ = session.Close() }()
	ok, err := e.reviewer.Apply(ctx, session, handle, promoteTarget(lesson), insight.ID)
	if err != nil {
		lr.Error = "promote: " + err.Error()
		return lr
	}
	lr.Promoted = &ok
	return lr
}

// promoteTarget maps a lesson onto the shared promotion target.
func promoteTarget(l curriculum.Lesson) promote.Target {
	return promote.Target{Label: l.ID, EntityURN: l.EntityURN, Sink: l.Sink, Fact: l.Fact, Page: l.Page, Notes: "bench cold-start promote"}
}

// teachEpisode maps a teach episode's raw result into the report record.
func teachEpisode(rec episodeResult) EpisodeRecord {
	return EpisodeRecord{
		Email: rec.email, SessionID: rec.sessionID, ToolCalls: rec.toolCalls, ToolErrors: rec.toolErrors,
		WallMS: rec.wallMS, InputTokens: rec.usage.InputTokens, OutputTokens: rec.usage.OutputTokens,
		CacheReadTokens: rec.usage.CacheReadInputTokens, CacheCreationTokens: rec.usage.CacheCreationInputTokens,
		Audit: rec.audit, AuditReadError: rec.auditReadErr, Error: rec.err,
	}
}

// evalCheckpoint runs the fixed eval set with k fresh evaluators and aggregates
// the checkpoint. Each evaluator answers every eval task in its own session.
func (e *runEnv) evalCheckpoint(ctx context.Context, index int, lesson curriculum.Lesson, evalTasks []task.Task, lessonCount, promotedSoFar int) Checkpoint {
	cp := Checkpoint{Index: index, PromotedSoFar: promotedSoFar}
	if lesson.ID != "" {
		cp.LessonID, cp.LessonTitle, cp.TrapClass = lesson.ID, lesson.Title, lesson.TrapClass
	}
	for r := 1; r <= e.opts.K; r++ {
		seq := evaluatorSeq(index, r, lessonCount, e.opts.K)
		for _, t := range evalTasks {
			cp.Attempts = append(cp.Attempts, e.evalAttempt(ctx, t, seq, r))
		}
	}
	cp.aggregate()
	return cp
}

// evalAttempt runs one eval task as a fresh evaluator and grades it.
func (e *runEnv) evalAttempt(ctx context.Context, t task.Task, seq, repeat int) EvalAttempt {
	rec := e.runEpisode(ctx, episodeSpec{
		stage: StageEval, unitID: t.ID, seq: seq,
		prompt: t.Prompt, system: evalSystem(t.Grading.Kind), budget: t.BudgetToolCalls,
	})
	att := EvalAttempt{
		TaskID: t.ID, TrapClasses: t.TrapClasses, Email: rec.email, SessionID: rec.sessionID,
		Repeat: repeat, MemoryWrites: rec.memoryWrites, FinalAnswer: rec.finalAnswer, WallMS: rec.wallMS,
		InputTokens: rec.usage.InputTokens, OutputTokens: rec.usage.OutputTokens,
		CacheReadTokens: rec.usage.CacheReadInputTokens, CacheCreationTokens: rec.usage.CacheCreationInputTokens,
		Audit: rec.audit, AuditReadError: rec.auditReadErr,
	}
	if rec.err != "" {
		att.Error = rec.err
		return att
	}
	att.Graded = true
	att.Correct = gradeEval(rec.finalAnswer, t.Grading)
	return att
}

// flush aggregates and calls OnCheckpoint so an interruption never discards
// completed, paid-for work.
func (e *runEnv) flush(res *Results) {
	if e.opts.OnCheckpoint == nil {
		return
	}
	res.Aggregate()
	e.opts.OnCheckpoint(res)
}

// attemptClient builds the MCP client for one episode, authenticating as the
// pool identity.
func (e *runEnv) attemptClient(seq int) *mcpc.Client {
	t := e.opts.Target
	t.Credential = pool.Credential(t.Credential, seq, e.opts.IdentityKeys)
	return mcpc.New(t.BaseURL, t.HTTPClient(e.opts.HTTPTimeout))
}

// adminSession connects a FRESH reviewer MCP session (base admin credential, no
// rotation) and mints its handle; the caller owns the session and must Close
// it. Each promotion and the preflight open their own short-lived session
// deliberately: a session cached for the whole run goes stale during a
// real-paced eval checkpoint (~50 minutes idle between promotes) and the
// streamable transport does not reconnect — the first completed real run lost
// every promotion to exactly that ("failed to connect", then "client is
// closing" on all later applies). The scripted smoke cannot catch staleness
// (its checkpoints take seconds), so freshness per use is the structural fix.
func (e *runEnv) adminSession(ctx context.Context) (*mcp.ClientSession, string, error) {
	client := mcpc.New(e.opts.Target.BaseURL, e.opts.Target.HTTPClient(e.opts.HTTPTimeout))
	session, err := client.Connect(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("admin session connect: %w", err)
	}
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, "", fmt.Errorf("admin session mint: %w", err)
	}
	e.recordPlatformVersion(info.PlatformVersion)
	return session, info.Handle, nil
}

// recordPlatformVersion captures the platform version once.
func (e *runEnv) recordPlatformVersion(v string) {
	if e.platformVersion == "" && v != "" {
		e.platformVersion = v
	}
}

// recordModel captures the model name once.
func (e *runEnv) recordModel(m string) {
	if e.model == "" && m != "" {
		e.model = m
	}
}
