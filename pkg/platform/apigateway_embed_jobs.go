package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// defaultEmbedJobsTimeout is the fall-back timeout the worker uses for
// its batched /api/embed POSTs when apigateway.embed_jobs.embed_timeout
// is unset. 5 minutes covers a 32-text batch on CPU-only Ollama with
// margin; GPU deployments can tighten this via config. See #445.
const defaultEmbedJobsTimeout = 5 * time.Minute

// workerEmbedder returns the embedding.Provider the api-gateway embed-
// jobs worker should use. When the platform's embedder is Ollama, the
// worker gets a dedicated Provider with a longer HTTP timeout
// (apigateway.embed_jobs.embed_timeout, default 5m) so a batched call
// on CPU-only Ollama does not exhaust the 30s default that
// request-path callers (memory_recall, capture_insight, etc.) share.
// For any other provider, the shared platform Provider is returned
// unchanged.
func (p *Platform) workerEmbedder() embedding.Provider {
	// Only the ollama provider needs the longer timeout today; other
	// providers (noop, future kinds) reuse the shared instance.
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

// WireAPIGatewayEmbedJobsFromDB initializes the api-gateway
// embedding job queue: the Postgres store, the Worker, the
// Reaper, the Reconciler, and the LISTEN adapter. Lifecycle
// callbacks are registered so Stop on the platform cleanly
// shuts every goroutine down.
//
// No-op unless the platform has BOTH a database connection AND
// an embedding provider: a queue without a worker that can
// embed is just an accumulating backlog, and a worker without
// a queue has nothing to do. File-mode and no-embedding
// deployments fall back to the lexical ranking path with no
// queue and no operator surface (api_list_endpoints returns
// the errEmbeddingsNotIndexed note).
//
// Idempotent: calling twice is a no-op on the second call so
// the platform wiring code can call this from multiple paths
// (initial setup, config reload) without risk of double-start.
func (p *Platform) WireAPIGatewayEmbedJobsFromDB() {
	if p.apiGatewayEmbedJobsStore != nil {
		return // already wired
	}
	if p.db == nil {
		slog.Info("apigateway embed jobs: skipped (no database)")
		return
	}
	if !embedding.IsConfigured(p.embeddingProv) {
		// Either no provider wired at all, or the noop placeholder that
		// returns zero vectors. Standing up the queue against the noop
		// would fill api_catalog_operation_embeddings with [0,...,0]
		// rows that the catalog health endpoint reports as "indexed"
		// while semantic ranking quietly degrades to nonsense (#429).
		// Lexical ranking via errEmbeddingsNotIndexed handles invokes
		// in this state, and the UI surfaces the unconfigured signal
		// via /api/v1/admin/embedding/status.
		slog.Info("apigateway embed jobs: skipped (embedding provider not configured)")
		return
	}
	catalogStore := p.APIGatewayCatalogStore()
	if catalogStore == nil {
		slog.Info("apigateway embed jobs: skipped (no catalog store)")
		return
	}

	leaseDuration := p.config.APIGateway.EmbedJobs.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = embedjobs.DefaultLeaseDuration
	}
	batchSize := p.config.APIGateway.EmbedJobs.BatchSize
	if batchSize <= 0 {
		batchSize = embedjobs.DefaultEmbedBatchSize
	}
	// embed_timeout >= lease_duration is an unusual ordering:
	// the heartbeat at lease_duration/3 cadence will renew
	// the DB lease through a long batch in normal operation,
	// but the configuration implies the operator expects a
	// single batch to outlive the lease, which is worth
	// flagging at startup so a misconfigured pair doesn't
	// pass review on the assumption the heartbeat compensates
	// for any embed_timeout. Operators who meant this can
	// raise lease_duration to put a comfortable margin
	// between the two.
	if embedTimeout := p.config.APIGateway.EmbedJobs.EmbedTimeout; embedTimeout > 0 && embedTimeout >= leaseDuration {
		slog.Warn("apigateway embed jobs: embed_timeout >= lease_duration; consider raising lease_duration",
			"embed_timeout", embedTimeout, "lease_duration", leaseDuration)
	}

	store := embedjobs.NewPostgresStore(p.db, embedjobs.WithLeaseDuration(leaseDuration))
	p.apiGatewayEmbedJobsStore = store

	resolver := &catalogSpecResolver{store: catalogStore}
	computer := &apigatewayEmbeddingComputer{embedder: p.workerEmbedder()}
	persister := &catalogEmbeddingPersister{store: catalogStore}

	worker := embedjobs.NewWorker(embedjobs.WorkerConfig{
		Store:         store,
		Resolver:      resolver,
		Computer:      computer,
		Persister:     persister,
		Reloader:      &apigatewayConnectionReloader{registry: p.toolkitRegistry},
		Concurrency:   p.config.APIGateway.EmbedJobs.Workers,
		LeaseDuration: leaseDuration,
		BatchSize:     batchSize,
	})
	p.apiGatewayEmbedJobsWorker = worker

	reaper := embedjobs.NewReaper(store, 0)
	p.apiGatewayEmbedJobsReaper = reaper

	reconciler := embedjobs.NewReconciler(store, 0)
	p.apiGatewayEmbedJobsReconciler = reconciler

	// LISTEN/NOTIFY adapter. Best-effort: if the role lacks
	// LISTEN privilege we degrade to the worker's poll tick
	// (default 30s) and continue. The data path is unaffected.
	if p.config.Database.DSN != "" {
		listener := embedjobs.NewListener(p.config.Database.DSN, embedjobs.NotifyChannel, worker)
		p.apiGatewayEmbedJobsListener = listener
	}

	p.lifecycle.OnStart(func(ctx context.Context) error {
		worker.Start(ctx)
		reaper.Start(ctx)
		reconciler.Start(ctx)
		if p.apiGatewayEmbedJobsListener != nil {
			if err := p.apiGatewayEmbedJobsListener.Start(ctx); err != nil {
				slog.Warn("apigateway embed jobs: listener start failed; falling back to poll-only", "error", err)
				p.apiGatewayEmbedJobsListener = nil
			}
		}
		slog.Info("apigateway embed jobs: started")
		return nil
	})
	p.lifecycle.OnStop(func(ctx context.Context) error {
		return p.stopAPIGatewayEmbedJobs(ctx, worker, reaper, reconciler)
	})
}

// stopAPIGatewayEmbedJobs runs the embed-jobs shutdown sequence inside
// the bounded shutdown helper. Extracted from the OnStop closure so
// unit tests can exercise both the happy path and the deadline-abort
// path without spinning up the Postgres listener.
//
// Each component's Stop signals its goroutines via stopCh and blocks
// on wg.Wait. Under normal conditions every Stop completes in
// milliseconds; under failure (hung Postgres, stuck embedding call)
// Worker.Stop could block past the K8s termination grace period.
// boundedStop races the sequence against ctx.Done so the shutdown
// path always returns within its deadline. Abandoned work is safe:
// the Postgres-backed queue uses leases that expire, and another
// replica reclaims any uncompleted job on its next poll.
func (p *Platform) stopAPIGatewayEmbedJobs(
	ctx context.Context,
	worker *embedjobs.Worker,
	reaper *embedjobs.Reaper,
	reconciler *embedjobs.Reconciler,
) error {
	return boundedStop(ctx, "apigateway embed jobs", func() {
		if p.apiGatewayEmbedJobsListener != nil {
			p.apiGatewayEmbedJobsListener.Stop()
		}
		reconciler.Stop()
		reaper.Stop()
		worker.Stop()
	})
}

// boundedStop runs fn in a goroutine and races it against ctx.Done so
// a hung component cannot stall lifecycle shutdown past the supplied
// deadline. Returns nil on clean completion or ctx.Err() if the
// deadline fires first; in the deadline case a warning is logged with
// the component label so operators can correlate against the
// goroutine that hung.
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

// APIGatewayEmbedJobsStore returns the embedding job queue's
// store, or nil when no queue is wired. The admin handler reads
// this for its enqueue and read-side queries.
func (p *Platform) APIGatewayEmbedJobsStore() embedjobs.Store {
	if p.apiGatewayEmbedJobsStore == nil {
		return nil
	}
	return p.apiGatewayEmbedJobsStore
}

// catalogSpecResolver implements embedjobs.SpecResolver against
// a catalog.Store. The worker calls GetSpecContent on every job
// claim to fetch the current spec content (which may have
// changed since the job was enqueued).
type catalogSpecResolver struct {
	store apigatewaycatalog.Store
}

// GetSpecContent returns the content column on the spec row.
// Returns ("", err) on any store error (treated as a retryable
// failure by the worker).
func (r *catalogSpecResolver) GetSpecContent(ctx context.Context, catalogID, specName string) (string, error) {
	spec, err := r.store.GetSpec(ctx, catalogID, specName)
	if err != nil {
		return "", fmt.Errorf("catalogSpecResolver: %w", err)
	}
	return spec.Content, nil
}

// apigatewayEmbeddingComputer wraps
// apigatewaykit.ComputeOperationEmbeddings. The translation
// between embedjobs.ExistingEmbedding /
// catalog.OperationEmbedding keeps the embedjobs package free
// of the pgvector dependency the catalog package pulls in.
type apigatewayEmbeddingComputer struct {
	embedder embeddingProvider
}

// embeddingProvider is the local minimal interface this file
// uses to refer to the platform's embedding.Provider. Declared
// inline so the type assertion in WireAPIGatewayEmbedJobsFromDB
// stays explicit and so this file does not pull the embedding
// package's full surface.
type embeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
	Kind() string
}

// Compute translates the embedjobs-side dedup map into a
// catalog.OperationEmbedding-keyed map, invokes the apigateway
// kit's ComputeOperationEmbeddings, and translates the result
// back. The two intermediate types exist so embedjobs does not
// import pgvector through every transitive consumer.
//
// The per-batch PersistBatch callback the worker supplies is
// wrapped by translateBatchPersist so the catalog-typed rows
// it receives flow into the worker's embedjobs-typed callback
// without forcing embedjobs to import the catalog package.
func (c *apigatewayEmbeddingComputer) Compute(ctx context.Context, req embedjobs.ComputeRequest) ([]embedjobs.ComputedEmbedding, error) {
	catalogExisting := make(map[string]apigatewaycatalog.OperationEmbedding, len(req.Existing))
	for k, v := range req.Existing {
		catalogExisting[k] = apigatewaycatalog.OperationEmbedding{
			OperationID: v.OperationID,
			TextHash:    v.TextHash,
			Embedding:   v.Embedding,
			Model:       v.Model,
			Dim:         v.Dim,
		}
	}
	rows, err := apigatewaykit.ComputeOperationEmbeddings(ctx, apigatewaykit.ComputeRequest{
		Embedder:     c.embedder,
		Content:      req.Content,
		SpecName:     req.SpecName,
		Existing:     catalogExisting,
		BatchSize:    req.BatchSize,
		Progress:     req.Progress,
		PersistBatch: translateBatchPersist(req.PersistBatch),
	})
	if err != nil {
		return nil, fmt.Errorf("apigatewayEmbeddingComputer: %w", err)
	}
	out := make([]embedjobs.ComputedEmbedding, len(rows))
	for i, r := range rows {
		out[i] = embedjobs.ComputedEmbedding{
			OperationID: r.OperationID,
			TextHash:    r.TextHash,
			Embedding:   r.Embedding,
			Model:       r.Model,
			Dim:         r.Dim,
		}
	}
	return out, nil
}

// translateBatchPersist adapts the catalog-typed per-batch
// callback the apigateway kit invokes into the embedjobs-typed
// callback the worker supplies. nil in -> nil out so the
// computer's "no persist callback" path is preserved end-to-end.
func translateBatchPersist(cb func([]embedjobs.ComputedEmbedding) error) func([]apigatewaycatalog.OperationEmbedding) error {
	if cb == nil {
		return nil
	}
	return func(rows []apigatewaycatalog.OperationEmbedding) error {
		out := make([]embedjobs.ComputedEmbedding, len(rows))
		for i, r := range rows {
			out[i] = embedjobs.ComputedEmbedding{
				OperationID: r.OperationID,
				TextHash:    r.TextHash,
				Embedding:   r.Embedding,
				Model:       r.Model,
				Dim:         r.Dim,
			}
		}
		return cb(out)
	}
}

// catalogEmbeddingPersister wraps catalog.Store's embedding
// methods so the worker can write vectors without knowing about
// the catalog package's full surface.
type catalogEmbeddingPersister struct {
	store apigatewaycatalog.Store
}

// ListExisting reads the current set of persisted embedding
// rows for (catalogID, specName) and translates them into the
// embedjobs-side ExistingEmbedding type for dedup.
func (p *catalogEmbeddingPersister) ListExisting(ctx context.Context, catalogID, specName string) (map[string]embedjobs.ExistingEmbedding, error) {
	rows, err := p.store.ListOperationEmbeddings(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogEmbeddingPersister: %w", err)
	}
	out := make(map[string]embedjobs.ExistingEmbedding, len(rows))
	for _, r := range rows {
		out[r.OperationID] = embedjobs.ExistingEmbedding{
			OperationID: r.OperationID,
			TextHash:    r.TextHash,
			Embedding:   r.Embedding,
			Model:       r.Model,
			Dim:         r.Dim,
		}
	}
	return out, nil
}

// Upsert atomically replaces the persisted embedding rows for
// (catalogID, specName) with the supplied set via the catalog
// store's transactional delete+insert.
func (p *catalogEmbeddingPersister) Upsert(ctx context.Context, catalogID, specName string, rows []embedjobs.ComputedEmbedding) error {
	if err := p.store.UpsertOperationEmbeddings(ctx, catalogID, specName, toCatalogEmbeddings(rows)); err != nil {
		return fmt.Errorf("catalogEmbeddingPersister: %w", err)
	}
	return nil
}

// UpsertBatch writes a single chunk's worth of embedding rows
// in place without disturbing rows outside the supplied set.
// The worker invokes this via the PersistBatch callback so a
// job that fails on chunk N still has chunks 0..N-1 visible to
// the next attempt's ListExisting dedup pass.
func (p *catalogEmbeddingPersister) UpsertBatch(ctx context.Context, catalogID, specName string, rows []embedjobs.ComputedEmbedding) error {
	if err := p.store.UpsertOperationEmbeddingsBatch(ctx, catalogID, specName, toCatalogEmbeddings(rows)); err != nil {
		return fmt.Errorf("catalogEmbeddingPersister: %w", err)
	}
	return nil
}

// toCatalogEmbeddings is the shared embedjobs->catalog row
// translation used by both Upsert and UpsertBatch. Factored out
// so the two persistence sites share one source of truth for
// the column mapping.
func toCatalogEmbeddings(rows []embedjobs.ComputedEmbedding) []apigatewaycatalog.OperationEmbedding {
	out := make([]apigatewaycatalog.OperationEmbedding, len(rows))
	for i, r := range rows {
		out[i] = apigatewaycatalog.OperationEmbedding{
			OperationID: r.OperationID,
			TextHash:    r.TextHash,
			Embedding:   r.Embedding,
			Model:       r.Model,
			Dim:         r.Dim,
		}
	}
	return out
}

// StampOperationCount writes the supplied count to the spec
// row's operation_count column so the reconciler's gap
// predicate sees a fully-indexed spec.
func (p *catalogEmbeddingPersister) StampOperationCount(ctx context.Context, catalogID, specName string, count int) error {
	if err := p.store.SetOperationCount(ctx, catalogID, specName, count); err != nil {
		return fmt.Errorf("catalogEmbeddingPersister: %w", err)
	}
	return nil
}

// apigatewayConnectionReloader implements
// embedjobs.ConnectionReloader: after a successful embed, the
// worker calls ReloadConnectionsByCatalog so live connections
// pick up the new vectors without waiting for the next admin
// save.
type apigatewayConnectionReloader struct {
	registry *registry.Registry
}

// ReloadConnectionsByCatalog asks every registered api-gateway
// toolkit to rebuild connections that mount the given catalog
// so their in-memory vector map picks up the new embedding
// rows the worker just wrote.
func (r *apigatewayConnectionReloader) ReloadConnectionsByCatalog(catalogID string) {
	if r.registry == nil {
		return
	}
	for _, tk := range r.registry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.ReloadConnectionsByCatalog(catalogID)
		}
	}
}
