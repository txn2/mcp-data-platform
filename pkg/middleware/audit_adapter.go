package middleware

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
)

// auditStoreAdapter adapts the PostgreSQL audit store to the middleware.AuditLogger interface.
type auditStoreAdapter struct {
	store *auditpostgres.Store
}

// NewAuditStoreAdapter creates an AuditLogger that logs to a PostgreSQL audit store.
func NewAuditStoreAdapter(store *auditpostgres.Store) AuditLogger {
	return &auditStoreAdapter{store: store}
}

// Log records an audit event by converting from middleware.AuditEvent to audit.Event.
func (a *auditStoreAdapter) Log(ctx context.Context, event AuditEvent) error {
	// Convert middleware.AuditEvent to audit.Event
	auditEvent := audit.NewEvent(event.ToolName).
		WithRequestID(event.RequestID).
		WithUser(event.UserID, event.UserEmail).
		WithPersona(event.Persona).
		WithToolkit(event.ToolkitKind, event.ToolkitName).
		WithConnection(event.Connection).
		WithParameters(audit.SanitizeParameters(event.Parameters)).
		WithResult(event.Success, event.ErrorMessage, event.DurationMS)

	// Override timestamp from the event
	auditEvent.Timestamp = event.Timestamp

	return a.store.Log(ctx, *auditEvent)
}

// Close releases resources. The adapter itself has no resources to release,
// as the store lifecycle is managed by the platform.
func (a *auditStoreAdapter) Close() error {
	return nil
}
