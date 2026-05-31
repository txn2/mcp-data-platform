package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/memory/memoryindex"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// defaultEmbedJobsTimeout is the fall-back timeout the index-jobs
// worker uses for its batched embedding calls when
// apigateway.embed_jobs.embed_timeout is unset. 5 minutes covers a
// 32-text batch on CPU-only Ollama with margin; GPU deployments can
// tighten this via config. See #445.
const defaultEmbedJobsTimeout = 5 * time.Minute

// workerEmbedder returns the embedding.Provider the index-jobs
// worker should use. When the platform's embedder is Ollama, the
// worker gets a dedicated Provider with a longer HTTP timeout
// (apigateway.embed_jobs.embed_timeout, default 5m) so a batched
// call on CPU-only Ollama does not exhaust the 30s default that
// request-path callers (memory_recall, capture_insight, etc.) share.
// For any other provider, the shared platform Provider is returned
// unchanged.
func (p *Platform) workerEmbedder() embedding.Provider {
	if p.config.Memory.Embedding.Provider != "ollama" {
		return p.embeddingProv
	}
	timeout := p.config.APIGateway.EmbedJobs.EmbedTimeout
	if timeout <= 0 {
		timeout = defaultEmbedJobsTimeout
	}
	return embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL:     p.config.Memory.Embedding.Ollama.URL,
		Model:   p.config.Memory.Embedding.Ollama.Model,
		Timeout: timeout,
	})
}

// WireAPIGatewayEmbedJobsFromDB initializes the shared index-jobs
// queue (pkg/indexjobs) and registers the api-catalog consumer on
// it: the Postgres store, the Source/Sink registry, the Worker, the
// Reaper, the Reconciler, and the LISTEN adapter. The admin handler
// reads its api-catalog-shaped view through an AdminStore over the
// same generic store. Lifecycle callbacks shut every goroutine down
// cleanly on platform Stop.
//
// No-op unless the platform has BOTH a database connection AND a
// configured embedding provider: a queue without a worker that can
// embed is just an accumulating backlog, and standing the queue up
// against the noop provider would fill the vector tables with zero
// vectors the health endpoints report as "indexed" while ranking
// silently degrades (#429). File-mode and no-embedding deployments
// fall back to lexical ranking with no queue.
//
// Idempotent: a second call is a no-op.
func (p *Platform) WireAPIGatewayEmbedJobsFromDB() {
	if !p.indexJobsPreconditions() {
		return
	}

	leaseDuration, batchSize := p.resolveEmbedJobsTuning()
	store := indexjobs.NewPostgresStore(p.db, indexjobs.WithLeaseDuration(leaseDuration))
	reg := indexjobs.NewRegistry()
	p.registerIndexConsumers(reg, store)
	if len(reg.Kinds()) == 0 {
		// db + embedder are present but nothing registered (no catalog
		// store and tools registration somehow failed). A worker with
		// no consumers has nothing to do.
		slog.Info("index jobs: skipped (no consumers registered)")
		return
	}
	p.indexJobsStore = store
	p.indexJobsRegistry = reg

	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store:         store,
		Registry:      reg,
		Embedder:      p.workerEmbedder(),
		Concurrency:   p.config.APIGateway.EmbedJobs.Workers,
		LeaseDuration: leaseDuration,
		BatchSize:     batchSize,
	})
	p.indexJobsWorker = worker

	reaper := indexjobs.NewReaper(store, 0)
	p.indexJobsReaper = reaper

	reconciler := indexjobs.NewReconciler(store, reg, 0)
	p.indexJobsReconciler = reconciler

	// LISTEN/NOTIFY adapter. Best-effort: if the role lacks LISTEN
	// privilege we degrade to the worker's poll tick and continue.
	if p.config.Database.DSN != "" {
		p.indexJobsListener = indexjobs.NewListener(p.config.Database.DSN, indexjobs.NotifyChannel, worker)
	}

	p.lifecycle.OnStart(func(ctx context.Context) error {
		worker.Start(ctx)
		reaper.Start(ctx)
		reconciler.Start(ctx)
		if p.indexJobsListener != nil {
			if err := p.indexJobsListener.Start(ctx); err != nil {
				slog.Warn("index jobs: listener start failed; falling back to poll-only", "error", err)
				p.indexJobsListener = nil
			}
		}
		p.bootstrapToolsIndex(ctx)
		slog.Info("index jobs: started", "kinds", reg.Kinds())
		return nil
	})
	p.lifecycle.OnStop(func(ctx context.Context) error {
		return p.stopIndexJobs(ctx, worker, reaper, reconciler)
	})
}

// indexJobsPreconditions reports whether the shared index-jobs
// framework should be wired. It requires a database and a configured
// embedding provider (#429: never stand the queue up against the noop
// provider) and must not already be wired. Which consumers actually
// register is decided in registerIndexConsumers: api-catalog needs its
// catalog store, the tools consumer always registers.
func (p *Platform) indexJobsPreconditions() bool {
	switch {
	case p.indexJobsStore != nil:
		return false // already wired
	case p.db == nil:
		slog.Info("index jobs: skipped (no database)")
		return false
	case !embedding.IsConfigured(p.embeddingProv):
		slog.Info("index jobs: skipped (embedding provider not configured)")
		return false
	}
	return true
}

// registerIndexConsumers registers every available consumer on the
// shared registry. The api-catalog consumer registers only when its
// catalog store is present; the tools consumer always registers,
// because the framework preconditions (database + embedding provider)
// are all it needs to index the in-process tool registry. A
// registration error is a wiring bug (duplicate/mismatched kind), so
// it is logged and that consumer is skipped rather than aborting the
// others.
func (p *Platform) registerIndexConsumers(reg *indexjobs.Registry, store *indexjobs.PostgresStore) {
	if catalogStore := p.APIGatewayCatalogStore(); catalogStore != nil {
		if err := reg.Register(
			&catalogSource{store: catalogStore, registry: p.toolkitRegistry},
			catalogindex.NewSink(catalogStore),
		); err != nil {
			slog.Error("index jobs: api-catalog registration failed", "error", err)
		} else {
			p.apiGatewayEmbedAdminStore = catalogindex.NewAdminStore(store, p.db)
		}
	}

	toolsStore := toolsindex.NewStore(p.db)
	if err := reg.Register(&toolsSource{p: p}, toolsindex.NewSink(toolsStore)); err != nil {
		slog.Error("index jobs: tools registration failed", "error", err)
	} else {
		p.toolsIndexStore = toolsStore
	}

	// Memory consumer: backfills embeddings the synchronous write path
	// could not produce (saved during an embedder outage) or that a model
	// swap left stale. Registered only when memory is enabled (its store
	// is wired); the reconciler's FindGaps discovers gaps from the
	// memory_records table directly, so no bootstrap enqueue is needed.
	if p.memoryStore != nil {
		memStore := memoryindex.NewStore(p.db)
		if err := reg.Register(
			memoryindex.NewSource(memStore),
			memoryindex.NewSink(memStore, embedding.ModelName(p.embeddingProv)),
		); err != nil {
			slog.Error("index jobs: memory registration failed", "error", err)
		}
	}
}

// bootstrapToolsIndex enqueues the initial tools index job at startup.
// The tool corpus is not a DB table the reconciler can discover on its
// own (it diffs an already-recorded expected count, which does not
// exist until the first embed), so the first index for a fresh
// deployment must be kicked off explicitly. Idempotent: the partial
// unique index collapses a duplicate enqueue, and the worker's
// text-hash dedup skips re-embedding unchanged tools, so running it on
// every boot is cheap.
func (p *Platform) bootstrapToolsIndex(ctx context.Context) {
	if p.toolsIndexStore == nil || p.indexJobsStore == nil {
		return
	}
	if _, err := p.indexJobsStore.Enqueue(ctx,
		indexjobs.Key{SourceKind: toolsindex.SourceKind, SourceID: toolsindex.SourceID},
		indexjobs.TriggerWrite); err != nil {
		slog.Warn("index jobs: tools bootstrap enqueue failed", "error", err)
	}
}

// resolveEmbedJobsTuning returns the worker lease duration and batch
// size, defaulting unset config and warning on the unusual
// embed_timeout >= lease_duration ordering (the heartbeat compensates
// in normal operation, but the pairing is worth flagging at startup).
func (p *Platform) resolveEmbedJobsTuning() (lease time.Duration, batch int) {
	lease = p.config.APIGateway.EmbedJobs.LeaseDuration
	if lease <= 0 {
		lease = indexjobs.DefaultLeaseDuration
	}
	batch = p.config.APIGateway.EmbedJobs.BatchSize
	if batch <= 0 {
		batch = indexjobs.DefaultEmbedBatchSize
	}
	if embedTimeout := p.config.APIGateway.EmbedJobs.EmbedTimeout; embedTimeout > 0 && embedTimeout >= lease {
		slog.Warn("index jobs: embed_timeout >= lease_duration; consider raising lease_duration",
			"embed_timeout", embedTimeout, "lease_duration", lease)
	}
	return lease, batch
}

// stopIndexJobs runs the index-jobs shutdown sequence inside the
// bounded shutdown helper. Each component's Stop signals its
// goroutines and blocks on their WaitGroup; boundedStop races the
// sequence against ctx.Done so shutdown always returns within its
// deadline. Abandoned work is safe: leases expire and another
// replica reclaims any uncompleted job on its next poll.
func (p *Platform) stopIndexJobs(
	ctx context.Context,
	worker *indexjobs.Worker,
	reaper *indexjobs.Reaper,
	reconciler *indexjobs.Reconciler,
) error {
	return boundedStop(ctx, "index jobs", func() {
		if p.indexJobsListener != nil {
			p.indexJobsListener.Stop()
		}
		reconciler.Stop()
		reaper.Stop()
		worker.Stop()
	})
}

// boundedStop runs fn in a goroutine and races it against ctx.Done
// so a hung component cannot stall lifecycle shutdown past the
// supplied deadline. Returns nil on clean completion or ctx.Err() if
// the deadline fires first.
func boundedStop(ctx context.Context, component string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		slog.Warn("shutdown: bounded stop deadline reached; abandoning in-flight work",
			"component", component, "error", ctx.Err())
		return ctx.Err() //nolint:wrapcheck // ctx.Err() is the expected sentinel; lifecycle aggregates it
	}
}

// APIGatewayEmbedJobsStore returns the api-catalog admin view of the
// index-jobs queue (enqueue + read-side queries for the UI), or nil
// when no queue is wired. The admin handler reads this.
func (p *Platform) APIGatewayEmbedJobsStore() catalogindex.Store {
	if p.apiGatewayEmbedAdminStore == nil {
		return nil
	}
	return p.apiGatewayEmbedAdminStore
}

// IndexJobsReporter returns the cross-kind index-jobs reporter the
// admin Indexing dashboard reads (per-kind counts, coverage, job list,
// re-index), or nil when no queue is wired (no database or no
// configured embedding provider). The dashboard renders a degraded
// empty state for the nil case.
func (p *Platform) IndexJobsReporter() *indexjobs.Reporter {
	if p.indexJobsStore == nil || p.indexJobsRegistry == nil {
		return nil
	}
	return indexjobs.NewReporter(p.indexJobsStore, p.indexJobsRegistry)
}

// catalogSource implements indexjobs.Source for the api-catalog
// kind. LoadItems fetches the current spec content and parses it
// into per-operation embeddable items; OnSucceeded reloads live
// connections so their in-memory vector map picks up the new rows.
type catalogSource struct {
	store    apigatewaycatalog.Store
	registry *registry.Registry
}

// Kind reports the api-catalog source kind.
func (*catalogSource) Kind() string { return catalogindex.SourceKind }

// LoadItems decodes the source_id, fetches the spec content, and
// returns one item per operation. A missing spec surfaces as an
// error (the worker treats it as terminal: the spec was deleted).
func (s *catalogSource) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	catalogID, specName, ok := catalogindex.DecodeSourceID(sourceID)
	if !ok {
		return nil, fmt.Errorf("catalogSource: malformed source_id %q", sourceID)
	}
	spec, err := s.store.GetSpec(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogSource: get spec: %w", err)
	}
	ops, err := apigatewaykit.BuildOperationItems(spec.Content, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogSource: build items: %w", err)
	}
	items := make([]indexjobs.Item, len(ops))
	for i, op := range ops {
		items[i] = indexjobs.Item{ItemID: op.OperationID, Text: op.Text}
	}
	return items, nil
}

// OnSucceeded asks every registered api-gateway toolkit to rebuild
// connections that mount the catalog so their in-memory vector map
// picks up the freshly-written rows.
func (s *catalogSource) OnSucceeded(sourceID string) {
	if s.registry == nil {
		return
	}
	catalogID, _, ok := catalogindex.DecodeSourceID(sourceID)
	if !ok {
		return
	}
	for _, tk := range s.registry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.ReloadConnectionsByCatalog(catalogID)
		}
	}
}
