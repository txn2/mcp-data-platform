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

// Version is one immutable snapshot of a script's versioned fields (source,
// params, display name, description, tags), with the author who produced it and
// the approval stamp bound to this specific version. Stamps never change once
// set: approving v5 does not alter what was recorded for v4.
//
// The snapshot is what makes a run explainable months later — a run record
// names the version it executed, and the code of that version is still here.
type Version struct {
	ID          string     `json:"id" example:"sver_a1b2c3d4"`
	ScriptID    string     `json:"script_id" example:"script_a1b2c3d4"`
	Version     int        `json:"version" example:"3"`
	DisplayName string     `json:"display_name" example:"Daily Sales Report"`
	Description string     `json:"description" example:"Summarize yesterday's sales by region"`
	Source      string     `json:"source"`
	Params      []Param    `json:"params"`
	Tags        []string   `json:"tags" example:"sales,reporting"`
	Author      string     `json:"author" example:"jane@example.com"`
	Status      string     `json:"status" example:"applied"`
	ApprovedBy  string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" example:"2026-08-13T14:30:00Z"`
}

// VersionStore is the versioning capability of a script store. The PostgreSQL
// store implements it; a store without it (no-database deployments, plain test
// stores) degrades to unversioned updates through ApplyEdit's fallback. Every
// write method is transactional with the scripts row it touches.
type VersionStore interface {
	// UpdateWithVersion persists s like Store.Update and, when any versioned
	// snapshot field differs from the stored row, records a new applied version
	// authored by author and advances s.Version to it.
	UpdateWithVersion(ctx context.Context, s *Script, author string) error

	// CreateDraftVersion snapshots proposed's versioned fields as a new draft
	// version without touching the live row, returning the new version number.
	// The approved version keeps executing until the draft is approved.
	CreateDraftVersion(ctx context.Context, scriptID string, proposed *Script, author string) (int, error)

	// ListVersions returns every version of the script, newest first.
	ListVersions(ctx context.Context, scriptID string) ([]Version, error)

	// GetVersion returns one version with its full source, or nil, nil when the
	// script has no such version.
	GetVersion(ctx context.Context, scriptID string, version int) (*Version, error)
}
