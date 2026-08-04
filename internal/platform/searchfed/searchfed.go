// Package searchfed assembles the universal, topology-free search federation
// behind one Handle: the knowledge.Router that federates every searchable
// source a caller can access, and the search toolkit that exposes it as the one
// discovery entry point (#645).
//
// Unlike the store-owning layers, this layer is a reader/aggregator: it owns no
// store. Its constructor takes explicit inputs — the searchable source
// handles/stores (memory store, knowledge insight store, portal
// knowledge-page/asset/thread stores, prompt store, managed resource store), the semantic.Provider plus
// a catalog-enabled flag, the *registry.Registry, the shared embedding.Provider,
// the resolved search-timeout values, the persona connection boundary discovery
// enforces, and the toolkit instance name — performs
// the per-source provider selection (each source gated on existing and
// implementing the relevant searcher interface), builds the DataHub
// LineageExpander and the knowledge.Router, and constructs the search toolkit.
// It imports pkg/knowledge, pkg/knowledge/federation, pkg/toolkits/search,
// pkg/toolkits/knowledge, pkg/semantic, pkg/memory, pkg/portal,
// pkg/portal/knowledgepage, pkg/prompt, pkg/registry, and pkg/embedding — never
// pkg/platform. Every federated store is owned by another subsystem and is
// passed in; the registry, embedding provider, and semantic provider are shared
// foundations passed in the same way.
//
// The provider count governs registration: with zero providers New returns a
// nil Handle, so the caller registers no search toolkit and a store-less
// deployment with no catalog, endpoints, or connections gets no search tool. The
// layer owns no background goroutine, so it needs no Stop/Close. Toolkit
// registration stays a caller/registry concern: New builds the toolkit and
// exposes it via Toolkit() for the caller to register into the shared toolkit
// registry; consumers read the router through Router().
package searchfed

import (
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/knowledge/federation"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	searchkit "github.com/txn2/mcp-data-platform/pkg/toolkits/search"
)

// Config carries the resolved inputs the owner needs to assemble the search
// federation. The caller translates its own config and source handles into this
// shape so this package stays free of the platform's config types.
type Config struct {
	// ToolkitName is the search toolkit instance name (the caller passes its
	// default).
	ToolkitName string

	// ProviderTimeout / EmbedTimeout are passed through to the router; 0 keeps
	// the router's built-in defaults.
	ProviderTimeout time.Duration
	EmbedTimeout    time.Duration

	// CatalogEnabled gates the DataHub-backed catalog, context-documents, and
	// lineage-expander sources: they are added only when the configured semantic
	// provider is DataHub (a noop fallback would add an always-empty provider).
	// Combined with a non-nil SemanticProvider.
	CatalogEnabled bool
	// SemanticProvider backs the catalog / context-documents providers and the
	// lineage expander; nil when no real catalog is configured, leaving a plain
	// entity lookup.
	SemanticProvider semantic.Provider

	// CatalogIndex ranks the platform's own index of catalog dataset text on the
	// catalog provider's text path (#1131), so a fact applied to a description is
	// reachable from a topical query naming no entity. Nil (no database, or the
	// index disabled) leaves the catalog ranked by DataHub's keyword search
	// alone.
	CatalogIndex knowledge.CatalogIndexSearcher

	// Source stores federated into the corpus. Each is gated on being non-nil and
	// implementing the relevant searcher interface, so a source that is absent (or
	// whose implementation is not searchable) contributes no provider.
	MemoryStore        memory.Store
	InsightStore       knowledgekit.InsightStore
	KnowledgePageStore knowledgepage.Store
	AssetStore         portal.AssetStore
	ThreadStore        portal.ThreadStore
	PromptStore        prompt.Store
	ResourceStore      resource.Store

	// ResourceBlobs and ResourceBucket let the resources provider return a text
	// resource's contents inline from `fetch`. Nil leaves fetch returning
	// metadata plus the canonical URI; it does not affect searchability.
	ResourceBlobs  knowledge.ResourceContentReader
	ResourceBucket string

	// ResourceReads audits resources dereferenced through fetch (#1014). Nil
	// when audit is disabled, which leaves fetch serving the same content
	// unrecorded.
	ResourceReads resource.ReadRecorder

	// PersonasForRoles resolves a caller's roles to every persona they BELONG TO.
	// The resources provider scopes persona-visible material on that membership
	// set rather than on the single resolved persona, which falls back to the
	// configured default persona for a caller whose roles match none. Nil leaves
	// the caller carrying only the resolved persona.
	PersonasForRoles func(roles []string) []string

	// ConnectionScope is the persona connection boundary the topology sources
	// (catalog, connections, endpoints) apply to discovery, so a caller never sees
	// material belonging to a connection their persona could not reach (#1108).
	// Nil leaves those sources unfiltered, which is what a deployment with no
	// persona registry gets.
	ConnectionScope knowledge.ConnectionScope

	// Registry backs the registry-federated sources (API endpoints +
	// connections). Required: New walks it for endpoint searchers and connections
	// even when no store-backed source is present.
	Registry *registry.Registry
	// Embedding powers the router's intent embedding.
	Embedding embedding.Provider
}

// Handle owns the assembled search federation: the knowledge.Router and the
// search toolkit that exposes it. Router() backs both the search tool and any
// REST consumer; Toolkit() is registered by the caller into the shared toolkit
// registry. Both are nil on a nil Handle, which New returns when no source is
// searchable — so a store-less deployment registers no search tool. The layer
// owns no background goroutine, so there is no Stop/Close.
type Handle struct {
	router  *knowledge.Router
	toolkit *searchkit.Toolkit
}

// New assembles the router over every searchable source in cfg and builds the
// search toolkit. It returns nil when no provider is available: with zero
// providers there is no router and no toolkit to register, preserving the
// store-less no-op. Otherwise it wires the two search timeouts and logs "search
// enabled" with the provider count.
func New(cfg Config) *Handle {
	providers := storeProviders(cfg)
	providers = appendFederationProviders(cfg, providers)

	if len(providers) == 0 {
		return nil
	}

	// The lineage expander widens the entity path along DataHub lineage (the old
	// memory_recall graph strategy) once per search, shared across every
	// entity-keyed provider; nil when no real catalog is configured, leaving a
	// plain entity lookup.
	var lineage knowledge.LineageExpander
	if cfg.CatalogEnabled && cfg.SemanticProvider != nil {
		lineage = knowledge.NewLineageExpander(cfg.SemanticProvider)
	}

	router := knowledge.NewRouter(cfg.Embedding, lineage, providers...)
	router.SetProviderTimeout(cfg.ProviderTimeout) // 0 keeps the default
	router.SetEmbedTimeout(cfg.EmbedTimeout)       // 0 keeps the default
	router.SetConnectionScope(cfg.ConnectionScope) // nil leaves discovery unfiltered

	tk := searchkit.New(cfg.ToolkitName, router)
	tk.SetPersonasForRoles(cfg.PersonasForRoles)
	h := &Handle{router: router, toolkit: tk}
	slog.Info("search enabled", "providers", len(providers))
	return h
}

// storeProviders builds the store-backed search providers (memory, insights,
// technical catalog, context documents, knowledge pages, prompts, assets,
// managed resources, and feedback threads), each added only when its backing source exists and
// implements the relevant searcher interface, so a no-database deployment adds
// none of them.
func storeProviders(cfg Config) []knowledge.Provider {
	var providers []knowledge.Provider

	if cfg.MemoryStore != nil {
		// Lineage expansion of the entity path is the router's job (it runs once
		// for every entity-keyed provider), so the memory provider takes the URN
		// set as given.
		providers = append(providers, knowledge.NewMemoryProvider(cfg.MemoryStore))
	}
	// Insights are searchable only through the memory-backed adapter; the legacy
	// SQL store and the noop store do not implement InsightSearcher (and so are
	// not SearchableInsightStores).
	if s, ok := cfg.InsightStore.(knowledgekit.SearchableInsightStore); ok {
		providers = append(providers, knowledge.NewInsightsProvider(s))
	}
	// The technical catalog is a knowledge sink only when a real DataHub semantic
	// provider is configured (the noop fallback would add an always-empty
	// provider).
	if cfg.CatalogEnabled && cfg.SemanticProvider != nil {
		catalog := knowledge.NewCatalogProvider(cfg.SemanticProvider)
		// The platform's own index of dataset text, when one is wired, leads the
		// catalog text path; DataHub's keyword search stays behind it.
		catalog.SetIndexSearcher(cfg.CatalogIndex)
		providers = append(providers, catalog)
		// Context documents: a distinct search source (#692), present only when the
		// real catalog exposes document search.
		if ds, ok := semantic.DocumentSearcherFrom(cfg.SemanticProvider); ok {
			providers = append(providers, knowledge.NewContextDocumentsProvider(ds))
		}
		// Governance vocabulary: glossary terms, tags, and domains as entities in
		// their own right (#1160), present only when the real catalog exposes the
		// vocabulary reads. It is a sibling of the catalog rather than an arm of it,
		// so a definition is never crowded out of the display budget by a broad
		// dataset match.
		if gr, ok := semantic.GovernanceReaderFrom(cfg.SemanticProvider); ok {
			providers = append(providers, knowledge.NewGovernanceProvider(gr))
		}
	}
	return appendPortalStoreProviders(cfg, providers)
}

// appendPortalStoreProviders adds the providers whose backing store is a
// portal-owned corpus (knowledge pages, prompts, assets, managed resources,
// feedback threads). Each is gated the same way: the store must exist and
// implement the relevant searcher capability, so a store that cannot rank
// contributes nothing rather than an always-empty provider. Split from
// storeProviders to keep each within the cyclomatic budget.
func appendPortalStoreProviders(cfg Config, providers []knowledge.Provider) []knowledge.Provider {
	// Canonical knowledge pages (the internal-knowledge home for business
	// ontology) are shared and searchable over their full content.
	if s, ok := cfg.KnowledgePageStore.(knowledge.PageSearcher); ok {
		providers = append(providers, knowledge.NewKnowledgePagesProvider(s))
	}
	// Prompts are searchable and fetchable through the postgres prompt store
	// (search + read-by-id, the two halves of search/fetch).
	if s, ok := cfg.PromptStore.(knowledge.PromptSearcher); ok {
		providers = append(providers, knowledge.NewPromptsProvider(s))
	}
	// Assets are searchable and fetchable only through the postgres asset store.
	if s, ok := cfg.AssetStore.(knowledge.AssetSearcher); ok {
		providers = append(providers, knowledge.NewAssetsProvider(s))
	}
	// Managed resources (human-uploaded reference material) join the corpus
	// through the postgres resource store, which is the only implementation that
	// can rank (#1012). Uploading one is publishing into a void unless search can
	// find it.
	if s, ok := cfg.ResourceStore.(knowledge.ResourceSearcher); ok {
		rp := knowledge.NewResourcesProvider(s, cfg.ResourceBlobs, cfg.ResourceBucket)
		rp.SetReadRecorder(cfg.ResourceReads)
		providers = append(providers, rp)
	}
	// Feedback threads complete the search corpus (#686): a caller's own feedback
	// becomes discoverable knowledge. Lexical and per-user (threads carry no
	// embedding).
	if s, ok := cfg.ThreadStore.(knowledge.ThreadSearcher); ok {
		providers = append(providers, knowledge.NewThreadsProvider(s))
	}
	return providers
}

// appendFederationProviders adds the registry-federated search sources (API
// endpoints and connections) to the store-backed providers. Both are in the
// default corpus (#645): an agent should discover a relevant endpoint or
// connection from one query without first knowing a gateway exists.
func appendFederationProviders(cfg Config, providers []knowledge.Provider) []knowledge.Provider {
	// API endpoints aggregate the per-connection semantic ranking of every API
	// gateway toolkit.
	if searchers := federation.EndpointSearchers(cfg.Registry); len(searchers) > 0 {
		providers = append(providers, knowledge.NewEndpointsProvider(searchers...))
	}
	// Connections are the same set list_connections enumerates, surfaced by
	// relevance. Added whenever search will exist at all — when any other source
	// is present, or when at least one connection exists at startup — but never on
	// its own on a bare deployment with no other source and no connection, so such
	// a deployment still registers no search tool. The lister stays live, so once
	// registered, connections added later through the admin API are searchable.
	connLister := federation.NewConnectionLister(cfg.Registry)
	if len(providers) > 0 || len(connLister.Connections()) > 0 {
		providers = append(providers, knowledge.NewConnectionsProvider(connLister))
	}
	return providers
}

// Router returns the unified search federation router, or nil on a nil Handle
// (no searchable source configured). Consumers read it to serve the same
// grouped, scope-enforced results as the search tool.
func (h *Handle) Router() *knowledge.Router {
	if h == nil {
		return nil
	}
	return h.router
}

// Toolkit returns the search toolkit for the caller to register into the shared
// toolkit registry, or nil on a nil Handle (no searchable source → no search
// tool).
func (h *Handle) Toolkit() *searchkit.Toolkit {
	if h == nil {
		return nil
	}
	return h.toolkit
}
