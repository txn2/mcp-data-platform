package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// identitiesPerRun is the number of distinct pool identities each protocol
// attempt consumes: one teacher (teach, recall, update, abstain) and one learner
// (transfer). Each attempt uses fresh identities so the search-first gate's
// per-user discovery scope never leaks between attempts.
const identitiesPerRun = 2

// AdapterFactory builds the model adapter for one episode, keyed by protocol and
// stage so the scripted adapter can play a per-episode script.
type AdapterFactory func(protocolID, stage string) (llm.Adapter, error)

// Options configures a lifecycle run.
type Options struct {
	Target        target.Target
	HTTPTimeout   time.Duration
	Arm           string // a3 (the only lifecycle-capable arm)
	K             int
	ProtocolsDir  string
	TranscriptDir string
	Factory       AdapterFactory
	// ClaudeCLI, when non-nil, runs each episode through a real `claude -p`
	// client (connecting to the platform directly and threading its own handle)
	// instead of the in-process agent loop. Factory is unused in this mode. The
	// claude-cli episode path — including cache-token threading through the
	// EpisodeRecord (#966) — is testable via claudecli.Options.Exec, which injects
	// canned stream bytes through the real parser.
	ClaudeCLI *claudecli.Runner
	// ClientVersion is recorded on the manifest for the ClaudeCLI path.
	ClientVersion string
	LLMProvider   string
	GitCommit     string
	AuditTimeout  time.Duration
	// IdentityKeys is the identity-pool size the arm config defines. Each
	// protocol attempt consumes identitiesPerRun identities, so the run refuses
	// to start when protocols x k x 2 exceeds the pool (attempts would share a
	// discovery scope). It must be positive: the lifecycle needs distinct teacher
	// and learner identities, so there is no single-identity mode.
	IdentityKeys int
	// TeachBudget, when > 0, overrides the per-episode tool-call budget for the
	// capture-bearing stages (teach and update). It is the capture-budget lever
	// (issue #964): a larger teach-stage budget lets the teacher finish discovery
	// and still reach the capture tool. Zero uses the protocol's BudgetToolCalls.
	TeachBudget int
	// OnProtocol, if set, is called after every protocol attempt with the
	// aggregated results so far. benchrun wires it to flush the results file, so
	// a run that spends real API budget always leaves every completed protocol on
	// disk — an interruption never discards paid-for work.
	OnProtocol func(*Results)
	// OnSupersede mirrors OnProtocol for the isolated supersede sub-benchmark
	// (RunSupersede): it flushes the focused results after each attempt.
	OnSupersede func(*SupersedeResults)
	Log         *slog.Logger
}

// Run executes every protocol k times and returns the aggregated lifecycle
// results. A harness-level failure in any run is recorded on that run and
// surfaced in the returned error so the process exits nonzero.
func Run(ctx context.Context, opts Options) (*Results, error) {
	protocols, err := protocol.Load(opts.ProtocolsDir)
	if err != nil {
		return nil, err
	}
	res := &Results{Manifest: newManifest(opts, protocols)}
	// The lifecycle needs distinct teacher and learner identities per attempt (the
	// transfer stage is cross-identity by construction), so a single-identity run
	// is invalid — unlike the S1-S3 pipeline, there is no meaningful IdentityKeys=0
	// mode here.
	if opts.IdentityKeys <= 0 {
		return nil, errors.New("lifecycle requires an identity pool (-identity-keys > 0): teacher and learner must be distinct identities")
	}
	need := len(protocols) * opts.K * identitiesPerRun
	if need > opts.IdentityKeys {
		return nil, fmt.Errorf("%d identities needed (%d protocols x k=%d x %d) exceed the pool of %d; raise -identity-keys and the config pool",
			need, len(protocols), opts.K, identitiesPerRun, opts.IdentityKeys)
	}
	env := newRunEnv(opts)
	defer env.closeAdmin()

	failures := env.runAll(ctx, protocols, res)
	env.finishManifest(&res.Manifest)
	res.Aggregate()
	if failures > 0 {
		return res, fmt.Errorf("%d protocol run(s) failed at the harness level; see runs[].error", failures)
	}
	return res, nil
}

// newManifest builds the run manifest shared by the full lifecycle and the
// isolated supersede sub-benchmark, so a new manifest field is added in one
// place. The protocol-set hash covers exactly the protocols this run drove.
func newManifest(opts Options, protocols []protocol.Protocol) Manifest {
	return Manifest{
		StartedAt:       time.Now().UTC(),
		GitCommit:       opts.GitCommit,
		Target:          opts.Target.BaseURL,
		Arm:             opts.Arm,
		LLMProvider:     opts.LLMProvider,
		Seed:            gen.Seed,
		ProtocolSetHash: protocol.Hash(protocols),
		K:               opts.K,
	}
}

// newRunEnv wires the per-run clients (insights/changesets, audit, reviewer)
// shared by both entry points, so a newly-wired client is added once.
func newRunEnv(opts Options) *runEnv {
	life := lifecycleapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout))
	return &runEnv{
		opts:     opts,
		log:      opts.Log,
		audit:    auditapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout)),
		life:     life,
		reviewer: promote.Reviewer{Life: life, Log: opts.Log},
	}
}

// finishManifest stamps the carry-over fields captured during the run onto the
// manifest at the end of either entry point.
func (e *runEnv) finishManifest(m *Manifest) {
	m.FinishedAt = time.Now().UTC()
	m.PlatformVersion = e.platformVersion
	m.Model = e.model
	m.ClientVersion = e.opts.ClientVersion
}

// runAll executes every protocol k times, appending each run to res and
// returning the count of harness-level failures. Each attempt consumes a fresh
// pair of pool identities (attemptIndex-derived) so discovery scopes never leak.
func (e *runEnv) runAll(ctx context.Context, protocols []protocol.Protocol, res *Results) int {
	failures := 0
	attemptIndex := 0
	for _, p := range protocols {
		for attempt := 1; attempt <= e.opts.K; attempt++ {
			run := e.runProtocol(ctx, p, attempt, attemptIndex)
			if run.Error != "" {
				failures++
			}
			res.Runs = append(res.Runs, run)
			attemptIndex++
			e.checkpoint(res)
		}
	}
	return failures
}

// checkpoint flushes the results so far after each protocol so an interruption
// never discards completed, paid-for work. It aggregates first so the on-disk
// snapshot is a valid, self-consistent results file at every point.
func (e *runEnv) checkpoint(res *Results) {
	if e.opts.OnProtocol == nil {
		return
	}
	res.Aggregate()
	e.opts.OnProtocol(res)
}

// runEnv holds per-run clients and mutable manifest carry-overs.
type runEnv struct {
	opts     Options
	log      *slog.Logger
	audit    *auditapi.Client
	life     *lifecycleapi.Client
	reviewer promote.Reviewer

	// adminMCP is the lazily-built reviewer session that drives apply_knowledge
	// (base admin credential, no identity rotation), shared across promotes, with
	// its minted handle threaded on every call.
	adminMCP    *mcp.ClientSession
	adminHandle string

	platformVersion   string
	model             string
	currentProtocolID string
}

// runProtocol executes one protocol attempt through the full lifecycle.
func (e *runEnv) runProtocol(ctx context.Context, p protocol.Protocol, attempt, attemptIndex int) ProtocolRun {
	e.currentProtocolID = p.ID
	run := ProtocolRun{ProtocolID: p.ID, Title: p.Title, Sink: p.Sink, Attempt: attempt}
	teacherSeq := attemptIndex*identitiesPerRun + 1
	learnerSeq := attemptIndex*identitiesPerRun + 2
	log := e.log.With("protocol", p.ID, "attempt", attempt)

	if abort := e.teachAndRecall(ctx, p, teacherSeq, &run); abort {
		return run
	}
	if abort := e.runAdvancedStages(ctx, p, teacherSeq, learnerSeq, &run); abort {
		return run
	}
	e.abstain(ctx, p, teacherSeq, &run)
	log.Info("protocol graded",
		"captured", boolTrue(run.Captured), "recall", boolTrue(run.RecallCorrect),
		"promoted", boolTrue(run.Promoted), "transfer", boolTrue(run.TransferCorrect),
		"update", boolTrue(run.UpdateCorrect), "duplicated", boolTrue(run.Duplicated),
		"abstain", boolTrue(run.AbstainCorrect))
	return run
}

// runAdvancedStages runs the capture-dependent stages (promote+transfer OR
// supersede, never both — enforced by protocol.Validate). It is a no-op when the
// teach stage did not capture an insight to build on. Returns true when a harness
// failure aborts the run.
func (e *runEnv) runAdvancedStages(ctx context.Context, p protocol.Protocol, teacherSeq, learnerSeq int, run *ProtocolRun) bool {
	if !boolTrue(run.Captured) || run.InsightID == "" {
		return false
	}
	if p.Transfer != nil {
		return e.promoteAndTransfer(ctx, p, learnerSeq, run)
	}
	if p.Update != nil {
		return e.supersede(ctx, p, teacherSeq, run)
	}
	return false
}

// teachBudget returns the effective tool-call budget for a capture-bearing
// stage, honoring the TeachBudget override (issue #964 capture-budget lever).
func (e *runEnv) teachBudget(p protocol.Protocol) int {
	if e.opts.TeachBudget > 0 {
		return e.opts.TeachBudget
	}
	return p.BudgetToolCalls
}

// teachAndRecall runs the teach and personal-recall episodes and verifies
// capture via the insights API. Returns true when a harness failure aborts the
// run.
func (e *runEnv) teachAndRecall(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) bool {
	if abort := e.teachAndCapture(ctx, p, teacherSeq, run); abort {
		return true
	}
	return e.recall(ctx, p, teacherSeq, run)
}

// teachAndCapture runs the teach episode and verifies capture via the insights
// API. It records the capture-budget diagnosis (whether the teacher reached the
// capture tool and whether it exhausted its budget) so a capture miss can be
// attributed. Returns true when a harness failure aborts the run.
func (e *runEnv) teachAndCapture(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) bool {
	teach, _ := e.runEpisode(ctx, episodeSpec{
		stage: StageTeach, identity: "teacher", seq: teacherSeq,
		prompt: p.Teach.Prompt, system: teachSystem, budget: e.teachBudget(p),
	})
	run.Episodes = append(run.Episodes, teach)
	attempted := teach.CaptureAttempted
	run.CaptureAttempted = &attempted
	// Budget exhaustion is observable only on the in-process loop path, which owns
	// the tool-call budget. The claude-cli path runs its own turn budget, so leave
	// TeachBudgetExhausted nil there — a nil value excludes the run from the
	// budget-starvation rate rather than falsely asserting it was not starved.
	if e.opts.ClaudeCLI == nil {
		exhausted := teach.BudgetExhausted
		run.TeachBudgetExhausted = &exhausted
	}
	if teach.Error != "" {
		run.Error = "teach: " + teach.Error
		return true
	}

	insight, err := e.waitForInsight(ctx, poolEmail(teacherSeq), p.EntityURN)
	if err != nil {
		run.Error = "capture verify: " + err.Error()
		return true
	}
	captured := insight != nil && insight.LinksEntity(p.EntityURN)
	run.Captured = &captured
	if insight != nil {
		run.InsightID = insight.ID
	}
	return false
}

// recall runs the personal-recall episode (same identity, fresh session) and
// grades it. Returns true when a harness failure aborts the run.
func (e *runEnv) recall(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) bool {
	recall, ans := e.runEpisode(ctx, episodeSpec{
		stage: StageRecall, identity: "teacher", seq: teacherSeq,
		prompt: p.Recall.Prompt, system: recallSystem(p.Recall.Grading.Kind), budget: p.BudgetToolCalls,
	})
	run.Episodes = append(run.Episodes, recall)
	if recall.Error != "" {
		run.Error = "recall: " + recall.Error
		return true
	}
	rc := gradeRecall(ans, p.Recall.Grading)
	run.RecallCorrect = &rc
	if boolTrue(run.Captured) {
		surfaced := recall.SearchCalled
		run.RecallSurfaced = &surfaced
	}
	return false
}

// promoteAndTransfer approves and applies the insight, then runs the
// cross-identity transfer episode. Returns true on a harness abort.
func (e *runEnv) promoteAndTransfer(ctx context.Context, p protocol.Protocol, learnerSeq int, run *ProtocolRun) bool {
	promoted, err := e.promoteInsight(ctx, p, run.InsightID)
	if err != nil {
		run.Error = "promote: " + err.Error()
		return true
	}
	run.Promoted = &promoted
	if !promoted || p.Transfer == nil {
		return false
	}
	transfer, ans := e.runEpisode(ctx, episodeSpec{
		stage: StageTransfer, identity: "learner", seq: learnerSeq,
		prompt: p.Transfer.Prompt, system: recallSystem(p.Transfer.Grading.Kind), budget: p.BudgetToolCalls,
		surfaceFact: surfacedTarget(p),
	})
	run.Episodes = append(run.Episodes, transfer)
	if transfer.Error != "" {
		run.Error = "transfer: " + transfer.Error
		return true
	}
	tc := gradeRecall(ans, p.Transfer.Grading)
	run.TransferCorrect = &tc
	run.TransferSurfaced = transfer.FactSurfaced
	return false
}

// supersede runs the correction episode and its post-update recall, then checks
// via the insights API that the fact flipped and no duplicate remains. Returns
// true on a harness abort.
func (e *runEnv) supersede(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) bool {
	if p.Update == nil {
		return false
	}
	upd, _ := e.runEpisode(ctx, episodeSpec{
		stage: StageUpdate, identity: "teacher", seq: teacherSeq,
		prompt: p.Update.Prompt, system: teachSystem, budget: e.teachBudget(p),
	})
	run.Episodes = append(run.Episodes, upd)
	if upd.Error != "" {
		run.Error = "update: " + upd.Error
		return true
	}
	recall, ans := e.runEpisode(ctx, episodeSpec{
		stage: StageUpdateRecall, identity: "teacher", seq: teacherSeq,
		prompt: p.Update.Recall.Prompt, system: recallSystem(p.Update.Recall.Grading.Kind), budget: p.BudgetToolCalls,
	})
	run.Episodes = append(run.Episodes, recall)
	if recall.Error != "" {
		run.Error = "update recall: " + recall.Error
		return true
	}
	run.UpdateCorrect = gradeUpdate(ans, *p.Update)

	dup, err := e.duplicated(ctx, run.InsightID)
	if err != nil {
		run.Error = "duplicate check: " + err.Error()
		return true
	}
	run.Duplicated = &dup
	return false
}

// duplicated reports whether the supersede left the taught fact duplicated: a
// clean supersede transitions the taught insight to superseded (so the recall
// path surfaces only the correction), while a failure to detect the restatement
// leaves the original insight live alongside the correction. Scoping the check to
// the specific taught insight (not a live-count over the identity) makes it
// immune to insights left by earlier runs that reuse the same pool identity.
func (e *runEnv) duplicated(ctx context.Context, teachInsightID string) (bool, error) {
	in, err := e.life.GetInsight(ctx, teachInsightID)
	if err != nil {
		return false, err
	}
	return in.Status != promote.StatusSuperseded, nil
}

// gradeUpdate scores the post-update recall: correct only when it matches the
// new value AND, when a superseded numeric value is recorded, does not instead
// return that stale value. The stale check reuses the numeric grader against the
// superseded value so it shares the canonical tolerance and extraction rules.
func gradeUpdate(ans string, u protocol.UpdateStage) *bool {
	ok := gradeRecall(ans, u.Recall.Grading)
	if ok && u.SupersededValue != nil && gradeRecall(ans, supersededGrading(u)) {
		ok = false
	}
	return &ok
}

// abstain runs the never-taught-fact episode and scores whether the agent
// abstained. It is independent of capture, so it runs even when capture failed.
func (e *runEnv) abstain(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) {
	if p.Abstain == nil {
		return
	}
	rec, ans := e.runEpisode(ctx, episodeSpec{
		stage: StageAbstain, identity: "teacher", seq: teacherSeq,
		prompt: p.Abstain.Prompt, system: abstainSystem, budget: p.BudgetToolCalls,
	})
	run.Episodes = append(run.Episodes, rec)
	if rec.Error != "" {
		run.Error = "abstain: " + rec.Error
		return
	}
	ac := abstains(ans)
	run.AbstainCorrect = &ac
}

// attemptClient builds the MCP client for one episode, authenticating as the
// pool identity (or the base credential when rotation is off).
func (e *runEnv) attemptClient(seq int) *mcpc.Client {
	t := e.opts.Target
	t.Credential = pool.Credential(t.Credential, seq, e.opts.IdentityKeys)
	return mcpc.New(t.BaseURL, t.HTTPClient(e.opts.HTTPTimeout))
}

// recordPlatformVersion captures the platform version once.
func (e *runEnv) recordPlatformVersion(v string) {
	if e.platformVersion == "" && v != "" {
		e.platformVersion = v
	}
}

// recordModel captures the model id once.
func (e *runEnv) recordModel(m string) {
	if e.model == "" && m != "" {
		e.model = m
	}
}

// writeTranscript persists one episode transcript for manual audit.
func (e *runEnv) writeTranscript(spec episodeSpec, r agent.Result) {
	if e.opts.TranscriptDir == "" {
		return
	}
	if err := os.MkdirAll(e.opts.TranscriptDir, 0o750); err != nil {
		e.log.Warn("transcript dir", "error", err)
		return
	}
	path := filepath.Join(e.opts.TranscriptDir,
		fmt.Sprintf("%s-%s-%s.json", e.currentProtocolID, spec.stage, poolEmail(spec.seq)))
	payload := lifecycleTranscript{
		ProtocolID: e.currentProtocolID, Stage: spec.stage, Identity: spec.identity,
		Email: poolEmail(spec.seq), Transcript: r.Transcript,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		e.log.Warn("marshal transcript", "error", err)
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		e.log.Warn("write transcript", "error", err)
	}
}

// lifecycleTranscript is the per-episode transcript file layout.
type lifecycleTranscript struct {
	ProtocolID string        `json:"protocol_id"`
	Stage      string        `json:"stage"`
	Identity   string        `json:"identity"`
	Email      string        `json:"email"`
	Transcript []llm.Message `json:"transcript"`
}
