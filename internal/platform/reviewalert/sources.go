package reviewalert

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
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

// ScriptSource reports the managed-script review queue: versions no reviewer
// has decided on yet (#1287).
//
// It counts the same rows the review surface lists rather than a second
// definition of "pending", so the number in the email is the number of rows an
// operator finds when they open the queue.
type ScriptSource struct {
	Reviews script.ReviewStore
}

// Pending returns the script review queue's rollup.
//
// It reports no stale count: unlike insights, the platform has no second,
// established staleness window for script reviews, and inventing one to fill
// the field would put a number in the email that means nothing elsewhere. The
// age of the oldest review is the signal.
func (s ScriptSource) Pending(ctx context.Context, now time.Time) (notification.ReviewQueue, error) {
	pending, err := s.Reviews.ListPendingReviews(ctx)
	if err != nil {
		return notification.ReviewQueue{}, fmt.Errorf("reading the script review queue: %w", err)
	}
	q := notification.ReviewQueue{Pending: len(pending)}
	// The store returns the queue oldest first, but the oldest is computed here
	// rather than read off the first row: a rollup that silently depends on a
	// query's ORDER BY becomes wrong the moment somebody reorders the listing
	// for the UI's benefit.
	for _, p := range pending {
		if age := p.AgeDays(now); age > q.OldestAgeDays {
			q.OldestAgeDays = age
		}
	}
	return q, nil
}

// Verify interface compliance.
var (
	_ Source = InsightSource{}
	_ Source = ScriptSource{}
)
