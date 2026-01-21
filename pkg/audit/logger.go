// Package audit provides audit logging for the platform.
package audit

import (
	"context"
	"time"
)

// Logger defines the interface for audit logging.
type Logger interface {
	// Log records an audit event.
	Log(ctx context.Context, event Event) error

	// Query retrieves audit events matching the filter.
	Query(ctx context.Context, filter QueryFilter) ([]Event, error)

	// Close releases resources.
	Close() error
}

// Event represents an auditable event.
type Event struct {
	ID           string         `json:"id"`
	Timestamp    time.Time      `json:"timestamp"`
	DurationMS   int64          `json:"duration_ms"`
	RequestID    string         `json:"request_id"`
	UserID       string         `json:"user_id"`
	UserEmail    string         `json:"user_email,omitempty"`
	Persona      string         `json:"persona,omitempty"`
	ToolName     string         `json:"tool_name"`
	ToolkitKind  string         `json:"toolkit_kind,omitempty"`
	ToolkitName  string         `json:"toolkit_name,omitempty"`
	Connection   string         `json:"connection,omitempty"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Success      bool           `json:"success"`
	ErrorMessage string         `json:"error_message,omitempty"`
}

// QueryFilter defines criteria for querying audit events.
type QueryFilter struct {
	StartTime   *time.Time
	EndTime     *time.Time
	UserID      string
	ToolName    string
	ToolkitKind string
	Success     *bool
	Limit       int
	Offset      int
}

// Config configures audit logging.
type Config struct {
	Enabled       bool
	LogToolCalls  bool
	RetentionDays int
}
