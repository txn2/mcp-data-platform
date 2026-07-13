// Command loadgen is the MCP Data Platform load-test generator (issue #921). It
// drives named workloads (mcp-tool-call, mcp-session-churn, oauth-token,
// portal-read, audit-burst, soak) against a running platform over the MCP
// streamable-HTTP protocol and REST surfaces, scrapes the platform's own
// Prometheus metrics before/during/after the run, optionally captures pprof
// profiles, and writes a self-contained JSON report plus a human summary.
//
// It is a separate Go module so it stays out of the root module's coverage,
// test, and lint gates; see test/load/go.mod. Run it via the Makefile
// load-up/load-run/load-down targets, never as part of `make verify`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/profile"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
	"github.com/txn2/mcp-data-platform/test/load/internal/runner"
	"github.com/txn2/mcp-data-platform/test/load/internal/scenario"
	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
	"github.com/txn2/mcp-data-platform/test/load/internal/target"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "loadgen: %v\n", err)
		os.Exit(2)
	}
}

// flags holds the parsed command line. Negative sentinels mean "use the
// scenario's default" so an explicit 0 (no warmup, unbounded rate) is honored.
type flags struct {
	url         string
	metricsURL  string
	pprofURL    string
	authMode    string
	credential  string
	scenario    string
	concurrency int
	duration    time.Duration
	warmup      time.Duration
	rate        float64
	scrapeEvery time.Duration
	out         string
	profileDir  string
	release     bool
	list        bool
}

func parseFlags(args []string) (flags, error) {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	var f flags
	fs.StringVar(&f.url, "url", "http://localhost:8099", "platform base URL (MCP + REST)")
	fs.StringVar(&f.metricsURL, "metrics-url", "http://localhost:9091/metrics", "Prometheus /metrics URL (empty to disable scraping)")
	fs.StringVar(&f.pprofURL, "pprof-url", "", "debug pprof base URL, e.g. http://localhost:6060 (empty to disable profiling)")
	fs.StringVar(&f.authMode, "auth", "apikey", "auth mode: apikey, oauth, none")
	fs.StringVar(&f.credential, "credential", "", "API key or pre-issued OAuth token (defaults to $MCP_API_KEY)")
	fs.StringVar(&f.scenario, "scenario", "", "scenario name (see -list)")
	fs.IntVar(&f.concurrency, "concurrency", 0, "concurrent workers (0 = scenario default)")
	fs.DurationVar(&f.duration, "duration", 0, "measured duration (0 = scenario default)")
	fs.DurationVar(&f.warmup, "warmup", -1, "warmup duration (<0 = scenario default)")
	fs.Float64Var(&f.rate, "rate", -1, "aggregate iterations/sec (<0 = scenario default, 0 = unbounded)")
	fs.DurationVar(&f.scrapeEvery, "scrape-interval", 5*time.Second, "metrics sampling interval during the run")
	fs.StringVar(&f.out, "out", "", "JSON report path (default report-<scenario>.json)")
	fs.StringVar(&f.profileDir, "profile-dir", "profiles", "directory for captured pprof profiles")
	fs.BoolVar(&f.release, "release-build", false, "assert the target is a release build (required for publishable numbers)")
	fs.BoolVar(&f.list, "list", false, "list scenarios and exit")
	if err := fs.Parse(args); err != nil {
		return flags{}, err
	}
	if f.credential == "" {
		f.credential = os.Getenv("MCP_API_KEY")
	}
	return f, nil
}

func run(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if f.list {
		printScenarios()
		return nil
	}
	if f.scenario == "" {
		return errors.New("missing -scenario (see -list)")
	}
	sc, err := scenario.Get(f.scenario)
	if err != nil {
		return err
	}
	tgt, err := buildTarget(f)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := resolveConfig(f, sc.Defaults())
	env := harness.NewEnv(tgt, log, requestTimeout())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	log.Info("starting load run", "scenario", sc.Name(), "concurrency", cfg.Concurrency,
		"duration", cfg.Duration, "warmup", cfg.Warmup, "rate", cfg.RatePerSec, "target", f.url)

	rep, err := harness.Execute(ctx, sc, env, buildOptions(f, cfg))
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	return finish(f, sc.Name(), rep)
}

func printScenarios() {
	fmt.Println("scenarios:")
	for _, n := range scenario.Names() {
		fmt.Printf("  %s\n", n)
	}
}

// buildTarget assembles the target and validates that an auth mode requiring a
// credential has one.
func buildTarget(f flags) (target.Target, error) {
	tgt := target.Target{
		BaseURL:    f.url,
		MetricsURL: f.metricsURL,
		PprofURL:   f.pprofURL,
		Auth:       target.AuthMode(f.authMode),
		Credential: f.credential,
	}
	if (tgt.Auth == target.AuthAPIKey || tgt.Auth == target.AuthOAuthToken) && tgt.Credential == "" {
		return target.Target{}, fmt.Errorf("auth=%s requires -credential or $MCP_API_KEY", tgt.Auth)
	}
	return tgt, nil
}

// finish writes the JSON report, prints the human summary, and returns a
// non-nil error when the scenario's assertions failed (so the process exits
// non-zero for CI).
func finish(f flags, scenarioName string, rep *report.Report) error {
	out := f.out
	if out == "" {
		out = "report-" + scenarioName + ".json"
	}
	if err := rep.WriteJSON(out); err != nil {
		return err
	}
	fmt.Print(rep.HumanSummary())
	fmt.Printf("\nJSON report: %s\n", out)
	if !rep.Passed {
		return errors.New("scenario assertions failed")
	}
	return nil
}

// resolveConfig merges flag overrides over the scenario defaults.
func resolveConfig(f flags, d harness.RunDefaults) harness.RunDefaults {
	if f.concurrency > 0 {
		d.Concurrency = f.concurrency
	}
	if f.duration > 0 {
		d.Duration = f.duration
	}
	if f.warmup >= 0 {
		d.Warmup = f.warmup
	}
	if f.rate >= 0 {
		d.RatePerSec = f.rate
	}
	return d
}

func buildOptions(f flags, d harness.RunDefaults) harness.Options {
	var scraper *scrape.Scraper
	if f.metricsURL != "" {
		scraper = scrape.New(f.metricsURL, nil)
	}
	var profiler *profile.Capturer
	if f.pprofURL != "" {
		profiler = profile.New(f.pprofURL, f.profileDir, nil)
	}
	return harness.Options{
		Config: runner.Config{
			Concurrency: d.Concurrency,
			Warmup:      d.Warmup,
			Duration:    d.Duration,
			RatePerSec:  d.RatePerSec,
		},
		ScrapeInterval: f.scrapeEvery,
		Scraper:        scraper,
		Profiler:       profiler,
		ProfilePrefix:  f.scenario,
		ReportTarget:   f.url,
		AuthMode:       f.authMode,
		ReleaseBuild:   f.release,
	}
}

// requestTimeout sizes the per-request HTTP timeout generously so a slow tool
// call under load is not clipped by the client. CPU profiles stream separately
// with their own client.
func requestTimeout() time.Duration {
	return 60 * time.Second
}
