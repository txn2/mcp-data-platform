package script

import (
	"context"
	"errors"
	"time"
)

// ErrVersionConflict marks a version write rejected because the script or
// version state changed underneath the caller (a draft already resolved, a
// script retired, an edit racing an approval). REST handlers map it to 409; any
// other store error is an internal failure.
var ErrVersionConflict = errors.New("script version conflict")

// Version status constants. A version row is the immutable snapshot of a
// script's reviewable substance at one mutation:
//
//   - draft: a proposed edit to a script whose approved version is being
//     executed, awaiting approval; the live row keeps carrying the applied
//     snapshot and the approved version keeps executing.
//   - applied: the snapshot was applied to the live row.
//   - superseded: a draft still pending when a different draft was approved.
//   - rejected: a draft a reviewer explicitly rejected.
const (
	VersionStatusDraft      = "draft"
	VersionStatusApplied    = "applied"
	VersionStatusSuperseded = "superseded"
	VersionStatusRejected   = "rejected"
)

// Author is who produced a version and the authority they held while doing it.
//
// The roles half is what makes the execution gate honest. The middleware
// resolves a caller's persona from their roles, and an approved script runs
// with nobody present — no token, no session, no live identity to resolve — so
// the authority it presents has to have been captured at some earlier moment.
// Capturing it from the AUTHOR, at the moment they wrote the version, means an
// approved run can only ever do what the person who wrote that code could
// already do. An approver cannot widen it, because approval copies these roles
// rather than accepting them from the request.
type Author struct {
	// Email identifies the person recorded on the version.
	Email string
	// Roles is the authority they held when they wrote it.
	Roles []string
}

// Version is one immutable snapshot of a script's versioned fields (source,
// params, display name, description, category, tags), with the author who
// produced it and the approval stamp bound to this specific version. Stamps
// never change once set: approving v5 does not alter what was recorded for v4.
//
// The snapshot is what makes a run explainable months later — a run record
// names the version it executed, and the code of that version is still here.
type Version struct {
	ID          string   `json:"id" example:"sver_a1b2c3d4"`
	ScriptID    string   `json:"script_id" example:"script_a1b2c3d4"`
	Version     int      `json:"version" example:"3"`
	DisplayName string   `json:"display_name" example:"Daily Sales Report"`
	Description string   `json:"description" example:"Summarize yesterday's sales by region"`
	Category    string   `json:"category,omitempty" example:"reporting"`
	Source      string   `json:"source"`
	Params      []Param  `json:"params"`
	Tags        []string `json:"tags" example:"sales,reporting"`
	Author      string   `json:"author" example:"jane@example.com"`
	// AuthorRoles is the authority the author held when this snapshot was
	// written — the ceiling on what approving it can grant. See Author.
	AuthorRoles []string   `json:"author_roles,omitempty" example:"analyst"`
	Status      string     `json:"status" example:"applied"`
	ApprovedBy  string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	// AutoApproved marks an approval the PLATFORM made rather than a person
	// (#1367): a personal script's own owner wrote this version, so the grant
	// was minted from what its code reaches instead of being asked of a
	// reviewer. ApprovedBy still names the owner, because they are accountable
	// for it; this is what separates their authorship from somebody's decision,
	// so an operator reading the history can tell which scripts nobody reviewed.
	AutoApproved bool `json:"auto_approved,omitempty" example:"false"`
	// Grants is the capability set bound to this version at approval: what the
	// approver approved this code to be able to do. It is empty on every
	// version that was never approved, and the approval action is the only
	// writer — changing what a script may reach means approving it again, which
	// re-stamps the approval alongside the new grant.
	Grants    Grants    `json:"grants"`
	CreatedAt time.Time `json:"created_at" example:"2026-08-13T14:30:00Z"`
}

// Approved reports whether this version carries an approval stamp.
func (v *Version) Approved() bool { return v.ApprovedAt != nil }

// VersionStore is the versioning capability of a script store. The PostgreSQL
// store implements it; a store without it (no-database deployments, plain test
// stores) degrades to unversioned updates through ApplyEdit's fallback. Every
// write method is transactional with the scripts row it touches.
type VersionStore interface {
	// UpdateWithVersion persists s like Store.Update and, when any versioned
	// snapshot field differs from the stored row, records a new applied version
	// authored by author and advances s.Version to it.
	//
	// ungated carries the edit funnel's verdict that this edit needs no review,
	// which the store cannot re-derive: an edit to a script with an approved
	// version reaches the live row ONLY when automatic approval decided it
	// covers it (#1367), and that decision needs a static read of the source
	// this layer has no business doing. Everything else is re-validated against
	// the row as locked and refused as a conflict, which is what stops an edit
	// racing an approval from swapping code out from under it.
	UpdateWithVersion(ctx context.Context, s *Script, author Author, ungated bool) error

	// CreateDraftVersion snapshots proposed's versioned fields as a new draft
	// version without touching the live row, returning the new version number.
	// The approved version keeps executing until the draft is approved.
	CreateDraftVersion(ctx context.Context, scriptID string, proposed *Script, author Author) (int, error)

	// ListVersions returns every version of the script, newest first.
	ListVersions(ctx context.Context, scriptID string) ([]Version, error)

	// GetVersion returns one version with its full source, or nil, nil when the
	// script has no such version.
	GetVersion(ctx context.Context, scriptID string, version int) (*Version, error)

	// GetVersionByID returns one version by its id, or nil, nil when no such
	// version exists. It is how a run loads the code it is allowed to execute:
	// the execution gate is an id, so the runner resolves that id and never a
	// version number, which could be renumbered or point at a later draft.
	GetVersionByID(ctx context.Context, id string) (*Version, error)
}

// ApprovalStore is the execution gate's write side: the one operation that
// makes a script executable by the platform.
//
// It is separate from VersionStore because it is a different kind of act. Every
// VersionStore method records what an author did; this one records what a
// reviewer decided, and it is the only path that may write
// scripts.approved_version_id.
type ApprovalStore interface {
	// ApproveVersion stamps the named version as approved by approver, binds
	// grants to it, and points the script's execution gate at it.
	//
	// The grant's Roles are ignored: the implementation copies them from the
	// version's own author, so an approval can never widen authority beyond
	// what the author held. It returns the approved version as stored.
	//
	// Approving a draft applies its snapshot to the live row, so the served
	// script and the executed version are the same code. Any other pending
	// draft is superseded. It returns ErrVersionConflict when the version was
	// already resolved or the script moved underneath the reviewer.
	ApproveVersion(ctx context.Context, scriptID string, version int, approver string, grants Grants) (*Version, error)
}

// AutoApprovalStore is the execution gate's other write: the approval the
// platform makes for a personal script on behalf of its owner (#1367).
//
// It is separate from ApprovalStore for the same reason that one is separate
// from VersionStore — it records a different act. Everything else about it is
// identical, deliberately: it binds a grant, applies the snapshot, and moves the
// execution pointer through the same transaction body, so an automatically
// approved version is not a second kind of approved version.
type AutoApprovalStore interface {
	// AutoApproveVersion stamps the named version approved on the owner's own
	// authorship, binds grants to it, marks it as an approval nobody reviewed,
	// and points the script's execution gate at it.
	//
	// The grant's Roles are ignored here exactly as they are in ApproveVersion:
	// the implementation copies them from the version's author, who for an
	// automatic approval is the owner. It returns ErrVersionConflict when the
	// version was already resolved or the script moved underneath the write.
	AutoApproveVersion(ctx context.Context, scriptID string, version int, owner string, grants Grants) (*Version, error)
}
