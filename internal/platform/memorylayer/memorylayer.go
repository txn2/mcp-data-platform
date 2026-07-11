// Package memorylayer assembles the memory layer behind one Handle: the
// Postgres-backed memory store, the embedding provider that powers vector
// search, the memory_manage / memory_capture toolkit (with its recall-first
// checker), the memory↔enrichment middleware adapter, and the background
// staleness watcher.
//
// Construction takes explicit inputs — a *sql.DB, the shared semantic.Provider
// (nil disables the staleness watcher), and the resolved memory / embedding /
// staleness config values — so the subsystem is constructible and testable
// without a Platform. It imports pkg/memory, pkg/toolkits/memory, pkg/embedding,
// and pkg/middleware, never pkg/platform. The *sql.DB and the embedding provider
// back many other subsystems, so they stay owned by Platform: the *sql.DB is
// passed in, and the embedding provider is built here and handed back via
// EmbeddingProvider for Platform to store and pass on to the other owners.
//
// Construction is two-phase: New builds the store + embedder + toolkit + adapter
// and constructs (but does not start) the staleness watcher, and Start launches
// the watcher goroutine. The split lets Platform register the toolkit between the
// two — preserving the original order (register, then start the watcher) so a
// registration failure leaves no detached goroutine running.
//
// Toolkit registration stays a Platform/registry concern: New builds the toolkit
// and exposes it via Toolkit() for Platform to register into the shared toolkit
// registry. The only background resource this package owns for shutdown is the
// staleness watcher, stopped via Stop.
package memorylayer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// providerOllama is the memory.embedding.provider value that selects the Ollama
// embedder. Any other value (including the empty string) selects the noop
// provider with a startup WARN.
const providerOllama = "ollama"

// Config carries the resolved memory / embedding / staleness values the owner
// needs to assemble the layer. Platform translates its own config into this
// shape (and resolves the toolkit instance name) so this package stays free of
// the platform's config types and defaulting rules.
type Config struct {
	// ToolkitName is the memory toolkit instance name (the platform passes its
	// default).
	ToolkitName string
	// EmbeddingProvider selects the embedding backend: "ollama" builds the
	// Ollama provider from Ollama below; any other value selects the noop
	// provider (with the startup WARN). Reported verbatim in the enabled log.
	EmbeddingProvider string
	// Ollama configures the Ollama embedder; used only when EmbeddingProvider
	// is "ollama".
	Ollama embedding.OllamaConfig
	// StalenessEnabled gates the background staleness watcher; the watcher also
	// requires a non-nil semantic provider (see New).
	StalenessEnabled bool
	// Staleness tunes the watcher's interval and batch size; ignored when the
	// watcher is not started.
	Staleness memory.StalenessConfig
}

// Handle owns the assembled memory layer: the memory store, the embedding
// provider, the memory toolkit (and its recall-first checker), the
// memory↔enrichment adapter, and the background staleness watcher (its
// goroutine). The read accessors expose the store / embedder / toolkit / adapter
// that Platform surfaces through its MemoryStore() / EmbeddingProvider()
// accessors, registers into the shared toolkit registry, wires as the
// thread-linker + reflexive-capture target, reads in the knowledge insight
// adapter and search router, gates the indexqueue memory consumer on, and
// injects into the enrichment middleware. Stop is the shutdown seam Platform
// wires into stopBackgroundTrackers (the watcher must stop before Platform
// closes its *sql.DB).
type Handle struct {
	store            memory.Store
	embedder         embedding.Provider
	toolkit          *memorykit.Toolkit
	adapter          middleware.MemoryProvider
	stalenessWatcher *memory.StalenessWatcher
}

// New assembles the memory store, embedder, toolkit (with its recall-first
// checker), enrichment adapter, and — when StalenessEnabled and a non-nil
// semantic provider is supplied — constructs the background staleness watcher.
// It does NOT start the watcher goroutine: the caller registers the toolkit
// first, then calls Start (so a registration failure leaves no goroutine
// running). It returns (nil, nil) when db is nil: the memory layer is a no-op
// without a database, matching the platform precondition, so a memory-disabled /
// no-DB deployment still gets the nil handle (and downstream nil/noop embedder
// behavior). It returns an error only when the memory toolkit fails to build.
func New(db *sql.DB, semanticProvider semantic.Provider, cfg Config) (*Handle, error) {
	if db == nil {
		return nil, nil //nolint:nilnil // nil handle = memory layer disabled (no database)
	}

	store := memory.NewPostgresStore(db)
	embedder := buildEmbedder(cfg)

	tk, err := memorykit.New(cfg.ToolkitName, store, embedder)
	if err != nil {
		return nil, fmt.Errorf("memorylayer: creating memory toolkit: %w", err)
	}
	// Recall-first for memory_capture (#633): supersede a near-duplicate instead
	// of appending. Uses a raw hybrid similarity over the caller's own memory
	// (not the normalized search fusion, whose per-provider min-max scores are
	// not thresholdable). Nil-safe: with no real embedder the check yields no
	// match and capture simply appends.
	tk.SetRecallChecker(&recallChecker{store: store})

	h := &Handle{
		store:    store,
		embedder: embedder,
		toolkit:  tk,
		// Middleware adapter for cross-enrichment.
		adapter: &middlewareBridge{store: store},
	}

	// Construct the staleness watcher only when enabled and a semantic provider
	// is available to check entity state against. Start (below, called by the
	// caller after registration) launches its goroutine.
	if cfg.StalenessEnabled && semanticProvider != nil {
		h.stalenessWatcher = memory.NewStalenessWatcher(store, semanticProvider, cfg.Staleness)
	}
	return h, nil
}

// Start launches the background staleness-watcher goroutine (idempotent, and a
// no-op on a nil Handle or when the watcher was not constructed — staleness
// disabled or no semantic provider). The caller invokes this after registering
// the toolkit, so a failed registration never leaves a detached watcher running.
func (h *Handle) Start() {
	if h == nil || h.stalenessWatcher == nil {
		return
	}
	h.stalenessWatcher.Start(context.Background())
}

// buildEmbedder selects the embedding provider from config. Ollama when
// requested; otherwise the noop provider with a specific WARN — the operator's
// only signal that semantic ranking is off. The platform still boots so Trino,
// S3, DataHub, OAuth, audit, and every other non-embedding feature remains
// available; semantic ranking degrades to the lexical fallback and memory writes
// persist Embedding: nil (the toolkit guards see Kind() == KindNoop) (#429).
func buildEmbedder(cfg Config) embedding.Provider {
	if cfg.EmbeddingProvider == providerOllama {
		return embedding.NewOllamaProvider(cfg.Ollama)
	}
	slog.Warn("memory.embedding.provider not configured; semantic ranking disabled (set memory.embedding.provider to 'ollama' to enable)",
		"config_key", "memory.embedding.provider",
		"current_value", cfg.EmbeddingProvider)
	return embedding.NewNoopProvider(embedding.DefaultDimension)
}

// MemoryStore returns the memory store, or nil on a nil Handle (memory disabled
// or no database).
func (h *Handle) MemoryStore() memory.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// EmbeddingProvider returns the embedding provider the layer built, or nil on a
// nil Handle. Platform stores this as its own field and passes it to the other
// subsystems (portalstore, indexqueue, api-gateway, search/knowledge) that share
// the embedder.
func (h *Handle) EmbeddingProvider() embedding.Provider {
	if h == nil {
		return nil
	}
	return h.embedder
}

// Toolkit returns the memory toolkit for Platform to register into the shared
// toolkit registry and to wire as the thread-linker + reflexive-capture target,
// or nil on a nil Handle.
func (h *Handle) Toolkit() *memorykit.Toolkit {
	if h == nil {
		return nil
	}
	return h.toolkit
}

// MemoryProvider returns the memory↔enrichment adapter for Platform to inject
// into the semantic-enrichment middleware config, or nil on a nil Handle.
func (h *Handle) MemoryProvider() middleware.MemoryProvider {
	if h == nil {
		return nil
	}
	return h.adapter
}

// Stop stops the background staleness watcher and waits for its goroutine to
// exit. No-op on a nil Handle or when the watcher was never started (staleness
// disabled or no semantic provider). Platform calls this from
// stopBackgroundTrackers, before it closes the shared *sql.DB.
func (h *Handle) Stop() {
	if h == nil || h.stalenessWatcher == nil {
		return
	}
	slog.Debug("shutdown: stopping staleness watcher")
	h.stalenessWatcher.Stop()
}
