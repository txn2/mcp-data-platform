package webhookwire

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
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

// storageEvery is how often the per-source storage is read for the
// webhook_source_bytes gauge: the read aggregates every window record, so it
// is kept off the scrape path and refreshed at most this often.
const storageEvery = time.Minute

// registerStorageGauge installs the sampler webhook_source_bytes reads each
// source's objects from (#2150). A scrape answers from the last read; one
// older than storageEvery starts a new read in the background, so the scrape
// never waits on the database.
func registerStorageGauge(m *observability.Metrics, windows *whstore.Store) {
	c := &storageCache{read: windows.Storage, now: time.Now}
	m.RegisterWebhookStorage(c.sample)
}

// storageCache is the last per-source storage read and its refresh.
type storageCache struct {
	read func(ctx context.Context, source string) ([]whstore.SourceStorage, error)
	now  func() time.Time

	mu      sync.Mutex
	samples []observability.WebhookStorageSample
	readAt  time.Time
	reading bool
}

// sample is the last read, starting a new one when it is stale.
func (c *storageCache) sample(ctx context.Context) ([]observability.WebhookStorageSample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.reading && c.now().Sub(c.readAt) >= storageEvery {
		c.reading = true
		go c.refresh(context.WithoutCancel(ctx))
	}
	return c.samples, nil
}

// refresh reads every source's storage and keeps it; a failed read keeps the
// last one and is tried again on a later scrape.
func (c *storageCache) refresh(ctx context.Context) {
	rows, err := c.read(ctx, "")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading = false
	if err != nil {
		slog.WarnContext(ctx, "webhooks: reading per-source storage", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	out := make([]observability.WebhookStorageSample, 0, len(rows))
	for _, r := range rows {
		out = append(out, observability.WebhookStorageSample{Source: r.Source, RawBytes: r.RawBytes, CompactedBytes: r.CompactedBytes})
	}
	c.samples, c.readAt = out, c.now()
}
