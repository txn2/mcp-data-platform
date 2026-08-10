package graphprobe

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
	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// probeName marks a completion archive's manifest, and is how reread tells a
// completion archive from a lookup-era one (whose manifests carry no probe
// field).
const probeName = "graph-completion"

// SystemSearch is the episode scaffold for the search arms. It says what the
// agent is and what the deliverable is, and deliberately says nothing about
// references, pages, or reading anything in full: the platform's own
// delivered instructions are the only channel that mentions `fetch`, which is
// the asymmetry the probe pre-states rather than removes.
const SystemSearch = `You are an operations agent connected to a data platform over MCP. You write operational documents grounded in what this platform records.

Rules:
- Use the search tool to discover what is already recorded before writing.
- Ground the document in what the tools return; do not write from prior knowledge about this company or its jobs.
- Where the available material is silent on something the document needs, say so plainly in that part of the document rather than inventing it.
- Your final reply is the document itself, in markdown, with no preamble after it.`

// SystemNoSearch is the scaffold for the no-search arms, identical but for
// the discovery bullet: there is no search tool to name, and naming any other
// route would instruct the behavior being measured.
const SystemNoSearch = `You are an operations agent connected to a data platform over MCP. You write operational documents grounded in what this platform records.

Rules:
- Read what is already recorded before writing.
- Ground the document in what the tools return; do not write from prior knowledge about this company or its jobs.
- Where the available material is silent on something the document needs, say so plainly in that part of the document rather than inventing it.
- Your final reply is the document itself, in markdown, with no preamble after it.`

// EpisodeRunner drives one query session. *claudecli.Runner satisfies it.
type EpisodeRunner interface {
	Run(ctx context.Context, req claudecli.Request) (claudecli.Result, error)
	Model() string
}

// Options configures one probe run: one model, one corpus arm (from the plant
// record), one search condition.
type Options struct {
	Target       target.Target
	IdentityKeys int
	Runner       EpisodeRunner
	Planted      Planted
	Gate         GateReport
	// Corpus is the page set this run's plant record was rendered from; the
	// classifier reads reference depths over it and validation proves it
	// before any episode spends wall clock.
	Corpus graphfix.Corpus
	Cells  []graphfix.CompletionCell
	// SearchEnabled distinguishes the search arms from the no-search arms. In
	// a no-search run the client is invoked with the search tool disallowed
	// and each prompt opens with the cell's entry reference, because an
	// episode that cannot search has no other way to hold a reference at all.
	SearchEnabled bool
	// ElicitCompleteness appends PromptCompleteness to every cell prompt and
	// grades each final document's completeness claim, so overclaim is
	// measurable (#1250). The probe ran without it; study runs set it.
	ElicitCompleteness bool
	K                  int
	OutDir             string
	GitCommit          string
	// ClientVersion and DisallowedTools describe the episode client as it was
	// actually invoked, for the manifest.
	ClientVersion   string
	DisallowedTools []string
	Log             *slog.Logger
}

// Manifest describes one probe run.
type Manifest struct {
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	GitCommit       string    `json:"git_commit"`
	PlatformVersion string    `json:"platform_version,omitempty"`
	Model           string    `json:"model"`
	ClientVersion   string    `json:"client_version,omitempty"`
	DisallowedTools []string  `json:"disallowed_tools,omitempty"`
	// Probe is probeName for completion archives; lookup-era archives carry
	// no probe field, which is how reread dispatches.
	Probe string `json:"probe,omitempty"`
	// Arm is the corpus arm, from the plant record: "graph" or "stripped".
	Arm string `json:"arm,omitempty"`
	// SearchEnabled records the search condition of this run.
	SearchEnabled bool `json:"search_enabled"`
	// ElicitCompleteness records whether prompts carried the completeness
	// elicitation and claims were graded.
	ElicitCompleteness bool `json:"elicit_completeness,omitempty"`
	Cells              int  `json:"cells"`
	K                  int  `json:"k"`
	Attempts           int  `json:"attempts"`
	// Exploratory is always true: a premise probe is a decision input, never a
	// published finding (the study lifecycle, bench/docs/findings-register.md).
	Exploratory bool `json:"exploratory"`
	// Scaffold is the episode system prompt, verbatim.
	Scaffold string `json:"scaffold"`
	// CorpusPages is how many pages the fixture planted.
	CorpusPages int `json:"corpus_pages"`
}

// CompletionAttempt is one completion cell run once.
type CompletionAttempt struct {
	CellID    string            `json:"cell_id"`
	Replicate int               `json:"replicate"`
	Seq       int               `json:"seq"`
	Email     string            `json:"email"`
	Prompt    string            `json:"prompt"`
	WallMS    int64             `json:"wall_ms"`
	ToolCalls int               `json:"tool_calls"`
	Reading   CompletionReading `json:"reading"`
	Coverage  Coverage          `json:"coverage"`
	// Claim is the document's graded completeness claim, present only when
	// the run elicited one; Overclaim is the claim read against Coverage.
	Claim     *CompletenessClaim `json:"claim,omitempty"`
	Overclaim bool               `json:"overclaim,omitempty"`
	// FinalDoc is the episode's final message, the graded document. Archived
	// in the results as well as the transcript because it is the primary
	// evidence for every coverage number.
	FinalDoc string `json:"final_doc"`
	// Error is a harness failure. An attempt with an error is not a result and
	// must not be counted as one.
	Error string `json:"error,omitempty"`
}

// CompletionResults is the archived completion run.
type CompletionResults struct {
	Manifest Manifest                  `json:"manifest"`
	Gate     GateReport                `json:"gate"`
	Planted  Planted                   `json:"planted"`
	Cells    []graphfix.CompletionCell `json:"cells"`
	Attempts []CompletionAttempt       `json:"attempts"`
}

// Run executes every cell k times under one (arm, search) condition and
// writes the archive.
//
// It refuses to start on a gate that did not pass or that swept a different
// plant arm than the one it is running against: the gate is the probe's
// pre-stated precondition and an archive must carry the reading that gated
// the corpus it actually ran on.
func Run(ctx context.Context, opts Options) (*CompletionResults, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	res := &CompletionResults{
		Manifest: Manifest{
			StartedAt: time.Now().UTC(), GitCommit: opts.GitCommit,
			Model: opts.Runner.Model(), ClientVersion: opts.ClientVersion,
			DisallowedTools: opts.DisallowedTools,
			Probe:           probeName, Arm: opts.Planted.Arm(), SearchEnabled: opts.SearchEnabled,
			ElicitCompleteness: opts.ElicitCompleteness,
			Cells:              len(opts.Cells), K: opts.K, Exploratory: true,
			Scaffold: opts.scaffold(), CorpusPages: len(opts.Planted.Pages),
		},
		Gate: opts.Gate, Planted: opts.Planted, Cells: opts.Cells,
	}
	seq := 0
	for rep := 1; rep <= opts.K; rep++ {
		for _, cell := range opts.Cells {
			seq++
			log.Info("cell attempt", "cell", cell.ID, "arm", res.Manifest.Arm,
				"search", opts.SearchEnabled, "replicate", rep, "seq", seq)
			a := opts.attempt(ctx, cell, rep, seq, res)
			res.Attempts = append(res.Attempts, a)
			if a.Error != "" {
				log.Warn("attempt failed", "cell", cell.ID, "error", a.Error)
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

// scaffold returns the system prompt for this run's search condition.
func (o Options) scaffold() string {
	if o.SearchEnabled {
		return SystemSearch
	}
	return SystemNoSearch
}

// prompt composes one cell's episode prompt. The no-search arms open with the
// entry reference; the search arms are the bare task. An eliciting run
// appends the frozen completeness suffix in every arm identically.
func (o Options) prompt(cell graphfix.CompletionCell) (string, error) {
	text := cell.Prompt
	if o.ElicitCompleteness {
		text += "\n\n" + PromptCompleteness
	}
	if o.SearchEnabled {
		return text, nil
	}
	id := o.Planted.Pages[cell.EntryKey]
	if id == "" {
		return "", fmt.Errorf("graphprobe: plant record holds no id for entry page %s", cell.EntryKey)
	}
	return fmt.Sprintf("%s mcp:knowledge_page:%s.\n\n%s", cell.EntryIntro, id, text), nil
}

// validate rejects a run that cannot produce interpretable results.
func (o Options) validate() error {
	if err := o.validateShape(); err != nil {
		return err
	}
	if err := o.validateGate(); err != nil {
		return err
	}
	if err := o.validateCellsBelong(); err != nil {
		return err
	}
	return o.Corpus.Validate()
}

// validateShape checks the run's own fields.
func (o Options) validateShape() error {
	switch {
	case o.Runner == nil:
		return errors.New("graphprobe: no episode runner")
	case len(o.Corpus.Pages) == 0:
		return errors.New("graphprobe: no corpus; an empty page set would validate and then classify every read at depth -1")
	case len(o.Cells) == 0:
		return errors.New("graphprobe: no cells")
	case o.K < 1:
		return fmt.Errorf("graphprobe: k must be positive, got %d", o.K)
	case o.OutDir == "":
		return errors.New("graphprobe: no output directory")
	case len(o.Planted.Pages) == 0:
		return errors.New("graphprobe: no planted corpus; run the plant first and pass its record")
	case o.IdentityKeys > 0 && o.IdentityKeys < len(o.Cells)*o.K:
		return fmt.Errorf("graphprobe: %d cells x k=%d needs %d identities, pool holds %d",
			len(o.Cells), o.K, len(o.Cells)*o.K, o.IdentityKeys)
	}
	return nil
}

// validateCellsBelong rejects cells that are not the run corpus's own: a
// reading graded over the wrong reference graph would be silently
// uninterpretable.
func (o Options) validateCellsBelong() error {
	for _, cell := range o.Cells {
		got, ok := o.Corpus.CellByID(cell.ID)
		if !ok || got.EntryKey != cell.EntryKey {
			return fmt.Errorf("graphprobe: cell %q is not a cell of the run's corpus", cell.ID)
		}
	}
	return nil
}

// validateGate rejects a run whose gate reading cannot vouch for the corpus
// it is about to run against.
func (o Options) validateGate() error {
	switch {
	case len(o.Gate.Results) == 0:
		return errors.New("graphprobe: no fixture-gate reading; the gate is a pre-stated precondition and its result is archived with the run")
	case o.Gate.Stripped != o.Planted.Stripped:
		return errors.New("graphprobe: the gate report swept a different corpus arm than the plant record; re-run the gate against this plant")
	case !o.Gate.Pass:
		return errors.New("graphprobe: the fixture gate did not pass; re-author or drop the failing cell rather than running it")
	}
	return nil
}

// attempt runs one cell once.
func (o Options) attempt(ctx context.Context, cell graphfix.CompletionCell, rep, seq int, res *CompletionResults) CompletionAttempt {
	a := CompletionAttempt{
		CellID: cell.ID, Replicate: rep, Seq: seq,
		Email: pool.Email(seq),
	}
	prompt, err := o.prompt(cell)
	if err != nil {
		a.Error = err.Error()
		return a
	}
	a.Prompt = prompt
	start := time.Now()
	out, err := o.Runner.Run(ctx, claudecli.Request{
		Endpoint:   o.Target.BaseURL,
		Credential: pool.Credential(o.Target.Credential, seq, o.IdentityKeys),
		System:     o.scaffold(),
		Prompt:     prompt,
	})
	a.WallMS = time.Since(start).Milliseconds()
	if err != nil {
		a.Error = fmt.Sprintf("episode: %v", err)
		return a
	}
	if !out.ServerConnected {
		a.Error = fmt.Sprintf("episode: the bench MCP server did not connect (status %q)", out.ServerStatus)
		return a
	}
	a.ToolCalls = out.MCPCalls
	if out.PlatformVersion != "" {
		res.Manifest.PlatformVersion = out.PlatformVersion
	}
	o.writeTranscript(cell, rep, out.Transcript)
	if out.IsError {
		a.Error = "episode: claude reported " + out.Subtype
		return a
	}
	a.FinalDoc = out.FinalText
	a.Reading = ReadCompletion(out.Transcript, o.Corpus, cell, o.Planted)
	a.Coverage = GradeCoverage(out.FinalText, cell, a.Reading)
	if o.ElicitCompleteness {
		claim := ReadCompletenessClaim(out.FinalText)
		a.Claim = &claim
		a.Overclaim = Overclaim(a.Coverage, claim)
	}
	return a
}

// writeTranscript archives one episode's conversation beside the results, so a
// reading can be re-derived from the raw record rather than trusted.
func (o Options) writeTranscript(cell graphfix.CompletionCell, rep int, msgs []llm.Message) {
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
	_ = os.WriteFile(filepath.Join(dir, cell.ID+fmt.Sprintf("-r%d.json", rep)), raw, 0o600)
}

// writeArchive writes the run's results file, refusing to overwrite one that
// already exists so a re-run can never destroy paid-for results.
func writeArchive(dir string, res *CompletionResults) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("graphprobe: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, "results.json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("graphprobe: %s already exists; write into a fresh directory", path)
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("graphprobe: marshaling results: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("graphprobe: writing %s: %w", path, err)
	}
	return nil
}
