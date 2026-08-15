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
// The governance shape deliberately copies pkg/prompt — live row plus immutable
// per-mutation snapshots, one ApplyEdit funnel, mixed-edit refusal, gate
// re-validation under the row lock — rather than genericizing it. The rules are
// domain-tuned and abstracting across two domains before both exist would fix
// the wrong shape; see the ApplyEdit and RequiresReview comments for where the
// two deliberately diverge.
package script

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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

// Scope constants define script visibility levels, matching prompt scopes.
const (
	ScopeGlobal   = "global"
	ScopePersona  = "persona"
	ScopePersonal = "personal"
)

// validScopes is the set of allowed scope values.
var validScopes = map[string]bool{
	ScopeGlobal:   true,
	ScopePersona:  true,
	ScopePersonal: true,
}

// Status constants define the script lifecycle.
//
//   - draft: authored, not yet approved for execution. Every script starts here
//     and stays here until a version is approved.
//   - active: the script has an approved version and the platform will execute
//     it. Set by the approval action.
//   - deprecated: still readable and still explains past runs, but should no
//     longer be scheduled or called.
//   - superseded: replaced by another script, named in SupersededBy.
//
// There is exactly one approval concept in this domain and it is the VERSION
// (see ApprovedVersionID). Status reports the consequence; it is not a second,
// independent gate.
const (
	StatusDraft      = "draft"
	StatusActive     = "active"
	StatusDeprecated = "deprecated"
	StatusSuperseded = "superseded"
)

// validStatuses is the set of allowed status values.
var validStatuses = map[string]bool{
	StatusDraft:      true,
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
	StatusDraft:      {StatusActive: true, StatusDeprecated: true, StatusSuperseded: true},
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

// ValidateScope checks that a scope value is allowed.
func ValidateScope(scope string) error {
	if !validScopes[scope] {
		return fmt.Errorf("invalid scope %q: must be global, persona, or personal", scope)
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
		return fmt.Errorf("invalid status %q: must be draft, active, deprecated, or superseded", status)
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

// Script is the live record of one managed script: its identity, its currently
// served source and parameter contract, and the pointer to the version the
// platform is allowed to execute.
type Script struct {
	ID          string   `json:"id" example:"script_a1b2c3d4"`
	Name        string   `json:"name" example:"daily-sales-report"`
	DisplayName string   `json:"display_name" example:"Daily Sales Report"`
	Description string   `json:"description" example:"Summarize yesterday's sales by region"`
	Source      string   `json:"source" example:"rows = platform.query(connection='primary', sql='SELECT 1')"`
	Params      []Param  `json:"params"`
	Scope       string   `json:"scope" example:"personal"`
	Personas    []string `json:"personas" example:"analyst"`
	OwnerEmail  string   `json:"owner_email" example:"jane@example.com"`
	Tags        []string `json:"tags" example:"sales,reporting"`
	Enabled     bool     `json:"enabled" example:"true"`

	// Lifecycle.
	Status       string     `json:"status" example:"draft"`
	SupersededBy string     `json:"superseded_by,omitempty" example:"daily-sales-report-v2"`
	DeprecatedAt *time.Time `json:"deprecated_at,omitempty"`

	// Version is the number of the snapshot the live row currently carries.
	// Pending draft versions above this number exist in the history and are not
	// served.
	Version int `json:"version" example:"3"`

	// ApprovedVersionID is THE execution gate: the id of the one version the
	// platform may execute. Empty means the script has no approved version and
	// nothing will run it — which is every script today, because the approval
	// action and the runner that reads this pointer arrive with the execution
	// gate. Draft execution (manage_script run_draft) deliberately never reads
	// it: a draft runs under its author's own identity and authority, so it
	// needs no approval, and it also cannot stand in for one.
	ApprovedVersionID string `json:"approved_version_id,omitempty"`

	CreatedAt time.Time `json:"created_at" example:"2026-08-13T14:30:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-08-13T14:30:00Z"`
}

// VisibleTo reports whether a caller identified by email and holding persona
// may see this script. It is the one definition of script visibility, shared by
// the read path and by the list predicate, so a script can never be listable
// but unreadable or the reverse. Admin authority is applied by the caller, not
// here: this answers only what the scope rules say.
func (s *Script) VisibleTo(email, persona string) bool {
	return scopeVisible(s.Scope, s.Personas, s.OwnerEmail, email, persona)
}

// VisibleToAny reports whether a caller who BELONGS TO any of personas may see
// this script. It is the discovery arity of the same rule: a listing scopes on
// the single persona a request resolved to act as, while search and fetch scope
// on the caller's whole membership set, which is an entitlement they hold
// rather than a property of one request. An empty set means "belongs to no
// persona", so persona-scoped scripts are invisible — the fail-closed answer.
func (s *Script) VisibleToAny(email string, personas []string) bool {
	return scopeVisibleToAny(s.Scope, s.Personas, s.OwnerEmail, email, personas)
}

// scopeVisible is the one definition of script visibility, over the three
// fields that carry it. Every surface answers through it — the record, the
// contract document, and the store predicate — so a script can never be
// listable but unreadable, or findable but unfetchable.
func scopeVisible(scope string, scriptPersonas []string, ownerEmail, email, persona string) bool {
	switch scope {
	case ScopeGlobal:
		return true
	case ScopePersona:
		return persona != "" && slices.Contains(scriptPersonas, persona)
	default:
		// Both sides must be identified. A script whose owner is empty — one
		// authored by a principal carrying no email, such as an API key — would
		// otherwise match a caller who is equally unidentified, and a personal
		// script would be readable by anyone holding its id. The store's list
		// and search predicates require the same, so a caller can never fetch
		// what a listing would have hidden.
		return ownerEmail != "" && ownerEmail == email
	}
}

// scopeVisibleToAny applies scopeVisible across a persona membership set.
func scopeVisibleToAny(scope string, scriptPersonas []string, ownerEmail, email string, personas []string) bool {
	if scope != ScopePersona {
		return scopeVisible(scope, scriptPersonas, ownerEmail, email, "")
	}
	for _, p := range personas {
		if scopeVisible(scope, scriptPersonas, ownerEmail, email, p) {
			return true
		}
	}
	return false
}

// Validate checks the whole record: name, scope, source, parameter contract,
// tags, and status. Mutation surfaces call it on the FINAL state rather than
// field by field as arguments arrive, so a record that was valid before an edit
// and invalid after it is refused — which is the case a per-argument check
// misses, since the offending combination may involve a field the caller never
// sent.
func (s *Script) Validate() error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if err := ValidateScope(s.Scope); err != nil {
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
	if s.Scope == ScopePersona && len(s.Personas) == 0 {
		return errors.New("a persona-scoped script must name at least one persona")
	}
	return ValidateStatus(s.Status)
}

// Executable reports whether the platform may execute this script on its own —
// on a schedule or through a run tool. It is false until a version is approved.
func (s *Script) Executable() bool { return s.ApprovedVersionID != "" }

// PrincipalPrefix marks a user id belonging to a managed script rather than a
// person, following the apikey:<name> service-principal convention.
const PrincipalPrefix = "script:"

// Principal is the identity an approved run of this script authenticates as.
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
//
// Moving to active is refused for a script with no approved version: active
// asserts that the platform will execute this script, and with no
// ApprovedVersionID there is nothing it is allowed to execute. That keeps
// status a report of the execution gate rather than a way around it.
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
		if !s.Executable() {
			return errors.New("cannot activate a script with no approved version")
		}
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
	Scope      string   // "global", "persona", "personal", or "" for all
	Personas   []string // filter by persona membership (OR match)
	OwnerEmail string   // filter by owner
	Enabled    *bool    // filter by enabled state
	Status     string   // filter by lifecycle status; "" for all
	Search     string   // free-text search on name, display_name, description
	Limit      int      // cap the number of rows returned; 0 means the store default

	// VisibleTo and VisiblePersona apply the scope rules of Script.VisibleTo as
	// a query predicate: global scripts, the persona-scoped scripts of
	// VisiblePersona, and the personal scripts of VisibleTo. They exist as a
	// pair because filtering by owner alone would hide the shared scripts a
	// caller is entitled to see, and filtering by nothing would list the
	// persona-scoped scripts of personas they do not hold. Empty VisibleTo
	// disables the predicate, which is the admin case.
	VisibleTo      string
	VisiblePersona string
}

// Store defines the interface for script persistence. It mirrors the prompt
// store's resolution contract: shared names are globally unique and resolve
// with Get, personal names are unique only within an owner and need GetPersonal.
type Store interface {
	// Create persists a new script and its first version, assigning ID when
	// empty. The author is recorded on that version along with the authority
	// they held, which is the ceiling on what approving it can grant.
	Create(ctx context.Context, s *Script, author Author) error

	// Get retrieves a shared (global or persona) script by its globally unique
	// name. Returns nil, nil if not found.
	Get(ctx context.Context, name string) (*Script, error)

	// GetPersonal retrieves a personal script by owner and name. Returns
	// nil, nil if not found.
	GetPersonal(ctx context.Context, ownerEmail, name string) (*Script, error)

	// GetByID retrieves a script by ID. Returns nil, nil if not found.
	GetByID(ctx context.Context, id string) (*Script, error)

	// Update modifies an existing script.
	Update(ctx context.Context, s *Script) error

	// Delete removes a script by ID.
	Delete(ctx context.Context, id string) error

	// List returns scripts matching the filter, newest first.
	List(ctx context.Context, filter ListFilter) ([]Script, error)
}
