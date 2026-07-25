// Package pkcorpus drives the perishable-knowledge study's capture-corpus
// run (#1054, protocol section 6 stage 1). Real capture episodes run over
// the perishable fixture, one per scenario per replicate, and whatever the
// platform's own capture tool records is archived verbatim. The corpus is
// evidence, not a deliverable: the frozen seed set curated from it (stage
// 2) is what the study runs, and the corpus is what lets the study claim
// its phrasings are artifacts of real capture rather than strawmen the
// study wrote for itself.
package pkcorpus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// EpisodeRunner drives one capture episode. *claudecli.Runner satisfies
// it; it is an interface so the corpus runner is exercisable without the
// claude binary.
type EpisodeRunner interface {
	Run(ctx context.Context, req claudecli.Request) (claudecli.Result, error)
	Model() string
}

// Options configures a corpus run.
type Options struct {
	// Target is the platform MCP + admin REST endpoint and the admin
	// credential the pool identities derive from.
	Target target.Target
	// IdentityKeys is the size of the configured identity pool.
	IdentityKeys int
	// Fixture is the perishable fixture's control-plane client, used to
	// place the account in each scenario's world before its episode.
	Fixture *fixturectl.Client
	// Insights reads captured insights back through the admin API.
	Insights *lifecycleapi.Client
	// Runner drives the episodes.
	Runner EpisodeRunner
	// Replicates is how many times each scenario runs. Replicates are what
	// give the corpus phrasing variety, so this is the knob that decides
	// whether curation has anything to choose between.
	Replicates int
	// OutDir receives the manifest, the corpus, and the transcripts.
	OutDir string
	// GitCommit is recorded in the manifest.
	GitCommit string
	// ClientVersion is the driving client's version, recorded in the
	// manifest. Report 1's rule: a claude-cli number is never compared
	// against a raw-API number without its client version alongside it.
	ClientVersion string
	// CaptureWait is how long to wait for a captured insight to become
	// readable after an episode ends.
	CaptureWait time.Duration
	// Log receives progress.
	Log *slog.Logger
}

// Manifest describes one corpus run.
type Manifest struct {
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	GitCommit       string    `json:"git_commit"`
	PlatformVersion string    `json:"platform_version,omitempty"`
	Model           string    `json:"model"`
	ClientVersion   string    `json:"client_version,omitempty"`
	ScenariosHash   string    `json:"scenarios_hash"`
	Replicates      int       `json:"replicates"`
	Episodes        int       `json:"episodes"`
	CapturedTotal   int       `json:"captured_total"`
}

// Episode is one capture episode's archived outcome.
type Episode struct {
	ScenarioID string    `json:"scenario_id"`
	Class      string    `json:"class"`
	World      string    `json:"world"`
	Replicate  int       `json:"replicate"`
	Seq        int       `json:"seq"`
	Email      string    `json:"email"`
	StartedAt  time.Time `json:"started_at"`
	WallMS     int64     `json:"wall_ms"`
	ToolCalls  int       `json:"tool_calls"`
	ToolErrors int       `json:"tool_errors"`
	Handle     string    `json:"handle,omitempty"`
	FinalText  string    `json:"final_text"`
	// Captured is every insight this episode's identity recorded, verbatim.
	Captured []Captured `json:"captured"`
	// Error is a harness failure. An episode that captured nothing is not
	// an error: a model declining to record is itself corpus evidence.
	Error string `json:"error,omitempty"`
}

// Captured is one insight exactly as the platform stored it.
type Captured struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	CapturedBy  string    `json:"captured_by"`
	Category    string    `json:"category"`
	InsightText string    `json:"insight_text"`
	Status      string    `json:"status"`
	EntityURNs  []string  `json:"entity_urns,omitempty"`
}

// Corpus is the archived run.
type Corpus struct {
	Manifest  Manifest   `json:"manifest"`
	Scenarios []Scenario `json:"scenarios"`
	System    string     `json:"system"`
	Episodes  []Episode  `json:"episodes"`
}

// Run executes the corpus run and writes the archive. It returns the
// corpus even when individual episodes failed: a partial corpus is data,
// and discarding it would throw away a completed free run.
func Run(ctx context.Context, opts Options) (*Corpus, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	if err := opts.preflight(ctx); err != nil {
		return nil, err
	}
	scenarios := Scenarios()
	corpus := &Corpus{
		Manifest: Manifest{
			StartedAt: time.Now().UTC(), GitCommit: opts.GitCommit,
			Model: opts.Runner.Model(), ClientVersion: opts.ClientVersion,
			ScenariosHash: ScenariosHash(), Replicates: opts.Replicates,
		},
		Scenarios: scenarios,
		System:    System,
	}
	seq := 0
	for rep := 1; rep <= opts.Replicates; rep++ {
		for _, sc := range scenarios {
			seq++
			log.Info("capture episode", "scenario", sc.ID, "replicate", rep, "world", sc.World, "seq", seq)
			ep := opts.runEpisode(ctx, sc, rep, seq, corpus)
			corpus.Episodes = append(corpus.Episodes, ep)
			corpus.Manifest.CapturedTotal += len(ep.Captured)
			if ep.Error != "" {
				log.Warn("capture episode failed", "scenario", sc.ID, "replicate", rep, "error", ep.Error)
			}
		}
	}
	corpus.Manifest.Episodes = len(corpus.Episodes)
	corpus.Manifest.FinishedAt = time.Now().UTC()
	if err := writeArchive(opts.OutDir, corpus); err != nil {
		return corpus, err
	}
	return corpus, nil
}

// validate rejects an unrunnable configuration before any episode is spent.
func (o Options) validate() error {
	switch {
	case o.Runner == nil:
		return errors.New("pkcorpus: no episode runner")
	case o.Fixture == nil:
		return errors.New("pkcorpus: no fixture client")
	case o.Insights == nil:
		return errors.New("pkcorpus: no insights client")
	case o.Replicates < 1:
		return fmt.Errorf("pkcorpus: replicates must be positive, got %d", o.Replicates)
	case o.IdentityKeys < 1:
		return fmt.Errorf("pkcorpus: identity-keys must be positive, got %d", o.IdentityKeys)
	case o.OutDir == "":
		return errors.New("pkcorpus: no output directory")
	}
	return ValidateScenarios()
}

// preflight refuses to start when a pool identity the run will use already
// holds knowledge.
//
// This is not fussiness. Pool identities repeat across runs, and an agent
// whose first move is `search` finds its own earlier insight, correctly
// declines to record the same thing twice, and produces an episode that
// captured nothing. That episode is not a fresh capture and must not be
// read as evidence about what capture writes: it is evidence about what an
// agent does when the knowledge is already there. Observed on the first
// three-replicate run, where the five identities carried over from an
// earlier run captured nothing at all. Clear the store between runs.
func (o Options) preflight(ctx context.Context) error {
	for seq := 1; seq <= len(Scenarios())*o.Replicates; seq++ {
		email := pool.Email(seq)
		existing, err := o.Insights.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email})
		if err != nil {
			return fmt.Errorf("pkcorpus: preflight read for %s: %w", email, err)
		}
		if len(existing) > 0 {
			return fmt.Errorf("pkcorpus: identity %s already holds %d insight(s); "+
				"an agent that finds its own earlier knowledge declines to record it again, "+
				"so the run would archive empty episodes as if capture had written nothing. "+
				"Clear the knowledge store before the run", email, len(existing))
		}
	}
	return nil
}

// runEpisode places the account in the scenario's world, runs one capture
// session as a fresh identity, and reads back what it recorded.
func (o Options) runEpisode(ctx context.Context, sc Scenario, rep, seq int, corpus *Corpus) Episode {
	ep := Episode{
		ScenarioID: sc.ID, Class: sc.Class, World: sc.World,
		Replicate: rep, Seq: seq, Email: pool.Email(seq),
		StartedAt: time.Now().UTC(),
	}
	// Reset into the scenario's world: each capture episode observes a
	// clean account in exactly one state, so a captured belief is about
	// that state and nothing else.
	if err := o.Fixture.ResetWorld(ctx, sc.World); err != nil {
		ep.Error = fmt.Sprintf("set world %s: %v", sc.World, err)
		return ep
	}
	if err := o.Fixture.SetPhase(ctx, "capture"); err != nil {
		ep.Error = fmt.Sprintf("set phase: %v", err)
		return ep
	}
	start := time.Now()
	res, err := o.Runner.Run(ctx, claudecli.Request{
		Endpoint:   o.Target.BaseURL,
		Credential: pool.Credential(o.Target.Credential, seq, o.IdentityKeys),
		System:     System,
		Prompt:     sc.Prompt,
	})
	ep.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		ep.Error = fmt.Sprintf("episode: %v", err)
		return ep
	}
	ep.ToolCalls, ep.ToolErrors = res.MCPCalls, res.ToolErrors
	ep.Handle, ep.FinalText = res.Handle, res.FinalText
	if res.PlatformVersion != "" {
		corpus.Manifest.PlatformVersion = res.PlatformVersion
	}
	o.writeTranscript(sc, rep, res.Transcript)
	switch {
	case res.IsError:
		ep.Error = fmt.Sprintf("client error (subtype %q): %.300s", res.Subtype, res.FinalText)
	case !res.ServerConnected:
		ep.Error = fmt.Sprintf("MCP server did not connect (status %q)", res.ServerStatus)
	}
	if ep.Error != "" {
		return ep
	}
	captured, err := o.readCaptured(ctx, ep.Email, ep.StartedAt)
	if err != nil {
		ep.Error = fmt.Sprintf("read captured: %v", err)
		return ep
	}
	ep.Captured = captured
	return ep
}

// readCaptured polls for the insights this identity recorded during the
// episode. Capture is asynchronous relative to the episode's final answer,
// so a short wait is the difference between an archived corpus entry and a
// silently empty one; the wait ending empty is a real outcome, not an
// error.
func (o Options) readCaptured(ctx context.Context, email string, since time.Time) ([]Captured, error) {
	deadline := time.Now().Add(o.CaptureWait)
	for {
		insights, err := o.Insights.ListInsights(ctx, lifecycleapi.InsightFilter{
			CapturedBy: email,
			Since:      since,
		})
		if err != nil {
			return nil, err
		}
		out := make([]Captured, 0, len(insights))
		for _, in := range insights {
			out = append(out, Captured{
				ID: in.ID, CreatedAt: in.CreatedAt, CapturedBy: in.CapturedBy,
				Category: in.Category, InsightText: in.InsightText,
				Status: in.Status, EntityURNs: in.EntityURNs,
			})
		}
		if len(out) > 0 || !time.Now().Before(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// writeTranscript persists one episode's conversation for manual audit.
func (o Options) writeTranscript(sc Scenario, rep int, msgs []llm.Message) {
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
	name := fmt.Sprintf("%s-r%d.json", sc.ID, rep)
	_ = os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}

// writeArchive writes the run's durable output.
func writeArchive(dir string, corpus *Corpus) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("pkcorpus: create %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return fmt.Errorf("pkcorpus: marshal corpus: %w", err)
	}
	path := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("pkcorpus: write %s: %w", path, err)
	}
	return nil
}
