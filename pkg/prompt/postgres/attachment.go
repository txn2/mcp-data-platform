package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time check: the PostgreSQL store persists prompt attachments.
var _ prompt.AttachmentStore = (*Store)(nil)

// Error format strings shared by the attachment queries, named because the
// same wrap appears on every failure path of a multi-statement operation.
const (
	errListAttachments    = "listing prompt attachments: %w"
	errReorderAttachments = "reordering prompt attachments: %w"
)

// attachmentColumns is the column list read by every attachment SELECT,
// mirrored by scanAttachment so the scan order cannot drift from the query.
const attachmentColumns = `prompt_id, resource_id, position, attached_by`

// scanAttachment reads one row in attachmentColumns order.
func scanAttachment(sc rowScanner) (prompt.Attachment, error) {
	var a prompt.Attachment
	if err := sc.Scan(&a.PromptID, &a.ResourceID, &a.Position, &a.AttachedBy); err != nil {
		return a, fmt.Errorf("scanning prompt attachment row: %w", err)
	}
	return a, nil
}

// Attach appends a resource to a prompt's attachment list. The position is
// computed inside the INSERT so two concurrent attaches cannot both read the
// same "next" value. Re-attaching an existing resource is a no-op that keeps
// the original position: a double-submit must never silently reorder an
// author's materials.
func (s *Store) Attach(ctx context.Context, a prompt.Attachment) error {
	const q = `
		INSERT INTO prompt_resource_attachments (prompt_id, resource_id, position, attached_by)
		SELECT $1, $2,
		       COALESCE((SELECT MAX(position) + 1 FROM prompt_resource_attachments WHERE prompt_id = $1), 0),
		       $3
		ON CONFLICT (prompt_id, resource_id) DO NOTHING`
	if _, err := s.db.ExecContext(ctx, q, a.PromptID, a.ResourceID, a.AttachedBy); err != nil {
		return fmt.Errorf("attaching resource to prompt: %w", err)
	}
	return nil
}

// Detach removes one link, returning prompt.ErrAttachmentNotFound when the
// prompt does not attach that resource.
func (s *Store) Detach(ctx context.Context, promptID, resourceID string) error {
	const q = `DELETE FROM prompt_resource_attachments WHERE prompt_id = $1 AND resource_id = $2`
	res, err := s.db.ExecContext(ctx, q, promptID, resourceID)
	if err != nil {
		return fmt.Errorf("detaching resource from prompt: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("detaching resource from prompt: %w", err)
	}
	if n == 0 {
		return prompt.ErrAttachmentNotFound
	}
	return nil
}

// ListByPrompt returns one prompt's attachments in authored order.
func (s *Store) ListByPrompt(ctx context.Context, promptID string) ([]prompt.Attachment, error) {
	const q = "SELECT " + attachmentColumns + `
		FROM prompt_resource_attachments WHERE prompt_id = $1 ORDER BY position, resource_id`
	rows, err := s.db.QueryContext(ctx, q, promptID)
	if err != nil {
		return nil, fmt.Errorf(errListAttachments, err)
	}
	defer func() { _ = rows.Close() }()

	out := []prompt.Attachment{}
	for rows.Next() {
		a, scanErr := scanAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errListAttachments, err)
	}
	return out, nil
}

// ListByResource returns the ids of prompts that attach a resource.
func (s *Store) ListByResource(ctx context.Context, resourceID string) ([]string, error) {
	const q = `SELECT prompt_id FROM prompt_resource_attachments WHERE resource_id = $1 ORDER BY prompt_id`
	rows, err := s.db.QueryContext(ctx, q, resourceID)
	if err != nil {
		return nil, fmt.Errorf("listing prompts attaching resource: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning attaching prompt id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing prompts attaching resource: %w", err)
	}
	return ids, nil
}

// Reorder rewrites one prompt's attachment order to exactly resourceIDs. It
// runs in a transaction that deletes the prompt's rows and reinserts them, so a
// caller never observes a half-ordered list, and it rejects an id that is not
// already attached rather than silently creating a link that skipped the scope
// check.
func (s *Store) Reorder(ctx context.Context, promptID string, resourceIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(errReorderAttachments, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := attachedByResource(ctx, tx, promptID)
	if err != nil {
		return err
	}
	for _, id := range resourceIDs {
		if _, ok := existing[id]; !ok {
			return fmt.Errorf("resource %s is not attached to this prompt: %w", strconv.Quote(id), prompt.ErrAttachmentNotFound)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM prompt_resource_attachments WHERE prompt_id = $1`, promptID); err != nil {
		return fmt.Errorf(errReorderAttachments, err)
	}
	for i, id := range resourceIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_resource_attachments (prompt_id, resource_id, position, attached_by)
			VALUES ($1, $2, $3, $4)`, promptID, id, i, existing[id]); err != nil {
			return fmt.Errorf(errReorderAttachments, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf(errReorderAttachments, err)
	}
	return nil
}

// attachedByResource reads a prompt's current attachments inside a transaction,
// keyed by resource id with the original attributor as the value so a reorder
// preserves who attached each item.
func attachedByResource(ctx context.Context, tx *sql.Tx, promptID string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT resource_id, attached_by FROM prompt_resource_attachments WHERE prompt_id = $1`, promptID)
	if err != nil {
		return nil, fmt.Errorf("reading current prompt attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var id, by string
		if err := rows.Scan(&id, &by); err != nil {
			return nil, fmt.Errorf("scanning current prompt attachment: %w", err)
		}
		out[id] = by
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading current prompt attachments: %w", err)
	}
	return out, nil
}
