package script

import (
	"context"
	"time"
)

// PendingReview is one version waiting for a reviewer's decision, with the
// script it belongs to, flattened into the shape a queue lists.
//
// It deliberately carries no source. A queue is read to decide what to open
// next, and a script's source is the largest field in the record; the review
// surface fetches the version itself once a reviewer picks a row.
type PendingReview struct {
	ScriptID    string `json:"script_id" example:"script_a1b2c3d4"`
	ScriptName  string `json:"script_name" example:"daily-sales-report"`
	DisplayName string `json:"display_name" example:"Daily Sales Report"`
	Description string `json:"description" example:"Summarize yesterday's sales by region"`
	OwnerEmail  string `json:"owner_email" example:"jane@example.com"`
	Scope       string `json:"scope" example:"global"`
	// Version is the number a reviewer would approve, and VersionStatus is that
	// version's row status — "draft" for a proposed change, "applied" for the
	// snapshot a never-approved script is already serving.
	Version       int    `json:"version" example:"3"`
	VersionID     string `json:"version_id" example:"sver_a1b2c3d4"`
	VersionStatus string `json:"version_status" example:"draft"`
	// Author is who wrote the version and AuthorRoles is the authority
	// approving it would bind, which approval copies rather than accepts.
	Author      string   `json:"author" example:"jane@example.com"`
	AuthorRoles []string `json:"author_roles" example:"analyst"`
	// FirstApproval marks a script that has never had an approved version, so
	// nothing of it executes today. The distinction matters to a reviewer:
	// approving a change alters what already runs unattended, while approving a
	// first version starts something running.
	FirstApproval bool `json:"first_approval" example:"true"`
	// CreatedAt is when the version was authored, which is how long the
	// decision has been outstanding.
	CreatedAt time.Time `json:"created_at" example:"2026-08-14T09:00:00Z"`
}

// AgeDays returns how many whole days the review has been waiting as of now.
func (p PendingReview) AgeDays(now time.Time) int {
	d := now.Sub(p.CreatedAt)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

// ReviewStore is the read side of the review queue: what is waiting for a
// human. It is separate from VersionStore because it answers a question about
// every script at once rather than about one script's history, and separate
// from ApprovalStore because reading the queue decides nothing.
type ReviewStore interface {
	// ListPendingReviews returns every version awaiting approval across all
	// scripts, oldest first, which is the order a queue is worked.
	//
	// Two states qualify, and both mean "the platform is not executing this
	// version": a pending draft, and the live version of a script that has no
	// approved version at all. A script the execution gate would refuse anyway
	// — disabled, deprecated, or superseded — is excluded, because approving it
	// would change nothing and nothing could clear it from the queue.
	ListPendingReviews(ctx context.Context) ([]PendingReview, error)
}

// RejectionStore is the review decision that is not an approval.
//
// It sits beside ApprovalStore rather than in it because the two decisions have
// different reach: approving resolves the whole queue for a script (it applies a
// snapshot, supersedes competing drafts, and moves the execution gate), while
// rejecting takes one proposal out of consideration and changes nothing about
// what runs.
type RejectionStore interface {
	// RejectVersion marks a pending draft rejected, leaving the live script and
	// its approved version untouched.
	//
	// Only a draft can be rejected. The live version of a never-approved script
	// is also awaiting review, but rejecting it would mark the code the script
	// is serving as rejected while it kept being served; declining that version
	// means leaving it unapproved, which is already what it is. Returns
	// ErrVersionConflict when the version is not a pending draft.
	RejectVersion(ctx context.Context, scriptID string, version int) error
}
