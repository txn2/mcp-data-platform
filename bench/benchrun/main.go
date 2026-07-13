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

	"github.com/txn2/mcp-data-platform/bench/internal/judge"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycle"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pipeline"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// config carries the parsed flags.
type config struct {
	url          string
	credential   string
	arm          string
	suite        string
	k            int
	llmProvider  string
	model        string
	maxTokens    int64
	script       string
	tasksDir     string
	out          string
	gitCommit    string
	httpTimeout  time.Duration
	llmTimeout   time.Duration
	auditTimeout time.Duration
	identityKeys int
	summarize    string
	compare      string
	compareOut   string
	calibrate    bool
	rubric       string
	calibration  string
	lifecycle    bool
	protocolsDir string
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
	flag.StringVar(&cfg.arm, "arm", "", "benchmark arm (a0|a1|a2|a3), required")
	flag.StringVar(&cfg.suite, "suite", "", "suite filter (s1|s2|s3), empty = all")
	flag.IntVar(&cfg.k, "k", 3, "repeats per task (pass^k)")
	flag.StringVar(&cfg.llmProvider, "llm", "anthropic", "model adapter: anthropic|scripted")
	flag.StringVar(&cfg.model, "model", "claude-sonnet-5", "model id for -llm anthropic")
	flag.Int64Var(&cfg.maxTokens, "max-tokens", 8192, "max tokens per completion")
	flag.StringVar(&cfg.script, "script", "", "playback script for -llm scripted")
	flag.StringVar(&cfg.tasksDir, "tasks", "tasks", "task YAML directory")
	flag.StringVar(&cfg.out, "out", "results.json", "results JSON output path")
	flag.StringVar(&cfg.gitCommit, "git-commit", "", "repository commit for the manifest")
	flag.DurationVar(&cfg.httpTimeout, "http-timeout", 120*time.Second, "platform HTTP timeout")
	flag.DurationVar(&cfg.llmTimeout, "llm-timeout", 5*time.Minute, "model API request timeout")
	flag.DurationVar(&cfg.auditTimeout, "audit-timeout", 15*time.Second, "audit read-back timeout per session")
	flag.IntVar(&cfg.identityKeys, "identity-keys", 264, "per-attempt identity pool size matching the arm config (0 = single identity)")
	flag.StringVar(&cfg.summarize, "summarize", "", "print the human summary of an existing results JSON and exit")
	flag.StringVar(&cfg.compare, "compare", "", "comma-separated per-arm results JSON files: render the cross-arm comparison and exit")
	flag.StringVar(&cfg.compareOut, "compare-out", "", "write the cross-arm comparison markdown to this path (with -compare)")
	flag.BoolVar(&cfg.calibrate, "calibrate", false, "run the judge calibration and print its human-agreement rate")
	flag.StringVar(&cfg.rubric, "rubric", "judge/rubric.yaml", "judge rubric file (with -calibrate)")
	flag.StringVar(&cfg.calibration, "calibration", "judge/calibration.yaml", "judge calibration file (with -calibrate)")
	flag.BoolVar(&cfg.lifecycle, "lifecycle", false, "run the S5 memory-insight-knowledge lifecycle protocols instead of the S1-S3 task suites")
	flag.StringVar(&cfg.protocolsDir, "protocols", "protocols", "protocol YAML directory (with -lifecycle)")
	flag.Parse()
	return cfg
}

// run dispatches the read-only modes (summarize, compare, calibrate) or a full
// benchmark run.
func run(cfg config) error {
	switch {
	case cfg.summarize != "" && cfg.lifecycle:
		res, err := lifecycle.LoadJSON(cfg.summarize)
		if err != nil {
			return err
		}
		fmt.Print(res.HumanSummary())
		return nil
	case cfg.summarize != "":
		res, err := report.LoadJSON(cfg.summarize)
		if err != nil {
			return err
		}
		fmt.Print(res.HumanSummary())
		return nil
	case cfg.compare != "":
		return runCompare(cfg)
	case cfg.calibrate:
		return runCalibrate(cfg)
	case cfg.lifecycle:
		return runLifecycle(cfg)
	default:
		return runBenchmark(cfg)
	}
}

// runLifecycle executes the S5 lifecycle protocols and writes outputs. Like the
// task benchmark, the results JSON is written even on failure so partial
// evidence is never discarded.
func runLifecycle(cfg config) error {
	if cfg.arm == "" {
		return errors.New("-arm is required")
	}
	factory, err := buildLifecycleFactory(cfg)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	res, runErr := lifecycle.Run(context.Background(), lifecycle.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		K:             cfg.k,
		ProtocolsDir:  cfg.protocolsDir,
		TranscriptDir: transcriptDir(cfg.out),
		Factory:       factory,
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		IdentityKeys:  cfg.identityKeys,
		Log:           log,
	})
	if res != nil {
		if err := res.WriteJSON(cfg.out); err != nil {
			return err
		}
		fmt.Print(res.HumanSummary())
		fmt.Println("results:", cfg.out)
	}
	return runErr
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
	factory, err := buildFactory(cfg)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	res, runErr := pipeline.Run(context.Background(), pipeline.Options{
		Target:        target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		HTTPTimeout:   cfg.httpTimeout,
		Arm:           cfg.arm,
		Suite:         cfg.suite,
		K:             cfg.k,
		TasksDir:      cfg.tasksDir,
		TranscriptDir: transcriptDir(cfg.out),
		Factory:       factory,
		LLMProvider:   cfg.llmProvider,
		GitCommit:     cfg.gitCommit,
		AuditTimeout:  cfg.auditTimeout,
		IdentityKeys:  cfg.identityKeys,
		Log:           log,
	})
	if res != nil {
		if err := res.WriteJSON(cfg.out); err != nil {
			return err
		}
		fmt.Print(res.HumanSummary())
		fmt.Println("results:", cfg.out)
	}
	return runErr
}

// transcriptDir derives the transcript directory from the results path.
func transcriptDir(out string) string {
	return filepath.Join(filepath.Dir(out), "transcripts")
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
