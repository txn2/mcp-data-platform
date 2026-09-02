package knowledgelayer

import (
	"context"
	"log/slog"
	"time"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// ReviewQueueInfo summarizes the pending apply_knowledge review queue so an agent
// can nudge a reviewer about aging review debt, e.g. "6 insights pending review,
// oldest 94 days" (#764). platform_info reports it only for a caller who can
// reach apply_knowledge and only when the queue is non-empty.
type ReviewQueueInfo struct {
	Pending              int `json:"pending"`
	OldestPendingAgeDays int `json:"oldest_pending_age_days,omitempty"`
	PendingOver30d       int `json:"pending_over_30d,omitempty"`
}

// PendingReviewSummary summarizes the pending review queue for platform_info so
// an agent can nudge a reviewer about aging review debt (#764). It returns nil
// on a nil Handle, when knowledge is disabled (no insight store), when the stats
// lookup fails, or when the queue is empty; a failed lookup must not fail
// orientation, so the error is logged and swallowed rather than propagated.
func (h *Handle) PendingReviewSummary(ctx context.Context) *ReviewQueueInfo {
	store := h.InsightStore()
	if store == nil {
		return nil
	}
	// Prefer the store's cheap pending-count + staleness path over the full Stats
	// fan-out: platform_info runs once per session and needs only the review-debt
	// nudge, not the category/confidence group-bys (#764).
	review, err := knowledgekit.PendingReviewOf(ctx, store)
	if err != nil {
		slog.WarnContext(ctx, "platform_info: pending review queue stats unavailable", "error", err)
		return nil
	}
	if review.TotalPending == 0 {
		return nil
	}
	info := &ReviewQueueInfo{
		Pending:        review.TotalPending,
		PendingOver30d: review.PendingOver30d,
	}
	if review.OldestPendingAt != nil {
		info.OldestPendingAgeDays = knowledgekit.AgeDays(*review.OldestPendingAt, time.Now())
	}
	return info
}
