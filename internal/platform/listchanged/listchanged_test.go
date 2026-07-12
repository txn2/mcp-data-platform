package listchanged

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// recordingBroadcaster counts publishes and records the methods seen.
type recordingBroadcaster struct {
	mu      sync.Mutex
	count   atomic.Int32
	methods []string
	err     error
}

func (r *recordingBroadcaster) Publish(_ context.Context, ev session.Event) error {
	r.count.Add(1)
	r.mu.Lock()
	r.methods = append(r.methods, ev.Method)
	r.mu.Unlock()
	return r.err
}

func (r *recordingBroadcaster) lastMethod() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.methods) == 0 {
		return ""
	}
	return r.methods[len(r.methods)-1]
}

const testMethod = "notifications/prompts/list_changed"

// TestNotify_PublishesAfterDebounce proves a single Notify results in exactly
// one publish of the configured method once the debounce window elapses.
func TestNotify_PublishesAfterDebounce(t *testing.T) {
	b := &recordingBroadcaster{}
	n := New(b, testMethod)

	n.Notify()
	if got := b.count.Load(); got != 0 {
		t.Fatalf("published %d times before debounce window; want 0", got)
	}

	waitForCount(t, b, 1)
	if m := b.lastMethod(); m != testMethod {
		t.Errorf("method = %q, want %q", m, testMethod)
	}
}

// TestNotify_DebounceCollapsesBurst proves a burst of Notify calls within the
// debounce window collapses to a single publish — a bulk import produces one
// notification, not one per row.
func TestNotify_DebounceCollapsesBurst(t *testing.T) {
	b := &recordingBroadcaster{}
	n := New(b, testMethod)

	for range 50 {
		n.Notify()
	}
	waitForCount(t, b, 1)
	// Give any stray second dispatch time to appear, then assert the burst
	// collapsed. The tight synchronous loop completes well within the debounce
	// window, so every call lands on one timer -> exactly one publish.
	time.Sleep(3 * debounceWindow)
	if got := b.count.Load(); got != 1 {
		t.Errorf("burst produced %d publishes; want 1 (debounce collapse)", got)
	}
}

// TestNotify_NilReceiverAndNilBroadcaster proves both no-op guards: a nil
// *Notifier and a Notifier over a nil broadcaster never panic.
func TestNotify_NilReceiverAndNilBroadcaster(_ *testing.T) {
	var nilNotifier *Notifier
	nilNotifier.Notify() // must not panic
	nilNotifier.Stop()   // must not panic

	n := New(nil, testMethod)
	n.Notify() // no broadcaster -> no-op, no panic
	time.Sleep(2 * debounceWindow)
}

// TestStop_CancelsPendingAndBlocksFuture proves Stop cancels an in-flight
// debounce (so a timer cannot fire after shutdown) and makes later Notify calls
// no-ops.
func TestStop_CancelsPendingAndBlocksFuture(t *testing.T) {
	b := &recordingBroadcaster{}
	n := New(b, testMethod)

	n.Notify()
	n.Stop() // cancel before the window elapses
	time.Sleep(3 * debounceWindow)
	if got := b.count.Load(); got != 0 {
		t.Errorf("Stop did not cancel pending notification: %d publishes", got)
	}

	n.Notify() // post-Stop is a no-op
	time.Sleep(3 * debounceWindow)
	if got := b.count.Load(); got != 0 {
		t.Errorf("Notify after Stop published %d times; want 0", got)
	}

	n.Stop() // idempotent
}

// syncBuffer is a bytes.Buffer guarded by a mutex so the test goroutine can read
// the log while the debounce-timer goroutine writes it, without a data race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p) //nolint:wrapcheck // test io.Writer; bytes.Buffer.Write never errors
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestNotify_PublishErrorSwallowed proves a broadcaster publish error is logged
// and swallowed — list_changed is best-effort and must never surface to the
// write path.
func TestNotify_PublishErrorSwallowed(t *testing.T) {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	b := session.NewMemoryBroadcaster(nil)
	_ = b.Close() // Publish now returns ErrBroadcasterClosed
	n := New(b, testMethod)

	n.Notify()
	// Poll the log buffer for the swallowed-error warning.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "listchanged: publish failed") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("expected publish-failure warning in slog output, got %q", buf.String())
}

// waitForCount blocks until the broadcaster has published at least want times or
// a generous deadline elapses.
func waitForCount(t *testing.T, b *recordingBroadcaster, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.count.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: published %d times, want >= %d", b.count.Load(), want)
}
