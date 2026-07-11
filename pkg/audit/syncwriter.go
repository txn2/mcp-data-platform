package audit

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SyncWriter writes each event to the underlying Logger synchronously, on the
// caller's goroutine, bounded by a per-write timeout. It is the durable
// counterpart to AsyncWriter: it has no queue, so it sheds nothing on
// backpressure — a slow store slows the caller (backpressure) rather than
// dropping events. Compliance deployments choose sync delivery to trade
// tool-call latency for zero queue-overflow drops (issue #898).
//
// A write that fails or exceeds the per-write timeout is a genuinely lost event:
// it is logged and counted (via the same audit_events_dropped_total metric
// AsyncWriter uses, so the loss is visible regardless of delivery mode). Log
// still returns nil in that case — audit must never fail a tool call.
//
// Like AsyncWriter, each write runs against baseCtx (not the caller's request
// context, which the audit middleware deliberately passes as context.Background
// so a client disconnect cannot abandon an audit write mid-request). Close
// cancels baseCtx, so an in-flight write against a stalled store is abandoned at
// shutdown instead of holding a request goroutine — and a connection from the
// shared pool — for the full per-write timeout while the platform tears down
// (the guarantee #884 gave the async path, kept here for the sync path).
type SyncWriter struct {
	logger       Logger
	writeTimeout time.Duration
	metrics      *observability.Metrics
	lost         atomic.Int64

	baseCtx    context.Context
	baseCancel context.CancelFunc
}

// SyncOption configures a SyncWriter.
type SyncOption func(*SyncWriter)

// WithSyncWriteTimeout sets the per-write timeout. Non-positive values are
// ignored so the default timeout stands.
func WithSyncWriteTimeout(d time.Duration) SyncOption {
	return func(w *SyncWriter) {
		if d > 0 {
			w.writeTimeout = d
		}
	}
}

// WithSyncMetrics attaches the observability recorder used to count lost events.
// A nil recorder is accepted; observability.Metrics is itself nil-safe.
func WithSyncMetrics(m *observability.Metrics) SyncOption {
	return func(w *SyncWriter) { w.metrics = m }
}

// NewSyncWriter wraps logger in a synchronous, timeout-bounded writer. Unlike
// NewAsyncWriter it starts no goroutine: each Log call does the store write
// inline. Call Close on shutdown to cancel any in-flight write.
func NewSyncWriter(logger Logger, opts ...SyncOption) *SyncWriter {
	w := &SyncWriter{
		logger:       logger,
		writeTimeout: DefaultAsyncWriteTimeout,
	}
	w.baseCtx, w.baseCancel = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Log writes e to the underlying logger on the caller's goroutine, bounding the
// attempt with the per-write timeout on baseCtx. The passed context is ignored
// (matching AsyncWriter): the audit middleware hands this writer
// context.Background so a request ending does not abandon the write; shutdown
// cancellation flows through Close, not the request context. A failed or
// abandoned write is logged and counted, but Log always returns nil so a store
// outage never fails the tool call.
func (w *SyncWriter) Log(_ context.Context, e Event) error {
	ctx, cancel := context.WithTimeout(w.baseCtx, w.writeTimeout)
	defer cancel()
	if err := w.logger.Log(ctx, e); err != nil {
		n := w.lost.Add(1)
		w.metrics.RecordAuditEventDropped(context.Background())
		slog.Error("audit: sync write failed, event lost",
			"error", err,
			"tool", e.ToolName,
			"request_id", e.RequestID,
			"dropped_total", n,
		)
	}
	return nil
}

// Close cancels baseCtx so any in-flight write against a stalled store is
// abandoned immediately rather than blocking shutdown for the per-write timeout.
// It has no queue to drain, so ctx is unused and Close returns nil at once;
// implementing this Close(ctx) signature lets the audit store adapter treat the
// sync and async writers uniformly (pkg/middleware auditFlusher). Idempotent.
func (w *SyncWriter) Close(_ context.Context) error {
	w.baseCancel()
	return nil
}

// Lost returns the cumulative number of events lost to a failed or abandoned
// synchronous write.
func (w *SyncWriter) Lost() int64 {
	return w.lost.Load()
}
