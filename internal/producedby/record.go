package producedby

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Note records one write against whatever producer the context names.
//
// It is best effort and reports nothing back to its caller, on the reasoning
// the audit path uses: it sits inside the write funnels themselves, and losing
// the note that a write happened must never lose the write. A deployment with
// no store, and a call arriving with no producer on its context, both record
// nothing and are not failures.
func Note(ctx context.Context, s Store, w Write) {
	if s == nil {
		return
	}
	p, ok := From(ctx)
	if !ok {
		return
	}
	w.Producer = p
	if err := s.Record(ctx, w); err != nil {
		slog.Warn("recording what produced this file failed; the write itself succeeded",
			"target_kind", logsan.SanitizeForLog(w.TargetKind),
			"target_id", logsan.SanitizeForLog(w.TargetID),
			"producer_kind", logsan.SanitizeForLog(p.Kind),
			"producer_id", logsan.SanitizeForLog(p.ID),
			"error", logsan.SanitizeForLog(err.Error()),
		)
	}
}
