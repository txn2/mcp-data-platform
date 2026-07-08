package platform

import (
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/platform/indexqueue"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// defaultEmbedJobsTimeout is the fall-back timeout the index-jobs
// worker uses for its batched embedding calls when
// apigateway.embed_jobs.embed_timeout is unset. 5 minutes covers a
// 32-text batch on CPU-only Ollama with margin; GPU deployments can
// tighten this via config. See #445.
const defaultEmbedJobsTimeout = 5 * time.Minute

// providerOllama is the embedding.provider config value that selects a
// dedicated longer-timeout worker embedder (see workerEmbedder).
const providerOllama = "ollama"

// workerEmbedder returns the embedding.Provider the index-jobs
// worker should use. When the platform's embedder is Ollama, the
// worker gets a dedicated Provider with a longer HTTP timeout
// (apigateway.embed_jobs.embed_timeout, default 5m) so a batched
// call on CPU-only Ollama does not exhaust the 30s default that
// request-path callers (memory_recall, capture_insight, etc.) share.
// For any other provider, the shared platform Provider is returned
// unchanged.
func (p *Platform) workerEmbedder() embedding.Provider {
	if p.config.Memory.Embedding.Provider != providerOllama {
		return p.embeddingProv
	}
	timeout := p.config.APIGateway.EmbedJobs.EmbedTimeout
	if timeout <= 0 {
		timeout = defaultEmbedJobsTimeout
	}
	return embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL:           p.config.Memory.Embedding.Ollama.URL,
		Model:         p.config.Memory.Embedding.Ollama.Model,
		Timeout:       timeout,
		MaxInputBytes: p.config.Memory.Embedding.Ollama.MaxInputBytes,
	})
}

// WireAPIGatewayEmbedJobsFromDB initializes the shared index-jobs
// queue (pkg/platform/indexqueue) and wires its Start/Stop into the
// lifecycle. It translates platform config into indexqueue.Config —
// resolving worker tuning and retention, choosing the worker embedder,
// and reporting which optional consumers are enabled by the presence of
// their platform sub-store — then delegates assembly to the owner.
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

	lease, batch := p.resolveEmbedJobsTuning()
	handle := indexqueue.New(indexqueue.Config{
		DB:                p.db,
		Embedder:          p.workerEmbedder(),
		ModelName:         embedding.ModelName(p.embeddingProv),
		LeaseDuration:     lease,
		BatchSize:         batch,
		Workers:           p.config.APIGateway.EmbedJobs.Workers,
		RetentionDays:     p.resolveRetentionDays(),
		DSN:               p.config.Database.DSN,
		CatalogStore:      p.APIGatewayCatalogStore(),
		ToolkitRegistry:   p.toolkitRegistry,
		ToolEnumerator:    platformToolEnumerator{p: p},
		DiscoveryToolName: platformFindToolsName,
		Consumers: indexqueue.Consumers{
			Memory:               p.memoryStore != nil,
			Prompts:              p.promptStore != nil,
			PortalAssets:         p.portalAssetStore != nil,
			PortalCollections:    p.portalCollectionStore != nil,
			PortalKnowledgePages: p.portalKnowledgePageStore != nil,
		},
	})
	if handle == nil {
		// db + embedder are present but nothing registered. A worker with no
		// consumers has nothing to do; the owner already logged the skip.
		return
	}
	p.indexQueue = handle
	p.lifecycle.OnComponent(handle.Start, handle.Stop)
}

// indexJobsPreconditions reports whether the shared index-jobs
// framework should be wired. It requires a database and a configured
// embedding provider (#429: never stand the queue up against the noop
// provider) and must not already be wired. Which consumers actually
// register is decided by the owner package: api-catalog needs its
// catalog store, the tools consumer always registers.
func (p *Platform) indexJobsPreconditions() bool {
	switch {
	case p.indexQueue != nil:
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

// resolveRetentionDays returns the index_jobs history retention window in
// days. Unset (zero) config falls back to indexjobs.DefaultRetentionDays;
// a negative value passes through unchanged and signals "retention
// disabled" to the owner (which then never wires a retainer). An
// explicit positive value flows through verbatim.
func (p *Platform) resolveRetentionDays() int {
	days := p.config.APIGateway.EmbedJobs.RetentionDays
	if days == 0 {
		return indexjobs.DefaultRetentionDays
	}
	return days
}

// APIGatewayEmbedJobsStore returns the api-catalog admin view of the
// index-jobs queue (enqueue + read-side queries for the UI), or nil
// when no queue is wired. The admin handler reads this.
func (p *Platform) APIGatewayEmbedJobsStore() catalogindex.Store {
	return p.indexQueue.AdminStore()
}

// IndexJobsReporter returns the cross-kind index-jobs reporter the
// admin Indexing dashboard reads (per-kind counts, coverage, job list,
// re-index), or nil when no queue is wired (no database or no
// configured embedding provider). The dashboard renders a degraded
// empty state for the nil case.
func (p *Platform) IndexJobsReporter() *indexjobs.Reporter {
	return p.indexQueue.Reporter()
}
