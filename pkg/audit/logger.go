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
	ID          string    `json:"id" example:"evt_a1b2c3d4e5f6"`
	Timestamp   time.Time `json:"timestamp" example:"2026-04-15T10:41:18Z"`
	DurationMS  int64     `json:"duration_ms" example:"143"`
	RequestID   string    `json:"request_id" example:"req_x9y8z7"`
	SessionID   string    `json:"session_id" example:"sess_abc123"`
	UserID      string    `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserEmail   string    `json:"user_email,omitempty" example:"marcus.johnson@example.com"`
	Persona     string    `json:"persona,omitempty" example:"data-engineer"`
	ToolName    string    `json:"tool_name" example:"datahub_get_schema"`
	ToolkitKind string    `json:"toolkit_kind,omitempty" example:"datahub"`
	ToolkitName string    `json:"toolkit_name,omitempty" example:"acme-catalog"`
	Connection  string    `json:"connection,omitempty" example:"acme-catalog"`
	// Purpose is the one sentence the caller gave for WHY this call was made:
	// the wider task it serves, stated by the agent as the `purpose` argument
	// and taken off the request before the tool saw it (issue #1317). Empty on a
	// call the platform does not gate, on a caller that cannot thread arguments
	// (an MCP App, a script run, the REST shim), and on every row written before
	// the feature existed. It is not an argument value, so the parameter
	// redaction policy does not apply to it.
	Purpose               string         `json:"purpose,omitempty" example:"Sizing Q3 revenue by region for the board deck."`
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
	// EnrichmentMatchKind records how the semantic enrichment matched
	// the target table or column: "urn" when the URN-equality lookup
	// resolved exactly, "semantic" when an exact lookup missed and the
	// platform fell back to similarity search (suggested match, not
	// asserted), or empty when no enrichment ran. Operators use this
	// to measure the false-positive rate of similarity-based
	// suggestions (issue #444).
	EnrichmentMatchKind string `json:"enrichment_match_kind,omitempty" example:"urn"`
	Authorized          bool   `json:"authorized" example:"true"`
	// EventKind is the high-level category of the event, and the value the
	// admin audit API's event_kind filter matches on. A tool call carries
	// "mcp_tool_call" for the MCP toolkits (trino, datahub, s3, mcp gateway)
	// or "apigateway_invoke" for upstream HTTP API calls via the apigateway
	// toolkit, which lets the portal split MCP activity from gateway noise
	// without coupling to tool-name patterns. The rest of the platform's
	// audited acts carry a kind of their own: "prompt_serve",
	// "resource_read", "resource_move", "script_run", and "admin". See the
	// EventType constants in event.go for the complete set.
	EventKind EventType `json:"event_kind,omitempty" example:"mcp_tool_call"`
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
	"source":             true,
	"connection":         true,
	"duration_ms":        true,
	"success":            true,
	"enrichment_applied": true,
	"enrichment_mode":    true,
}

// QueryFilter defines criteria for querying audit events.
type QueryFilter struct {
	ID string
	// IDs selects a set of events by identifier in one query. It exists for
	// provenance capture (issue #1320), which resolves the event ids an agent
	// cited as an asset's sources; combine it with UserID or SessionID to keep
	// the lookup scoped to the caller's own calls. Empty means no id-set filter.
	IDs         []string
	StartTime   *time.Time
	EndTime     *time.Time
	UserID      string
	SessionID   string
	ToolName    string
	ToolkitKind string
	Source      string
	EventKind   string
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
