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
	ID                    string         `json:"id" example:"evt_a1b2c3d4e5f6"`
	Timestamp             time.Time      `json:"timestamp" example:"2026-04-15T10:41:18Z"`
	DurationMS            int64          `json:"duration_ms" example:"143"`
	RequestID             string         `json:"request_id" example:"req_x9y8z7"`
	SessionID             string         `json:"session_id" example:"sess_abc123"`
	UserID                string         `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserEmail             string         `json:"user_email,omitempty" example:"marcus.johnson@example.com"`
	Persona               string         `json:"persona,omitempty" example:"data-engineer"`
	ToolName              string         `json:"tool_name" example:"datahub_get_schema"`
	ToolkitKind           string         `json:"toolkit_kind,omitempty" example:"datahub"`
	ToolkitName           string         `json:"toolkit_name,omitempty" example:"acme-catalog"`
	Connection            string         `json:"connection,omitempty" example:"acme-catalog"`
	Parameters            map[string]any `json:"parameters,omitempty"`
	Success               bool           `json:"success" example:"true"`
	ErrorMessage          string         `json:"error_message,omitempty"`
	ResponseChars         int            `json:"response_chars" example:"2450"`
	RequestChars          int            `json:"request_chars" example:"120"`
	ContentBlocks         int            `json:"content_blocks" example:"2"`
	Transport             string         `json:"transport" example:"http"`
	Source                string         `json:"source" example:"mcp"`
	EnrichmentApplied     bool           `json:"enrichment_applied" example:"true"`
	EnrichmentTokensFull  int            `json:"enrichment_tokens_full" example:"850"`
	EnrichmentTokensDedup int            `json:"enrichment_tokens_dedup" example:"350"`
	EnrichmentMode        string         `json:"enrichment_mode,omitempty" example:"summary"`
	Authorized            bool           `json:"authorized" example:"true"`
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
	"enrichment_mode":    true,
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
