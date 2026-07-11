// Package knowledgelayer assembles the knowledge-capture layer behind one
// Handle: the insight store (the memory-backed adapter over memory_records when
// a memory store is present, else the legacy Postgres store), the changeset
// store and DataHub writer that back apply_knowledge, and the capture_insight /
// apply_knowledge toolkit itself.
//
// Construction takes explicit inputs — a *sql.DB, the memory.Store for the
// insight adapter (nil selects the Postgres store), the shared
// embedding.Provider that powers the knowledge-page write-guard dedup probe, and
// the resolved knowledge / apply / page-guard / DataHub-connection config values
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
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// DataHubConfig carries the resolved DataHub connection values the owner needs
// to build the real client-backed apply writer. Platform resolves its own
// toolkit config (keeping its toolkitcfg coupling out of this package) and
// passes a non-nil value when the apply datahub_connection is configured, or nil
// to select the noop writer (with the startup WARN).
type DataHubConfig struct {
	URL     string
	Token   string
	Timeout time.Duration
	Debug   bool
}

// Config carries the resolved knowledge / apply / page-guard values the owner
// needs to assemble the layer. Platform translates its own config into this
// shape so this package stays free of the platform's config types.
type Config struct {
	// ToolkitName is the knowledge toolkit instance name (the platform passes its
	// default).
	ToolkitName string
	// ApplyEnabled gates the apply_knowledge dependencies: the changeset store,
	// the DataHub writer, the knowledge-page writer, and the page guards.
	ApplyEnabled bool
	// ApplyDataHubConnection is the connection name apply writes to; reported in
	// the enabled log and the noop-writer WARN.
	ApplyDataHubConnection string
	// ApplyRequireConfirmation is passed through to the toolkit's ApplyConfig.
	ApplyRequireConfirmation bool
	// PageGuards are the resolved knowledge-page write-guard thresholds (#705),
	// applied only when apply is enabled.
	PageGuards knowledgepage.PageGuards
	// DataHub is the resolved DataHub connection for the apply writer; nil selects
	// the noop writer with a startup WARN. Used only when ApplyEnabled.
	DataHub *DataHubConfig
}

// Handle owns the assembled knowledge-capture layer: the insight store, the
// changeset store and DataHub writer for apply_knowledge, and the knowledge
// toolkit. The read accessors expose the pieces Platform surfaces through its
// KnowledgeInsightStore() / KnowledgeChangesetStore() / KnowledgeDataHubWriter()
// admin accessors, reads for platform_info's pending-review count and the search
// router's insights provider, registers into the shared toolkit registry, and
// wires the prompt creator + guarded backfill onto. The layer owns no background
// goroutine, so there is no Stop/Close.
type Handle struct {
	insightStore   knowledgekit.InsightStore
	changesetStore knowledgekit.ChangesetStore
	toolkit        *knowledgekit.Toolkit
	dataHubWriter  knowledgekit.DataHubWriter
}

// New assembles the insight store (the memory-backed adapter when memStore is
// non-nil, else the legacy Postgres store), the knowledge toolkit, and — when
// cfg.ApplyEnabled — the changeset store, DataHub writer, knowledge-page writer,
// and page guards for apply_knowledge. It returns (nil, nil) when db is nil: the
// knowledge layer is a no-op without a database, matching the platform
// precondition. It returns an error only when the toolkit or the DataHub client
// fails to build.
func New(db *sql.DB, memStore memory.Store, embeddingProv embedding.Provider, cfg Config) (*Handle, error) {
	if db == nil {
		return nil, nil //nolint:nilnil // nil handle = knowledge layer disabled (no database)
	}

	// Use the memory-backed adapter when a memory store is available (the
	// migration drops knowledge_insights in favor of memory_records); otherwise
	// fall back to the legacy Postgres store.
	var store knowledgekit.InsightStore
	if memStore != nil {
		store = knowledgekit.NewMemoryInsightAdapter(memStore)
	} else {
		store = knowledgekit.NewPostgresStore(db)
	}
	return NewFromInsightStore(db, store, embeddingProv, cfg)
}

// NewFromInsightStore assembles the Handle and its toolkit from an already-built
// insight store. New delegates here after selecting the adapter-vs-postgres
// store; it is also the seam that lets callers (and tests) inject their own
// insight store implementation. When cfg.ApplyEnabled it configures the
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
		if err := h.configureApply(db, embeddingProv, cfg); err != nil {
			return nil, err
		}
	}

	slog.Info("knowledge capture enabled")
	return h, nil
}

// configureApply wires the apply_knowledge tool dependencies: the Postgres
// changeset store, the DataHub writer (real or noop), the knowledge-page writer,
// and the page guards. Called only when cfg.ApplyEnabled.
func (h *Handle) configureApply(db *sql.DB, embeddingProv embedding.Provider, cfg Config) error {
	csStore := knowledgekit.NewPostgresChangesetStore(db)
	h.changesetStore = csStore

	writer, err := buildDataHubWriter(cfg.ApplyDataHubConnection, cfg.DataHub)
	if err != nil {
		return fmt.Errorf("knowledgelayer: creating datahub writer: %w", err)
	}
	h.dataHubWriter = writer

	h.toolkit.SetApplyConfig(knowledgekit.ApplyConfig{
		Enabled:             true,
		DataHubConnection:   cfg.ApplyDataHubConnection,
		RequireConfirmation: cfg.ApplyRequireConfirmation,
	}, csStore, writer)

	// #633 Goal 3: let apply promote business_knowledge/operational_rule captures
	// to canonical knowledge pages. Built directly from the DB (the portal handle
	// is not yet available when knowledge initializes) because apply requires the
	// same DB the changeset store already uses.
	h.toolkit.SetPageWriter(knowledgepage.NewPostgresStoreSearcher(db))
	// Knowledge-page write guards (#705); the embedding provider powers the dedup
	// probe and is inactive under a noop provider.
	h.toolkit.SetPageGuards(cfg.PageGuards, embeddingProv)

	slog.Info("knowledge apply enabled",
		"datahub_connection", cfg.ApplyDataHubConnection,
		"require_confirmation", cfg.ApplyRequireConfirmation)
	return nil
}

// buildDataHubWriter creates a DataHubWriter backed by a real DataHub client
// when a connection is configured (dhCfg non-nil), or falls back to a noop
// writer with a WARN — the operator's only signal that apply cannot write to
// DataHub. connName is reported in both the WARN and the real-writer log.
func buildDataHubWriter(connName string, dhCfg *DataHubConfig) (knowledgekit.DataHubWriter, error) {
	if dhCfg == nil {
		slog.Warn("knowledge apply: datahub connection not found, using noop writer",
			"connection", connName)
		return &knowledgekit.NoopDataHubWriter{}, nil
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = dhCfg.URL
	clientCfg.Token = dhCfg.Token
	clientCfg.Timeout = dhCfg.Timeout
	clientCfg.Debug = dhCfg.Debug

	c, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client for connection %q: %w", connName, err)
	}

	slog.Info("knowledge apply: using datahub writer", "connection", connName)
	return knowledgekit.NewDataHubClientWriter(c), nil
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

// DataHubWriter returns the apply_knowledge DataHub writer, or nil on a nil
// Handle or when apply is disabled.
func (h *Handle) DataHubWriter() knowledgekit.DataHubWriter {
	if h == nil {
		return nil
	}
	return h.dataHubWriter
}
