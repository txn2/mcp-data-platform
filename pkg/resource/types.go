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
	ID      string `json:"id" example:"res_01HK7R9F"`
	Scope   Scope  `json:"scope" example:"persona"`
	ScopeID string `json:"scope_id,omitempty" example:"data-engineer"` // persona name or user sub; empty for global
	// Path is the slash-separated folder path this resource is filed under
	// inside its library, and the tail of its URI ahead of the filename. A
	// one-segment path is what every resource carried before folders (#1529).
	Path          string    `json:"path" example:"runbooks/etl"`
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
	Scopes []ScopeFilter // visibility scopes (derived from claims)
	// Path narrows the listing to one folder and everything beneath it. Empty
	// is the whole library. It is a prefix rather than an equality so opening a
	// folder reports what the folder holds, subfolders included, which is what
	// makes a count under a folder mean anything.
	Path   string
	Tag    string // optional tag filter
	Query  string // optional text search in display_name/description
	Sort   Sort   // ordering; empty selects SortUpdated
	Limit  int
	Offset int
}

// ScopeFilter identifies a single scope+id pair for visibility filtering.
type ScopeFilter struct {
	Scope   Scope
	ScopeID string // empty for global
}

// Update holds mutable fields for a PATCH operation.
//
// Scope and ScopeID are the move (#1502) and are not metadata: they change who
// can see the file and they change its canonical URI. They travel on the same
// request because the permission check a move needs is the one the update route
// already runs -- the caller must be able to modify the resource before the
// destination is even considered -- and splitting them would have meant a second
// route re-deriving CanModifyResource.
type Update struct {
	DisplayName *string  `json:"display_name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Path refiles the resource in another folder of its library. Like Scope it
	// is not metadata: the folder path is half of the resource's URI, so an edit
	// to it rewrites the address and records the one it vacated (#1528).
	Path *string `json:"path,omitempty"`
	// Scope names the library to move the resource into. Nil leaves it where it
	// is, which is what every request that is not a move sends.
	Scope *Scope `json:"scope,omitempty"`
	// ScopeID is the persona name or the user's sub or address, and is empty for
	// the global library. It is read only when Scope is set: a scope id on its
	// own names no library.
	ScopeID *string `json:"scope_id,omitempty"`
}

// Fields reports whether the update carries any metadata edit. A request that is
// only a move has none, and applying an empty Update would still bump
// updated_at and drop the stored embedding for nothing.
func (u Update) Fields() bool {
	return u.DisplayName != nil || u.Description != nil || u.Tags != nil
}

// Relocates reports whether the update moves the resource: into another
// library, into another folder, or both. The two travel together because they
// are the two halves of one URI, and an edit touching either rewrites the
// address once rather than twice (#1528).
func (u Update) Relocates() bool {
	return u.Scope != nil || u.Path != nil
}

// Move is a resource's new home: the resource, the library it is filed in, the
// folder path it takes inside it, the URI those two compose, and the URI it is
// leaving.
//
// FromURI is carried rather than re-read inside the store so the alias the move
// records is the address the caller checked its permissions and its collision
// against, not whatever the row happened to say a moment later.
type Move struct {
	// ID is the resource being refiled. It is on the value rather than a
	// separate argument because a folder rename hands the store a batch.
	ID      string
	Scope   Scope
	ScopeID string
	Path    string
	URI     string
	FromURI string
}
