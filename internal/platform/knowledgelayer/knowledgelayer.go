// Package knowledgelayer assembles the knowledge-capture layer behind one
// Handle: the insight store (the memory-backed adapter over memory_records when
// a memory store is present, else the legacy Postgres store), the changeset
// store and platform catalog that back apply_knowledge, and the capture_insight /
// apply_knowledge toolkit itself.
//
// Construction takes explicit inputs — a *sql.DB, the memory.Store for the
// insight adapter (nil selects the Postgres store), the shared
// embedding.Provider that powers the knowledge-page write-guard dedup probe, and
// the resolved knowledge / apply / page-guard config values and the catalog
// — so the subsystem is constructible and testable without a Platform. It
// imports pkg/toolkits/knowledge, pkg/memory, pkg/embedding, and
// pkg/portal/knowledgepage, never pkg/platform. The *sql.DB, the memory store,
// and the embedding provider back many other subsystems, so they stay owned by
// Platform and are passed in.
//
// The layer owns no background goroutine, so it needs no Stop/Close (unlike the
// memory layer's staleness watcher). Toolkit registration and the prompt-creator
// wiring stay Platform/registry concerns: New builds the toolkit and exposes it
// via Toolkit() for Platform to register into the shared toolkit registry and to
// wire the prompt creator onto (that wiring reaches back into Platform, so it
// cannot move here).
package knowledgelayer

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Config carries the resolved knowledge / apply / page-guard values the owner
// needs to assemble the layer. Platform translates its own config into this
// shape so this package stays free of the platform's config types.
type Config struct {
	// ToolkitName is the knowledge toolkit instance name (the platform passes its
	// default).
	ToolkitName string
	// ApplyEnabled gates the apply_knowledge dependencies: the changeset store,
	// the catalog, the knowledge-page writer, and the page guards.
	ApplyEnabled bool
	// ApplyRequireConfirmation is passed through to the toolkit's ApplyConfig.
	ApplyRequireConfirmation bool
	// PageGuards are the resolved knowledge-page write-guard thresholds (#705),
	// applied only when apply is enabled.
	PageGuards knowledgepage.PageGuards
	// Catalog is the platform catalog apply writes curation to (#1920); nil
	// leaves catalog changes refused while the knowledge-page and
	// agent-instruction sinks still apply. Used only when ApplyEnabled.
	Catalog catalog.Store
	// PortalBaseURL is the deployment's public portal address. A promotion that
	// writes a knowledge page hands back where that page is read (#1696); empty
	// leaves the address off, since a deployment that declared no public
	// address cannot know it.
	PortalBaseURL string
}

// Handle owns the assembled knowledge-capture layer: the insight store, the
// changeset store and catalog for apply_knowledge, and the knowledge toolkit.
// The read accessors expose the pieces Platform surfaces through its
// KnowledgeInsightStore() / KnowledgeChangesetStore() / KnowledgeCatalog()
// admin accessors, reads for platform_info's pending-review count and the search
// router's insights provider, registers into the shared toolkit registry, and
// wires the prompt creator + guarded backfill onto. The layer owns no background
// goroutine, so there is no Stop/Close.
type Handle struct {
	insightStore   knowledgekit.InsightStore
	changesetStore knowledgekit.ChangesetStore
	toolkit        *knowledgekit.Toolkit
	catalog        catalog.Store
	// pageProducer is the write-path index-job producer the apply path's page
	// writer was built with, exposed for the index-jobs queue to bind once it
	// exists. Nil when apply is disabled (no page writer to notify for).
	pageProducer *indexjobs.Producer
}

// New assembles the insight store, the knowledge toolkit, and — when
// cfg.ApplyEnabled — the changeset store, catalog, knowledge-page writer,
// and page guards for apply_knowledge. It returns (nil, nil) when db is nil: the
// knowledge layer is a no-op without a database, matching the platform
// precondition. It returns an error only when the toolkit fails to build.
func New(db *sql.DB, memStore memory.Store, embeddingProv embedding.Provider, cfg Config) (*Handle, error) {
	if db == nil {
		return nil, nil //nolint:nilnil // nil handle = knowledge layer disabled (no database)
	}

	// An insight is a knowledge-dimension memory record: migration 000031 moved
	// capture into memory_records and dropped knowledge_insights. That table is
	// created by a migration, so it is there whenever the database is, and a
	// caller who passed no memory store gets one built here rather than a store
	// for a table that no longer exists (#1517). Turning the memory toolkit off
	// stops registering memory tools; it does not take the row store away from
	// knowledge.
	if memStore == nil {
		memStore = memory.NewPostgresStore(db)
	}
	return NewFromInsightStore(db, knowledgekit.NewMemoryInsightAdapter(memStore), embeddingProv, cfg)
}

// NewFromInsightStore assembles the Handle and its toolkit from an already-built
// insight store. New delegates here after wrapping the memory store; it is also
// the seam that lets callers (and tests) inject their own insight store
// implementation. When cfg.ApplyEnabled it configures the
// apply_knowledge dependencies from db / embeddingProv / cfg — which requires a
// non-nil db (the changeset and page stores are Postgres-backed), so enabling
// apply with a nil db is a construction error rather than a query-time failure.
func NewFromInsightStore(db *sql.DB, store knowledgekit.InsightStore, embeddingProv embedding.Provider, cfg Config) (*Handle, error) {
	tk, err := knowledgekit.New(cfg.ToolkitName, store)
	if err != nil {
		return nil, fmt.Errorf("knowledgelayer: creating knowledge toolkit: %w", err)
	}

	h := &Handle{insightStore: store, toolkit: tk}

	if cfg.ApplyEnabled {
		if db == nil {
			return nil, errors.New("knowledgelayer: apply enabled requires a non-nil database")
		}
		h.configureApply(db, embeddingProv, cfg)
	}

	slog.Info("knowledge capture enabled")
	return h, nil
}

// configureApply wires the apply_knowledge tool dependencies: the Postgres
// changeset store, the catalog, the knowledge-page writer, and the page guards.
// Called only when cfg.ApplyEnabled.
func (h *Handle) configureApply(db *sql.DB, embeddingProv embedding.Provider, cfg Config) {
	csStore := knowledgekit.NewPostgresChangesetStore(db)
	h.changesetStore = csStore
	h.catalog = cfg.Catalog

	h.toolkit.SetApplyConfig(knowledgekit.ApplyConfig{
		Enabled:             true,
		RequireConfirmation: cfg.ApplyRequireConfirmation,
	}, csStore, cfg.Catalog)

	// #633 Goal 3: let apply promote business_knowledge/operational_rule captures
	// to canonical knowledge pages. Built directly from the DB (the portal handle
	// is not yet available when knowledge initializes) because apply requires the
	// same DB the changeset store already uses.
	// The apply path writes pages through its own store instance, so it carries
	// its own producer: a promoted page enqueues its index job the same way a
	// page saved through the portal does (#1256). Unbound until the index-jobs
	// queue binds it, and a no-op if no queue is ever wired.
	h.pageProducer = indexjobs.NewProducer(knowledgepageindex.SourceKind)
	h.toolkit.SetPageWriter(knowledgepage.NewPostgresStoreSearcher(db,
		indexjobs.WithProducer(h.pageProducer)))
	// Knowledge-page write guards (#705); the embedding provider powers the dedup
	// probe and is inactive under a noop provider.
	h.toolkit.SetPageGuards(cfg.PageGuards, embeddingProv)
	// Where a promoted page is read, so the promotion can hand the address back
	// rather than leaving the caller to compose one (#1696).
	h.toolkit.SetPortalBaseURL(cfg.PortalBaseURL)

	slog.Info("knowledge apply enabled",
		"catalog", cfg.Catalog != nil,
		"require_confirmation", cfg.ApplyRequireConfirmation)
}

// InsightStore returns the insight store, or nil on a nil Handle (knowledge
// disabled or no database).
func (h *Handle) InsightStore() knowledgekit.InsightStore {
	if h == nil {
		return nil
	}
	return h.insightStore
}

// ChangesetStore returns the apply_knowledge changeset store, or nil on a nil
// Handle or when apply is disabled.
func (h *Handle) ChangesetStore() knowledgekit.ChangesetStore {
	if h == nil {
		return nil
	}
	return h.changesetStore
}

// Toolkit returns the knowledge toolkit for Platform to register into the shared
// toolkit registry, wire the prompt creator onto, and run the guarded backfill
// against, or nil on a nil Handle.
func (h *Handle) Toolkit() *knowledgekit.Toolkit {
	if h == nil {
		return nil
	}
	return h.toolkit
}

// Catalog returns the catalog apply_knowledge writes to, or nil on a nil
// Handle, when apply is disabled, or when there is no catalog.
func (h *Handle) Catalog() catalog.Store {
	if h == nil {
		return nil
	}
	return h.catalog
}

// PageIndexProducer returns the write-path index-job producer behind the apply
// path's knowledge-page writer, for the index-jobs queue to bind. Nil on a nil
// Handle or when apply is disabled.
func (h *Handle) PageIndexProducer() *indexjobs.Producer {
	if h == nil {
		return nil
	}
	return h.pageProducer
}
