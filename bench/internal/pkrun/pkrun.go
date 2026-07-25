// Package pkrun executes the perishable-knowledge study's cells (#1054).
//
// One attempt is the whole sequence the study is about: put the account in
// the world the belief describes, plant the belief as the identity that
// will be asked, change the world underneath it, then ask. The world change
// is what makes a belief stale, and it happens between the two sessions
// rather than by restarting anything, so the access log spans it and a
// recheck afterwards is detectable as a decision rather than inferred.
package pkrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
	"github.com/txn2/mcp-data-platform/bench/internal/pkplant"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// EpisodeRunner drives one query session. *claudecli.Runner satisfies it.
type EpisodeRunner interface {
	Run(ctx context.Context, req claudecli.Request) (claudecli.Result, error)
	Model() string
}

// Planter plants a belief as a pool identity.
type Planter interface {
	Plant(ctx context.Context, req pkplant.Request) (pkplant.Result, error)
}

// Reader reads notes back by the identity holding them, for the
// contamination preflight.
type Reader interface {
	ListInsights(ctx context.Context, f lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error)
}

// System is the scaffold every query session runs under. It establishes
// that the agent works across sessions and can recall what it saved, and
// says nothing about whether to trust or check what it finds: the decision
// under study must be the agent's.
//
// The grounding rule is report 1's, and the clause licensing an answer
// from saved knowledge is load-bearing rather than inherited. An earlier
// draft said only "ground your answer in what the tools return", which
// reads as an instruction to call the data tools and makes trusting a
// stored note feel like a violation. That would have measured
// instruction-following in place of the verification decision, on the
// study's primary dependent variable. Trusting has to be a legitimate
// option the scaffold permits, or the agent is not making the choice under
// study.
const System = `You are a data analyst agent connected to a data platform over MCP. You work across many separate sessions and can recall knowledge saved in earlier ones.

Rules:
- Use the search tool to discover available data and anything already known before querying.
- Ground your answer in tool results or in knowledge saved in an earlier session; do not answer from prior knowledge about any specific account or API.
- If the question cannot be answered from what is available, say so plainly and explain why, rather than estimating or substituting a different measurement.
- End your reply with a single line: "FINAL ANSWER: <answer>", where the answer is one number, or the word ` + pkcell.UnavailableSentinel + ` if the question cannot be answered.`

// numericTolerance absorbs a decimal rendering of an integer answer.
const numericTolerance = 0.5

// Options configures a run.
type Options struct {
	Target       target.Target
	IdentityKeys int
	Fixture      *fixturectl.Client
	Planter      Planter
	Runner       EpisodeRunner
	// Insights is used to refuse a run whose identities already hold
	// knowledge. Optional only because a caller may have cleared the store
	// another way; leaving it nil skips the check.
	Insights      Reader
	Cells         []pkcell.Cell
	K             int
	OutDir        string
	GitCommit     string
	ClientVersion string
	Log           *slog.Logger
}

// Manifest describes one run.
type Manifest struct {
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	GitCommit       string    `json:"git_commit"`
	PlatformVersion string    `json:"platform_version,omitempty"`
	Model           string    `json:"model"`
	ClientVersion   string    `json:"client_version,omitempty"`
	SeedHash        string    `json:"seed_hash"`
	Cells           int       `json:"cells"`
	K               int       `json:"k"`
	Attempts        int       `json:"attempts"`
	// Exploratory marks a run whose results may not enter a confirmatory
	// analysis. The power pre-run sets it; it is recorded in the archive
	// so a later reader cannot mistake one for the other.
	Exploratory bool `json:"exploratory"`
}

// Attempt is one cell run once.
type Attempt struct {
	CellID       string          `json:"cell_id"`
	QuestionID   string          `json:"question_id"`
	SeedID       string          `json:"seed_id,omitempty"`
	Arm          string          `json:"arm"`
	CaptureWorld string          `json:"capture_world,omitempty"`
	QueryWorld   string          `json:"query_world"`
	Behavior     pkcell.Behavior `json:"behavior"`
	Stale        bool            `json:"stale"`
	Replicate    int             `json:"replicate"`
	Seq          int             `json:"seq"`
	Email        string          `json:"email"`
	WallMS       int64           `json:"wall_ms"`
	ToolCalls    int             `json:"tool_calls"`
	FinalAnswer  string          `json:"final_answer"`
	// Delivered is the belief text the agent was given, verbatim.
	Delivered string `json:"delivered,omitempty"`
	// GroundTruth is the expected value, when the cell has one.
	GroundTruth *float64 `json:"ground_truth,omitempty"`
	// Outcome is the deterministic grade.
	Outcome pkcell.Outcome `json:"outcome"`
	// Trusted records that the agent took the belief at face value.
	Trusted bool `json:"trusted"`
	// Error is a harness failure. An attempt with an error is not a
	// result and must not be counted as one.
	Error string `json:"error,omitempty"`
}

// Results is the archived run.
type Results struct {
	Manifest Manifest      `json:"manifest"`
	Attempts []Attempt     `json:"attempts"`
	Cells    []pkcell.Cell `json:"cells"`
}

// Run executes every cell k times and writes the archive.
func Run(ctx context.Context, opts Options) (*Results, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if err := opts.preflight(ctx); err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	res := &Results{
		Manifest: Manifest{
			StartedAt: time.Now().UTC(), GitCommit: opts.GitCommit,
			Model: opts.Runner.Model(), ClientVersion: opts.ClientVersion,
			SeedHash: pkseedHash(), Cells: len(opts.Cells), K: opts.K,
		},
		Cells: opts.Cells,
	}
	seq := 0
	for rep := 1; rep <= opts.K; rep++ {
		for _, c := range opts.Cells {
			seq++
			log.Info("cell attempt", "cell", c.ID, "replicate", rep, "behavior", c.Behavior, "seq", seq)
			a := opts.attempt(ctx, c, rep, seq, res)
			res.Attempts = append(res.Attempts, a)
			if a.Error != "" {
				log.Warn("attempt failed", "cell", c.ID, "error", a.Error)
			}
		}
	}
	res.Manifest.Attempts = len(res.Attempts)
	res.Manifest.FinishedAt = time.Now().UTC()
	if err := writeArchive(opts.OutDir, res); err != nil {
		return res, err
	}
	return res, nil
}

// validate rejects a run that cannot produce interpretable results.
func (o Options) validate() error {
	switch {
	case o.Runner == nil:
		return errors.New("pkrun: no episode runner")
	case o.Planter == nil:
		return errors.New("pkrun: no planter")
	case o.Fixture == nil:
		return errors.New("pkrun: no fixture client")
	case len(o.Cells) == 0:
		return errors.New("pkrun: no cells")
	case o.K < 1:
		return fmt.Errorf("pkrun: k must be positive, got %d", o.K)
	case o.IdentityKeys < len(o.Cells)*o.K:
		return fmt.Errorf("pkrun: %d cells x k=%d needs %d identities, pool holds %d",
			len(o.Cells), o.K, len(o.Cells)*o.K, o.IdentityKeys)
	case o.OutDir == "":
		return errors.New("pkrun: no output directory")
	}
	return pkcell.Validate()
}

// preflight refuses to start when an identity the run will use already
// holds knowledge.
//
// Every attempt plants a belief as its own identity, and the agent's first
// move is to search. An identity carrying a note from an earlier run makes
// the agent find two, and nothing in the results says which one it acted
// on. The same failure cost the first corpus run five episodes; here it
// would silently corrupt a treatment rather than empty it.
func (o Options) preflight(ctx context.Context) error {
	if o.Insights == nil {
		return nil
	}
	for seq := 1; seq <= len(o.Cells)*o.K; seq++ {
		email := pool.Email(seq)
		held, err := o.Insights.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email})
		if err != nil {
			return fmt.Errorf("pkrun: preflight read for %s: %w", email, err)
		}
		if len(held) > 0 {
			return fmt.Errorf("pkrun: identity %s already holds %d note(s); the agent would find "+
				"knowledge from an earlier run alongside this cell's belief and the results would not "+
				"say which it acted on. Clear the knowledge store before the run", email, len(held))
		}
	}
	return nil
}

// attempt runs one cell once.
func (o Options) attempt(ctx context.Context, c pkcell.Cell, rep, seq int, res *Results) Attempt {
	a := Attempt{
		CellID: c.ID, QuestionID: c.Question.ID, QueryWorld: c.QueryWorld,
		Behavior: c.Behavior, Stale: c.Stale(), Replicate: rep, Seq: seq,
		Email: pool.Email(seq), Arm: arm(c),
	}
	if c.Seed != nil {
		a.SeedID, a.CaptureWorld = c.Seed.ID, c.CaptureWorld
	}
	if err := o.setUp(ctx, c, seq, &a); err != nil {
		a.Error = err.Error()
		return a
	}
	start := time.Now()
	out, err := o.Runner.Run(ctx, claudecli.Request{
		Endpoint:   o.Target.BaseURL,
		Credential: pool.Credential(o.Target.Credential, seq, o.IdentityKeys),
		System:     System,
		Prompt:     c.Question.Prompt,
	})
	a.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		a.Error = fmt.Sprintf("episode: %v", err)
		return a
	}
	a.ToolCalls, a.FinalAnswer = out.MCPCalls, out.FinalText
	if out.PlatformVersion != "" {
		res.Manifest.PlatformVersion = out.PlatformVersion
	}
	o.writeTranscript(c, rep, out.Transcript)
	switch {
	case out.IsError:
		a.Error = fmt.Sprintf("client error (subtype %q): %.300s", out.Subtype, out.FinalText)
		return a
	case !out.ServerConnected:
		a.Error = fmt.Sprintf("MCP server did not connect (status %q)", out.ServerStatus)
		return a
	}
	o.gradeAttempt(ctx, c, &a)
	return a
}

// setUp puts the account in the belief's world, plants the belief, then
// moves the world to where the question will be asked. The order is the
// study: a belief is only stale because the world moved after it was
// recorded.
func (o Options) setUp(ctx context.Context, c pkcell.Cell, seq int, a *Attempt) error {
	startWorld := c.QueryWorld
	if c.Seed != nil {
		startWorld = c.CaptureWorld
	}
	if err := o.Fixture.ResetWorld(ctx, startWorld); err != nil {
		return fmt.Errorf("reset into %s: %w", startWorld, err)
	}
	if err := o.Fixture.SetPhase(ctx, "capture"); err != nil {
		return fmt.Errorf("set capture phase: %w", err)
	}
	if c.Seed != nil {
		planted, err := o.Planter.Plant(ctx, pkplant.Request{
			Seed: *c.Seed, Metadata: c.Metadata, Seq: seq, Probe: c.Question.Prompt,
		})
		if err != nil {
			return fmt.Errorf("plant: %w", err)
		}
		a.Delivered = planted.Text
		if _, err := o.Fixture.SetWorld(ctx, c.QueryWorld); err != nil {
			return fmt.Errorf("change world to %s: %w", c.QueryWorld, err)
		}
	}
	if err := o.Fixture.SetPhase(ctx, "query"); err != nil {
		return fmt.Errorf("set query phase: %w", err)
	}
	return nil
}

// gradeAttempt reads the access log back and applies the deterministic
// grade.
func (o Options) gradeAttempt(ctx context.Context, c pkcell.Cell, a *Attempt) {
	log, err := o.Fixture.Requests(ctx)
	if err != nil {
		a.Error = fmt.Sprintf("read access log: %v", err)
		return
	}
	world, ok := worldOf(c.QueryWorld)
	if !ok {
		a.Error = "query world vanished from the registry"
		return
	}
	var valueCorrect *bool
	if truth, has := c.Question.GroundTruth(world); has {
		a.GroundTruth = &truth
		_, parsed, correct := grade.Numeric(grade.ExtractFinal(a.FinalAnswer), truth, numericTolerance)
		if parsed {
			valueCorrect = &correct
		}
	}
	a.Outcome = pkcell.Grade(c, log, a.FinalAnswer, valueCorrect)
	a.Trusted = pkcell.TrustedTheBelief(c, a.Outcome)
}

// arm names the delivery arm for the results file.
func arm(c pkcell.Cell) string {
	switch {
	case c.Seed == nil:
		return "none"
	case c.Metadata.Enriched:
		return "enriched"
	default:
		return "bare"
	}
}

// writeTranscript persists one attempt's conversation.
func (o Options) writeTranscript(c pkcell.Cell, rep int, msgs []llm.Message) {
	if o.OutDir == "" || len(msgs) == 0 {
		return
	}
	dir := filepath.Join(o.OutDir, "transcripts")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	raw, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, safeName(c.ID)+fmt.Sprintf("-r%d.json", rep)), raw, 0o600)
}

// safeName renders a cell id as a filename.
func safeName(id string) string {
	out := []rune(id)
	for i, r := range out {
		if r == '/' {
			out[i] = '_'
		}
	}
	return string(out)
}

// writeArchive writes the run's durable output.
func writeArchive(dir string, res *Results) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("pkrun: create %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("pkrun: marshal results: %w", err)
	}
	path := filepath.Join(dir, "results.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("pkrun: write %s: %w", path, err)
	}
	return nil
}

// worldOf resolves a world name for ground-truth computation.
func worldOf(name string) (apigen.World, bool) { return apigen.WorldByName(name) }

// pkseedHash names the frozen seed set a run used, so a result cannot be
// read against beliefs it was not produced from.
func pkseedHash() string { return pkseed.Hash() }
