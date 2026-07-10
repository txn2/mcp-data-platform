package audit

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeLogger is a configurable audit.Logger for exercising the AsyncWriter.
// Log behavior is driven by the injected fn; Query and Close are unused but
// present to satisfy the interface.
type fakeLogger struct {
	fn func(ctx context.Context, e Event) error
}

func (l *fakeLogger) Log(ctx context.Context, e Event) error { return l.fn(ctx, e) }
func (*fakeLogger) Query(_ context.Context, _ QueryFilter) ([]Event, error) {
	return nil, nil
}
func (*fakeLogger) Close() error { return nil }

// recordingLogger collects every event it is asked to write.
type recordingLogger struct {
	mu     sync.Mutex
	events []Event
	delay  time.Duration
}

func (l *recordingLogger) Log(_ context.Context, e Event) error {
	if l.delay > 0 {
		time.Sleep(l.delay)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	return nil
}

func (*recordingLogger) Query(_ context.Context, _ QueryFilter) ([]Event, error) {
	return nil, nil
}
func (*recordingLogger) Close() error { return nil }

func (l *recordingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// TestAsyncWriter_DrainDeliversAll enqueues N events against a slow-but-working
// store, calls Close, and asserts all N rows were written (issue #884 drain
// acceptance criterion).
func TestAsyncWriter_DrainDeliversAll(t *testing.T) {
	store := &recordingLogger{delay: time.Millisecond}
	w := NewAsyncWriter(store)

	const n = 50
	for range n {
		if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if got := store.count(); got != n {
		t.Errorf("expected %d events written, got %d", n, got)
	}
	if got := w.Dropped(); got != 0 {
		t.Errorf("expected 0 drops, got %d", got)
	}
}

// TestAsyncWriter_DropWhenFull fills the queue past capacity against a blocked
// store and asserts Log never blocks and the drop counter equals the overflow
// count (issue #884 drop acceptance criterion).
func TestAsyncWriter_DropWhenFull(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		once.Do(func() { close(started) })
		<-release // block forever until the test releases
		return nil
	}}

	const capacity = 4
	w := NewAsyncWriter(store, WithQueueCapacity(capacity))
	defer close(release)

	// First event is pulled by the drain goroutine and blocks in the store.
	if err := w.Log(context.Background(), Event{ToolName: "first"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	<-started // drain goroutine is now parked inside store.Log

	// Fill the buffered queue exactly to capacity.
	for range capacity {
		if err := w.Log(context.Background(), Event{ToolName: "buffered"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}

	// Every further enqueue overflows and must be dropped without blocking.
	const overflow = 7
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range overflow {
			_ = w.Log(context.Background(), Event{ToolName: "overflow"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Log blocked on a full queue; enqueue must be non-blocking")
	}

	if got := w.Dropped(); got != overflow {
		t.Errorf("expected %d drops, got %d", overflow, got)
	}
	if got := w.QueueDepth(); got != capacity {
		t.Errorf("expected queue depth %d, got %d", capacity, got)
	}
}

// TestAsyncWriter_WriteTimeout proves a store call exceeding the per-write
// timeout is abandoned and the writer proceeds to the next event (issue #884
// timeout acceptance criterion).
func TestAsyncWriter_WriteTimeout(t *testing.T) {
	fast := make(chan struct{}, 1)
	store := &fakeLogger{fn: func(ctx context.Context, e Event) error {
		if e.ToolName == "slow" {
			<-ctx.Done() // honor cancellation: abandoned at the per-write timeout
			return ctx.Err()
		}
		fast <- struct{}{}
		return nil
	}}

	w := NewAsyncWriter(store, WithWriteTimeout(20*time.Millisecond))
	defer func() { _ = w.Close(context.Background()) }()

	if err := w.Log(context.Background(), Event{ToolName: "slow"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if err := w.Log(context.Background(), Event{ToolName: "fast"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	select {
	case <-fast:
		// Writer moved past the timed-out slow event to the fast one.
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not proceed to the next event after a write timeout")
	}

	// The slow event is written before the fast one (single FIFO drain
	// goroutine), so by the time the fast event arrives the timed-out write
	// has already been counted as a lost event. This is what keeps
	// audit_events_dropped_total honest about write-timeout loss (#884).
	if got := w.Dropped(); got != 1 {
		t.Errorf("expected 1 lost event from the write timeout, got %d", got)
	}
}

// TestAsyncWriter_BoundedGoroutines asserts that a store whose Log blocks
// forever does not cause the writer to accumulate one goroutine per enqueue:
// exactly one drain goroutine exists regardless of enqueue volume (issue #884
// bounded-goroutine acceptance criterion, at the writer level).
func TestAsyncWriter_BoundedGoroutines(t *testing.T) {
	release := make(chan struct{})
	store := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		<-release
		return nil
	}}

	before := runtime.NumGoroutine()
	w := NewAsyncWriter(store, WithQueueCapacity(1024))
	defer close(release)

	const n = 100
	for range n {
		if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}

	// Allow any transient goroutines to settle.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	// One drain goroutine (plus scheduler slack); nowhere near +100.
	if grew := after - before; grew > 10 {
		t.Errorf("goroutine count grew by %d after %d enqueues; expected bounded growth", grew, n)
	}
}

// TestAsyncWriter_CloseIdempotent verifies Close can be called more than once
// without panicking on a double channel close.
func TestAsyncWriter_CloseIdempotent(t *testing.T) {
	w := NewAsyncWriter(&recordingLogger{})
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

// TestAsyncWriter_CloseTimeout verifies Close returns a deadline error when the
// store cannot drain the queue before ctx expires.
func TestAsyncWriter_CloseTimeout(t *testing.T) {
	release := make(chan struct{})
	store := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		<-release // ignores ctx, blocks forever
		return nil
	}}
	w := NewAsyncWriter(store)
	defer close(release)

	if err := w.Log(context.Background(), Event{ToolName: "stuck"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := w.Close(ctx)
	if err == nil {
		t.Fatal("expected Close to return a deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// TestAsyncWriter_CloseTimeoutAbandonsBacklog verifies that when Close's
// deadline passes with events still queued, the drain goroutine abandons the
// backlog (via baseCtx cancellation) instead of writing into a store the caller
// is about to tear down, and counts every abandoned event as lost (issue #884
// finding 1). The store here honors context cancellation, as the real
// PostgreSQL store's ExecContext does.
func TestAsyncWriter_CloseTimeoutAbandonsBacklog(t *testing.T) {
	store := &fakeLogger{fn: func(ctx context.Context, _ Event) error {
		<-ctx.Done() // never commits; only ends when the write ctx is canceled
		return ctx.Err()
	}}
	w := NewAsyncWriter(store, WithQueueCapacity(16))

	const n = 8
	for range n {
		if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); err == nil {
		t.Fatal("expected Close to report a drain timeout, got nil")
	}

	// After baseCtx is canceled the backlog drains fast (each write's ctx is
	// already canceled), and every event is counted as lost.
	deadline := time.Now().Add(2 * time.Second)
	for w.Dropped() != n {
		if time.Now().After(deadline) {
			t.Fatalf("expected %d lost events after abandoning backlog, got %d", n, w.Dropped())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAsyncWriter_LogAfterCloseDrops verifies that once closed, Log is
// non-blocking, drops the event, and counts it rather than panicking on a
// send to the closed queue.
func TestAsyncWriter_LogAfterCloseDrops(t *testing.T) {
	w := NewAsyncWriter(&recordingLogger{})
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if err := w.Log(context.Background(), Event{ToolName: "late"}); err != nil {
		t.Fatalf("Log after Close returned error: %v", err)
	}
	if got := w.Dropped(); got != 1 {
		t.Errorf("expected 1 drop after close, got %d", got)
	}
}

// TestAsyncWriter_Defaults verifies the zero-option constructor uses the
// documented default capacity and write timeout.
func TestAsyncWriter_Defaults(t *testing.T) {
	w := NewAsyncWriter(&recordingLogger{})
	defer func() { _ = w.Close(context.Background()) }()

	if cap(w.queue) != DefaultAsyncQueueCapacity {
		t.Errorf("expected default capacity %d, got %d", DefaultAsyncQueueCapacity, cap(w.queue))
	}
	if w.writeTimeout != DefaultAsyncWriteTimeout {
		t.Errorf("expected default write timeout %v, got %v", DefaultAsyncWriteTimeout, w.writeTimeout)
	}
}

// TestAsyncWriter_OptionsIgnoreNonPositive verifies capacity/timeout options
// reject non-positive values and leave the defaults in place.
func TestAsyncWriter_OptionsIgnoreNonPositive(t *testing.T) {
	w := NewAsyncWriter(&recordingLogger{}, WithQueueCapacity(0), WithWriteTimeout(-time.Second))
	defer func() { _ = w.Close(context.Background()) }()

	if cap(w.queue) != DefaultAsyncQueueCapacity {
		t.Errorf("expected default capacity retained, got %d", cap(w.queue))
	}
	if w.writeTimeout != DefaultAsyncWriteTimeout {
		t.Errorf("expected default write timeout retained, got %v", w.writeTimeout)
	}
}

// TestAsyncWriter_NilMetricsSafe verifies the writer drops without panicking
// when no metrics recorder is attached (deployments without observability).
func TestAsyncWriter_NilMetricsSafe(t *testing.T) {
	release := make(chan struct{})
	store := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		<-release
		return nil
	}}
	w := NewAsyncWriter(store, WithQueueCapacity(1), WithMetrics(nil))
	defer close(release)

	// Fill the in-flight slot + the single buffer, then overflow.
	for range 5 {
		if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}
	if w.Dropped() == 0 {
		t.Error("expected at least one drop with a 1-deep queue and blocked store")
	}
}
