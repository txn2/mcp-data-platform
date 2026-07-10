package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

const (
	// DefaultAsyncQueueCapacity is the bounded-queue depth used when no
	// capacity option is supplied. Sized so a brief store stall (a slow
	// transaction, a failover) buffers rather than drops, while a
	// sustained outage sheds load instead of growing without bound.
	DefaultAsyncQueueCapacity = 1024

	// DefaultAsyncWriteTimeout bounds a single store write. The store must
	// honor context cancellation (the PostgreSQL store's ExecContext
	// does); a write that exceeds this deadline is abandoned and the
	// writer proceeds to the next event.
	DefaultAsyncWriteTimeout = 5 * time.Second
)

// AsyncWriter decouples audit emission from storage latency. Events are queued
// on a bounded channel and written by a single goroutine with a per-write
// timeout. When the queue is full the event is dropped and counted; audit
// delivery is best-effort by design.
//
// The single-goroutine design is the bound: no matter how many concurrent tool
// calls enqueue or how long the store stalls, exactly one write goroutine
// exists, so a stalled database can never accumulate goroutines. Compare the
// old per-call detached goroutine, which grew without limit under a stalled
// store (issue #884).
type AsyncWriter struct {
	logger       Logger
	queue        chan Event
	writeTimeout time.Duration
	metrics      *observability.Metrics

	// mu guards closed and serializes the queue close against concurrent
	// enqueues. Log takes it for reading and holds it across the
	// non-blocking send so Close (write lock) cannot close the channel
	// mid-send; this is what makes close(queue) safe without a
	// send-on-closed-channel panic.
	mu     sync.RWMutex
	closed bool

	// baseCtx is the parent of every per-write timeout context. Close
	// cancels it once its own deadline passes, so any write in flight (or
	// still queued) is abandoned immediately rather than executing against
	// a store the platform is about to tear down (issue #884). A store
	// that honors cancellation therefore returns fast; the drain goroutine
	// then burns through the backlog and exits instead of writing into a
	// closed connection pool.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	dropped atomic.Int64
	done    chan struct{}
}

// AsyncOption configures an AsyncWriter.
type AsyncOption func(*AsyncWriter)

// WithQueueCapacity sets the bounded queue depth. Non-positive values are
// ignored so the default capacity stands.
func WithQueueCapacity(n int) AsyncOption {
	return func(w *AsyncWriter) {
		if n > 0 {
			w.queue = make(chan Event, n)
		}
	}
}

// WithWriteTimeout sets the per-write timeout. Non-positive values are ignored
// so the default timeout stands.
func WithWriteTimeout(d time.Duration) AsyncOption {
	return func(w *AsyncWriter) {
		if d > 0 {
			w.writeTimeout = d
		}
	}
}

// WithMetrics attaches the observability recorder used to count dropped events.
// A nil recorder is accepted; observability.Metrics is itself nil-safe, so the
// writer works unchanged in deployments without metrics wired.
func WithMetrics(m *observability.Metrics) AsyncOption {
	return func(w *AsyncWriter) { w.metrics = m }
}

// NewAsyncWriter wraps logger in a bounded async writer and starts its single
// drain goroutine. Callers must call Close to stop intake and drain the queue.
func NewAsyncWriter(logger Logger, opts ...AsyncOption) *AsyncWriter {
	w := &AsyncWriter{
		logger:       logger,
		writeTimeout: DefaultAsyncWriteTimeout,
		done:         make(chan struct{}),
	}
	w.baseCtx, w.baseCancel = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(w)
	}
	if w.queue == nil {
		w.queue = make(chan Event, DefaultAsyncQueueCapacity)
	}
	go w.run()
	return w
}

// run drains the queue until it is closed, writing each event with a bounded
// per-write timeout. Exactly one instance runs per writer.
func (w *AsyncWriter) run() {
	defer close(w.done)
	for e := range w.queue {
		w.write(e)
	}
}

// write persists one event, abandoning the attempt when the per-write timeout
// elapses or Close cancels baseCtx (the store must honor context
// cancellation). A failed or abandoned write is counted as a lost event and
// logged; audit is best-effort and must never wedge the drain loop.
func (w *AsyncWriter) write(e Event) {
	ctx, cancel := context.WithTimeout(w.baseCtx, w.writeTimeout)
	defer cancel()
	if err := w.logger.Log(ctx, e); err != nil {
		n := w.countLoss()
		slog.Error("audit: async write failed, event lost",
			"error", err,
			"tool", e.ToolName,
			"request_id", e.RequestID,
			"dropped_total", n,
		)
	}
}

// Log enqueues e without blocking. When the queue is full (or the writer is
// closing) the event is dropped, counted, and logged; the caller is never
// blocked or failed. The returned error is always nil — audit delivery is
// best-effort — but the signature satisfies the Logger contract so an
// AsyncWriter can wrap any Logger transparently.
func (w *AsyncWriter) Log(_ context.Context, e Event) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.recordDrop(e)
		return nil
	}
	select {
	case w.queue <- e:
	default:
		w.recordDrop(e)
	}
	return nil
}

// countLoss increments the lost-event counter and its metric, returning the
// new cumulative total. Shared by the queue-full drop path and the
// failed/abandoned write path so audit_events_dropped_total reflects every
// lost event, not just queue overflow.
func (w *AsyncWriter) countLoss() int64 {
	n := w.dropped.Add(1)
	w.metrics.RecordAuditEventDropped(context.Background())
	return n
}

// recordDrop counts one event dropped because the queue was full (or the writer
// was closing) and logs it once.
func (w *AsyncWriter) recordDrop(e Event) {
	n := w.countLoss()
	slog.Error("audit: event dropped, queue full",
		"tool", e.ToolName,
		"request_id", e.RequestID,
		"dropped_total", n,
	)
}

// Dropped returns the cumulative number of lost events: queue-full drops plus
// writes that failed or were abandoned at the per-write timeout.
func (w *AsyncWriter) Dropped() int64 {
	return w.dropped.Load()
}

// QueueDepth returns the number of events currently queued. Exposed for tests
// and diagnostics; it is a point-in-time snapshot with no ordering guarantee.
func (w *AsyncWriter) QueueDepth() int {
	return len(w.queue)
}

// Close stops accepting new events and drains the queue, returning when the
// queue is empty or ctx expires (whichever comes first). When ctx expires with
// events still queued, baseCtx is canceled so the drain goroutine abandons the
// backlog immediately instead of writing into a store the caller is about to
// close; the abandoned events are counted as lost. Close is idempotent and safe
// to call concurrently with Log.
func (w *AsyncWriter) Close(ctx context.Context) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	w.mu.Unlock()

	select {
	case <-w.done:
		w.baseCancel()
		return nil
	case <-ctx.Done():
		// Deadline hit with a backlog remaining: cancel in-flight and
		// queued writes so none execute against the store the caller is
		// about to tear down (issue #884).
		w.baseCancel()
		return fmt.Errorf("audit: async writer drain interrupted: %w", ctx.Err())
	}
}
