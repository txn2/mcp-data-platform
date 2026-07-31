// Package indexqueue assembles the shared background embedding queue
// (pkg/indexjobs) behind one Handle: the Postgres store, the Source/Sink
// registry, the worker/reaper/reconciler, the optional retention sweep and
// LISTEN/NOTIFY adapter, and every enabled consumer (api-catalog, tools,
// memory, prompts, portal assets/collections/knowledge-pages, managed
// resources, catalog datasets).
//
// New takes an explicit Config: callers translate their own config into Config
// at the boundary and wire the returned Handle's Start/Stop into their own
// lifecycle. The package must not import pkg/platform. The tools source obtains
// the live in-process tool corpus through the injected ToolEnumerator.
package indexqueue

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/assetindex"
	"github.com/txn2/mcp-data-platform/internal/platform/collectionindex"
	"github.com/txn2/mcp-data-platform/internal/platform/datasetindex"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/internal/platform/memoryindex"
	"github.com/txn2/mcp-data-platform/internal/platform/promptindex"
	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// logKeyError is the structured-log key for an error value, kept consistent
// with the platform package's own logging.
const logKeyError = "error"

// Consumers gates the optional DB-backed consumers by the presence of their
// platform sub-store. Each consumer's Source/Sink needs only the queue's *sql.DB
// and the embedding model name to build, so the caller passes a boolean per
// consumer rather than threading the stores themselves through Config.
type Consumers struct {
	Memory               bool
	Prompts              bool
	PortalAssets         bool
	PortalCollections    bool
	PortalKnowledgePages bool
	Resources            bool
	// CatalogDatasets registers the catalog-dataset consumer, which mirrors the
	// configured semantic catalog's dataset text into the platform's own index
	// (#1131). Unlike the others it is not gated on a platform sub-store: its
	// corpus lives in DataHub, so the caller reports whether a real catalog is
	// configured and the index is enabled.
	CatalogDatasets bool
}

// Config carries the values New needs to assemble the queue. Callers build it
// from their own config so this package stays free of platform config types.
type Config struct {
	// DB backs the queue store and every consumer sink. Required.
	DB *sql.DB

	// Embedder is the worker's embedding provider, already resolved by the
	// caller (e.g. a dedicated longer-timeout Ollama provider). ModelName is the
	// platform embedder's model name, recorded on each consumer sink so a model
	// swap invalidates stale vectors.
	Embedder  embedding.Provider
	ModelName string

	// Worker tuning, already defaulted by the caller.
	LeaseDuration time.Duration
	BatchSize     int
	Workers       int

	// RetentionDays > 0 wires a retention sweep that bounds finished history;
	// <= 0 disables it (the worker/reaper/reconciler still run, history is never
	// purged).
	RetentionDays int

	// DSN enables the LISTEN/NOTIFY adapter when non-empty; empty falls back to
	// the worker's poll tick.
	DSN string

	// CatalogStore, when non-nil, registers the api-catalog consumer and backs
	// the admin view. ToolkitRegistry lets a successful api-catalog embed reload
	// live api-gateway connections so their in-memory vector map picks up the
	// new rows; may be nil.
	CatalogStore    apigatewaycatalog.Store
	ToolkitRegistry *registry.Registry

	// ToolEnumerator supplies the live, globally-visible tool corpus the tools
	// source embeds. DiscoveryToolName is the discovery tool's own name, excluded
	// from the corpus so a find-tools query never ranks the discovery tool itself.
	ToolEnumerator    ToolEnumerator
	DiscoveryToolName string

	// Consumers gates the optional DB-backed consumers.
	Consumers Consumers

	// CatalogLister enumerates the semantic catalog for the catalog-dataset
	// consumer, and CatalogIndex carries that consumer's operator tuning (sweep
	// interval, entry cap). Both are used only when Consumers.CatalogDatasets is
	// set; a nil lister leaves the consumer unregistered.
	CatalogLister      datasetindex.Lister
	CatalogIndexConfig datasetindex.Config

	// ResourceBlobs and ResourceBucket locate managed-resource content for the
	// resources consumer, which extracts a text prefix from the uploaded file so
	// search matches what is inside it and not just its metadata. Unlike every
	// other consumer, a resource's indexable text is not in Postgres. A nil
	// reader leaves the consumer indexing metadata only.
	ResourceBlobs  resourceindex.BlobReader
	ResourceBucket string
}

// Handle owns the assembled queue and its runtime goroutines. All components
// are constructed by New and driven by Start/Stop; the read accessors expose the
// admin view, cross-kind reporter, and tools vector store the platform's read
// paths consume.
//
// The listener and retainer are nil when disabled (no DSN / retention off); the
// listener is additionally cleared to nil inside Start when the database role
// lacks LISTEN privilege, degrading to poll-only.
type Handle struct {
	store      *indexjobs.PostgresStore
	registry   *indexjobs.Registry
	worker     *indexjobs.Worker
	reaper     *indexjobs.Reaper
	reconciler *indexjobs.Reconciler
	retainer   *indexjobs.Retainer
	listener   listenerControl
	adminStore *catalogindex.AdminStore
	toolsStore *toolsindex.Store
}

// listenerControl is the subset of *indexjobs.Listener that Start/Stop drive:
// the LISTEN/NOTIFY adapter is started and stopped. Narrowed to an interface so
// the listener-failure fallback (degrade to poll-only) is testable with a fake
// that fails on Start, without leaking pq's background reconnect goroutine.
type listenerControl interface {
	Start(ctx context.Context) error
	Stop()
}

// New assembles the queue from cfg: it builds the store and registry, registers
// every enabled consumer, and constructs the worker, reaper, reconciler, and —
// when enabled — the retention sweep and LISTEN adapter.
//
// It returns nil (with no error) when no consumer registered: a worker with no
// consumers has nothing to do, so the caller wires nothing. The caller is
// responsible for the db-present and configured-embedder preconditions (#429);
// New trusts them.
func New(cfg Config) *Handle {
	store := indexjobs.NewPostgresStore(cfg.DB, indexjobs.WithLeaseDuration(cfg.LeaseDuration))
	reg := indexjobs.NewRegistry()
	h := &Handle{store: store, registry: reg}

	h.registerConsumers(cfg)
	if len(reg.Kinds()) == 0 {
		slog.Info("index jobs: skipped (no consumers registered)")
		return nil
	}

	h.worker = indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store:         store,
		Registry:      reg,
		Embedder:      cfg.Embedder,
		Concurrency:   cfg.Workers,
		LeaseDuration: cfg.LeaseDuration,
		BatchSize:     cfg.BatchSize,
	})
	h.reaper = indexjobs.NewReaper(store, 0)
	h.reconciler = indexjobs.NewReconciler(store, reg, 0)

	// Retention sweep: bound finished history so the reconciler's per-unit
	// success rows do not accumulate unbounded (#523). A non-positive window
	// disables it; the worker/reaper/reconciler still run, history just never
	// gets purged.
	if cfg.RetentionDays > 0 {
		h.retainer = indexjobs.NewRetainer(store, cfg.RetentionDays, 0)
	}

	// LISTEN/NOTIFY adapter. Best-effort: if the role lacks LISTEN privilege we
	// degrade to the worker's poll tick in Start and continue.
	if cfg.DSN != "" {
		h.listener = indexjobs.NewListener(cfg.DSN, indexjobs.NotifyChannel, h.worker)
	}

	return h
}

// registerConsumers registers every enabled consumer on the registry. The
// api-catalog consumer registers only when its catalog store is present; the
// tools consumer always registers (the framework preconditions are all it
// needs to index the in-process tool registry). Each remaining consumer
// registers only when the caller reports its store is wired. A registration
// error is a wiring bug (duplicate/mismatched kind), so it is logged and that
// consumer is skipped rather than aborting the others.
func (h *Handle) registerConsumers(cfg Config) {
	if cfg.CatalogStore != nil {
		if err := h.registry.Register(
			&catalogSource{store: cfg.CatalogStore, registry: cfg.ToolkitRegistry},
			catalogindex.NewSink(cfg.CatalogStore),
		); err != nil {
			slog.Error("index jobs: api-catalog registration failed", logKeyError, err)
		} else {
			h.adminStore = catalogindex.NewAdminStore(h.store, cfg.DB)
		}
	}

	toolsStore := toolsindex.NewStore(cfg.DB)
	toolsSrc := &toolsSource{enum: cfg.ToolEnumerator, discoveryToolName: cfg.DiscoveryToolName}
	// The Sink's gap check diffs the live tool corpus against the persisted
	// vectors, so it needs the same items the worker indexes; LoadItems (keyed
	// on the single tools source) is that enumeration.
	toolsItems := func(ctx context.Context) ([]indexjobs.Item, error) {
		return toolsSrc.LoadItems(ctx, toolsindex.SourceID)
	}
	if err := h.registry.Register(toolsSrc, toolsindex.NewSink(toolsStore, toolsItems)); err != nil {
		slog.Error("index jobs: tools registration failed", logKeyError, err)
	} else {
		h.toolsStore = toolsStore
	}

	h.registerDataConsumers(cfg)
}

// registerDataConsumers registers the DB-backed data consumers (memory,
// prompts, portal assets/collections/knowledge-pages). Each registers only
// when the caller reports its store is wired. Every one relies on the
// reconciler to discover gaps from its own table, so none needs a bootstrap
// enqueue. Split from registerConsumers, and driven through tryRegister, to
// keep each method's cyclomatic complexity within budget.
func (h *Handle) registerDataConsumers(cfg Config) {
	// Memory consumer: backfills embeddings the synchronous write path could
	// not produce (saved during an embedder outage) or that a model swap left
	// stale.
	tryRegister(cfg.Consumers.Memory, "memory", func() error {
		memStore := memoryindex.NewStore(cfg.DB)
		return h.registry.Register(
			memoryindex.NewSource(memStore),
			memoryindex.NewSink(memStore, cfg.ModelName),
		)
	})
	// Prompts consumer: embeds approved prompts for semantic discovery (#557).
	tryRegister(cfg.Consumers.Prompts, "prompts", func() error {
		promptStore := promptindex.NewStore(cfg.DB)
		return h.registry.Register(
			promptindex.NewSource(promptStore),
			promptindex.NewSink(promptStore, cfg.ModelName),
		)
	})
	// Portal asset + collection consumers: embed saved assets and curated
	// collections for relevance search (#550).
	tryRegister(cfg.Consumers.PortalAssets, "portal assets", func() error {
		assetStore := assetindex.NewStore(cfg.DB)
		return h.registry.Register(
			assetindex.NewSource(assetStore),
			assetindex.NewSink(assetStore, cfg.ModelName),
		)
	})
	tryRegister(cfg.Consumers.PortalCollections, "portal collections", func() error {
		collStore := collectionindex.NewStore(cfg.DB)
		return h.registry.Register(
			collectionindex.NewSource(collStore),
			collectionindex.NewSink(collStore, cfg.ModelName),
		)
	})
	tryRegister(cfg.Consumers.PortalKnowledgePages, "portal knowledge pages", func() error {
		return knowledgepageindex.RegisterConsumer(h.registry, cfg.DB, cfg.ModelName)
	})
	// Catalog-dataset consumer: mirrors the semantic catalog's dataset text into
	// the platform's own index so a fact applied to a description is reachable
	// from a topical query that names no entity (#1131). Its corpus is DataHub,
	// not a table, so it needs the catalog lister rather than a sub-store.
	tryRegister(cfg.Consumers.CatalogDatasets && cfg.CatalogLister != nil, "catalog datasets", func() error {
		return datasetindex.RegisterConsumer(h.registry, cfg.DB, cfg.CatalogLister,
			cfg.ModelName, cfg.CatalogIndexConfig)
	})
	// Resources consumer: embeds human-uploaded reference material, including a
	// bounded text prefix read from its blob, so an uploaded file is discoverable
	// through search by what is inside it (#1012).
	tryRegister(cfg.Consumers.Resources, "resources", func() error {
		resStore := resourceindex.NewStore(cfg.DB)
		return h.registry.Register(
			resourceindex.NewSource(resStore, cfg.ResourceBlobs, cfg.ResourceBucket),
			resourceindex.NewSink(resStore, cfg.ModelName),
		)
	})
}

// tryRegister registers one gated consumer: when enabled, it runs register and
// logs a registration error (a wiring bug: duplicate/mismatched kind) rather
// than aborting the others. Collapsing the per-consumer enabled+error branches
// into one helper keeps registerDataConsumers within the cyclomatic budget.
func tryRegister(enabled bool, label string, register func() error) {
	if !enabled {
		return
	}
	if err := register(); err != nil {
		slog.Error("index jobs: "+label+" registration failed", logKeyError, err)
	}
}

// Start launches the worker, reaper, reconciler, and (when enabled) the
// retention sweep and LISTEN adapter, then enqueues the initial tools index
// job. A LISTEN-privilege failure is non-fatal: the listener is cleared and the
// worker's poll tick takes over. It satisfies the lifecycle start signature so
// the caller can wire it directly.
func (h *Handle) Start(ctx context.Context) error {
	h.worker.Start(ctx)
	h.reaper.Start(ctx)
	h.reconciler.Start(ctx)
	if h.retainer != nil {
		h.retainer.Start(ctx)
	}
	if h.listener != nil {
		if err := h.listener.Start(ctx); err != nil {
			slog.Warn("index jobs: listener start failed; falling back to poll-only", logKeyError, err)
			h.listener = nil
		}
	}
	h.bootstrapToolsIndex(ctx)
	slog.Info("index jobs: started", "kinds", h.registry.Kinds())
	return nil
}

// Stop runs the index-jobs shutdown sequence inside the bounded shutdown
// helper. Each component's Stop signals its goroutines and blocks on their
// WaitGroup; boundedStop races the sequence against ctx.Done so shutdown always
// returns within its deadline. Abandoned work is safe: leases expire and another
// replica reclaims any uncompleted job on its next poll.
func (h *Handle) Stop(ctx context.Context) error {
	return boundedStop(ctx, "index jobs", func() {
		if h.listener != nil {
			h.listener.Stop()
		}
		if h.retainer != nil {
			h.retainer.Stop()
		}
		h.reconciler.Stop()
		h.reaper.Stop()
		h.worker.Stop()
	})
}

// bootstrapToolsIndex enqueues the initial tools index job at startup. The tool
// corpus is not a DB table the reconciler can discover on its own (it diffs an
// already-recorded expected count, which does not exist until the first embed),
// so the first index for a fresh deployment must be kicked off explicitly.
// Idempotent: the partial unique index collapses a duplicate enqueue, and the
// worker's text-hash dedup skips re-embedding unchanged tools, so running it on
// every boot is cheap.
func (h *Handle) bootstrapToolsIndex(ctx context.Context) {
	if h.toolsStore == nil || h.store == nil {
		return
	}
	if _, err := h.store.Enqueue(ctx,
		indexjobs.Key{SourceKind: toolsindex.SourceKind, SourceID: toolsindex.SourceID},
		indexjobs.TriggerWrite); err != nil {
		slog.Warn("index jobs: tools bootstrap enqueue failed", logKeyError, err)
	}
}

// Reporter returns the cross-kind index-jobs reporter the admin Indexing
// dashboard reads (per-kind counts, coverage, job list, re-index), or nil on a
// nil Handle (no queue wired). The dashboard renders a degraded empty state for
// the nil case.
func (h *Handle) Reporter() *indexjobs.Reporter {
	if h == nil || h.store == nil || h.registry == nil {
		return nil
	}
	return indexjobs.NewReporter(h.store, h.registry)
}

// AdminStore returns the api-catalog admin view of the queue (enqueue +
// read-side queries for the UI), or nil when no api-catalog consumer is wired
// or the Handle is nil. Returned as the interface so callers stay off the
// concrete type.
func (h *Handle) AdminStore() catalogindex.Store {
	if h == nil || h.adminStore == nil {
		return nil
	}
	return h.adminStore
}

// ToolsIndexStore returns the tools vector store the platform_find_tools
// semantic ranking reads, or nil when the tools consumer did not register or
// the Handle is nil.
func (h *Handle) ToolsIndexStore() *toolsindex.Store {
	if h == nil {
		return nil
	}
	return h.toolsStore
}

// Registry returns the source/sink registry the queue routes by source_kind, or
// nil on a nil Handle. It backs the Reporter and exposes the registered kinds
// (and their sources) for introspection.
func (h *Handle) Registry() *indexjobs.Registry {
	if h == nil {
		return nil
	}
	return h.registry
}

// boundedStop runs fn in a goroutine and races it against ctx.Done so a hung
// component cannot stall lifecycle shutdown past the supplied deadline. Returns
// nil on clean completion or ctx.Err() if the deadline fires first.
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
			"component", component, logKeyError, ctx.Err())
		return ctx.Err() //nolint:wrapcheck // ctx.Err() is the expected sentinel; lifecycle aggregates it
	}
}
