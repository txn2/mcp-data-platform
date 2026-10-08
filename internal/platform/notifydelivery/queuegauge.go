package notifydelivery

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// queueStateReader is the PostgreSQL queue's read of its own open rows.
type queueStateReader interface {
	QueueState(ctx context.Context) ([]notifyqueue.KindState, error)
}

// registerQueueGauge installs the sampler background_queue_items and
// background_queue_oldest_age_seconds read the notification queue from, by
// transport kind (#1897). The alerting path -- connection revocations, the
// review queue, failed scripts -- travels this queue, so a backlog here is
// alerts nobody is receiving.
func registerQueueGauge(m *observability.Metrics, queue any) {
	q, ok := queue.(queueStateReader)
	if !ok {
		return
	}
	m.RegisterBackgroundQueue(bgloop.NameNotifyWorker, func(ctx context.Context) ([]observability.BackgroundQueueSample, error) {
		states, err := q.QueueState(ctx)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store's message names the read
		}
		out := make([]observability.BackgroundQueueSample, 0, len(states))
		for _, s := range states {
			out = append(out, observability.BackgroundQueueSample{
				Kind: s.Kind, Pending: s.Pending, Running: s.Sending, Waiting: s.Waiting, OldestAge: s.OldestDue,
			})
		}
		return out, nil
	})
}
