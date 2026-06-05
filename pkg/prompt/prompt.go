// Package prompt provides prompt management for the MCP data platform.
// It defines the Store interface for prompt persistence and the Prompt type
// that represents a user-managed MCP prompt.
package prompt

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// maxNameLength is the maximum allowed length for a prompt name.
const maxNameLength = 128

// validNamePattern matches lowercase letters, digits, hyphens, and underscores.
var validNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// validScopes is the set of allowed scope values for database-stored prompts.
var validScopes = map[string]bool{
	ScopeGlobal:   true,
	ScopePersona:  true,
	ScopePersonal: true,
}

// ValidateName checks that a prompt name is well-formed.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("name must be at most %d characters", maxNameLength)
	}
	if !validNamePattern.MatchString(name) {
		return fmt.Errorf("name must contain only lowercase letters, digits, hyphens, and underscores")
	}
	return nil
}

// ValidateScope checks that a scope value is allowed for database-stored prompts.
func ValidateScope(scope string) error {
	if !validScopes[scope] {
		return fmt.Errorf("invalid scope %q: must be global, persona, or personal", scope)
	}
	return nil
}

// Scope constants define prompt visibility levels.
const (
	ScopeGlobal   = "global"
	ScopePersona  = "persona"
	ScopePersonal = "personal"
)

// Source constants define prompt origins.
const (
	SourceOperator = "operator"
	SourceAgent    = "agent"
	SourceSystem   = "system"
)

// Status constants define the prompt promotion lifecycle. A prompt starts as
// draft, becomes approved (on admin promotion to persona/global scope), may
// later be deprecated, and is finally superseded by a replacement.
const (
	StatusDraft      = "draft"
	StatusApproved   = "approved"
	StatusDeprecated = "deprecated"
	StatusSuperseded = "superseded"
)

// validStatuses is the set of allowed status values.
var validStatuses = map[string]bool{
	StatusDraft:      true,
	StatusApproved:   true,
	StatusDeprecated: true,
	StatusSuperseded: true,
}

// validStatusTransitions defines the allowed status transitions. It follows the
// same validated-transition-graph pattern as the knowledge-insight lifecycle,
// but with prompt-specific states.
var validStatusTransitions = map[string]map[string]bool{
	StatusDraft:      {StatusApproved: true, StatusSuperseded: true},
	StatusApproved:   {StatusDeprecated: true, StatusSuperseded: true},
	StatusDeprecated: {StatusSuperseded: true},
}

// ValidateStatus checks that a status value is recognized.
func ValidateStatus(status string) error {
	if !validStatuses[status] {
		return fmt.Errorf("invalid status %q: must be draft, approved, deprecated, or superseded", status)
	}
	return nil
}

// ValidateStatusTransition checks whether a status transition is allowed.
func ValidateStatusTransition(from, to string) error {
	allowed, ok := validStatusTransitions[from]
	if !ok || !allowed[to] {
		return fmt.Errorf("invalid status transition from %q to %q", from, to)
	}
	return nil
}

// Argument describes a prompt argument.
type Argument struct {
	Name        string `json:"name" example:"date"`
	Description string `json:"description" example:"The date to analyze (YYYY-MM-DD)"`
	Required    bool   `json:"required" example:"true"`
}

// Prompt represents a user-managed MCP prompt.
type Prompt struct {
	ID          string     `json:"id" example:"prompt_a1b2c3d4"`
	Name        string     `json:"name" example:"daily-sales-report"`
	DisplayName string     `json:"display_name" example:"Daily Sales Report"`
	Description string     `json:"description" example:"Generate a daily sales summary by region"`
	Content     string     `json:"content" example:"Analyze sales data for {date} grouped by region."`
	Arguments   []Argument `json:"arguments"`
	Category    string     `json:"category" example:"analysis"`
	Scope       string     `json:"scope" example:"persona"`
	Personas    []string   `json:"personas" example:"analyst,data-engineer"`
	OwnerEmail  string     `json:"owner_email" example:"admin@example.com"`
	Source      string     `json:"source" example:"database"`
	Enabled     bool       `json:"enabled" example:"true"`
	Tags        []string   `json:"tags" example:"sales,reporting"`

	// Promotion lifecycle.
	Status            string     `json:"status" example:"approved"`
	ApprovedBy        string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt        *time.Time `json:"approved_at,omitempty"`
	DeprecatedAt      *time.Time `json:"deprecated_at,omitempty"`
	SupersededBy      string     `json:"superseded_by,omitempty" example:"daily-sales-report-v2"`
	ReviewRequested   bool       `json:"review_requested" example:"false"`
	RequestedScope    string     `json:"requested_scope,omitempty" example:"persona"`
	RequestedPersonas []string   `json:"requested_personas,omitempty" example:"analyst"`

	CreatedAt time.Time `json:"created_at" example:"2026-01-15T14:30:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-15T14:30:00Z"`
}

// ListFilter controls which prompts are returned by List.
type ListFilter struct {
	Scope      string   // "global", "persona", "personal", or "" for all
	Personas   []string // filter by persona membership (OR match)
	OwnerEmail string   // filter by owner
	Enabled    *bool    // filter by enabled state
	Search     string   // free-text search on name, display_name, description
}

// Store defines the interface for prompt persistence.
type Store interface {
	// Create persists a new prompt.
	Create(ctx context.Context, p *Prompt) error

	// Get retrieves a non-personal (global or persona) prompt by name, which is
	// globally unique. Returns nil, nil if not found. Personal prompts are
	// per-owner and must be fetched with GetPersonal.
	Get(ctx context.Context, name string) (*Prompt, error)

	// GetPersonal retrieves a personal prompt by its owner and name. Personal
	// names are unique only within an owner, so the owner is required to
	// disambiguate. Returns nil, nil if not found.
	GetPersonal(ctx context.Context, ownerEmail, name string) (*Prompt, error)

	// GetByID retrieves a prompt by ID. Returns nil, nil if not found.
	GetByID(ctx context.Context, id string) (*Prompt, error)

	// Update modifies an existing prompt.
	Update(ctx context.Context, p *Prompt) error

	// Delete removes a prompt by name.
	Delete(ctx context.Context, name string) error

	// DeleteByID removes a prompt by ID.
	DeleteByID(ctx context.Context, id string) error

	// List returns prompts matching the filter.
	List(ctx context.Context, filter ListFilter) ([]Prompt, error)

	// Count returns the number of prompts matching the filter.
	Count(ctx context.Context, filter ListFilter) (int, error)
}
