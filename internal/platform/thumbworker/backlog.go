package thumbworker

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// rendererBacklog is a store's count of the documents owed a tile by renderer
// generation renderer: the ones no replica holds, and the ones held.
type rendererBacklog interface {
	ThumbnailBacklog(ctx context.Context, renderer int) (pending, waiting int64, err error)
}

// collectionBacklog is the collection store's count of mosaics owed.
type collectionBacklog interface {
	CollectionThumbnailBacklog(ctx context.Context) (pending, waiting int64, err error)
}

// scriptBacklog is the script tile store's count of tiles owed.
type scriptBacklog interface {
	Backlog(ctx context.Context, renderer int) (pending, waiting int64, err error)
}

// Families, as the backlog and the render series label them: the first word of
// a job's name.
const (
	kindAsset      = "asset"
	kindResource   = "resource"
	kindCollection = "collection"
	kindScript     = "script"
)

// registerBacklog installs the sampler background_queue_items and
// background_queue_oldest_age_seconds read the renderer's backlog from, one
// series per family whose store can count it (#1897). The tiles carry no due
// time, so the oldest age is not reported.
func (w *Worker) registerBacklog(m *observability.Metrics) {
	counts := map[string]func(context.Context) (int64, int64, error){}
	if b, ok := w.deps.Assets.(rendererBacklog); ok {
		counts[kindAsset] = func(ctx context.Context) (int64, int64, error) { return b.ThumbnailBacklog(ctx, Renderer) }
	}
	if b, ok := w.deps.Resources.(rendererBacklog); ok {
		counts[kindResource] = func(ctx context.Context) (int64, int64, error) { return b.ThumbnailBacklog(ctx, Renderer) }
	}
	if b, ok := w.deps.Collections.(collectionBacklog); ok {
		counts[kindCollection] = b.CollectionThumbnailBacklog
	}
	if b, ok := w.deps.Scripts.(scriptBacklog); ok {
		counts[kindScript] = func(ctx context.Context) (int64, int64, error) { return b.Backlog(ctx, Renderer) }
	}
	if len(counts) == 0 {
		return
	}
	m.RegisterBackgroundQueue(bgloop.NameThumbnailWorker, cachedBacklog(counts, backlogMaxAge, time.Now))
}

// backlogMaxAge is how long one reading of the tile backlog is reported
// before the next scrape counts again. The counts scan whole tables (the
// collection mosaic's source is a window over every collection item), and a
// backlog moves in minutes, not seconds, so every replica counting on every
// scrape was database load for no reading anybody needed.
const backlogMaxAge = time.Minute

// cachedBacklog is the sampler over counts: each family counted at once,
// concurrently, and the result kept for maxAge.
func cachedBacklog(
	counts map[string]func(context.Context) (int64, int64, error), maxAge time.Duration, now func() time.Time,
) observability.BackgroundQueueSampler {
	var (
		mu     sync.Mutex
		cached []observability.BackgroundQueueSample
		at     time.Time
	)
	return func(ctx context.Context) ([]observability.BackgroundQueueSample, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && now().Sub(at) < maxAge {
			return cached, nil
		}
		var (
			outMu sync.Mutex
			out   = make([]observability.BackgroundQueueSample, 0, len(counts))
		)
		g, gctx := errgroup.WithContext(ctx)
		for kind, count := range counts {
			g.Go(func() error {
				pending, waiting, err := count(gctx)
				if err != nil {
					return err
				}
				outMu.Lock()
				out = append(out, observability.BackgroundQueueSample{Kind: kind, Pending: pending, Waiting: waiting})
				outMu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err //nolint:wrapcheck // the store's own error, logged by the scrape with the queue's name
		}
		slices.SortFunc(out, func(a, b observability.BackgroundQueueSample) int { return cmp.Compare(a.Kind, b.Kind) })
		cached, at = out, now()
		return out, nil
	}
}
