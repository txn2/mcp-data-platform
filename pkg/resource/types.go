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
	ID            string    `json:"id"`
	Scope         Scope     `json:"scope"`
	ScopeID       string    `json:"scope_id,omitempty"` // persona name or user sub; empty for global
	Category      string    `json:"category"`
	Filename      string    `json:"filename"`
	DisplayName   string    `json:"display_name"`
	Description   string    `json:"description"`
	MIMEType      string    `json:"mime_type"`
	SizeBytes     int64     `json:"size_bytes"`
	S3Key         string    `json:"s3_key"`
	URI           string    `json:"uri"`
	Tags          []string  `json:"tags"`
	UploaderSub   string    `json:"uploader_sub"`
	UploaderEmail string    `json:"uploader_email"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Filter specifies criteria for listing resources.
type Filter struct {
	Scopes   []ScopeFilter // visibility scopes (derived from claims)
	Category string        // optional category filter
	Tag      string        // optional tag filter
	Query    string        // optional text search in display_name/description
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
