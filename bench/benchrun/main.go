// Command benchrun executes the agent-effectiveness benchmark against a
// running mcp-data-platform deployment (issue #930 phase 1, #942): one arm,
// the pilot task set, k repeats, results JSON plus a human summary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/coldstart"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/judge"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycle"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pipeline"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// config carries the parsed flags.
type config struct {
	url           string
	credential    string
	arm           string
	suite         string
	k             int
	llmProvider   string
	model         string
	maxTokens     int64
	claudeBin     string
	mcpServerName string
	script        string
	tasksDir      string
	fixtureURL    string
	fixtureKey    string
	tier          string
	codeSpec      string
	out           string
	gitCommit     string
	httpTimeout   time.Duration
	llmTimeout    time.Duration
	auditTimeout  time.Duration
	sinkTimeout   time.Duration
	identityKeys  int
	summarize     string
	compare       string
	compareOut    string
	calibrate     bool
	rubric        string
	calibration   string
	lifecycle     bool
	protocolsDir  string
	baseline      string
	merge         string
	coldStart     bool
	curriculumDir string
	settle        time.Duration
	supersede     bool
	teachBudget   int
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "benchrun:", err)
		os.Exit(1)
	}
}

// parseFlags reads the CLI configuration.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform base URL (MCP + REST)")
	flag.StringVar(&cfg.credential, "credential", "", "admin API key (Bearer)")
	flag.StringVar(&cfg.arm, "arm", "", "benchmark arm (a0|a1|a2|a3, or b0|b1-lex|b1-hyb|b2 for the API-connection study), required")
	flag.StringVar(&cfg.suite, "suite", "", "suite filter (s1|s2|s3), empty = all")
	flag.IntVar(&cfg.k, "k", 3, "repeats per task (pass^k)")
	flag.StringVar(&cfg.llmProvider, "llm", "anthropic", "model adapter: anthropic|scripted|claude-cli")
	flag.StringVar(&cfg.model, "model", "claude-sonnet-5", "model id (or alias) for -llm anthropic and -llm claude-cli")
	flag.Int64Var(&cfg.maxTokens, "max-tokens", 8192, "max tokens per completion (-llm anthropic)")
	flag.StringVar(&cfg.claudeBin, "claude-bin", "claude", "claude executable for -llm claude-cli")
	flag.StringVar(&cfg.mcpServerName, "mcp-server-name", "bench", "MCP server key in the generated per-attempt config for -llm claude-cli")
	flag.StringVar(&cfg.script, "script", "", "playback script for -llm scripted")
	flag.StringVar(&cfg.tasksDir, "tasks", "tasks", "task YAML directory")
	flag.StringVar(&cfg.fixtureURL, "fixture-url", "", "fixture service base URL: marks an API-connection study run (#1027) with per-attempt reset, state/refusal grading, retrieval and failure-taxonomy analysis (b* arms; use with -tasks tasks-api)")
	flag.StringVar(&cfg.fixtureKey, "fixture-key", os.Getenv("APISVC_KEY"), "fixture service X-API-Key (with -fixture-url)")
	flag.StringVar(&cfg.tier, "tier", "", "API-connection study catalog tier (t0|t1|t2), recorded on the manifest")
	flag.StringVar(&cfg.codeSpec, "code-spec", "", "tier spec fixture placed in the b2 code-mode workspace (required with -arm b2)")
	flag.StringVar(&cfg.out, "out", "results.json", "results JSON output path")
	flag.StringVar(&cfg.gitCommit, "git-commit", "", "repository commit for the manifest")
	flag.DurationVar(&cfg.httpTimeout, "http-timeout", 120*time.Second, "platform HTTP timeout")
	flag.DurationVar(&cfg.llmTimeout, "llm-timeout", 5*time.Minute, "model API request timeout")
	flag.DurationVar(&cfg.auditTimeout, "audit-timeout", 15*time.Second, "audit read-back timeout per session")
	flag.DurationVar(&cfg.sinkTimeout, "sink-timeout", 0, "post-apply sink read-back window for promotions (0 = the promote default, 15s); raise for a store that serves reads slowly")
	flag.IntVar(&cfg.identityKeys, "identity-keys", pool.Size, "per-attempt identity pool size matching the arm config (0 = single identity)")
	flag.StringVar(&cfg.summarize, "summarize", "", "print the human summary of an existing results JSON and exit")
	flag.StringVar(&cfg.compare, "compare", "", "comma-separated per-arm results JSON files: render the cross-arm comparison and exit")
	flag.StringVar(&cfg.compareOut, "compare-out", "", "write the cross-arm comparison markdown to this path (with -compare)")
	flag.BoolVar(&cfg.calibrate, "calibrate", false, "run the judge calibration and print its human-agreement rate")
	flag.StringVar(&cfg.rubric, "rubric", "judge/rubric.yaml", "judge rubric file (with -calibrate)")
	flag.StringVar(&cfg.calibration, "calibration", "judge/calibration.yaml", "judge calibration file (with -calibrate)")
	flag.BoolVar(&cfg.lifecycle, "lifecycle", false, "run the S5 memory-insight-knowledge lifecycle protocols instead of the S1-S3 task suites")
	flag.StringVar(&cfg.protocolsDir, "protocols", "protocols", "protocol YAML directory (with -lifecycle)")
	flag.StringVar(&cfg.baseline, "baseline", "", "committed baseline results JSON: after the run, gate on regression and exit nonzero if the candidate falls below it (S1-S3 per-suite, or S5 lifecycle metrics with -lifecycle)")
	flag.StringVar(&cfg.merge, "merge", "", "comma-separated per-pass lifecycle result JSONs (with -lifecycle): merge independent k=1 passes into one k=N result and exit")
	flag.BoolVar(&cfg.coldStart, "cold-start", false, "run the cold-start knowledge-growth curriculum (issue #963) instead of the task suites")
	flag.StringVar(&cfg.curriculumDir, "curriculum", "curriculum", "curriculum YAML directory (with -cold-start)")
	flag.DurationVar(&cfg.settle, "settle", 5*time.Minute, "pause between a successful promote and the next eval checkpoint (with -cold-start), matching the a3 semantic-cache TTL so a stale pre-promotion cache entry cannot attenuate the lift; 0 disables (scripted smoke)")
	flag.BoolVar(&cfg.supersede, "supersede", false, "run the isolated supersede sub-benchmark (issue #964: the supersede protocols only) instead of the full S5 lifecycle")
	flag.IntVar(&cfg.teachBudget, "teach-budget", 0, "override the per-episode tool-call budget for the capture-bearing stages (teach, update); 0 = protocol budget (issue #964 capture-budget lever)")
	flag.Parse()
	return cfg
}

// run dispatches a read-only mode (summarize, merge, compare, calibrate) or a
// full benchmark run.
func run(cfg config) error {
	if handled, err := runReadOnly(cfg); handled {
		return err
	}
	if cfg.coldStart {
		return runColdStart(cfg)
	}
	if cfg.supersede {
		return runSupersede(cfg)
	}
	if cfg.lifecycle {
		return runLifecycle(cfg)
	}
	return runBenchmark(cfg)
}

// runReadOnly handles the exit-early modes that only read committed results.
// The bool reports whether a mode matched; when false, the caller proceeds to a
// live benchmark run.
func runReadOnly(cfg config) (bool, error) {
	switch {
	case cfg.summarize != "":
		return true, runSummarize(cfg)
	case cfg.merge != "":
		// -merge only makes sense for lifecycle results; refuse rather than fall
		// through to a live (paid) benchmark run when -lifecycle is forgotten.
		if !cfg.lifecycle {
			return true, errors.New("-merge requires -lifecycle (it merges S5 lifecycle passes)")
		}
		return true, runMergeLifecycle(cfg)
	case cfg.compare != "":
		return true, runCompare(cfg)
	case cfg.calibrate:
		return true, runCalibrate(cfg)
	}
	return false, nil
}

// runSummarize prints the human summary of an existing results JSON, choosing
// the result shape from the run-mode flags. The lifecycle, supersede, and
// cold-start summaries re-aggregate from the per-attempt records the file
// carries before printing, so a results file written by an older harness renders
// under the CURRENT metric definitions instead of showing a zero for a metric
// that did not exist when it was written (the capture decomposition, #1136).
// Aggregation is deterministic and derives only from those records, so
// re-deriving never changes what a file written by this harness reports.
func runSummarize(cfg config) error {
	switch {
	case cfg.coldStart:
		res, err := coldstart.LoadJSON(cfg.summarize)
		if err != nil {
			return err
		}
		res.Aggregate()
		fmt.Print(res.HumanSummary())
	case cfg.supersede:
		res, err := lifecycle.LoadSupersedeJSON(cfg.summarize)
		if err != nil {
			return err
		}
		res.Aggregate()
		fmt.Print(res.HumanSummary())
	case cfg.lifecycle:
		res, err := lifecycle.LoadJSON(cfg.summarize)
		if err != nil {
			return err
		}
		res.Aggregate()
		fmt.Print(res.HumanSummary())
	default:
		res, err := report.LoadJSON(cfg.summarize)
		if err != nil {
			return err
		}
		fmt.Print(res.HumanSummary())
	}
	return nil
}

// runLifecycle executes the S5 lifecycle protocols and writes outputs. Like the
// task benchmark, the results JSON is written even on failure so partial
// evidence is never discarded.
func runLifecycle(cfg config) error {
	if cfg.arm == "" {
		return errors.New("-arm is required")
	}
	if err := refuseExistingRunArtifacts(cfg.out); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opts := lifecycle.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		K:             cfg.k,
		ProtocolsDir:  cfg.protocolsDir,
		TranscriptDir: transcriptDir(cfg.out),
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		SinkTimeout:   cfg.sinkTimeout,
		IdentityKeys:  cfg.identityKeys,
		TeachBudget:   cfg.teachBudget,
		// Flush the results file after every protocol so an interruption (timeout,
		// exhausted API budget, crash) never discards completed, paid-for work.
		OnProtocol: func(r *lifecycle.Results) {
			if err := r.WriteJSON(cfg.out); err != nil {
				log.Warn("checkpoint write", "error", err)
			}
		},
		Log: log,
	}
	if err := configureLifecycleProvider(cfg, &opts); err != nil {
		return err
	}
	res, runErr := lifecycle.Run(context.Background(), opts)
	if res != nil {
		if err := writeAndSummarize(res, cfg.out); err != nil {
			return err
		}
	}
	if runErr != nil {
		return runErr
	}
	if cfg.baseline != "" && res != nil {
		return gateOnLifecycleBaseline(res, cfg.baseline)
	}
	return nil
}

// configureLifecycleProvider sets the model-driving fields on a lifecycle
// Options from the config: the real claude-cli runner (plus its client version)
// or the in-process adapter factory. Shared by the full lifecycle and supersede
// runs, which drive episodes identically.
func configureLifecycleProvider(cfg config, opts *lifecycle.Options) error {
	if cfg.llmProvider == claudeCLIProvider {
		runner, version, err := buildClaudeRunner(cfg)
		if err != nil {
			return err
		}
		opts.ClaudeCLI, opts.ClientVersion = runner, version
		return nil
	}
	factory, err := buildLifecycleFactory(cfg)
	if err != nil {
		return err
	}
	opts.Factory = factory
	return nil
}

// gateOnLifecycleBaseline compares the just-completed lifecycle run against a
// committed baseline and returns a non-nil error (nonzero exit) if any headline
// S5 metric regressed beyond the default thresholds, so CI catches a lifecycle
// capability loss the same way it catches an S1-S3 one (issue #966).
func gateOnLifecycleBaseline(candidate *lifecycle.Results, baselinePath string) error {
	base, err := lifecycle.LoadJSON(baselinePath)
	if err != nil {
		return fmt.Errorf("load lifecycle baseline: %w", err)
	}
	if err := lifecycle.BaselineCompatible(candidate, base); err != nil {
		return fmt.Errorf("baseline %s: %w", baselinePath, err)
	}
	t := lifecycle.DefaultThresholds()
	regs := lifecycle.CheckRegression(candidate, base, t)
	fmt.Print(lifecycle.RegressionReport(candidate, base, t, regs))
	if len(regs) > 0 {
		return fmt.Errorf("lifecycle regression gate: %d metric(s) fell below baseline %s", len(regs), baselinePath)
	}
	return nil
}

// runSupersede executes the isolated supersede sub-benchmark (issue #964) and
// writes outputs. It reuses the lifecycle adapter/factory wiring but drives only
// the supersede protocols through teach -> capture -> correct -> status-check,
// producing the focused supersede scorecard. Like the other runs the results
// JSON is flushed per attempt so an interruption never discards paid-for work.
func runSupersede(cfg config) error {
	if cfg.arm == "" {
		return errors.New("-arm is required")
	}
	if cfg.baseline != "" {
		return errors.New("-baseline is not supported with -supersede (the regression gate scores S1-S3 task suites, not supersede metrics)")
	}
	if err := refuseExistingRunArtifacts(cfg.out); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opts := lifecycle.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		K:             cfg.k,
		ProtocolsDir:  cfg.protocolsDir,
		TranscriptDir: transcriptDir(cfg.out),
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		SinkTimeout:   cfg.sinkTimeout,
		IdentityKeys:  cfg.identityKeys,
		TeachBudget:   cfg.teachBudget,
		OnSupersede: func(r *lifecycle.SupersedeResults) {
			if err := r.WriteJSON(cfg.out); err != nil {
				log.Warn("checkpoint write", "error", err)
			}
		},
		Log: log,
	}
	if err := configureLifecycleProvider(cfg, &opts); err != nil {
		return err
	}
	res, runErr := lifecycle.RunSupersede(context.Background(), opts)
	if res != nil {
		if err := writeAndSummarize(res, cfg.out); err != nil {
			return err
		}
	}
	return runErr
}

// runColdStart executes the cold-start knowledge-growth curriculum and writes
// outputs. Like the other runs, the results JSON is flushed per checkpoint so an
// interruption never discards paid-for work. The -baseline gate scores the
// S1-S3 report shape, so it is refused here (cold-start produces a curve, not
// per-suite accuracy).
func runColdStart(cfg config) error {
	if cfg.arm == "" {
		return errors.New("-arm is required")
	}
	if cfg.baseline != "" {
		return errors.New("-baseline is not supported with -cold-start (the regression gate scores S1-S3 task suites, not the learning curve)")
	}
	if err := refuseExistingRunArtifacts(cfg.out); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opts := coldstart.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		K:             cfg.k,
		CurriculumDir: cfg.curriculumDir,
		TasksDir:      cfg.tasksDir,
		TranscriptDir: transcriptDir(cfg.out),
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		SinkTimeout:   cfg.sinkTimeout,
		IdentityKeys:  cfg.identityKeys,
		Settle:        cfg.settle,
		OnCheckpoint: func(r *coldstart.Results) {
			if err := r.WriteJSON(cfg.out); err != nil {
				log.Warn("checkpoint write", "error", err)
			}
		},
		Log: log,
	}
	if cfg.llmProvider == claudeCLIProvider {
		runner, version, err := buildClaudeRunner(cfg)
		if err != nil {
			return err
		}
		opts.ClaudeCLI, opts.ClientVersion = runner, version
	} else {
		factory, err := buildColdStartFactory(cfg)
		if err != nil {
			return err
		}
		opts.Factory = factory
	}
	res, runErr := coldstart.Run(context.Background(), opts)
	if res != nil {
		if err := writeAndSummarize(res, cfg.out); err != nil {
			return err
		}
	}
	return runErr
}

// buildColdStartFactory constructs the per-episode adapter factory: a shared
// stateless model adapter, or a fresh scripted adapter per episode keyed by unit
// (lesson or task id) and stage. The scripted map has the same shape as the
// lifecycle smoke (unit -> stage -> steps), so it reuses the same loader.
func buildColdStartFactory(cfg config) (coldstart.AdapterFactory, error) {
	switch cfg.llmProvider {
	case "anthropic":
		adapter, err := llm.NewAnthropic(cfg.model, cfg.maxTokens, cfg.llmTimeout)
		if err != nil {
			return nil, err
		}
		return func(string, string) (llm.Adapter, error) { return adapter, nil }, nil
	case "scripted":
		if cfg.script == "" {
			return nil, errors.New("-script is required for -llm scripted")
		}
		script, err := llm.LoadLifecycleScript(cfg.script)
		if err != nil {
			return nil, err
		}
		return func(unitID, stage string) (llm.Adapter, error) {
			stages, ok := script[unitID]
			if !ok {
				return nil, fmt.Errorf("cold-start script has no unit %s", unitID)
			}
			steps, ok := stages[stage]
			if !ok {
				return nil, fmt.Errorf("cold-start script has no %s/%s stage", unitID, stage)
			}
			return llm.NewScripted(steps), nil
		}, nil
	default:
		return nil, fmt.Errorf("unknown -llm provider %q", cfg.llmProvider)
	}
}

// summarizable is any run result that can persist itself and render a summary,
// satisfied by report.Results (S1-S3), lifecycle.Results (S5), and
// coldstart.Results (#963), so the run paths share one write-and-print block.
type summarizable interface {
	WriteJSON(path string) error
	HumanSummary() string
}

// writeAndSummarize persists a run's results (so partial evidence is never
// discarded) and prints its human summary.
func writeAndSummarize(res summarizable, out string) error {
	if err := res.WriteJSON(out); err != nil {
		return err
	}
	fmt.Print(res.HumanSummary())
	fmt.Println("results:", out)
	return nil
}

// buildLifecycleFactory constructs the per-episode adapter factory for the
// selected provider: a shared stateless model adapter, or a fresh scripted
// adapter per episode from the committed lifecycle smoke.
func buildLifecycleFactory(cfg config) (lifecycle.AdapterFactory, error) {
	switch cfg.llmProvider {
	case "anthropic":
		adapter, err := llm.NewAnthropic(cfg.model, cfg.maxTokens, cfg.llmTimeout)
		if err != nil {
			return nil, err
		}
		return func(string, string) (llm.Adapter, error) { return adapter, nil }, nil
	case "scripted":
		if cfg.script == "" {
			return nil, errors.New("-script is required for -llm scripted")
		}
		script, err := llm.LoadLifecycleScript(cfg.script)
		if err != nil {
			return nil, err
		}
		return func(protocolID, stage string) (llm.Adapter, error) {
			stages, ok := script[protocolID]
			if !ok {
				return nil, fmt.Errorf("lifecycle script has no protocol %s", protocolID)
			}
			steps, ok := stages[stage]
			if !ok {
				return nil, fmt.Errorf("lifecycle script has no %s/%s stage", protocolID, stage)
			}
			return llm.NewScripted(steps), nil
		}, nil
	default:
		return nil, fmt.Errorf("unknown -llm provider %q", cfg.llmProvider)
	}
}

// runMergeLifecycle combines several independent single-pass (k=1) lifecycle
// runs into one k=N result. Each pass must be a k=1 run of every protocol once
// (the platform reset to clean seeded state between passes), so a protocol's N
// attempts — one per pass — are genuinely independent, which the within-run
// k-repeats of a single benchrun are not (they share one accumulating knowledge
// store). It refuses passes that were not k=1, or that disagree on arm /
// protocol set / model / seed, so a merged scorecard is never silently
// mislabeled or miscounted. Each pass's runs are renumbered to attempt 1..N for
// traceability in the failure list; the metric that cares about N, pass^k, keys
// on protocol id and run count (lifecycle.passKRate), not on the Attempt field.
func runMergeLifecycle(cfg config) error {
	paths := strings.Split(cfg.merge, ",")
	if err := ensureDistinctMergeOutput(cfg.out, paths); err != nil {
		return err
	}
	merged := &lifecycle.Results{}
	for i, p := range paths {
		if err := foldPass(merged, strings.TrimSpace(p), i+1); err != nil {
			return err
		}
	}
	merged.Manifest.K = len(paths)
	merged.Aggregate()
	if err := merged.WriteJSON(cfg.out); err != nil {
		return err
	}
	fmt.Print(merged.HumanSummary())
	fmt.Println("results:", cfg.out)
	// The merged k=N scorecard is the canonical thing CI gates, so -baseline must
	// apply here too (not only to a single-process run) — otherwise the standard
	// multi-pass workflow would silently skip the regression gate.
	if cfg.baseline != "" {
		return gateOnLifecycleBaseline(merged, cfg.baseline)
	}
	return nil
}

// ensureDistinctMergeOutput refuses a merge whose -out would overwrite one of
// its input passes. The merged scorecard is derived data; clobbering a raw
// per-pass file with it would destroy paid-for evidence that can never be
// regenerated without re-running. Collision is detected with os.SameFile
// (device+inode), which — unlike a string comparison of cleaned paths — catches
// a case-variant spelling on a case-insensitive filesystem (macOS/Windows) and a
// symlinked input/output pair, both of which resolve to the same on-disk file.
// If the output path does not yet exist (even via a case-insensitive or symlink
// alias), no input can be overwritten, so the merge is allowed.
func ensureDistinctMergeOutput(out string, inputs []string) error {
	outInfo, err := os.Stat(out)
	if err != nil {
		return nil //nolint:nilerr // a non-existent output cannot overwrite any input
	}
	for _, in := range inputs {
		in = strings.TrimSpace(in)
		inInfo, err := os.Stat(in)
		if err != nil {
			continue // a missing input is foldPass's error to report with a clearer message
		}
		if os.SameFile(outInfo, inInfo) {
			return fmt.Errorf("-out %s is the same file as input pass %s; choose a distinct output path so raw pass data is never overwritten", out, in)
		}
	}
	return nil
}

// foldPass validates one pass and appends its runs to merged as attempt `pass`.
func foldPass(merged *lifecycle.Results, path string, pass int) error {
	r, err := lifecycle.LoadJSON(path)
	if err != nil {
		return err
	}
	if r.Manifest.K != 1 {
		return fmt.Errorf("merge expects single-pass (k=1) inputs; %s has k=%d", path, r.Manifest.K)
	}
	if pass == 1 {
		merged.Manifest = r.Manifest
	} else if err := sameConfig(merged.Manifest, r.Manifest, path); err != nil {
		return err
	}
	if merged.Manifest.StartedAt.IsZero() || r.Manifest.StartedAt.Before(merged.Manifest.StartedAt) {
		merged.Manifest.StartedAt = r.Manifest.StartedAt
	}
	if r.Manifest.FinishedAt.After(merged.Manifest.FinishedAt) {
		merged.Manifest.FinishedAt = r.Manifest.FinishedAt
	}
	for _, run := range r.Runs {
		run.Attempt = pass // one attempt of each protocol per independent pass
		merged.Runs = append(merged.Runs, run)
	}
	return nil
}

// sameConfig refuses to merge passes that were not produced under the same arm,
// protocol set, model, client path, and seed — merging across configurations
// would publish a scorecard mislabeled with pass 1's manifest. The client path
// is compared by provider (anthropic vs claude-cli vs scripted), not exact CLI
// version, mirroring BaselineCompatible: the paths produce incomparable numbers,
// while a benign `claude` patch bump must not split a multi-pass run.
func sameConfig(a, b lifecycle.Manifest, path string) error {
	switch {
	case a.Arm != b.Arm:
		return fmt.Errorf("merge: %s arm %q != %q", path, b.Arm, a.Arm)
	case a.ProtocolSetHash != b.ProtocolSetHash:
		return fmt.Errorf("merge: %s protocol-set hash %q != %q", path, b.ProtocolSetHash, a.ProtocolSetHash)
	case a.Model != b.Model:
		return fmt.Errorf("merge: %s model %q != %q", path, b.Model, a.Model)
	case a.LLMProvider != b.LLMProvider:
		return fmt.Errorf("merge: %s llm provider %q != %q — anthropic and claude-cli passes are not comparable", path, b.LLMProvider, a.LLMProvider)
	case a.Seed != b.Seed:
		return fmt.Errorf("merge: %s seed %d != %d", path, b.Seed, a.Seed)
	}
	return nil
}

// runCompare loads one results JSON per arm, renders the cross-arm comparison
// to the terminal, and optionally writes the markdown page.
func runCompare(cfg config) error {
	paths := strings.Split(cfg.compare, ",")
	all := make([]*report.Results, 0, len(paths))
	for _, p := range paths {
		res, err := report.LoadJSON(strings.TrimSpace(p))
		if err != nil {
			return err
		}
		all = append(all, res)
	}
	cmp := report.NewComparison(all)
	fmt.Print(cmp.HumanTable())
	if cfg.compareOut != "" {
		if err := os.WriteFile(cfg.compareOut, []byte(cmp.Markdown()), 0o600); err != nil {
			return fmt.Errorf("write comparison markdown: %w", err)
		}
		fmt.Println("comparison markdown:", cfg.compareOut)
	}
	return nil
}

// runCalibrate runs the judge over the committed calibration set using the
// rubric's PINNED model (not -model), and prints the human-agreement rate.
func runCalibrate(cfg config) error {
	rubric, err := judge.LoadRubric(cfg.rubric)
	if err != nil {
		return err
	}
	cal, err := judge.LoadCalibration(cfg.calibration, rubric)
	if err != nil {
		return err
	}
	adapter, err := llm.NewAnthropic(rubric.Model, cfg.maxTokens, cfg.llmTimeout)
	if err != nil {
		return err
	}
	res, err := judge.Calibrate(context.Background(), adapter, rubric, cal)
	if err != nil {
		return err
	}
	fmt.Print(res.Summary())
	return nil
}

// runBenchmark executes the pipeline and writes outputs. The results JSON is
// written even when the run fails so partial evidence is never discarded.
func runBenchmark(cfg config) error {
	if cfg.arm == "" {
		return errors.New("-arm is required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opts := pipeline.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		Tier:          cfg.tier,
		Suite:         cfg.suite,
		K:             cfg.k,
		TasksDir:      cfg.tasksDir,
		TranscriptDir: transcriptDir(cfg.out),
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		IdentityKeys:  cfg.identityKeys,
		// Flush the results file after every attempt so an interruption never
		// discards completed, paid-for work.
		OnAttempt: func(r *report.Results) {
			if err := r.WriteJSON(cfg.out); err != nil {
				log.Warn("checkpoint write", "error", err)
			}
		},
		Log: log,
	}
	if cfg.fixtureURL != "" {
		opts.Fixture = fixturectl.New(cfg.fixtureURL, cfg.fixtureKey, cfg.httpTimeout)
	}
	if cfg.llmProvider == claudeCLIProvider {
		runner, version, err := buildClaudeRunner(cfg)
		if err != nil {
			return err
		}
		opts.ClaudeCLI, opts.ClientVersion = runner, version
	} else {
		factory, err := buildFactory(cfg)
		if err != nil {
			return err
		}
		opts.Factory = factory
	}
	res, runErr := pipeline.Run(context.Background(), opts)
	return finishBenchmark(cfg, res, runErr)
}

// finishBenchmark persists the results (even on failure, so partial evidence is
// never discarded), prints the summary, and — on a clean run with -baseline set
// — gates on per-suite regression against the committed baseline.
func finishBenchmark(cfg config, res *report.Results, runErr error) error {
	if res != nil {
		if err := writeAndSummarize(res, cfg.out); err != nil {
			return err
		}
	}
	if runErr != nil {
		return runErr
	}
	if cfg.baseline != "" && res != nil {
		return gateOnBaseline(res, cfg.baseline)
	}
	return nil
}

// gateOnBaseline compares the just-completed run against a committed baseline and
// returns a non-nil error (nonzero exit) if any suite regressed beyond the
// default thresholds, so a CI run fails loudly on a real capability loss.
func gateOnBaseline(candidate *report.Results, baselinePath string) error {
	base, err := report.LoadJSON(baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline: %w", err)
	}
	if err := report.BaselineCompatible(candidate, base); err != nil {
		return fmt.Errorf("baseline %s: %w", baselinePath, err)
	}
	t := report.DefaultThresholds()
	regs := report.CheckRegression(candidate, base, t)
	fmt.Print(report.RegressionReport(candidate, base, t, regs))
	if len(regs) > 0 {
		return fmt.Errorf("regression gate: %d suite metric(s) fell below baseline %s", len(regs), baselinePath)
	}
	return nil
}

// transcriptDir derives the per-run transcript directory from the results path.
// It keys on the FULL output filename (extension included), not just its parent
// directory or stem, so several passes or runs written into the SAME directory
// under different -out names each get their own transcript directory and never
// overwrite one another's raw transcripts (results.json -> results.json.transcripts/).
// Keeping the extension means two outputs that share a stem but differ by
// extension (results.json, results.txt) do not collide. Isolating on the output
// name means multi-pass orchestration cannot silently clobber paid-for data.
func transcriptDir(out string) string {
	return filepath.Join(filepath.Dir(out), filepath.Base(out)+".transcripts")
}

// refuseExistingRunArtifacts errors when out or its sibling transcript
// directory already exists. A run's results and transcripts are paid-for
// evidence that can never be regenerated; an existing -out would be silently
// clobbered by the per-checkpoint flush, and the transcript dir by the
// deterministic per-episode filenames — a run interrupted before its first
// flush leaves only transcripts, which a rerun to the same -out would destroy.
// The timestamped-run modes (-cold-start, -lifecycle, -supersede) enforce this;
// the default S1-S3 task mode deliberately does NOT: its -out is the fixed
// per-arm slot (build/bench-results/results-<arm>.json) that bench-compare and
// the CI regression gate consume, so overwriting the slot on each run is that
// mode's documented contract — copy a slot result aside (as bench/results/
// does) before rerunning an arm. There is deliberately no override on the
// guarded modes: the operator picks a new path, every run's data is kept.
func refuseExistingRunArtifacts(out string) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("-out %s already exists; refusing to overwrite a prior run's results; choose a new -out path (all run data is kept)", out)
	}
	if dir := transcriptDir(out); dirExists(dir) {
		return fmt.Errorf("transcript directory %s already exists (an earlier run's episode transcripts); refusing to overwrite; choose a new -out path (all run data is kept)", dir)
	}
	return nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// claudeCLIProvider is the -llm value selecting the real Claude Code client
// path (see package claudecli).
const claudeCLIProvider = "claude-cli"

// buildClaudeRunner constructs the claude-cli runner and probes the Claude Code
// version for the manifest, so a subscription run made through Claude Code is
// never silently compared against a raw Messages API run.
func buildClaudeRunner(cfg config) (*claudecli.Runner, string, error) {
	opts := claudecli.Options{
		Bin:        cfg.claudeBin,
		Model:      cfg.model,
		ServerName: cfg.mcpServerName,
	}
	// The b2 arm is code mode (#1027): no MCP server; the workspace carries
	// the tier spec and the model calls the fixture API itself.
	if cfg.arm == "b2" {
		if cfg.codeSpec == "" {
			return nil, "", errors.New("-arm b2 requires -code-spec (the tier spec fixture for the workspace)")
		}
		if cfg.fixtureURL == "" {
			return nil, "", errors.New("-arm b2 requires -fixture-url (the model calls the fixture API directly)")
		}
		spec, err := os.ReadFile(cfg.codeSpec) // #nosec G304 -- operator-supplied spec fixture path
		if err != nil {
			return nil, "", fmt.Errorf("read -code-spec: %w", err)
		}
		opts.CodeMode = true
		opts.Workspace = map[string][]byte{"spec.json": spec}
	}
	runner, err := claudecli.New(opts)
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	version, err := runner.Version(ctx)
	if err != nil {
		return nil, "", err
	}
	return runner, version, nil
}

// buildFactory constructs the adapter factory for the selected provider.
func buildFactory(cfg config) (pipeline.AdapterFactory, error) {
	switch cfg.llmProvider {
	case "anthropic":
		adapter, err := llm.NewAnthropic(cfg.model, cfg.maxTokens, cfg.llmTimeout)
		if err != nil {
			return nil, err
		}
		return func(task.Task) (llm.Adapter, error) { return adapter, nil }, nil
	case "scripted":
		if cfg.script == "" {
			return nil, errors.New("-script is required for -llm scripted")
		}
		script, err := llm.LoadScript(cfg.script)
		if err != nil {
			return nil, err
		}
		return func(t task.Task) (llm.Adapter, error) {
			steps, ok := script[t.ID]
			if !ok {
				return nil, fmt.Errorf("script has no steps for task %s", t.ID)
			}
			return llm.NewScripted(steps), nil
		}, nil
	default:
		return nil, fmt.Errorf("unknown -llm provider %q", cfg.llmProvider)
	}
}
