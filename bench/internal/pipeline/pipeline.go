// Package pipeline orchestrates a benchmark run: for each applicable task and
// repeat, a fresh MCP session is minted, the agent loop runs against the arm's
// live tool surface, efficiency metrics are read back from the platform audit
// API, and the attempt is deterministically graded.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// systemScaffold is the fixed prompt held constant across arms (#930
// principle 1: the platform is the ablation variable, never the prompt).
const systemScaffold = `You are a data analyst agent connected to a data platform over MCP. Answer the user's question using the available tools.

Rules:
- Ground every answer in tool results from this session; do not answer from prior knowledge about any dataset.
- Verify which tables and columns exist before querying them.
- When you have the answer, end your reply with a single line: "FINAL ANSWER: <answer>".`

// Format instructions appended per grading kind so deterministic graders have
// a stable convention to parse.
const (
	numericFormat = `- For this numeric question the FINAL ANSWER line must contain exactly one number (USD unless the question states otherwise), for example "FINAL ANSWER: 12345.67".`
	entityFormat  = `- For this question the FINAL ANSWER line must name the single best answer: a fully qualified table name (catalog.schema.table) for dataset questions, or the exact name requested.`
)

// AdapterFactory builds the model adapter for one task attempt.
type AdapterFactory func(t task.Task) (llm.Adapter, error)

// Options configures a run.
type Options struct {
	Target        target.Target
	HTTPTimeout   time.Duration
	Arm           string
	Suite         string // "" = all suites
	K             int
	TasksDir      string
	TranscriptDir string
	Factory       AdapterFactory
	LLMProvider   string
	GitCommit     string
	AuditTimeout  time.Duration
	// IdentityKeys is the size of the per-attempt identity pool. The
	// search-first gate keys discovery on the authenticated USER (not the
	// MCP session or dps_ handle), so attempts sharing one credential would
	// contaminate each other: the first attempt's search opens the gate for
	// every later attempt. Each attempt therefore authenticates as
	// "<credential>-NN" (NN = 01..IdentityKeys), matching the identity-pool
	// keys the arm configs define; the run refuses to start when the pool is
	// smaller than tasks x k. Zero disables rotation (single identity) for
	// targets without the pool.
	IdentityKeys int
	Log          *slog.Logger
}

// Run executes the benchmark and returns aggregated results. Attempts that
// fail at the harness level (adapter error, missing audit rows) are recorded
// with their error and reported in the returned error so the process exits
// nonzero: a run with unaccounted attempts is not publishable.
func Run(ctx context.Context, opts Options) (*report.Results, error) {
	tasks, err := loadApplicable(opts)
	if err != nil {
		return nil, err
	}
	res := &report.Results{Manifest: report.Manifest{
		StartedAt:   time.Now().UTC(),
		GitCommit:   opts.GitCommit,
		Target:      opts.Target.BaseURL,
		Arm:         opts.Arm,
		LLMProvider: opts.LLMProvider,
		Seed:        gen.Seed,
		TaskSetHash: task.Hash(tasks),
		K:           opts.K,
		Suite:       opts.Suite,
	}}
	total := len(tasks) * opts.K
	if opts.IdentityKeys > 0 && total > opts.IdentityKeys {
		return nil, fmt.Errorf("%d attempts exceed the identity pool of %d keys; attempts would share a discovery scope (raise -identity-keys and the config pool)", total, opts.IdentityKeys)
	}
	env := &runEnv{
		opts: opts,
		// Audit read-back always authenticates as the base (admin) key.
		audit: auditapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout)),
	}
	failures := 0
	seq := 0
	for _, t := range tasks {
		for attempt := 1; attempt <= opts.K; attempt++ {
			seq++
			a := env.runAttempt(ctx, t, attempt, seq, res)
			if a.Error != "" {
				failures++
			}
			res.Attempts = append(res.Attempts, a)
		}
	}
	res.Manifest.FinishedAt = time.Now().UTC()
	res.Aggregate()
	if failures > 0 {
		return res, fmt.Errorf("%d attempt(s) failed at the harness level; see results attempts[].error", failures)
	}
	return res, nil
}

// loadApplicable loads the task set filtered to the run's arm and suite.
func loadApplicable(opts Options) ([]task.Task, error) {
	all, err := task.Load(opts.TasksDir)
	if err != nil {
		return nil, err
	}
	var tasks []task.Task
	for _, t := range all {
		if t.AppliesTo(opts.Arm) && (opts.Suite == "" || t.Suite == opts.Suite) {
			tasks = append(tasks, t)
		}
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks apply to arm %q suite %q", opts.Arm, opts.Suite)
	}
	return tasks, nil
}

// runEnv holds per-run clients.
type runEnv struct {
	opts  Options
	audit *auditapi.Client
}

// attemptClient builds the MCP client for one attempt, authenticating as the
// attempt's pool identity (or the base credential when rotation is off).
func (e *runEnv) attemptClient(seq int) *mcpc.Client {
	t := e.opts.Target
	if e.opts.IdentityKeys > 0 {
		t.Credential = fmt.Sprintf("%s-%02d", t.Credential, seq)
	}
	return mcpc.New(t.BaseURL, t.HTTPClient(e.opts.HTTPTimeout))
}

// runAttempt executes one task attempt end to end. Harness failures land in
// Attempt.Error; graded outcomes (right or wrong) do not.
func (e *runEnv) runAttempt(ctx context.Context, t task.Task, attempt, seq int, res *report.Results) report.Attempt {
	a := report.Attempt{TaskID: t.ID, Suite: t.Suite, Attempt: attempt}
	log := e.opts.Log.With("task", t.ID, "attempt", attempt, "arm", e.opts.Arm)

	session, err := e.attemptClient(seq).Connect(ctx)
	if err != nil {
		a.Error = fmt.Sprintf("connect: %v", err)
		return a
	}
	defer func() { _ = session.Close() }()

	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		a.Error = fmt.Sprintf("mint session handle: %v", err)
		return a
	}
	a.SessionID = info.Handle
	if res.Manifest.PlatformVersion == "" {
		res.Manifest.PlatformVersion = info.PlatformVersion
	}

	tools, err := mcpc.ListTools(ctx, session)
	if err != nil {
		a.Error = fmt.Sprintf("list tools: %v", err)
		return a
	}

	adapter, err := e.opts.Factory(t)
	if err != nil {
		a.Error = fmt.Sprintf("build adapter: %v", err)
		return a
	}
	if res.Manifest.Model == "" {
		res.Manifest.Model = adapter.Model()
	}

	// audited counts calls CONFIRMED to produce an audit row under this
	// session's handle: completed calls minus platform refusals (authz, the
	// gates, the per-user limiter), which short-circuit in middleware OUTER
	// to the audit middleware and leave no row. indeterminate counts
	// transport-level failures where the platform MAY have audited the call
	// before the error surfaced client-side (a protocol error for an unknown
	// tool name is logged by the audit middleware; a client timeout races a
	// server that finishes and audits). platform_info's own row carries the
	// transport session id (the handle is minted inside its handler), so it
	// is in neither count.
	audited, indeterminate := 0, 0
	exec := func(ctx context.Context, name string, args map[string]any) llm.ToolResult {
		r := mcpc.Call(ctx, session, name, args, info.Handle)
		if r.TransportErr != nil {
			indeterminate++
			return llm.ToolResult{Text: "transport error: " + r.TransportErr.Error(), IsError: true}
		}
		if !preAuditRefusal(r.ErrorCode) {
			audited++
		}
		return llm.ToolResult{Text: r.Text, IsError: r.ToolErr}
	}

	start := time.Now()
	result, err := agent.Run(ctx, adapter, agent.Config{
		System: systemScaffold + "\n" + formatInstruction(t),
		Prompt: t.Prompt,
		Tools:  tools,
		Budget: t.BudgetToolCalls,
	}, exec)
	a.WallMS = time.Since(start).Milliseconds()
	fillAgentResult(&a, result)
	e.writeTranscript(&a, t, result, log)
	if err != nil {
		a.Error = fmt.Sprintf("agent loop: %v", err)
		return a
	}
	e.settleAndGrade(ctx, &a, t, info.Handle, audited, audited+indeterminate, log)
	return a
}

// settleAndGrade reads the session's audit trail back (failing the attempt
// loudly when rows are missing) and applies the deterministic grader.
func (e *runEnv) settleAndGrade(ctx context.Context, a *report.Attempt, t task.Task, handle string, minAudited, maxAudited int, log *slog.Logger) {
	events, err := e.audit.WaitForSession(ctx, handle, minAudited, maxAudited, e.opts.AuditTimeout)
	if err != nil {
		a.Error = fmt.Sprintf("audit read-back: %v", err)
		return
	}
	a.Audit = auditapi.Summarize(events)
	gradeAttempt(a, t)
	log.Info("attempt graded", "correct", a.Correct, "tool_calls", a.ToolCalls, "wall_ms", a.WallMS)
}

// fillAgentResult copies loop outcomes onto the attempt.
func fillAgentResult(a *report.Attempt, r agent.Result) {
	a.FinalAnswer = grade.ExtractFinal(r.FinalAnswer)
	a.ToolCalls = r.ToolCalls
	a.ToolErrors = r.ToolErrors
	a.BudgetExhausted = r.BudgetExhausted
	a.InputTokens = r.Usage.InputTokens
	a.OutputTokens = r.Usage.OutputTokens
}

// gradeAttempt applies the task's deterministic grader.
func gradeAttempt(a *report.Attempt, t task.Task) {
	switch t.Grading.Kind {
	case task.GradeNumeric:
		got, ok, correct := grade.Numeric(a.FinalAnswer, *t.Grading.Value, t.Grading.AbsTolerance)
		if ok {
			a.GotValue = &got
		}
		a.Correct = correct
	case task.GradeEntity:
		a.MatchedAlias, a.Correct = grade.Entity(a.FinalAnswer, t.Grading.Aliases, t.Grading.WrongAliases)
	}
}

// preAuditRefusal reports whether a structured error code marks a platform
// refusal issued outer to the audit middleware (pkg/middleware error contract:
// auth, authz, session gate, search-first gate). Such calls complete from the
// client's view but leave no audit row.
func preAuditRefusal(code string) bool {
	switch code {
	case "unauthenticated", "unauthorized", "session_required", "session_expired",
		"search_required", "setup_required", "rate_limited":
		return true
	}
	return false
}

// formatInstruction returns the per-grading-kind answer format rule.
func formatInstruction(t task.Task) string {
	if t.Grading.Kind == task.GradeNumeric {
		return numericFormat
	}
	return entityFormat
}

// transcript is the per-attempt file layout (manual rubric review reads these).
type transcript struct {
	TaskID     string        `json:"task_id"`
	Arm        string        `json:"arm"`
	Attempt    int           `json:"attempt"`
	SessionID  string        `json:"session_id"`
	Rubric     []string      `json:"rubric,omitempty"`
	Transcript []llm.Message `json:"transcript"`
}

// writeTranscript persists the attempt transcript with the task's rubric notes
// attached (the pilot's rubric items are reviewed manually from these files);
// failure to write is logged, not fatal (the graded outcome stands).
func (e *runEnv) writeTranscript(a *report.Attempt, t task.Task, r agent.Result, log *slog.Logger) {
	if e.opts.TranscriptDir == "" {
		return
	}
	if err := os.MkdirAll(e.opts.TranscriptDir, 0o750); err != nil {
		log.Warn("transcript dir", "error", err)
		return
	}
	rubric := make([]string, 0, len(t.Rubric))
	for _, item := range t.Rubric {
		rubric = append(rubric, item.ID+": "+item.Note)
	}
	path := filepath.Join(e.opts.TranscriptDir, fmt.Sprintf("%s-%s-k%d.json", a.TaskID, e.opts.Arm, a.Attempt))
	raw, err := json.MarshalIndent(transcript{
		TaskID: a.TaskID, Arm: e.opts.Arm, Attempt: a.Attempt, SessionID: a.SessionID,
		Rubric:     rubric,
		Transcript: r.Transcript,
	}, "", "  ")
	if err != nil {
		log.Warn("marshal transcript", "error", err)
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		log.Warn("write transcript", "error", err)
		return
	}
	a.TranscriptPath = path
}
