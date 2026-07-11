package audit

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// goroutineID returns the current goroutine's ID by parsing the "goroutine N"
// prefix of its stack header. Test-only, used to prove the sync writer runs the
// store write on the caller's goroutine rather than spawning one.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n]) // ["goroutine", "N", "[running]:"]
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// TestSyncWriter_WritesOnCallerGoroutine asserts the store write happens inline,
// on the goroutine that called Log, with no queue or background writer. It
// records the goroutine that ran the write and compares it to the caller's.
func TestSyncWriter_WritesOnCallerGoroutine(t *testing.T) {
	var writeGID, callerGID uint64
	inner := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		writeGID = goroutineID()
		return nil
	}}

	w := NewSyncWriter(inner)
	callerGID = goroutineID()
	if err := w.Log(context.Background(), Event{ToolName: "trino_query"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	if writeGID == 0 {
		t.Fatal("store write never ran")
	}
	if writeGID != callerGID {
		t.Errorf("write ran on goroutine %d, want caller goroutine %d (sync write must not spawn)",
			writeGID, callerGID)
	}
}

// TestSyncWriter_WriteHappensBeforeReturn is the ordering counterpart: the event
// is visible in the store the instant Log returns, no drain/sleep needed.
func TestSyncWriter_WriteHappensBeforeReturn(t *testing.T) {
	store := &recordingLogger{}
	w := NewSyncWriter(store)

	if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	if got := store.count(); got != 1 {
		t.Errorf("expected 1 event written synchronously, got %d", got)
	}
}

// TestSyncWriter_StoreErrorLoggedNeverFails asserts a store error is counted as
// a lost event (via the metrics recorder) but Log still returns nil, so audit
// never fails the tool call.
func TestSyncWriter_StoreErrorLoggedNeverFails(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("building metrics: %v", err)
	}
	inner := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		return errors.New("db down")
	}}
	w := NewSyncWriter(inner, WithSyncMetrics(m))

	if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
		t.Fatalf("Log must return nil even on store error, got %v", err)
	}
	if got := w.Lost(); got != 1 {
		t.Errorf("expected 1 lost event, got %d", got)
	}
}

// TestSyncWriter_NilMetricsSafe verifies a failed write does not panic when no
// metrics recorder is attached (deployments without observability).
func TestSyncWriter_NilMetricsSafe(t *testing.T) {
	inner := &fakeLogger{fn: func(_ context.Context, _ Event) error {
		return errors.New("db down")
	}}
	w := NewSyncWriter(inner, WithSyncMetrics(nil))
	if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if got := w.Lost(); got != 1 {
		t.Errorf("expected 1 lost event, got %d", got)
	}
}

// TestSyncWriter_AppliesWriteTimeout asserts the per-write timeout bounds a
// stalled store: the write context carries a deadline, so a store that honors
// cancellation returns and the event is counted lost rather than blocking
// forever.
func TestSyncWriter_AppliesWriteTimeout(t *testing.T) {
	var hadDeadline bool
	inner := &fakeLogger{fn: func(ctx context.Context, _ Event) error {
		_, hadDeadline = ctx.Deadline()
		<-ctx.Done()
		return ctx.Err()
	}}
	w := NewSyncWriter(inner, WithSyncWriteTimeout(20*time.Millisecond))

	start := time.Now()
	if err := w.Log(context.Background(), Event{ToolName: "tool"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	elapsed := time.Since(start)

	if !hadDeadline {
		t.Error("write context carried no deadline; per-write timeout not applied")
	}
	if elapsed > time.Second {
		t.Errorf("write blocked %v; per-write timeout did not bound it", elapsed)
	}
	if got := w.Lost(); got != 1 {
		t.Errorf("expected 1 lost event from abandoned write, got %d", got)
	}
}

// TestSyncWriter_IgnoresCallerCancellation asserts the passed (request) context
// does NOT govern the write: a canceled request context must not abandon an
// audit write, matching AsyncWriter and the middleware's context.Background
// contract. The store here would block on its own ctx.Done, so if the write
// were bound to the canceled caller ctx it would abort; instead it completes.
func TestSyncWriter_IgnoresCallerCancellation(t *testing.T) {
	store := &recordingLogger{}
	w := NewSyncWriter(store, WithSyncWriteTimeout(5*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Log(ctx, Event{ToolName: "tool"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if got := store.count(); got != 1 {
		t.Errorf("expected write to complete despite canceled caller ctx, got %d written", got)
	}
	if got := w.Lost(); got != 0 {
		t.Errorf("expected no loss, got %d", got)
	}
}

// TestSyncWriter_CloseCancelsInflight asserts Close cancels a write blocked on a
// stalled store, so shutdown does not hang for the full per-write timeout — the
// guarantee #884 gave the async path, kept for the sync path.
func TestSyncWriter_CloseCancelsInflight(t *testing.T) {
	inner := &fakeLogger{fn: func(ctx context.Context, _ Event) error {
		<-ctx.Done() // stalled store: unblocks only when the write ctx is canceled
		return ctx.Err()
	}}
	w := NewSyncWriter(inner, WithSyncWriteTimeout(30*time.Second))

	done := make(chan struct{})
	go func() {
		_ = w.Log(context.Background(), Event{ToolName: "tool"})
		close(done)
	}()

	// Close cancels baseCtx, abandoning the in-flight write well before the
	// 30s per-write timeout would.
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not abandon the in-flight write")
	}
	if got := w.Lost(); got != 1 {
		t.Errorf("expected abandoned write counted as lost, got %d", got)
	}
}

// TestSyncWriter_NonPositiveTimeoutKeepsDefault guards the option's guard rail.
func TestSyncWriter_NonPositiveTimeoutKeepsDefault(t *testing.T) {
	w := NewSyncWriter(&recordingLogger{}, WithSyncWriteTimeout(0))
	if w.writeTimeout != DefaultAsyncWriteTimeout {
		t.Errorf("expected default write timeout %v, got %v", DefaultAsyncWriteTimeout, w.writeTimeout)
	}
}
