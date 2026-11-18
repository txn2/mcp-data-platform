// Package scriptqueue samples the managed-script run queue and the due
// schedules for background_queue_items and
// background_queue_oldest_age_seconds (#1897), apart from the run worker
// that drains them.
package scriptqueue

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// runQueueReader is the run store's read of its own queue (scriptstore).
type runQueueReader interface {
	RunQueueState(ctx context.Context) (scriptstore.QueueState, error)
}

// dueScheduleReader is the schedule store's read of its due schedules.
type dueScheduleReader interface {
	DueScheduleState(ctx context.Context) (int64, time.Duration, error)
}

// RegisterGauges installs the samplers background_queue_items and
// background_queue_oldest_age_seconds read the run queue and the due
// schedules from (#1897). It runs on every replica, the worker-off ones
// included: a deployment whose every worker is off is the one whose runs and
// schedules pile up, and the replica serving the scrape must be able to say so.
func RegisterGauges(m *observability.Metrics, runs, schedules any) {
	if q, ok := runs.(runQueueReader); ok {
		m.RegisterBackgroundQueue(bgloop.NameScriptWorker, func(ctx context.Context) ([]observability.BackgroundQueueSample, error) {
			st, err := q.RunQueueState(ctx)
			if err != nil {
				return nil, err //nolint:wrapcheck // the store's message names the read
			}
			return []observability.BackgroundQueueSample{{
				Pending: st.Pending, Running: st.Running, Waiting: st.Waiting, OldestAge: st.OldestDue,
			}}, nil
		})
	}
	if s, ok := schedules.(dueScheduleReader); ok {
		m.RegisterBackgroundQueue(bgloop.NameScriptScheduler, func(ctx context.Context) ([]observability.BackgroundQueueSample, error) {
			due, oldest, err := s.DueScheduleState(ctx)
			if err != nil {
				return nil, err //nolint:wrapcheck // the store's message names the read
			}
			return []observability.BackgroundQueueSample{{Pending: due, OldestAge: oldest}}, nil
		})
	}
}
