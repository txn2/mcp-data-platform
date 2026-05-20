package platform

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestBoundedStop_CleanCompletionReturnsNil asserts the happy path:
// when fn returns before the context deadline, boundedStop returns
// nil and the caller observes the stop as successful.
func TestBoundedStop_CleanCompletionReturnsNil(t *testing.T) {
	var ran atomic.Bool
	err := boundedStop(context.Background(), "test", func() {
		ran.Store(true)
	})
	if err != nil {
		t.Errorf("boundedStop returned %v; want nil on clean completion", err)
	}
	if !ran.Load() {
		t.Errorf("boundedStop did not invoke fn")
	}
}

// TestBoundedStop_DeadlineReturnsCtxErr proves the bounded-shutdown
// invariant: a hung fn must not stall past ctx.Done. The platform
// shutdown chain (cmd/main.go closeServer -> Platform.Stop ->
// lifecycle.OnStop callbacks) relies on this to fit inside the K8s
// terminationGracePeriodSeconds budget.
func TestBoundedStop_DeadlineReturnsCtxErr(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := boundedStop(ctx, "hung-component", func() {
		<-release // simulate a worker that refuses to stop
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("boundedStop err = %v; want context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("boundedStop took %v; want return shortly after 50ms deadline", elapsed)
	}
}

// TestBoundedStop_PreCanceledCtxReturnsImmediately covers the edge
// case where the caller passes an already-canceled context. fn may
// or may not run (race between the goroutine launching and the
// select observing ctx.Done), but boundedStop must return promptly
// either way.
func TestBoundedStop_PreCanceledCtxReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := boundedStop(ctx, "test", func() {
		// Either runs or doesn't, but must not block.
		time.Sleep(10 * time.Millisecond)
	})
	elapsed := time.Since(start)

	if err == nil {
		// Race: fn finished before the select saw ctx.Done. That is
		// still correct behavior (no leak, returned promptly).
		return
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("boundedStop err = %v; want context.Canceled or nil (race-tolerant)", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("boundedStop took %v; want immediate return on pre-canceled ctx", elapsed)
	}
}
