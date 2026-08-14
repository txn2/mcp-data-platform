package scriptstore

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification for the review surface.
var (
	_ script.ReviewStore    = (*Store)(nil)
	_ script.RejectionStore = (*Store)(nil)
)

// pendingReviewQuery selects the one version each script is waiting on.
//
// Two states qualify, and both mean the platform is executing nothing of this
// version: a pending draft, and the live version of a script whose execution
// gate is empty.
//
// A script that cannot execute at all is excluded, and that is the same list
// the execution gate refuses on (pkg/script/run.go, RefuseRun): disabled,
// deprecated, or superseded. Approving one of those changes nothing — the gate
// refuses the run whatever the reviewer decides — so a queue that held them
// would hold a decision nobody can make, and there would be no way to work it
// off: rejecting is confined to drafts, so a disabled script's unapproved live
// version would sit there forever, counted by every alert. Re-enabling the
// script puts it back in the queue, which is the state where approving it means
// something again.
//
// DISTINCT ON keeps the queue at one row per script, which is the domain
// answer rather than a page size: approving any version supersedes every other
// pending draft of the same script (see applySnapshot), so the older drafts are
// not separate decisions a reviewer could make — the reviewable proposal is the
// highest-numbered one. That is also what bounds this query without a LIMIT
// that would have to truncate silently.
const pendingReviewQuery = `
SELECT script_id, script_name, display_name, description, owner_email, scope,
       version, version_id, version_status, author, author_roles,
       first_approval, created_at
  FROM (
    SELECT DISTINCT ON (s.id)
           s.id AS script_id, s.name AS script_name, s.display_name, s.description,
           s.owner_email, s.scope,
           v.version, v.id AS version_id, v.status AS version_status,
           v.author, v.author_roles,
           (s.approved_version_id IS NULL) AS first_approval, v.created_at
      FROM script_versions v
      JOIN scripts s ON s.id = v.script_id
     WHERE s.enabled
       AND s.status <> $1
       AND s.status <> $2
       AND (v.status = $3
            OR (s.approved_version_id IS NULL AND v.version = s.version))
     ORDER BY s.id, v.version DESC
  ) pending
 ORDER BY created_at ASC, script_name ASC`

// ListPendingReviews returns every version awaiting approval, oldest first.
func (s *Store) ListPendingReviews(ctx context.Context) ([]script.PendingReview, error) {
	rows, err := s.db.QueryContext(ctx, pendingReviewQuery,
		script.StatusSuperseded, script.StatusDeprecated, script.VersionStatusDraft)
	if err != nil {
		return nil, fmt.Errorf("list pending script reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.PendingReview{}
	for rows.Next() {
		p := script.PendingReview{}
		if err := rows.Scan(&p.ScriptID, &p.ScriptName, &p.DisplayName, &p.Description,
			&p.OwnerEmail, &p.Scope, &p.Version, &p.VersionID,
			&p.VersionStatus, &p.Author, pq.Array(&p.AuthorRoles),
			&p.FirstApproval, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning pending script review row: %w", err)
		}
		if p.AuthorRoles == nil {
			p.AuthorRoles = []string{}
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending script reviews: %w", err)
	}
	return out, nil
}

// RejectVersion marks a pending draft rejected.
//
// The status predicate is in the UPDATE rather than in a read before it: a
// draft that was approved, superseded, or already rejected between the read and
// the write must not be re-labeled, and an affected-row count is the only
// answer that cannot race.
func (s *Store) RejectVersion(ctx context.Context, scriptID string, version int) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE script_versions SET status = $3
		 WHERE script_id = $1 AND version = $2 AND status = $4`,
		scriptID, version, script.VersionStatusRejected, script.VersionStatusDraft)
	if err != nil {
		return fmt.Errorf("reject script version: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading script version rejection result: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("version %d is not a pending draft, so there is nothing to reject: %w",
			version, script.ErrVersionConflict)
	}
	return nil
}
