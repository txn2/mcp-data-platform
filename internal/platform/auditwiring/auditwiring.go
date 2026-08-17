// Package auditwiring assembles the layer that hangs off the audit log.
//
// The audit store is not read only by audit: an asset's provenance resolves its
// sources out of it (#1320), and the call catalog is written from the events
// passing through it (#1321). All three are one assembly — the store, the
// writer the configured delivery mode selects, the catalog decorator between
// them, and the capturer that reads back through the writer's flush barrier —
// and the order they are composed in is load-bearing. Composing them here keeps
// that order in one place, and keeps the platform facade from growing a third
// copy of it.
package auditwiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/provenance"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// cleanupInterval is how often each store prunes rows past their retention.
// Both the audit log and the call catalog sweep on the same cadence; each takes
// its own advisory lock, so several replicas sharing a database still delete
// once per tick.
const cleanupInterval = 24 * time.Hour

// Config is what the assembly needs from the deployment.
type Config struct {
	// DB is the database the audit log and the call catalog live in.
	DB *sql.DB
	// RetentionDays bounds how long audit rows are kept.
	RetentionDays int
	// SyncDelivery writes each event on the request goroutine instead of
	// through the bounded async writer, trading tool-call latency for
	// backpressure and zero queue-overflow drops (#898).
	SyncDelivery bool
	// Metrics receives the writer's counters. Optional.
	Metrics *observability.Metrics
	// BuildURN names the dataset a table reference belongs to, so a
	// cataloged query records the entities it read. Optional: without it a
	// record still carries its statement, purpose and outcome.
	BuildURN callrecord.URNBuilder

	// CallRetentionDays bounds how long a recorded call that came to nothing
	// is kept. Zero takes the catalog's default; a record something was built
	// from, promoted, declined, or re-run is never swept.
	CallRetentionDays int
}

// Layer is the assembled audit layer. Every member is reached through a
// nil-safe accessor: a deployment with audit disabled, and a platform built for
// a test that never assembled one, both answer "nothing here" rather than
// panicking on a write path that legitimately runs either way.
type Layer struct {
	store    *auditpostgres.Store
	logger   middleware.AuditLogger
	calls    *callrecord.PostgresStore
	capturer *provenance.Capturer
}

// Injected wraps a logger a deployment supplied itself. It has no store behind
// it, so nothing derived from the audit log is available: the caller owns the
// delivery guarantees and the storage.
func Injected(logger middleware.AuditLogger) *Layer {
	return &Layer{logger: logger}
}

// Logger is what the middleware writes audit events through.
func (l *Layer) Logger() middleware.AuditLogger {
	if l == nil {
		return nil
	}
	return l.logger
}

// Store is the audit log itself, read by the admin surfaces, the session read
// model and provenance capture. Nil when there is no database.
func (l *Layer) Store() *auditpostgres.Store {
	if l == nil {
		return nil
	}
	return l.store
}

// Calls is the catalog of data-access calls derived from the log.
func (l *Layer) Calls() *callrecord.PostgresStore {
	if l == nil {
		return nil
	}
	return l.calls
}

// Capturer resolves the calls behind one asset write. Its Capture is nil-safe,
// so a write path may call it unconditionally.
func (l *Layer) Capturer() *provenance.Capturer {
	if l == nil {
		return nil
	}
	return l.capturer
}

// Recording reports whether this deployment stores what its calls did.
//
// It is the question callers actually have — whether to hand a call its own
// identifier, whether to record a resource read — and asking it here keeps them
// from reasoning about which member of the layer being nil implies it.
func (l *Layer) Recording() bool { return l.Store() != nil }

// Close shuts the layer down in the order its assembly requires: the logger
// first, so a buffering writer drains its queue THROUGH the store, and only
// then the store and the sweepers that hold the same database handle. Closing
// the store first would strand every event still queued.
//
// A close error never stops the sequence: each step is attempted and the
// errors are joined, because a stalled writer must not keep the store open.
func (l *Layer) Close() error {
	if l == nil {
		return nil
	}
	var errs []error
	if closer, ok := l.logger.(io.Closer); ok {
		slog.Debug("shutdown: draining audit logger")
		errs = append(errs, closer.Close())
	}
	if l.calls != nil {
		slog.Debug("shutdown: stopping the call-catalog sweeper")
		errs = append(errs, l.calls.Close())
	}
	if l.store != nil {
		slog.Debug("shutdown: closing audit store")
		errs = append(errs, l.store.Close())
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("closing the audit layer: %w", err)
	}
	return nil
}

// Assemble builds the audit layer over db and starts the store's retention
// cleanup.
//
// The composition order is the point: the catalog decorator wraps the store
// *inside* the delivery writer, so cataloging a call happens on the writer's
// own goroutine and costs the call nothing; and the capturer is handed the
// finished logger as its flush barrier, so a capture waits for the calls still
// queued in that writer — the newest of which is usually the one that produced
// the asset being saved.
func Assemble(cfg Config) *Layer {
	store := auditpostgres.New(cfg.DB, auditpostgres.Config{RetentionDays: cfg.RetentionDays})
	store.StartCleanupRoutine(cleanupInterval)

	calls := callrecord.NewPostgresStore(cfg.DB, callrecord.Config{RetentionDays: cfg.CallRetentionDays})
	calls.StartCleanupRoutine(cleanupInterval)
	logger := NewLogger(callrecord.NewRecorder(store, calls, cfg.BuildURN), cfg.SyncDelivery, cfg.Metrics)

	return &Layer{
		store:    store,
		logger:   logger,
		calls:    calls,
		capturer: provenance.New(store, AsFlusher(logger)),
	}
}

// NewLogger wraps an audit store in the writer the delivery mode selects and
// adapts it to the middleware's logger interface (#898).
//
// Async (default): a bounded writer with a single drain goroutine, a per-write
// timeout, and drain-on-shutdown replaces the middleware's old per-call detached
// goroutine, which grew without bound under a stalled store (#884); a sustained
// outage sheds events. Sync: write on the request goroutine with a per-write
// timeout. Either way the adapter owns the writer; for async it drains the
// writer on Close through the platform's existing audit-logger Closer path, so
// no extra field is held anywhere.
func NewLogger(store audit.Logger, syncDelivery bool, m *observability.Metrics) middleware.AuditLogger {
	if syncDelivery {
		return middleware.NewAuditStoreAdapter(audit.NewSyncWriter(store, audit.WithSyncMetrics(m)))
	}
	return middleware.NewAuditStoreAdapter(audit.NewAsyncWriter(store, audit.WithMetrics(m)))
}

// AsFlusher returns the audit logger as a provenance flush barrier when it
// buffers writes, and nil when it does not: a synchronous writer has nothing to
// wait for, and neither has a logger a deployment injected itself.
func AsFlusher(logger middleware.AuditLogger) provenance.Flusher {
	f, ok := logger.(provenance.Flusher)
	if !ok {
		return nil
	}
	return f
}

// ProvenQueries adapts the call catalog to the enrichment middleware's lister:
// the caller's own recorded queries that already answered something on a
// dataset, which is what a describe of that table carries beside the catalog's
// own context (#1321).
//
// A nil catalog yields a nil lister rather than one that answers nothing, so a
// deployment without a database appends no such block at all.
func ProvenQueries(calls *callrecord.PostgresStore) middleware.ProvenQueryLister {
	if calls == nil {
		return nil
	}
	return func(ctx context.Context, urn, userID string, limit int) []middleware.ProvenQuery {
		records, err := calls.ForTargets(ctx, []string{urn}, userID, limit)
		if err != nil {
			slog.Debug("proven queries unavailable", "urn", urn, "error", err)
			return nil
		}
		proven := make([]middleware.ProvenQuery, 0, len(records))
		for _, rec := range records {
			proven = append(proven, middleware.ProvenQuery{
				Reference:   rec.Reference,
				Purpose:     rec.Purpose,
				Statement:   rec.Statement,
				Outcome:     rec.Outcome,
				SatisfiedBy: rec.SatisfiedBy,
				ReuseCount:  rec.ReuseCount,
				PromotedURN: rec.PromotedURN,
			})
		}
		return proven
	}
}
