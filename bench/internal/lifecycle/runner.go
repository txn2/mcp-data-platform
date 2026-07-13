package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// applyToolName is the reviewer-side promotion tool the harness drives (the
// promote stage is scripted, not agent-driven).
const applyToolName = "apply_knowledge"

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
	LLMProvider   string
	GitCommit     string
	AuditTimeout  time.Duration
	// IdentityKeys is the identity-pool size the arm config defines. Each
	// protocol attempt consumes identitiesPerRun identities, so the run refuses
	// to start when protocols x k x 2 exceeds the pool (attempts would share a
	// discovery scope). Zero disables rotation (single identity), for targets
	// without a pool.
	IdentityKeys int
	Log          *slog.Logger
}

// Run executes every protocol k times and returns the aggregated lifecycle
// results. A harness-level failure in any run is recorded on that run and
// surfaced in the returned error so the process exits nonzero.
func Run(ctx context.Context, opts Options) (*Results, error) {
	protocols, err := protocol.Load(opts.ProtocolsDir)
	if err != nil {
		return nil, err
	}
	res := &Results{Manifest: Manifest{
		StartedAt:       time.Now().UTC(),
		GitCommit:       opts.GitCommit,
		Target:          opts.Target.BaseURL,
		Arm:             opts.Arm,
		LLMProvider:     opts.LLMProvider,
		Seed:            gen.Seed,
		ProtocolSetHash: protocol.Hash(protocols),
		K:               opts.K,
	}}
	need := len(protocols) * opts.K * identitiesPerRun
	if opts.IdentityKeys > 0 && need > opts.IdentityKeys {
		return nil, fmt.Errorf("%d identities needed (%d protocols x k=%d x %d) exceed the pool of %d; raise -identity-keys and the config pool",
			need, len(protocols), opts.K, identitiesPerRun, opts.IdentityKeys)
	}
	env := &runEnv{
		opts:  opts,
		log:   opts.Log,
		audit: auditapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout)),
		life:  lifecycleapi.New(opts.Target.BaseURL, opts.Target.HTTPClient(opts.HTTPTimeout)),
	}
	defer env.closeAdmin()

	failures := env.runAll(ctx, protocols, res)
	res.Manifest.FinishedAt = time.Now().UTC()
	res.Manifest.PlatformVersion = env.platformVersion
	res.Manifest.Model = env.model
	res.Aggregate()
	if failures > 0 {
		return res, fmt.Errorf("%d protocol run(s) failed at the harness level; see runs[].error", failures)
	}
	return res, nil
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
		}
	}
	return failures
}

// runEnv holds per-run clients and mutable manifest carry-overs.
type runEnv struct {
	opts  Options
	log   *slog.Logger
	audit *auditapi.Client
	life  *lifecycleapi.Client

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
	if boolTrue(run.Captured) && run.InsightID != "" {
		if abort := e.promoteAndTransfer(ctx, p, learnerSeq, &run); abort {
			return run
		}
		if abort := e.supersede(ctx, p, teacherSeq, &run); abort {
			return run
		}
	}
	e.abstain(ctx, p, teacherSeq, &run)
	log.Info("protocol graded",
		"captured", boolTrue(run.Captured), "recall", boolTrue(run.RecallCorrect),
		"promoted", boolTrue(run.Promoted), "transfer", boolTrue(run.TransferCorrect),
		"update", boolTrue(run.UpdateCorrect), "duplicated", boolTrue(run.Duplicated),
		"abstain", boolTrue(run.AbstainCorrect))
	return run
}

// teachAndRecall runs the teach and personal-recall episodes and verifies
// capture via the insights API. Returns true when a harness failure aborts the
// run.
func (e *runEnv) teachAndRecall(ctx context.Context, p protocol.Protocol, teacherSeq int, run *ProtocolRun) bool {
	teach, _ := e.runEpisode(ctx, episodeSpec{
		stage: StageTeach, identity: "teacher", seq: teacherSeq,
		prompt: p.Teach.Prompt, system: teachSystem, budget: p.BudgetToolCalls,
	})
	run.Episodes = append(run.Episodes, teach)
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
	if captured {
		surfaced := recall.SearchCalled
		run.RecallSurfaced = &surfaced
	}
	return false
}

// promoteAndTransfer approves and applies the insight, then runs the
// cross-identity transfer episode. Returns true on a harness abort.
func (e *runEnv) promoteAndTransfer(ctx context.Context, p protocol.Protocol, learnerSeq int, run *ProtocolRun) bool {
	promoted, err := e.promote(ctx, p, run.InsightID)
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
	})
	run.Episodes = append(run.Episodes, transfer)
	if transfer.Error != "" {
		run.Error = "transfer: " + transfer.Error
		return true
	}
	tc := gradeRecall(ans, p.Transfer.Grading)
	run.TransferCorrect = &tc
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
		prompt: p.Update.Prompt, system: teachSystem, budget: p.BudgetToolCalls,
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
	run.UpdateCorrect = e.gradeUpdate(ans, *p.Update)

	live, err := e.liveInsightCount(ctx, poolEmail(teacherSeq), p.EntityURN)
	if err != nil {
		run.Error = "duplicate check: " + err.Error()
		return true
	}
	dup := live > 1
	run.Duplicated = &dup
	return false
}

// gradeUpdate scores the post-update recall: correct only when it matches the
// new value AND, when a superseded numeric value is recorded, does not return
// the stale value.
func (e *runEnv) gradeUpdate(ans string, u protocol.UpdateStage) *bool {
	ok := gradeRecall(ans, u.Recall.Grading)
	if ok && u.SupersededValue != nil {
		if got, present := gradedNumeric(ans, u.Recall.Grading); present && withinTolerance(got, *u.SupersededValue, u.Recall.Grading.AbsTolerance) {
			ok = false
		}
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

// withinTolerance reports whether got is within tol of want.
func withinTolerance(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// attemptClient builds the MCP client for one episode, authenticating as the
// pool identity (or the base credential when rotation is off).
func (e *runEnv) attemptClient(seq int) *mcpc.Client {
	t := e.opts.Target
	if e.opts.IdentityKeys > 0 {
		t.Credential = fmt.Sprintf("%s-%03d", t.Credential, seq)
	}
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
