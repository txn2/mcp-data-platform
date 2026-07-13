package runner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSeparatesWarmupAndMeasured(t *testing.T) {
	var warmup, measured atomic.Int64
	var resetCalled atomic.Bool

	iter := func(_ context.Context, _ int, phase Phase) {
		if phase == Warmup {
			warmup.Add(1)
		} else {
			measured.Add(1)
		}
		time.Sleep(time.Millisecond)
	}
	onStart := func() { resetCalled.Store(true) }

	res := Run(context.Background(), Config{
		Concurrency: 4,
		Warmup:      40 * time.Millisecond,
		Duration:    80 * time.Millisecond,
	}, iter, onStart)

	if !resetCalled.Load() {
		t.Error("onMeasuredStart was not called between phases")
	}
	if warmup.Load() == 0 {
		t.Error("expected warmup iterations")
	}
	if measured.Load() == 0 {
		t.Error("expected measured iterations")
	}
	if res.MeasuredWall < 60*time.Millisecond {
		t.Errorf("measured wall %v shorter than the measured phase", res.MeasuredWall)
	}
}

func TestRunNoWarmup(t *testing.T) {
	var warmup atomic.Int64
	iter := func(_ context.Context, _ int, phase Phase) {
		if phase == Warmup {
			warmup.Add(1)
		}
	}
	Run(context.Background(), Config{Concurrency: 2, Duration: 20 * time.Millisecond}, iter, nil)
	if warmup.Load() != 0 {
		t.Errorf("expected no warmup iterations, got %d", warmup.Load())
	}
}

func TestRunRateLimited(t *testing.T) {
	var count atomic.Int64
	iter := func(_ context.Context, _ int, _ Phase) { count.Add(1) }

	// 50/s for 200ms with high concurrency should yield ~10 iterations,
	// bounded well below what unbounded workers would produce. Allow slack for
	// the initial burst and scheduling.
	Run(context.Background(), Config{
		Concurrency: 20,
		Duration:    200 * time.Millisecond,
		RatePerSec:  50,
	}, iter, nil)

	got := count.Load()
	if got == 0 {
		t.Fatal("rate-limited run produced no iterations")
	}
	if got > 40 {
		t.Errorf("rate limiter ineffective: %d iterations in 200ms at 50/s (want ~10-30)", got)
	}
}

func TestRunRespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	iter := func(_ context.Context, _ int, _ Phase) {
		count.Add(1)
		time.Sleep(2 * time.Millisecond)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	// Long duration, but cancellation should cut it short.
	Run(ctx, Config{Concurrency: 2, Duration: 5 * time.Second}, iter, nil)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("run did not stop promptly on cancel: %v", elapsed)
	}
}

func TestRunDefaultsConcurrencyToOne(t *testing.T) {
	var count atomic.Int64
	iter := func(_ context.Context, _ int, _ Phase) { count.Add(1); time.Sleep(time.Millisecond) }
	Run(context.Background(), Config{Concurrency: 0, Duration: 20 * time.Millisecond}, iter, nil)
	if count.Load() == 0 {
		t.Error("expected iterations with defaulted concurrency")
	}
}
