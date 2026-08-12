package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time check: the PostgreSQL store provides prompt versioning.
var _ prompt.VersionStore = (*Store)(nil)

// versionColumns is the column list read by every prompt_versions SELECT,
// mirrored by scanVersion so the scan order cannot drift from the query.
const versionColumns = `id, prompt_id, version, display_name, description, content,
	arguments, tags, author, status, approved_by, approved_at, created_at`

// versionSelect is the base SELECT for the version columns.
const versionSelect = "SELECT " + versionColumns + " FROM prompt_versions"

// scanVersion reads one row in versionColumns order into a Version.
func scanVersion(sc rowScanner) (*prompt.Version, error) {
	v := &prompt.Version{}
	var argsJSON []byte
	err := sc.Scan(&v.ID, &v.PromptID, &v.Version, &v.DisplayName, &v.Description,
		&v.Content, &argsJSON, pq.Array(&v.Tags), &v.Author, &v.Status,
		&v.ApprovedBy, &v.ApprovedAt, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning prompt version row: %w", err)
	}
	if err := json.Unmarshal(argsJSON, &v.Arguments); err != nil {
		return nil, fmt.Errorf("unmarshal version arguments: %w", err)
	}
	if v.Arguments == nil {
		v.Arguments = []prompt.Argument{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return v, nil
}

// versionInsert carries one prompt_versions INSERT: the snapshot source, its
// author, the row status, and an optional approval stamp bound to the row.
type versionInsert struct {
	PromptID   string
	Version    int
	Snapshot   *prompt.Prompt
	Author     string
	Status     string
	ApprovedBy string
	ApprovedAt *time.Time
}

// insertVersionRow snapshots the versioned fields of ins.Snapshot as one
// immutable prompt_versions row within the caller's transaction.
func insertVersionRow(ctx context.Context, tx *sql.Tx, ins versionInsert) error {
	argsJSON, err := json.Marshal(ins.Snapshot.Arguments)
	if err != nil {
		return fmt.Errorf("marshal version arguments: %w", err)
	}
	tags := ins.Snapshot.Tags
	if tags == nil {
		tags = []string{}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO prompt_versions (prompt_id, version, display_name, description,
		                             content, arguments, tags, author, status,
		                             approved_by, approved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		ins.PromptID, ins.Version, ins.Snapshot.DisplayName, ins.Snapshot.Description,
		ins.Snapshot.Content, argsJSON, pq.Array(tags), ins.Author, ins.Status,
		ins.ApprovedBy, ins.ApprovedAt,
	)
	if err != nil {
		return fmt.Errorf("insert prompt version: %w", err)
	}
	return nil
}

// lockPromptStatus locks the prompt row for the transaction and returns its
// current status, mapping a missing row to a not-found error.
func lockPromptStatus(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM prompts WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("prompt %s not found", id)
	}
	if err != nil {
		return "", fmt.Errorf("lock prompt: %w", err)
	}
	return status, nil
}

// lockPrompt locks and returns the full prompt row for the transaction, or an
// error when the prompt does not exist.
func lockPrompt(ctx context.Context, tx *sql.Tx, id string) (*prompt.Prompt, error) {
	p, err := scanPrompt(tx.QueryRowContext(ctx, promptSelect+` WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("prompt %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("lock prompt: %w", err)
	}
	return p, nil
}

// nextVersionNumber allocates the next version number for a prompt. Callers
// hold the prompt row lock, which serializes allocation. GREATEST guards a row
// whose live version somehow exceeds its history (no snapshot rows).
func nextVersionNumber(ctx context.Context, tx *sql.Tx, promptID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT GREATEST(COALESCE(MAX(pv.version), 0),
		                (SELECT version FROM prompts WHERE id = $1)) + 1
		  FROM prompt_versions pv WHERE pv.prompt_id = $1`, promptID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("next version number: %w", err)
	}
	return n, nil
}

// stampApprovalTransition binds a first approval to the snapshot it approved:
// when this write carried the draft-to-approved transition, the prompt's
// approval stamp is copied onto its current (already applied) version row.
// Later approvals of later versions never touch this row again, so the stamp
// recorded for a version is immutable.
func stampApprovalTransition(ctx context.Context, tx *sql.Tx, p *prompt.Prompt, oldStatus string) error {
	if oldStatus == prompt.StatusApproved || p.Status != prompt.StatusApproved {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE prompt_versions
		   SET approved_by = $2, approved_at = $3
		 WHERE prompt_id = $1
		   AND version = (SELECT version FROM prompts WHERE id = $1)
		   AND approved_at IS NULL`,
		p.ID, p.ApprovedBy, p.ApprovedAt)
	if err != nil {
		return fmt.Errorf("stamp version approval: %w", err)
	}
	return nil
}

// requireUngated re-validates the review gate against the row as locked in
// this transaction. Callers decide gating from an earlier unlocked read; if
// the prompt was approved between that read and the write (an edit racing an
// approval), applying the edit here would slip unreviewed content into an
// approved shared prompt and overwrite the fresh approval with stale state,
// so the write is rejected as a conflict for the caller to re-read and retry.
func requireUngated(locked, p *prompt.Prompt) error {
	if prompt.RequiresReview(locked, p) {
		return fmt.Errorf("prompt %s was approved while this edit was in flight; re-read and retry (content changes to an approved shared prompt require review): %w",
			p.ID, prompt.ErrVersionConflict)
	}
	return nil
}

// UpdateWithVersion persists p like Update and, when any versioned snapshot
// field changed against the stored row, records a new applied version authored
// by author and advances p.Version to it. System rows are written without
// versioning (config mirrors). The review gate is re-validated under the row
// lock (see requireUngated).
func (s *Store) UpdateWithVersion(ctx context.Context, p *prompt.Prompt, author string) error {
	normalizeSlices(p)
	indexedChanged := false
	if err := s.withTx(ctx, "update prompt with version", func(tx *sql.Tx) error {
		before, err := lockPrompt(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if err := requireUngated(before, p); err != nil {
			return err
		}
		indexedChanged = indexTextChanged(before, p)
		if prompt.SnapshotChanged(before, p) && p.Source != prompt.SourceSystem {
			n, err := nextVersionNumber(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			p.Version = n
			if err := insertVersionRow(ctx, tx, versionInsert{
				PromptID: p.ID, Version: n, Snapshot: p,
				Author: author, Status: prompt.VersionStatusApplied,
			}); err != nil {
				return err
			}
		}
		if err := updateTx(ctx, tx, p); err != nil {
			return err
		}
		return stampApprovalTransition(ctx, tx, p, before.Status)
	}); err != nil {
		return err
	}
	if indexedChanged {
		s.index.NotifyWrite(ctx, p.ID)
	}
	return nil
}

// CreateDraftVersion snapshots proposed's versioned fields as a new draft
// version of the prompt without touching the live row, returning the new
// version number. System rows are read-only config mirrors and refuse drafts.
func (s *Store) CreateDraftVersion(ctx context.Context, promptID string, proposed *prompt.Prompt, author string) (int, error) {
	var n int
	err := s.withTx(ctx, "create draft version", func(tx *sql.Tx) error {
		locked, err := lockPrompt(ctx, tx, promptID)
		if err != nil {
			return err
		}
		if locked.Source == prompt.SourceSystem {
			return fmt.Errorf("prompt %s is a read-only system prompt and cannot have draft versions: %w",
				promptID, prompt.ErrVersionConflict)
		}
		n, err = nextVersionNumber(ctx, tx, promptID)
		if err != nil {
			return err
		}
		return insertVersionRow(ctx, tx, versionInsert{
			PromptID: promptID, Version: n, Snapshot: proposed,
			Author: author, Status: prompt.VersionStatusDraft,
		})
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListVersions returns every version of the prompt, newest first.
func (s *Store) ListVersions(ctx context.Context, promptID string) ([]prompt.Version, error) {
	rows, err := s.db.QueryContext(ctx,
		versionSelect+` WHERE prompt_id = $1 ORDER BY version DESC`, promptID)
	if err != nil {
		return nil, fmt.Errorf("list prompt versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []prompt.Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt versions: %w", err)
	}
	return out, nil
}

// GetVersion returns one version with its full content, or nil, nil when the
// prompt has no such version.
func (s *Store) GetVersion(ctx context.Context, promptID string, version int) (*prompt.Version, error) {
	v, err := scanVersion(s.db.QueryRowContext(ctx,
		versionSelect+` WHERE prompt_id = $1 AND version = $2`, promptID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt version: %w", err)
	}
	return v, nil
}

// getVersionTx reads one version row within the caller's transaction, mapping
// a missing row to nil, nil.
func getVersionTx(ctx context.Context, tx *sql.Tx, promptID string, version int) (*prompt.Version, error) {
	v, err := scanVersion(tx.QueryRowContext(ctx,
		versionSelect+` WHERE prompt_id = $1 AND version = $2 FOR UPDATE`, promptID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // internal contract matches GetVersion: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt version: %w", err)
	}
	return v, nil
}

// lockApprovableDraft locks and returns the prompt and the pending draft
// version to approve, rejecting a retired prompt, a missing version, or a
// version that is not a pending draft.
func lockApprovableDraft(ctx context.Context, tx *sql.Tx, promptID string, version int) (*prompt.Prompt, *prompt.Version, error) {
	p, err := lockPrompt(ctx, tx, promptID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != prompt.StatusApproved && p.Status != prompt.StatusDraft {
		return nil, nil, fmt.Errorf("cannot approve a version of a %s prompt: %w", p.Status, prompt.ErrVersionConflict)
	}
	v, err := getVersionTx(ctx, tx, promptID, version)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return nil, nil, fmt.Errorf("prompt %s has no version %d: %w", promptID, version, prompt.ErrVersionConflict)
	}
	if v.Status != prompt.VersionStatusDraft {
		return nil, nil, fmt.Errorf("version %d is %s, not a pending draft: %w", version, v.Status, prompt.ErrVersionConflict)
	}
	return p, v, nil
}

// applyDraftSnapshot copies the draft's versioned fields onto the live prompt
// and stamps the approval.
func applyDraftSnapshot(p *prompt.Prompt, v *prompt.Version, approver string, now time.Time) {
	p.DisplayName, p.Description, p.Content = v.DisplayName, v.Description, v.Content
	p.Arguments = v.Arguments
	p.Tags = v.Tags
	p.Version = v.Version
	p.Status = prompt.StatusApproved
	p.ApprovedBy = approver
	p.ApprovedAt = &now
}

// ApproveVersion applies draft version's snapshot to the live prompt row,
// marks the prompt approved with approver's stamp, binds the same stamp to the
// version row, and supersedes any other pending drafts. Returns the updated
// prompt.
func (s *Store) ApproveVersion(ctx context.Context, promptID string, version int, approver string) (*prompt.Prompt, error) {
	var (
		out            *prompt.Prompt
		indexedChanged bool
	)
	err := s.withTx(ctx, "approve version", func(tx *sql.Tx) error {
		p, v, err := lockApprovableDraft(ctx, tx, promptID, version)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		before := *p
		applyDraftSnapshot(p, v, approver, now)
		indexedChanged = indexTextChanged(&before, p)
		if err := updateTx(ctx, tx, p); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE prompt_versions SET status = $3, approved_by = $4, approved_at = $5
			 WHERE prompt_id = $1 AND version = $2`,
			promptID, version, prompt.VersionStatusApplied, approver, now); err != nil {
			return fmt.Errorf("stamp approved version: %w", err)
		}
		// Any other draft was proposed against a snapshot that is no longer
		// current; keep it in history but take it out of the pending queue.
		if _, err := tx.ExecContext(ctx, `
			UPDATE prompt_versions SET status = $3
			 WHERE prompt_id = $1 AND status = $2`,
			promptID, prompt.VersionStatusDraft, prompt.VersionStatusSuperseded); err != nil {
			return fmt.Errorf("supersede stale drafts: %w", err)
		}
		out = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	if indexedChanged {
		s.index.NotifyWrite(ctx, promptID)
	}
	return out, nil
}

// RejectVersion marks a pending draft version rejected, leaving the live
// prompt row untouched.
func (s *Store) RejectVersion(ctx context.Context, promptID string, version int) error {
	return s.withTx(ctx, "reject version", func(tx *sql.Tx) error {
		if _, err := lockPromptStatus(ctx, tx, promptID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE prompt_versions SET status = $3
			 WHERE prompt_id = $1 AND version = $2 AND status = $4`,
			promptID, version, prompt.VersionStatusRejected, prompt.VersionStatusDraft)
		if err != nil {
			return fmt.Errorf("reject version: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("prompt %s has no pending draft version %d: %w", promptID, version, prompt.ErrVersionConflict)
		}
		return nil
	})
}
