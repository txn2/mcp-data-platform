package searchfed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// searchableInsight embeds the noop insight store (for the InsightStore methods)
// and adds Search, so it satisfies knowledgekit.SearchableInsightStore — the
// capability the plain noop store lacks.
type searchableInsight struct {
	knowledgekit.InsightStore
}

func (searchableInsight) Search(context.Context, knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	return nil, nil
}

// stubThreadStore embeds portal.ThreadStore (a nil interface, for the store
// methods that are never called) and overrides SearchThreads so it also
// satisfies knowledge.ThreadSearcher — the capability searchfed casts for.
type stubThreadStore struct {
	portal.ThreadStore
}

func (stubThreadStore) SearchThreads(context.Context, string, string, int) ([]threads.Thread, error) {
	return nil, nil
}

// stubPageStore embeds knowledgepage.Store (nil) and adds Search — the one
// method PageSearcher needs beyond the Store method set — so it satisfies
// knowledge.PageSearcher.
type stubPageStore struct {
	knowledgepage.Store
}

func (stubPageStore) Search(context.Context, knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error) {
	return nil, nil
}

// stubPromptStore embeds prompt.Store (nil) and adds Search, so it satisfies
// knowledge.PromptSearcher (GetByID is promoted from prompt.Store).
type stubPromptStore struct {
	prompt.Store
}

func (stubPromptStore) Search(context.Context, prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return nil, nil
}

// stubAssetStore embeds portal.AssetStore (nil) and adds SearchAssets, so it
// satisfies knowledge.AssetSearcher (Get is promoted from portal.AssetStore).
type stubAssetStore struct {
	portal.AssetStore
}

func (stubAssetStore) SearchAssets(context.Context, portal.AssetSearchQuery) ([]portal.ScoredAsset, error) {
	return nil, nil
}

// stubResourceStore embeds resource.Store (nil) and adds Search, so it satisfies
// knowledge.ResourceSearcher (Get is promoted from resource.Store). A plain
// resource.Store without ranking must NOT contribute a provider.
type stubResourceStore struct {
	resource.Store
}

func (stubResourceStore) Search(context.Context, resource.SearchQuery) ([]resource.ScoredResource, error) {
	return nil, nil
}

// unrankedResourceStore is a resource.Store that does NOT implement
// knowledge.ResourceSearcher: it has Get (promoted from the embedded interface)
// but no Search, which is exactly what the capability assertion must reject.
// Embedding the interface leaves the store methods nil, which is fine — the
// provider is never built, so they are never called.
type unrankedResourceStore struct {
	resource.Store
}

// providerNames returns the Name() of every provider the handle's router holds.
func providerNames(t *testing.T, h *Handle) []string {
	t.Helper()
	require.NotNil(t, h)
	require.NotNil(t, h.Router())
	provs := h.Router().Providers()
	names := make([]string, 0, len(provs))
	for _, p := range provs {
		names = append(names, p.Name())
	}
	return names
}

func TestNew_ZeroProvidersIsNoop(t *testing.T) {
	// No store-backed source, catalog disabled, and an empty registry (no
	// endpoints, no connections) → no provider at all → nil handle, so the caller
	// registers no search tool.
	h := New(Config{ToolkitName: "default", Registry: registry.NewRegistry()})
	assert.Nil(t, h, "zero providers must yield a nil handle")

	// Every accessor is nil-safe on the nil handle.
	assert.Nil(t, h.Router())
	assert.Nil(t, h.Toolkit())
}

func TestNew_MemoryStoreProducesHandleAndToolkit(t *testing.T) {
	h := New(Config{
		ToolkitName: "default",
		MemoryStore: memory.NewNoopStore(),
		Registry:    registry.NewRegistry(),
	})
	require.NotNil(t, h)
	require.NotNil(t, h.Router())
	require.NotNil(t, h.Toolkit())
	assert.Equal(t, "default", h.Toolkit().Name())

	// A present store-backed source makes providers non-empty, so the live
	// connection lister is added even on an empty registry (federation rule).
	assert.Equal(t, []string{knowledge.SourceMemory, knowledge.SourceConnections}, providerNames(t, h))
}

func TestNew_InsightGating(t *testing.T) {
	t.Run("searchable insight store adds the insights provider", func(t *testing.T) {
		h := New(Config{
			ToolkitName:  "default",
			InsightStore: searchableInsight{InsightStore: knowledgekit.NewNoopStore()},
			Registry:     registry.NewRegistry(),
		})
		assert.Contains(t, providerNames(t, h), knowledge.SourceInsights)
	})

	t.Run("non-searchable insight store adds no insights provider", func(t *testing.T) {
		// The plain noop store implements InsightStore but not InsightSearcher, so
		// it is not a SearchableInsightStore and contributes no provider. With no
		// other source and an empty registry, that leaves zero providers.
		h := New(Config{
			ToolkitName:  "default",
			InsightStore: knowledgekit.NewNoopStore(),
			Registry:     registry.NewRegistry(),
		})
		assert.Nil(t, h, "an unsearchable insight store is the only would-be source → nil handle")
	})
}

func TestNew_CatalogGating(t *testing.T) {
	t.Run("datahub provider enabled adds the catalog provider", func(t *testing.T) {
		h := New(Config{
			ToolkitName:      "default",
			CatalogEnabled:   true,
			SemanticProvider: semantic.NewNoopProvider(),
			Registry:         registry.NewRegistry(),
		})
		assert.Contains(t, providerNames(t, h), knowledge.SourceCatalog)
	})

	t.Run("catalog disabled adds no catalog provider even with a provider", func(t *testing.T) {
		h := New(Config{
			ToolkitName:      "default",
			CatalogEnabled:   false,
			SemanticProvider: semantic.NewNoopProvider(),
			Registry:         registry.NewRegistry(),
		})
		assert.Nil(t, h, "catalog gated off and no other source → nil handle")
	})

	t.Run("catalog enabled but nil provider adds no catalog provider", func(t *testing.T) {
		h := New(Config{
			ToolkitName:      "default",
			CatalogEnabled:   true,
			SemanticProvider: nil,
			Registry:         registry.NewRegistry(),
		})
		assert.Nil(t, h, "catalog enabled with no provider and no other source → nil handle")
	})
}

func TestNew_ThreadSearcherProvider(t *testing.T) {
	h := New(Config{
		ToolkitName: "default",
		ThreadStore: stubThreadStore{},
		Registry:    registry.NewRegistry(),
	})
	assert.Contains(t, providerNames(t, h), knowledge.SourceFeedback)
}

func TestNew_PortalAndPromptStoreProviders(t *testing.T) {
	// The knowledge-page, prompt, and asset stores each contribute a provider
	// when their store implements the relevant searcher interface.
	h := New(Config{
		ToolkitName:        "default",
		KnowledgePageStore: stubPageStore{},
		PromptStore:        stubPromptStore{},
		AssetStore:         stubAssetStore{},
		Registry:           registry.NewRegistry(),
	})
	names := providerNames(t, h)
	assert.Contains(t, names, knowledge.SourceKnowledgePages)
	assert.Contains(t, names, knowledge.SourcePrompts)
	assert.Contains(t, names, knowledge.SourceAssets)
}

func TestNew_ResourceStoreProvider(t *testing.T) {
	// A ranking-capable resource store contributes the resources provider, so
	// uploaded reference material joins the search corpus (#1012).
	h := New(Config{
		ToolkitName:    "default",
		ResourceStore:  stubResourceStore{},
		ResourceBucket: "resources",
		Registry:       registry.NewRegistry(),
	})
	assert.Contains(t, providerNames(t, h), knowledge.SourceResources)

	// A store that exists but cannot rank contributes no provider, rather than an
	// always-empty one. The stub implements resource.Store without Search, which
	// is the shape the type assertion has to reject.
	h = New(Config{
		ToolkitName:   "default",
		ResourceStore: unrankedResourceStore{},
		MemoryStore:   memory.NewNoopStore(), // keeps a provider present so New returns a handle
		Registry:      registry.NewRegistry(),
	})
	assert.NotContains(t, providerNames(t, h), knowledge.SourceResources,
		"a resource store without ranking must not register the resources provider")

	// And with no store at all there is nothing to register.
	var absent resource.Store
	assert.Nil(t, New(Config{ToolkitName: "default", ResourceStore: absent, Registry: registry.NewRegistry()}),
		"no resource store and no other source must leave no handle")
}

func TestNew_ConnectionsFederationRule(t *testing.T) {
	// The connection lister is added only when another provider already exists (or
	// a connection is configured). With an empty registry and no store source,
	// connections alone never register a tool.
	empty := New(Config{ToolkitName: "default", Registry: registry.NewRegistry()})
	assert.Nil(t, empty, "no other source + no connection → no connections provider → nil handle")

	// With a store source present, the connections provider is appended.
	withStore := New(Config{
		ToolkitName: "default",
		MemoryStore: memory.NewNoopStore(),
		Registry:    registry.NewRegistry(),
	})
	assert.Contains(t, providerNames(t, withStore), knowledge.SourceConnections)
}
