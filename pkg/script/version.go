package script

import (
	"context"
	"errors"
	"time"
)

// ErrVersionConflict marks a version write rejected because the script or
// version state changed underneath the caller. REST handlers map it to 409;
// any other store error is an internal failure.
var ErrVersionConflict = errors.New("script version conflict")

// Version status constants. A version row is the immutable snapshot of a
// script's substance at one mutation:
//
//   - applied: the snapshot was applied to the live row. Every save produces
//     one, and the newest applied version is the version a run executes.
//   - superseded: a historical row that was never applied — a proposed edit
//     from the era in which some edits waited on a review that no longer
//     exists. Nothing writes it today; it is kept so old history reads true.
//     Rows from that era may also carry "rejected", a proposal a reviewer
//     declined, left as the decision it records.
const (
	VersionStatusApplied    = "applied"
	VersionStatusSuperseded = "superseded"
)

// Author is who produced a version and the authority they held while doing it.
//
// The roles half is what makes an unattended run honest. The middleware
// resolves a caller's persona from their roles, and a platform-executed script
// runs with nobody present — no token, no session, no live identity to resolve
// — so the authority it presents has to have been captured at some earlier
// moment. Capturing it from the AUTHOR, at the moment they saved the version,
// means a run can only ever do what the person who wrote that code could
// already do.
type Author struct {
	// Email identifies the person recorded on the version.
	Email string
	// Roles is the authority they held when they wrote it.
	Roles []string
}

// Version is one immutable snapshot of a script's versioned fields (source,
// params, display name, description, category, tags), with the author who
// produced it and the authority they held.
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
	// written, and the roles a run of this version presents. See Author.
	AuthorRoles []string  `json:"author_roles,omitempty" example:"analyst"`
	Status      string    `json:"status" example:"applied"`
	CreatedAt   time.Time `json:"created_at" example:"2026-08-13T14:30:00Z"`
}

// VersionStore is the versioning capability of a script store. The PostgreSQL
// store implements it; a store without it (no-database deployments, plain test
// stores) degrades to unversioned updates through ApplyEdit's fallback. Every
// write method is transactional with the scripts row it touches.
type VersionStore interface {
	// UpdateWithVersion persists s like Store.Update and, when any versioned
	// snapshot field differs from the stored row, records a new applied version
	// authored by author and advances s.Version to it.
	UpdateWithVersion(ctx context.Context, s *Script, author Author) error

	// ListVersions returns every version of the script, newest first.
	ListVersions(ctx context.Context, scriptID string) ([]Version, error)

	// GetVersion returns one version with its full source, or nil, nil when the
	// script has no such version.
	GetVersion(ctx context.Context, scriptID string, version int) (*Version, error)

	// GetVersionByID returns one version by its id, or nil, nil when no such
	// version exists. It is how a run loads the code it executes: a run is
	// queued against an id, which names one immutable snapshot for the life of
	// the script, where a version number could be renumbered.
	GetVersionByID(ctx context.Context, id string) (*Version, error)
}
