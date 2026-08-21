package reviewalert

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// InsightSource reports the knowledge insight review queue through the same
// fast-path rollup the portal and platform_info read (#764), so the alert fires
// on exactly the rows those surfaces already badge.
type InsightSource struct {
	Insights knowledgekit.InsightStore
}

// Pending returns the insight queue's rollup.
func (s InsightSource) Pending(ctx context.Context, now time.Time) (notification.ReviewQueue, error) {
	review, err := knowledgekit.PendingReviewOf(ctx, s.Insights)
	if err != nil {
		return notification.ReviewQueue{}, fmt.Errorf("reading the knowledge review queue: %w", err)
	}
	q := notification.ReviewQueue{
		Pending:        review.TotalPending,
		StaleCount:     review.PendingOver30d,
		StaleAfterDays: knowledgekit.PendingStalenessThresholdDays,
	}
	if review.OldestPendingAt != nil {
		q.OldestAgeDays = knowledgekit.AgeDays(*review.OldestPendingAt, now)
	}
	return q, nil
}

// Verify interface compliance.
var _ Source = InsightSource{}
