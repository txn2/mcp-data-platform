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

// Argument describes a prompt argument.
type Argument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Prompt represents a user-managed MCP prompt.
type Prompt struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Description string     `json:"description"`
	Content     string     `json:"content"`
	Arguments   []Argument `json:"arguments"`
	Category    string     `json:"category"`
	Scope       string     `json:"scope"`
	Personas    []string   `json:"personas"`
	OwnerEmail  string     `json:"owner_email"`
	Source      string     `json:"source"`
	Enabled     bool       `json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
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

	// Get retrieves a prompt by name. Returns nil, nil if not found.
	Get(ctx context.Context, name string) (*Prompt, error)

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
