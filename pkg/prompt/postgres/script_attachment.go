package postgres

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time check: the PostgreSQL store persists prompt-to-script links.
var _ prompt.ScriptAttachmentStore = (*Store)(nil)

// errListScriptAttachments wraps every failure path of the ordered read, which
// reports the same failure from two places (the query and the row iteration).
const errListScriptAttachments = "listing prompt script attachments: %w"

// scriptAttachmentColumns is the column list read by every script-attachment
// SELECT, mirrored by scanScriptAttachment so the scan order cannot drift from
// the query.
const scriptAttachmentColumns = `prompt_id, script_ref, position, attached_by`

// scanScriptAttachment reads one row in scriptAttachmentColumns order.
func scanScriptAttachment(sc rowScanner) (prompt.ScriptAttachment, error) {
	var a prompt.ScriptAttachment
	if err := sc.Scan(&a.PromptID, &a.ScriptRef, &a.Position, &a.AttachedBy); err != nil {
		return a, fmt.Errorf("scanning prompt script attachment row: %w", err)
	}
	return a, nil
}

// AttachScript appends a script to a prompt's referenced list. The position is
// computed inside the INSERT so two concurrent attaches cannot both read the
// same "next" value, and re-attaching keeps the original position: a repeated
// call must never silently reorder an author's procedure.
func (s *Store) AttachScript(ctx context.Context, a prompt.ScriptAttachment) error {
	const q = `
		INSERT INTO prompt_script_attachments (prompt_id, script_ref, position, attached_by)
		SELECT $1, $2,
		       COALESCE((SELECT MAX(position) + 1 FROM prompt_script_attachments WHERE prompt_id = $1), 0),
		       $3
		ON CONFLICT (prompt_id, script_ref) DO NOTHING`
	if _, err := s.db.ExecContext(ctx, q, a.PromptID, a.ScriptRef, a.AttachedBy); err != nil {
		return fmt.Errorf("attaching script to prompt: %w", err)
	}
	return nil
}

// DetachScript removes one link, returning prompt.ErrScriptAttachmentNotFound
// when the prompt does not reference that script.
func (s *Store) DetachScript(ctx context.Context, promptID, scriptRef string) error {
	const q = `DELETE FROM prompt_script_attachments WHERE prompt_id = $1 AND script_ref = $2`
	res, err := s.db.ExecContext(ctx, q, promptID, scriptRef)
	if err != nil {
		return fmt.Errorf("detaching script from prompt: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("detaching script from prompt: %w", err)
	}
	if n == 0 {
		return prompt.ErrScriptAttachmentNotFound
	}
	return nil
}

// ListScriptsByPrompt returns one prompt's script references in authored order.
func (s *Store) ListScriptsByPrompt(ctx context.Context, promptID string) ([]prompt.ScriptAttachment, error) {
	const q = "SELECT " + scriptAttachmentColumns + `
		FROM prompt_script_attachments WHERE prompt_id = $1 ORDER BY position, script_ref`
	rows, err := s.db.QueryContext(ctx, q, promptID)
	if err != nil {
		return nil, fmt.Errorf(errListScriptAttachments, err)
	}
	defer func() { _ = rows.Close() }()

	out := []prompt.ScriptAttachment{}
	for rows.Next() {
		a, scanErr := scanScriptAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errListScriptAttachments, err)
	}
	return out, nil
}
