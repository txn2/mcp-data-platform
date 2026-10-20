package notices

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/calltask"
)

// maxExportNotices bounds how many ended background exports one briefing
// names.
const maxExportNotices = 10

// ExportSource is where the briefing reads a caller's background exports
// that ended since they were last briefed (#2212).
type ExportSource interface {
	EndedBackground(ctx context.Context, userID string, since time.Time, limit int) ([]calltask.Task, error)
}

// ExportNotice is one background export that finished or failed since the
// caller was last briefed: a walk they started in one call and were told to
// read again later, which a session that never reads it again would
// otherwise not hear about.
type ExportNotice struct {
	Tool   string `json:"tool"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	// IdempotencyKey is what reads it again: the same tool called with it
	// answers with the export's result.
	IdempotencyKey string `json:"idempotency_key"`
	Message        string `json:"message,omitempty"`
	EndedAt        string `json:"ended_at"`
}

// newExportSource reads call_tasks over db.
func newExportSource(db *sql.DB) ExportSource { return calltask.NewPostgresStore(db) }

// endedExports returns the caller's background exports that ended since the
// watermark, newest first.
func (h *Handle) endedExports(ctx context.Context, c caller, since time.Time) ([]ExportNotice, error) {
	if h.exports == nil || c.id == "" {
		return nil, nil
	}
	tasks, err := h.exports.EndedBackground(ctx, c.id, since, maxExportNotices)
	if err != nil {
		return nil, fmt.Errorf("listing ended background exports: %w", err)
	}
	out := make([]ExportNotice, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, ExportNotice{
			Tool: t.ToolName, RunID: t.ID, Status: t.Status, IdempotencyKey: t.IdempotencyKey,
			Message: t.StatusMessage, EndedAt: stamp(t.UpdatedAt),
		})
	}
	return out, nil
}

// Exports returns the background exports the digest names, none for a nil
// digest.
func (d *Digest) Exports() []ExportNotice {
	if d == nil {
		return nil
	}
	return d.BackgroundExports
}
