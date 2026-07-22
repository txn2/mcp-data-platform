package prompt

import (
	"context"
	"errors"
	"time"
)

// ErrVersionConflict marks a version write rejected because the prompt or
// version state changed underneath the caller (a draft already resolved, a
// prompt retired, an edit racing an approval). REST handlers map it to 409;
// any other store error is an internal failure.
var ErrVersionConflict = errors.New("prompt version conflict")

// Version status constants. A version row is the immutable snapshot of a
// prompt's reviewable substance at one mutation:
//
//   - draft: a proposed edit to an approved shared prompt, awaiting admin
//     approval; the live prompt row continues to serve the previously applied
//     snapshot.
//   - applied: the snapshot was applied to the live row (either directly, for
//     edits that need no review, or via ApproveVersion). The version the live
//     row currently serves is Prompt.Version; older applied rows are history.
//   - superseded: a draft that was still pending when a different draft was
//     approved; kept for history, never applicable.
//   - rejected: a draft an admin explicitly rejected.
const (
	VersionStatusDraft      = "draft"
	VersionStatusApplied    = "applied"
	VersionStatusSuperseded = "superseded"
	VersionStatusRejected   = "rejected"
)

// Version is one immutable snapshot of a prompt's versioned fields (content,
// display name, description, arguments, tags), with the author who produced it
// and the approval stamp bound to this specific version. Approval stamps on a
// version never change once set: approving v5 does not alter the recorded
// approval of v4.
type Version struct {
	ID          string     `json:"id" example:"ver_a1b2c3d4"`
	PromptID    string     `json:"prompt_id" example:"prompt_a1b2c3d4"`
	Version     int        `json:"version" example:"4"`
	DisplayName string     `json:"display_name" example:"Daily Sales Report"`
	Description string     `json:"description" example:"Generate a daily sales summary by region"`
	Content     string     `json:"content" example:"Analyze sales data for {date} grouped by region."`
	Arguments   []Argument `json:"arguments"`
	Tags        []string   `json:"tags" example:"sales,reporting"`
	Author      string     `json:"author" example:"jane@example.com"`
	Status      string     `json:"status" example:"applied"`
	ApprovedBy  string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" example:"2026-06-12T14:30:00Z"`
}

// VersionStore is the optional versioning capability of a prompt store. The
// PostgreSQL store implements it; a store without it (some tests) degrades to
// unversioned updates via ApplyEdit's fallback. All write methods are
// transactional with the prompts row they touch.
type VersionStore interface {
	// UpdateWithVersion persists p like Store.Update and, when any versioned
	// snapshot field (content, display name, description, arguments, tags)
	// differs from the stored row, records a new applied version authored by
	// author and advances p.Version to it.
	UpdateWithVersion(ctx context.Context, p *Prompt, author string) error

	// CreateDraftVersion snapshots proposed's versioned fields as a new draft
	// version of the prompt without touching the live row, returning the new
	// version number. The live row continues to be served unchanged until the
	// draft is approved.
	CreateDraftVersion(ctx context.Context, promptID string, proposed *Prompt, author string) (int, error)

	// ListVersions returns every version of the prompt, newest first.
	ListVersions(ctx context.Context, promptID string) ([]Version, error)

	// GetVersion returns one version with its full content, or nil, nil when
	// the prompt has no such version.
	GetVersion(ctx context.Context, promptID string, version int) (*Version, error)

	// ApproveVersion applies draft version's snapshot to the live prompt row,
	// marks the prompt approved with approver's stamp, binds the same stamp to
	// the version row (status applied), and supersedes any other pending
	// drafts. Returns the updated prompt.
	ApproveVersion(ctx context.Context, promptID string, version int, approver string) (*Prompt, error)

	// RejectVersion marks a pending draft version rejected, leaving the live
	// prompt row untouched.
	RejectVersion(ctx context.Context, promptID string, version int) error
}

// Usage is the audit-derived usage rollup for one prompt: how many times it
// was served (prompts/get or manage_prompt use) within the audit retention
// window, and when it was last served. A prompt that was never served has no
// entry in a usage map.
type Usage struct {
	RunCount  int64      `json:"run_count" example:"37"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
}

// UsageReader reports serve-derived usage for a set of prompt IDs. The audit
// store implements it by aggregating prompt_serve audit events; prompts absent
// from the returned map have never been served (within retention).
type UsageReader interface {
	PromptUsage(ctx context.Context, promptIDs []string) (map[string]Usage, error)
}
