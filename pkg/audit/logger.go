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
	ID                string         `json:"id"`
	Timestamp         time.Time      `json:"timestamp"`
	DurationMS        int64          `json:"duration_ms"`
	RequestID         string         `json:"request_id"`
	SessionID         string         `json:"session_id"`
	UserID            string         `json:"user_id"`
	UserEmail         string         `json:"user_email,omitempty"`
	Persona           string         `json:"persona,omitempty"`
	ToolName          string         `json:"tool_name"`
	ToolkitKind       string         `json:"toolkit_kind,omitempty"`
	ToolkitName       string         `json:"toolkit_name,omitempty"`
	Connection        string         `json:"connection,omitempty"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	Success           bool           `json:"success"`
	ErrorMessage      string         `json:"error_message,omitempty"`
	ResponseChars     int            `json:"response_chars"`
	RequestChars      int            `json:"request_chars"`
	ContentBlocks     int            `json:"content_blocks"`
	Transport         string         `json:"transport"`
	Source            string         `json:"source"`
	EnrichmentApplied bool           `json:"enrichment_applied"`
	Authorized        bool           `json:"authorized"`
}

// SortOrder defines sort direction.
type SortOrder string

const (
	// SortAsc sorts ascending.
	SortAsc SortOrder = "asc"

	// SortDesc sorts descending.
	SortDesc SortOrder = "desc"
)

// ValidSortColumns lists columns that can be used for ORDER BY.
var ValidSortColumns = map[string]bool{
	"timestamp":          true,
	"user_id":            true,
	"tool_name":          true,
	"toolkit_kind":       true,
	"connection":         true,
	"duration_ms":        true,
	"success":            true,
	"enrichment_applied": true,
}

// QueryFilter defines criteria for querying audit events.
type QueryFilter struct {
	ID          string
	StartTime   *time.Time
	EndTime     *time.Time
	UserID      string
	SessionID   string
	ToolName    string
	ToolkitKind string
	Search      string
	Success     *bool
	SortBy      string
	SortOrder   SortOrder
	Limit       int
	Offset      int
}

// Config configures audit logging.
type Config struct {
	Enabled       bool
	LogToolCalls  bool
	RetentionDays int
}
