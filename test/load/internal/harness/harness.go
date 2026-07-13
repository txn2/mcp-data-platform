// Package harness assembles the pieces of one load run: it holds the shared
// per-run environment (Env), defines the Scenario/Worker contract each workload
// implements, and runs the fixed pipeline — scrape before, warmup (discarded),
// measured window (recorded, with periodic scrapes and a bounded CPU profile),
// scrape after, capture heap/goroutine profiles, then let the scenario assess
// the result.
package harness

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/mcpc"
	"github.com/txn2/mcp-data-platform/test/load/internal/profile"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
	"github.com/txn2/mcp-data-platform/test/load/internal/runner"
	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
	"github.com/txn2/mcp-data-platform/test/load/internal/stats"
	"github.com/txn2/mcp-data-platform/test/load/internal/target"
)

// cpuProfileMaxSeconds bounds the CPU profile so a long soak does not produce a
// multi-minute profile. The profile samples the first window of the measured
// phase, which is where saturation behavior appears.
const cpuProfileMaxSeconds = 30

// Env is the shared per-run environment handed to a scenario and its workers.
type Env struct {
	Target target.Target
	// HTTP is authenticated per the target's auth mode. Anon sends no auth.
	HTTP *http.Client
	Anon *http.Client
	MCP  *mcpc.Client
	Log  *slog.Logger

	// Scratch lets Setup pass data to workers (e.g. a seeded public share token).
	Scratch sync.Map

	rec       *stats.MultiRecorder
	recording atomic.Bool
}

// Record adds a latency sample for op, but only during the measured phase.
func (e *Env) Record(op string, d time.Duration, err error) {
	if e.recording.Load() {
		e.rec.Record(op, d, err)
	}
}

// Timed times fn and records it under op (measured phase only). It returns fn's
// error so the caller can branch (e.g. reconnect a dropped session).
func (e *Env) Timed(op string, fn func() error) error {
	start := time.Now()
	err := fn()
	e.Record(op, time.Since(start), err)
	return err
}

// RunDefaults are a scenario's recommended run parameters, overridable by flags.
type RunDefaults struct {
	Concurrency int
	Duration    time.Duration
	Warmup      time.Duration
	RatePerSec  float64
}

// MeasuredResetter is an optional Scenario capability. ResetForMeasure is called
// at the warmup→measured boundary (when the latency recorder is reset) so a
// scenario can zero counters it accumulates outside the recorder — for example
// the OAuth scenario's 2xx/429 tallies — keeping them consistent with the
// measured window rather than including discarded warmup iterations.
type MeasuredResetter interface {
	ResetForMeasure()
}

// Worker performs one scenario's repeated unit of work. One Worker exists per
// concurrent slot and may hold per-worker state (an MCP session, a refresh
// token chain).
type Worker interface {
	// Iterate performs one unit of work, recording latencies via Env.
	Iterate(ctx context.Context)
	// Close releases per-worker resources.
	Close()
}

// Scenario is one named workload.
type Scenario interface {
	Name() string
	Description() string
	Defaults() RunDefaults
	// Setup runs once before any worker starts (verify prerequisites, seed data).
	Setup(ctx context.Context, env *Env) error
	// NewWorker builds worker id. Returning an error aborts the run.
	NewWorker(ctx context.Context, env *Env, id int) (Worker, error)
	// Assess inspects the finished report and returns pass/fail assertions.
	Assess(env *Env, rep *report.Report) []report.Assertion
	// Teardown runs once after all workers stop.
	Teardown(ctx context.Context, env *Env)
}

// Options parameterize Execute beyond the scenario's own defaults.
type Options struct {
	Config         runner.Config
	ScrapeInterval time.Duration // 0 disables during-run sampling
	Scraper        *scrape.Scraper
	Profiler       *profile.Capturer
	ProfilePrefix  string
	ReportTarget   string // echoed into report.Config.Target
	AuthMode       string
	ReleaseBuild   bool
}

// NewEnv builds an Env for a target with a fresh recorder.
func NewEnv(t target.Target, log *slog.Logger, httpTimeout time.Duration) *Env {
	authed := t.HTTPClient(httpTimeout)
	return &Env{
		Target: t,
		HTTP:   authed,
		Anon:   t.AnonymousHTTPClient(httpTimeout),
		MCP:    mcpc.New(t.BaseURL, authed),
		Log:    log,
		rec:    stats.NewMultiRecorder(),
	}
}

// Execute runs the full pipeline for one scenario and returns its report. A
// non-nil error is a harness-level failure (setup, worker construction); a
// completed run with failing assertions returns a report with Passed=false and
// a nil error.
func Execute(ctx context.Context, sc Scenario, env *Env, opts Options) (*report.Report, error) {
	if err := sc.Setup(ctx, env); err != nil {
		return nil, err
	}
	defer sc.Teardown(ctx, env)

	workers, err := buildWorkers(ctx, sc, env, opts.Config.Concurrency)
	if err != nil {
		return nil, err
	}
	defer closeWorkers(workers)

	rep := &report.Report{
		Scenario:    sc.Name(),
		Description: sc.Description(),
		StartedAt:   time.Now(),
		Config: report.Config{
			Target:       opts.ReportTarget,
			AuthMode:     opts.AuthMode,
			Concurrency:  opts.Config.Concurrency,
			DurationSec:  opts.Config.Duration.Seconds(),
			WarmupSec:    opts.Config.Warmup.Seconds(),
			RatePerSec:   opts.Config.RatePerSec,
			ReleaseBuild: opts.ReleaseBuild,
		},
	}

	scrapes := &snapshotSet{}
	scrapes.add(scrapeOrLog(ctx, opts.Scraper, "before", env.Log))

	sampler := newSampler(opts.Scraper, opts.ScrapeInterval, scrapes, env.Log)
	cpu := &cpuCapture{prof: opts.Profiler, prefix: opts.ProfilePrefix, dur: opts.Config.Duration, log: env.Log}

	iter := func(ctx context.Context, id int, _ runner.Phase) { workers[id].Iterate(ctx) }
	res := runner.Run(ctx, opts.Config, iter, func() {
		env.rec.Reset()
		if r, ok := sc.(MeasuredResetter); ok {
			r.ResetForMeasure()
		}
		env.recording.Store(true)
		sampler.start(ctx)
		cpu.start(ctx)
	})
	env.recording.Store(false)
	sampler.stop()

	scrapes.add(scrapeOrLog(ctx, opts.Scraper, "after", env.Log))

	rep.FinishedAt = time.Now()
	rep.WallSeconds = res.MeasuredWall.Seconds()
	rep.Operations = env.rec.Summarize(res.MeasuredWall)
	rep.Scrapes = scrapes.ordered()
	rep.ComputeDeltas()
	rep.Profiles = collectProfiles(ctx, cpu, opts.Profiler, opts.ProfilePrefix, env.Log)
	rep.Assertions = sc.Assess(env, rep)
	rep.Passed = rep.AllAssertionsPassed()
	return rep, nil
}

func buildWorkers(ctx context.Context, sc Scenario, env *Env, conc int) ([]Worker, error) {
	conc = max(conc, 1)
	workers := make([]Worker, conc)
	for i := range conc {
		w, err := sc.NewWorker(ctx, env, i)
		if err != nil {
			closeWorkers(workers[:i])
			return nil, err
		}
		workers[i] = w
	}
	return workers, nil
}

func closeWorkers(workers []Worker) {
	for _, w := range workers {
		if w != nil {
			w.Close()
		}
	}
}
