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

// maxTags and maxTagLength bound a prompt's tag list, mirroring the limits
// applied to assets and managed resources so tag input is uniformly bounded.
const (
	maxTags      = 20
	maxTagLength = 100
)

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

// ValidateTags checks that a prompt's tag list is within bounds.
func ValidateTags(tags []string) error {
	if len(tags) > maxTags {
		return fmt.Errorf("too many tags: %d (max %d)", len(tags), maxTags)
	}
	for _, t := range tags {
		if len(t) > maxTagLength {
			return fmt.Errorf("tag exceeds %d characters", maxTagLength)
		}
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

// ApplyStatusTransition validates and applies a status change to the prompt,
// stamping the lifecycle metadata. A no-op when newStatus is empty or unchanged.
// Approval (-> approved) requires isAdmin; supersededBy is recorded when moving
// to superseded. now is passed in for testability. Returns an error on an
// invalid or unauthorized transition. Shared by the manage_prompt tool and the
// admin API so both enforce the lifecycle identically.
func (p *Prompt) ApplyStatusTransition(newStatus, supersededBy, actorEmail string, isAdmin bool, now time.Time) error {
	if newStatus == "" || newStatus == p.Status {
		return nil
	}
	if err := ValidateStatus(newStatus); err != nil {
		return err
	}
	if err := ValidateStatusTransition(p.Status, newStatus); err != nil {
		return err
	}
	if newStatus == StatusApproved && !isAdmin {
		return fmt.Errorf("only admins can approve a prompt")
	}
	switch newStatus {
	case StatusApproved:
		p.ApprovedBy = actorEmail
		p.ApprovedAt = &now
	case StatusDeprecated:
		p.DeprecatedAt = &now
	case StatusSuperseded:
		p.SupersededBy = supersededBy
	}
	p.Status = newStatus
	return nil
}

// ApplyPromotionRequest records an owner's request to promote a personal prompt
// into a shared scope. The prompt must be personal; the target must be persona
// (with at least one persona) or global. It only sets the request signal; the
// scope does not change until an admin approves (see ApprovePromotion).
func (p *Prompt) ApplyPromotionRequest(requestedScope string, requestedPersonas []string) error {
	if p.Scope != ScopePersonal {
		return fmt.Errorf("only personal prompts can request promotion")
	}
	if p.Status == StatusDeprecated || p.Status == StatusSuperseded {
		return fmt.Errorf("cannot request promotion of a %s prompt", p.Status)
	}
	if requestedScope != ScopePersona && requestedScope != ScopeGlobal {
		return fmt.Errorf("requested scope must be %q or %q", ScopePersona, ScopeGlobal)
	}
	if requestedScope == ScopePersona && len(requestedPersonas) == 0 {
		return fmt.Errorf("persona promotion requires at least one persona")
	}
	p.ReviewRequested = true
	p.RequestedScope = requestedScope
	if requestedScope == ScopePersona {
		p.RequestedPersonas = append([]string(nil), requestedPersonas...)
	} else {
		p.RequestedPersonas = []string{}
	}
	return nil
}

// ApprovePromotion applies a pending promotion request: it moves the prompt to
// the requested scope/personas, marks it approved (stamping the admin), and
// clears the request signal. Returns an error if there is no pending request or
// the prompt is no longer personal (its scope changed out from under the
// request). The caller is responsible for checking the target shared name is free.
func (p *Prompt) ApprovePromotion(actorEmail string, now time.Time) error {
	if !p.ReviewRequested {
		return fmt.Errorf("prompt has no pending promotion request")
	}
	if p.Scope != ScopePersonal {
		// The scope was changed (e.g. via a direct admin edit) after the request
		// was filed; the stale request must not silently re-scope it.
		p.clearPromotionRequest()
		return fmt.Errorf("prompt is no longer personal; promotion request is stale")
	}
	if p.RequestedScope != ScopePersona && p.RequestedScope != ScopeGlobal {
		return fmt.Errorf("invalid requested scope %q", p.RequestedScope)
	}
	p.Scope = p.RequestedScope
	if p.Scope == ScopePersona {
		p.Personas = append([]string(nil), p.RequestedPersonas...)
	} else {
		p.Personas = []string{}
	}
	// Promotion produces a freshly approved shared prompt; clear any stale
	// deprecation/supersede markers carried from the personal record.
	p.Status = StatusApproved
	p.ApprovedBy = actorEmail
	p.ApprovedAt = &now
	p.DeprecatedAt = nil
	p.SupersededBy = ""
	p.clearPromotionRequest()
	return nil
}

// RejectPromotion clears a pending promotion request, leaving the prompt
// personal and otherwise unchanged.
func (p *Prompt) RejectPromotion() {
	p.clearPromotionRequest()
}

// clearPromotionRequest resets the promotion-request signal fields.
func (p *Prompt) clearPromotionRequest() {
	p.ReviewRequested = false
	p.RequestedScope = ""
	p.RequestedPersonas = []string{}
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
	Status       string     `json:"status" example:"approved"`
	ApprovedBy   string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	DeprecatedAt *time.Time `json:"deprecated_at,omitempty"`
	SupersededBy string     `json:"superseded_by,omitempty" example:"daily-sales-report-v2"`

	// Promotion request: an owner asks to move a personal prompt into a shared
	// scope. ReviewRequested marks the prompt as pending in the admin queue;
	// RequestedScope/RequestedPersonas record the target. Cleared on approve or
	// reject (see ApplyPromotionRequest / ApprovePromotion).
	ReviewRequested   bool     `json:"review_requested" example:"false"`
	RequestedScope    string   `json:"requested_scope,omitempty" example:"persona"`
	RequestedPersonas []string `json:"requested_personas,omitempty" example:"analyst"`

	CreatedAt time.Time `json:"created_at" example:"2026-01-15T14:30:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-15T14:30:00Z"`
}

// ListFilter controls which prompts are returned by List.
type ListFilter struct {
	Scope           string   // "global", "persona", "personal", or "" for all
	Personas        []string // filter by persona membership (OR match)
	OwnerEmail      string   // filter by owner
	Enabled         *bool    // filter by enabled state
	Search          string   // free-text search on name, display_name, description
	ReviewRequested *bool    // filter by pending promotion request (admin queue)
	Source          string   // include only this origin (operator, agent, system); "" for all
	ExcludeSource   string   // exclude this origin (e.g. "system" to hide ingested static prompts)
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
