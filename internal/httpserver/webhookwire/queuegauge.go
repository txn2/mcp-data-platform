package webhookwire

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// registerCompactionGauge installs the sampler background_queue_items and
// background_queue_oldest_age_seconds read the compactor's backlog from
// (#1897). It is installed wherever the webhook tables are, compactor or
// not: a replica that only receives is the one that sees windows pile up
// when no compactor runs.
func registerCompactionGauge(m *observability.Metrics, windows *whstore.Store) {
	m.RegisterBackgroundQueue(bgloop.NameWebhookCompactor, func(ctx context.Context) ([]observability.BackgroundQueueSample, error) {
		st, err := windows.CompactionState(ctx)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store's message names the read
		}
		return []observability.BackgroundQueueSample{{
			Pending: st.Pending, Running: st.Running, Waiting: st.Waiting, OldestAge: st.OldestOwed,
		}}, nil
	})
}
