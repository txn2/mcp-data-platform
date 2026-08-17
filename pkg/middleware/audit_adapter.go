package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// auditDrainTimeout bounds how long the adapter's Close waits for the async
// audit writer to flush queued events before giving up. Events still queued
// when it expires are abandoned; audit is best-effort (#884). It is a package
// var, not a const, only so tests can shorten it to exercise the drain-timeout
// path deterministically; runtime code never mutates it.
var auditDrainTimeout = 10 * time.Second

// auditStore defines the interface for audit event storage.
// This allows for easier testing with mock implementations, and lets the
// platform interpose a bounded async writer (pkg/audit.AsyncWriter) between the
// adapter and the PostgreSQL store without the adapter knowing (issue #884).
type auditStore interface {
	Log(ctx context.Context, event audit.Event) error
}

// auditFlusher is implemented by a buffering audit store that must be drained
// on shutdown — pkg/audit.AsyncWriter. The adapter drains it from Close so the
// platform's existing audit-logger Closer path flushes the queue without the
// platform holding a separate writer reference.
type auditFlusher interface {
	Close(ctx context.Context) error
}

// auditFlushWaiter is implemented by a buffering audit store that can be
// drained without being closed — pkg/audit.AsyncWriter. Provenance capture
// waits on it so a call that just completed is readable from the store before
// its sources are resolved (#1320).
type auditFlushWaiter interface {
	Flush(ctx context.Context) error
}

// auditStoreAdapter adapts an audit store to the middleware.AuditLogger interface.
type auditStoreAdapter struct {
	store auditStore
}

// NewAuditStoreAdapter creates an AuditLogger that converts middleware audit
// events and writes them to the given store. The store is typically the
// PostgreSQL audit store wrapped in a bounded async writer, so the adapter's
// Log enqueues without blocking; passing the store directly writes
// synchronously (used by tests).
func NewAuditStoreAdapter(store auditStore) AuditLogger {
	return &auditStoreAdapter{store: store}
}

// Log records an audit event by converting from middleware.AuditEvent to audit.Event.
func (a *auditStoreAdapter) Log(ctx context.Context, event AuditEvent) error {
	// Convert middleware.AuditEvent to audit.Event
	auditEvent := audit.NewEvent(event.ToolName).
		WithRequestID(event.RequestID).
		WithSessionID(event.SessionID).
		WithUser(event.UserID, event.UserEmail).
		WithPersona(event.Persona).
		WithToolkit(event.ToolkitKind, event.ToolkitName).
		WithConnection(event.Connection).
		WithPurpose(event.Purpose).
		WithParameters(audit.SanitizeParameters(event.Parameters)).
		WithResult(event.Success, event.ErrorMessage, event.DurationMS).
		WithResponseSize(event.ResponseChars, event.ContentBlocks).
		WithRequestSize(event.RequestChars).
		WithTransport(event.Transport, event.Source).
		WithEnrichment(event.EnrichmentApplied).
		WithEnrichmentTokens(event.EnrichmentTokensFull, event.EnrichmentTokensDedup).
		WithEnrichmentMode(event.EnrichmentMode).
		WithEnrichmentMatchKind(event.EnrichmentMatchKind).
		WithAuthorized(event.Authorized).
		WithEventKind(audit.EventType(event.EventKind))

	// Override timestamp from the event
	auditEvent.Timestamp = event.Timestamp

	// Keep the id the tool-call middleware minted, so the id the call already
	// handed to its own caller (and to any asset that cited it as a source) is
	// the id of the stored row (#1320). Events assembled outside that path
	// carry none and keep the one NewEvent minted.
	if event.ID != "" {
		auditEvent.ID = event.ID
	}

	if err := a.store.Log(ctx, *auditEvent); err != nil {
		return fmt.Errorf("logging audit event: %w", err)
	}
	return nil
}

// Flush waits for the events this adapter has already enqueued to reach the
// store, so a reader that needs them can see them (#1320). It is a no-op when
// the underlying store writes synchronously — those events are already durable
// — and never fails the caller's own work: a flush error means the reader may
// see a slightly stale log, not that the write path is broken.
func (a *auditStoreAdapter) Flush(ctx context.Context) error {
	f, ok := a.store.(auditFlushWaiter)
	if !ok {
		return nil
	}
	if err := f.Flush(ctx); err != nil {
		return fmt.Errorf("flushing audit writer: %w", err)
	}
	return nil
}

// Close drains the underlying async audit writer, if any, so events queued at
// shutdown are flushed through the store before the platform closes the store
// and database. When the store is not a buffering writer (e.g. a test store or
// the raw PostgreSQL store), there is nothing to drain and Close is a no-op;
// that store's own lifecycle is managed by the platform.
func (a *auditStoreAdapter) Close() error {
	f, ok := a.store.(auditFlusher)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditDrainTimeout)
	defer cancel()
	if err := f.Close(ctx); err != nil {
		return fmt.Errorf("draining audit writer: %w", err)
	}
	return nil
}
