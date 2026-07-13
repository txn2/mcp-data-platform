// Package runner drives a fixed pool of worker goroutines through a warmup
// phase and a measured phase, optionally rate-limited to a target throughput.
// It is scenario-agnostic: the caller supplies a per-worker iteration function.
package runner

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config parameterizes a run.
type Config struct {
	Concurrency int
	Warmup      time.Duration
	Duration    time.Duration
	// RatePerSec caps the aggregate iteration rate across all workers. 0 means
	// unbounded (workers iterate as fast as they can). Used by the soak
	// scenario to hold a fixed moderate rate.
	RatePerSec float64
}

// Phase distinguishes warmup iterations (discarded) from measured iterations.
type Phase int

const (
	// Warmup iterations prime caches/connections and are not recorded.
	Warmup Phase = iota
	// Measured iterations are recorded into the report.
	Measured
)

// IterFunc performs one unit of work for worker id during the given phase.
type IterFunc func(ctx context.Context, workerID int, phase Phase)

// Result reports the actual wall-clock of the measured phase.
type Result struct {
	MeasuredWall time.Duration
}

// Run executes the warmup phase (if Warmup>0) then the measured phase,
// invoking onMeasuredStart between them (used to reset the recorder so warmup
// samples are discarded). It returns when both phases complete or ctx is
// canceled. The measured wall clock is the elapsed time of the measured phase.
func Run(ctx context.Context, cfg Config, iter IterFunc, onMeasuredStart func()) Result {
	conc := max(cfg.Concurrency, 1)

	if cfg.Warmup > 0 {
		runPhase(ctx, phaseParams{
			dur: cfg.Warmup, conc: conc, rate: cfg.RatePerSec, phase: Warmup, iter: iter,
		})
	}

	if onMeasuredStart != nil {
		onMeasuredStart()
	}

	start := time.Now()
	runPhase(ctx, phaseParams{
		dur: cfg.Duration, conc: conc, rate: cfg.RatePerSec, phase: Measured, iter: iter,
	})
	return Result{MeasuredWall: time.Since(start)}
}

type phaseParams struct {
	dur   time.Duration
	conc  int
	rate  float64
	phase Phase
	iter  IterFunc
}

// runPhase spins conc workers looping iter until the phase deadline (a call
// already in flight when the deadline fires is allowed to complete, not
// canceled) or until the parent ctx is canceled. When rate>0 a shared limiter
// throttles the aggregate rate.
//
// The distinction matters for clean numbers: iter runs on the parent ctx, while
// a separate deadline context only gates whether a NEW iteration starts. A tool
// call that began inside the measured window therefore finishes and is recorded
// as a real sample instead of being counted as an error when the window ends.
// In-flight calls stay bounded by the harness HTTP-client timeout and by parent
// cancellation (SIGINT).
func runPhase(ctx context.Context, p phaseParams) {
	deadline, cancel := context.WithTimeout(ctx, p.dur)
	defer cancel()

	var limiter *rate.Limiter
	if p.rate > 0 {
		// A small burst keeps the aggregate rate steady (the soak scenario wants
		// a flat moderate rate, not a one-second slug of tokens at startup).
		burst := max(int(p.rate/10), 1)
		limiter = rate.NewLimiter(rate.Limit(p.rate), burst)
	}

	var wg sync.WaitGroup
	for w := range p.conc {
		wg.Go(func() {
			workerLoop(ctx, deadline, w, p, limiter)
		})
	}
	wg.Wait()
}

// workerLoop runs iter on workCtx (the parent) until the deadline context is
// done (window elapsed) or the parent is canceled. The deadline gates loop
// control only; the in-flight iter completes on workCtx.
func workerLoop(workCtx, deadline context.Context, workerID int, p phaseParams, limiter *rate.Limiter) {
	for {
		if deadline.Err() != nil || workCtx.Err() != nil {
			return
		}
		if limiter != nil {
			if err := limiter.Wait(deadline); err != nil {
				return // window ended or parent canceled
			}
		}
		p.iter(workCtx, workerID, p.phase)
	}
}
