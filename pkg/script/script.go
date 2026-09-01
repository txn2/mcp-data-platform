// Package script is the managed-script domain: the live script record, its
// immutable version history, the typed parameter contract, and the single edit
// funnel every mutation surface crosses.
//
// A managed script is agent-authored Starlark that the platform stores,
// versions, and governs so a solved process (a KPI report, a daily export) can
// be re-run without re-deriving it through a model. The package holds the model
// and the rules; it executes nothing and knows nothing about Starlark. The
// engine lives in internal/platform/scriptrun and the MCP surface in
// internal/platform/scriptlayer, so the domain stays free of both.
//
// The governance shape deliberately copies pkg/prompt — live row plus
// immutable per-mutation snapshots, one ApplyEdit funnel — rather than
// genericizing it. The rules are domain-tuned, and abstracting across two
// domains would fix the wrong shape.
package script

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Bounds on the free-text fields of a script record, matched to the equivalent
// limits on prompts and assets so tag and name input is uniformly bounded.
const (
	maxNameLength = 128
	maxTags       = 20
	maxTagLength  = 100

	// MaxSourceBytes bounds the Starlark source of one script. Scripts are glue
	// — a few hundred lines that call the platform and shape the result — and
	// heavy computation belongs in SQL, not in the interpreter. The cap keeps a
	// pathological body out of the parser, the version history, and the review
	// surface a human is expected to actually read.
	MaxSourceBytes = 256 * 1024
)

// validNamePattern matches lowercase letters, digits, hyphens, and underscores.
var validNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Status constants define the script lifecycle.
//
//   - active: in service. A saved script runs — run_script executes its latest
//     saved version and a schedule fires it. Every script starts here.
//   - deprecated: still readable and still explains past runs, but no longer
//     executed.
//   - superseded: replaced by another script, named in SupersededBy.
const (
	StatusActive     = "active"
	StatusDeprecated = "deprecated"
	StatusSuperseded = "superseded"
)

// validStatuses is the set of allowed status values.
var validStatuses = map[string]bool{
	StatusActive:     true,
	StatusDeprecated: true,
	StatusSuperseded: true,
}

// validStatusTransitions defines the allowed status transitions, following the
// validated-transition-graph pattern the prompt and knowledge-insight
// lifecycles use. A deprecated script may return to active because deprecation
// is an operational judgement an operator can reverse; nothing leaves
// superseded, which names a replacement.
var validStatusTransitions = map[string]map[string]bool{
	StatusActive:     {StatusDeprecated: true, StatusSuperseded: true},
	StatusDeprecated: {StatusActive: true, StatusSuperseded: true},
}

// ValidateName checks that a script name is well-formed.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("name must be at most %d characters", maxNameLength)
	}
	if !validNamePattern.MatchString(name) {
		return errors.New("name must contain only lowercase letters, digits, hyphens, and underscores")
	}
	return nil
}

// ValidateTags checks that a script's tag list is within bounds.
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

// ValidateSource checks that Starlark source is present and within bounds. It
// is a size and presence check only; parsing is the engine's job.
func ValidateSource(src string) error {
	if src == "" {
		return errors.New("source is required")
	}
	if len(src) > MaxSourceBytes {
		return fmt.Errorf("source is %d bytes, over the %d-byte limit", len(src), MaxSourceBytes)
	}
	return nil
}

// ValidateStatus checks that a status value is recognized.
func ValidateStatus(status string) error {
	if !validStatuses[status] {
		return fmt.Errorf("invalid status %q: must be active, deprecated, or superseded", status)
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

// Script is the live record of one managed script: its identity, and the
// currently served source and parameter contract, which are what a run
// executes.
type Script struct {
	ID          string  `json:"id" example:"script_a1b2c3d4"`
	Name        string  `json:"name" example:"daily-sales-report"`
	DisplayName string  `json:"display_name" example:"Daily Sales Report"`
	Description string  `json:"description" example:"Summarize yesterday's sales by region"`
	Source      string  `json:"source" example:"rows = platform.query(connection='primary', sql='SELECT 1')"`
	Params      []Param `json:"params"`
	// OwnerEmail is the one person a script belongs to: the only caller who
	// sees, edits, runs, and schedules it, administrators aside. An
	// administrator can move it to another owner (Transfer).
	OwnerEmail string `json:"owner_email" example:"jane@example.com"`
	// Category files the script under one lowercase slug, the axis a listing
	// filters on and a reader scans. It is the same axis a resource and an
	// insight carry, written the same way (#1369).
	Category string   `json:"category,omitempty" example:"reporting"`
	Tags     []string `json:"tags" example:"sales,reporting"`
	Enabled  bool     `json:"enabled" example:"true"`

	// Lifecycle.
	Status       string     `json:"status" example:"active"`
	SupersededBy string     `json:"superseded_by,omitempty" example:"daily-sales-report-v2"`
	DeprecatedAt *time.Time `json:"deprecated_at,omitempty"`

	// Version is the number of the snapshot the live row currently carries,
	// which is the version a run executes: saving a version makes it the
	// version that runs.
	Version int `json:"version" example:"3"`

	CreatedAt time.Time `json:"created_at" example:"2026-08-13T14:30:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-08-13T14:30:00Z"`
}

// OwnedBy reports whether the named caller owns this script, which is the
// whole of script visibility: a script is one person's, and only that person
// (and an administrator, an authority the caller applies, not this method)
// sees it, edits it, runs it, or schedules it.
//
// Both sides must be identified. A script whose owner is empty — one authored
// by a principal carrying no email, such as an API key, or one whose owner
// predates this rule — would otherwise belong to every caller the platform
// cannot name either. Such a script is nobody's until an administrator
// transfers it. The store's list and search predicates require the same, so a
// caller can never fetch what a listing would have hidden.
func (s *Script) OwnedBy(email string) bool {
	return s != nil && s.OwnerEmail != "" && s.OwnerEmail == email
}

// Validate checks the whole record: name, source, parameter contract,
// tags, the fields that document the script (display name, description,
// category), and status. Mutation surfaces call it on the FINAL state rather than
// field by field as arguments arrive, so a record that was valid before an edit
// and invalid after it is refused — which is the case a per-argument check
// misses, since the offending combination may involve a field the caller never
// sent.
func (s *Script) Validate() error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if err := ValidateSource(s.Source); err != nil {
		return err
	}
	if err := ValidateParams(s.Params); err != nil {
		return err
	}
	if err := ValidateTags(s.Tags); err != nil {
		return err
	}
	if err := validateDisplayName(s.DisplayName); err != nil {
		return err
	}
	if err := validateDescription(s.Description); err != nil {
		return err
	}
	if err := validateCategory(s.Category); err != nil {
		return err
	}
	return ValidateStatus(s.Status)
}

// PrincipalPrefix marks a user id belonging to a managed script rather than a
// person, following the apikey:<name> service-principal convention.
const PrincipalPrefix = "script:"

// Principal is the identity a platform-executed run of this script
// authenticates as.
//
// A script gets its own principal rather than borrowing its owner's so every
// gate, rate limiter, and audit row can tell the two apart: a row reading
// script:daily-sales is a governed automation, and one reading the owner's
// address is that person at a keyboard. The owner stays accountable through the
// script's owner_email, which the run also carries.
func (s *Script) Principal() string { return PrincipalPrefix + s.Name }

// ApplyStatusTransition validates and applies a status change, stamping the
// lifecycle metadata. A no-op when newStatus is empty or unchanged. now is
// passed in for testability. Returns an error on an invalid transition.
func (s *Script) ApplyStatusTransition(newStatus, supersededBy string, now time.Time) error {
	if newStatus == "" || newStatus == s.Status {
		return nil
	}
	if err := ValidateStatus(newStatus); err != nil {
		return err
	}
	if err := ValidateStatusTransition(s.Status, newStatus); err != nil {
		return err
	}
	switch newStatus {
	case StatusActive:
		s.DeprecatedAt = nil
	case StatusDeprecated:
		s.DeprecatedAt = &now
	case StatusSuperseded:
		s.SupersededBy = supersededBy
	}
	s.Status = newStatus
	return nil
}

// ListFilter controls which scripts are returned by List.
type ListFilter struct {
	// OwnerEmail narrows the listing to one person's scripts, which is the
	// whole of visibility: a caller lists their own, and an administrator
	// leaves it empty to list every script on the platform.
	OwnerEmail string
	Enabled    *bool  // filter by enabled state
	Status     string // filter by lifecycle status; "" for all
	// Category narrows to one category slug; "" for all. Tags narrows to the
	// scripts carrying ANY of the named tags: a reader filtering by two tags is
	// asking for the union of two shelves, not for the scripts on both.
	Category string
	Tags     []string
	Search   string // free-text search on name, display_name, description
	Limit    int    // cap the number of rows returned; 0 means the store default
}

// ErrNotFound is what a store write reports when no script bears the ID it was
// given. A delete is the surface that acts on it: the script's page and the
// tool both read the script before removing it, so a delete that finds nothing
// means somebody removed it in between, which is a not-found for the caller
// rather than a failure of the platform.
var ErrNotFound = errors.New("script not found")

// Store defines the interface for script persistence. A script name is unique
// within its owner and nowhere else, so every lookup by name names an owner
// too: two analysts may each keep their own "daily-sales".
type Store interface {
	// Create persists a new script and its first version, assigning ID when
	// empty. The author is recorded on that version along with the authority
	// they held, which is what a run of that version presents.
	Create(ctx context.Context, s *Script, author Author) error

	// GetByName retrieves one owner's script by name. Returns nil, nil if not
	// found.
	GetByName(ctx context.Context, ownerEmail, name string) (*Script, error)

	// GetByID retrieves a script by ID. Returns nil, nil if not found.
	GetByID(ctx context.Context, id string) (*Script, error)

	// Update modifies an existing script.
	Update(ctx context.Context, s *Script) error

	// Delete removes a script by ID, and everything that cascades from it: the
	// version history, the schedule, the run history and the carried state. It
	// returns an error wrapping ErrNotFound when no script bears the ID, which
	// is what lets a caller tell "somebody removed it first" from a failure of
	// the platform's own.
	Delete(ctx context.Context, id string) error

	// List returns scripts matching the filter, newest first.
	List(ctx context.Context, filter ListFilter) ([]Script, error)

	// Transfer moves a script to a new owner and records the move as a version
	// authored by the administrator making it, whose roles the new version
	// carries: a run presents the authority captured on the version it
	// executes, so a transfer that left the old authority in place would keep
	// running the script as the person who no longer owns it.
	Transfer(ctx context.Context, id, newOwnerEmail string, author Author) error
}
