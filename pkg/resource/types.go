// Package resource provides the data layer for human-uploaded reference
// material (samples, playbooks, templates, references). Resources are
// scoped to global, persona, or user visibility and stored as blobs in
// S3 with metadata in PostgreSQL.
package resource

import (
	"time"
)

// Scope defines the visibility level of a resource.
type Scope string

const (
	// ScopeGlobal is visible to every authenticated user.
	ScopeGlobal Scope = "global"
	// ScopePersona is visible to users operating under the named persona.
	ScopePersona Scope = "persona"
	// ScopeUser is visible only to the owning user.
	ScopeUser Scope = "user"
)

// Resource represents a human-uploaded reference material entry.
type Resource struct {
	ID            string    `json:"id" example:"res_01HK7R9F"`
	Scope         Scope     `json:"scope" example:"persona"`
	ScopeID       string    `json:"scope_id,omitempty" example:"data-engineer"` // persona name or user sub; empty for global
	Category      string    `json:"category" example:"runbooks"`
	Filename      string    `json:"filename" example:"etl-runbook.md"`
	DisplayName   string    `json:"display_name" example:"ETL Runbook"`
	Description   string    `json:"description" example:"Step-by-step procedures for ETL pipeline operations"`
	MIMEType      string    `json:"mime_type" example:"text/markdown"`
	SizeBytes     int64     `json:"size_bytes" example:"34000"`
	S3Key         string    `json:"s3_key" example:"resources/res_01HK7R9F/etl-runbook.md"`
	URI           string    `json:"uri" example:"mcp://persona/data-engineer/runbooks/etl-runbook.md"`
	Tags          []string  `json:"tags"`
	UploaderSub   string    `json:"uploader_sub" example:"550e8400-e29b-41d4-a716-446655440000"`
	UploaderEmail string    `json:"uploader_email" example:"marcus.johnson@example.com"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// LastReadAt is when the resource's content was last served through any
	// surface, stamped by the read recorder. NULL means never read since the
	// deployment began auditing reads (#1014). It is the durable answer, unlike
	// the audit-derived Usage.LastReadAt, which is bounded by audit retention.
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
	// Usage is the audit-derived read activity of this resource. It is not a
	// stored column: the detail read fills it from the audit rollup, and it is
	// absent everywhere the rollup was not consulted.
	Usage *Usage `json:"usage,omitempty"`
}

// Sort names an ordering for the list path.
type Sort string

const (
	// SortUpdated orders by most recently updated, the default.
	SortUpdated Sort = "updated"
	// SortLastRead orders by most recently read, never-read resources last.
	// It is what a curator hunting dead weight sorts by.
	SortLastRead Sort = "last_read"
)

// orderByClause maps a sort to its SQL ordering. An unknown value falls back to
// the default so a malformed query parameter degrades to the normal list rather
// than failing or reaching the SQL.
func (s Sort) orderByClause() string {
	if s == SortLastRead {
		return "last_read_at DESC NULLS LAST, updated_at DESC"
	}
	return "updated_at DESC"
}

// Filter specifies criteria for listing resources.
type Filter struct {
	Scopes   []ScopeFilter // visibility scopes (derived from claims)
	Category string        // optional category filter
	Tag      string        // optional tag filter
	Query    string        // optional text search in display_name/description
	Sort     Sort          // ordering; empty selects SortUpdated
	Limit    int
	Offset   int
}

// ScopeFilter identifies a single scope+id pair for visibility filtering.
type ScopeFilter struct {
	Scope   Scope
	ScopeID string // empty for global
}

// Update holds mutable fields for a PATCH operation.
type Update struct {
	DisplayName *string  `json:"display_name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Category    *string  `json:"category,omitempty"`
}
