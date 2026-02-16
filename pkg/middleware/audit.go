package middleware

import (
	"context"
	"time"
)

// AuditLogger logs tool calls for auditing.
type AuditLogger interface {
	// Log records an audit event.
	Log(ctx context.Context, event AuditEvent) error
}

// AuditEvent represents an auditable event.
type AuditEvent struct {
	Timestamp         time.Time      `json:"timestamp"`
	RequestID         string         `json:"request_id"`
	SessionID         string         `json:"session_id"`
	UserID            string         `json:"user_id"`
	UserEmail         string         `json:"user_email"`
	Persona           string         `json:"persona"`
	ToolName          string         `json:"tool_name"`
	ToolkitKind       string         `json:"toolkit_kind"`
	ToolkitName       string         `json:"toolkit_name"`
	Connection        string         `json:"connection"`
	Parameters        map[string]any `json:"parameters"`
	Success           bool           `json:"success"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	ErrorCategory     string         `json:"error_category,omitempty"`
	DurationMS        int64          `json:"duration_ms"`
	ResponseChars     int            `json:"response_chars"`
	RequestChars      int            `json:"request_chars"`
	ContentBlocks     int            `json:"content_blocks"`
	Transport         string         `json:"transport"`
	Source            string         `json:"source"`
	EnrichmentApplied bool           `json:"enrichment_applied"`
	Authorized        bool           `json:"authorized"`
}

// NoopAuditLogger discards all audit events.
type NoopAuditLogger struct{}

// Log does nothing.
func (*NoopAuditLogger) Log(_ context.Context, _ AuditEvent) error {
	return nil
}
