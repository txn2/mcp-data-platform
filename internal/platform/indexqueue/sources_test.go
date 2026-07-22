package indexqueue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// twoOpSpec is a minimal two-operation OpenAPI document the catalogSource tests
// parse into items.
const twoOpSpec = `openapi: 3.0.0
info:
  title: t
  version: "1"
paths:
  /a:
    get:
      operationId: alpha
      responses:
        "200": {description: ok}
  /b:
    get:
      operationId: bravo
      responses:
        "200": {description: ok}
`

// seedCatalogStore inserts a catalog and a single spec used by the catalogSource
// tests below.
func seedCatalogStore(t *testing.T, store apigatewaycatalog.Store, catalogID, specName, content string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, apigatewaycatalog.Catalog{
		ID: catalogID, Name: catalogID, Version: "v1",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, catalogID, apigatewaycatalog.SpecEntry{
		SpecName: specName, Content: content, SourceKind: "inline",
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// TestCatalogSource_Kind pins the source kind the Source advertises; it must
// match catalogindex.SourceKind and the Sink it is paired with in the registry.
func TestCatalogSource_Kind(t *testing.T) {
	t.Parallel()
	s := &catalogSource{}
	if s.Kind() != catalogindex.SourceKind {
		t.Errorf("Kind() = %q; want %q", s.Kind(), catalogindex.SourceKind)
	}
}

// TestCatalogSource_LoadItems proves the Source decodes the source_id, fetches
// the spec content, and parses it into one item per operation with the
// synthesized operation ids as item ids.
func TestCatalogSource_LoadItems(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", twoOpSpec)

	s := &catalogSource{store: store}
	items, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("cat1", "spec1"))
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2", len(items))
	}
	got := map[string]bool{items[0].ItemID: true, items[1].ItemID: true}
	if !got["alpha"] || !got["bravo"] {
		t.Errorf("item ids = %v; want alpha+bravo", got)
	}
	for _, it := range items {
		if it.Text == "" {
			t.Errorf("item %q has empty text", it.ItemID)
		}
	}
}

// TestCatalogSource_LoadItems_MalformedSourceID covers the decode guard: a
// source_id without the delimiter cannot be attributed to a catalog/spec, so
// LoadItems errors rather than querying with empty keys.
func TestCatalogSource_LoadItems_MalformedSourceID(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: apigatewaycatalog.NewMemoryStore()}
	if _, err := s.LoadItems(context.Background(), "no-delimiter"); err == nil {
		t.Fatal("expected error on malformed source_id")
	}
}

// TestCatalogSource_LoadItems_MissingSpec proves a vanished spec surfaces as an
// error wrapping indexjobs.ErrSourceGone, so the worker resolves the unit
// (the spec was deleted between enqueue and claim) instead of recording an
// open failure nothing can ever supersede (#998).
func TestCatalogSource_LoadItems_MissingSpec(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: apigatewaycatalog.NewMemoryStore()}
	_, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("missing", "missing"))
	if err == nil {
		t.Fatal("expected error for missing spec")
	}
	if !errors.Is(err, indexjobs.ErrSourceGone) {
		t.Errorf("missing spec error = %v; must wrap indexjobs.ErrSourceGone", err)
	}
	if !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("missing spec error = %v; must name the missing spec", err)
	}
}

// TestCatalogSource_LoadItems_StoreErrorIsNotGone pins the boundary of the
// gone signal: an unreadable store is NOT a deleted source, so the error must
// not wrap ErrSourceGone (the worker would silently resolve a unit that still
// exists).
func TestCatalogSource_LoadItems_StoreErrorIsNotGone(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: &errStore{err: errors.New("db down")}}
	_, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("c", "s"))
	if err == nil {
		t.Fatal("expected store error to surface")
	}
	if errors.Is(err, indexjobs.ErrSourceGone) {
		t.Errorf("store error = %v; must NOT wrap ErrSourceGone", err)
	}
}

// TestCatalogSource_LoadItems_ParseError proves malformed spec content surfaces
// as a wrapped build-items error.
func TestCatalogSource_LoadItems_ParseError(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", "this is not yaml at all: [")

	s := &catalogSource{store: store}
	if _, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("cat1", "spec1")); err == nil {
		t.Fatal("expected parse error")
	}
}

// TestCatalogSource_LoadItems_StoreError proves a store read failure is surfaced
// (not swallowed). errStore returns from every method.
func TestCatalogSource_LoadItems_StoreError(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: &errStore{err: errors.New("db down")}}
	if _, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("c", "s")); err == nil {
		t.Fatal("expected store error to surface")
	}
}

// TestCatalogSource_OnSucceeded_NilRegistryIsNoOp proves OnSucceeded returns
// silently when no toolkit registry is wired (the platform may wire the queue
// before any toolkits register).
func TestCatalogSource_OnSucceeded_NilRegistryIsNoOp(_ *testing.T) {
	s := &catalogSource{registry: nil}
	s.OnSucceeded(catalogindex.EncodeSourceID("cat1", "spec1"))
}

// TestCatalogSource_OnSucceeded_MalformedSourceIDIsNoOp covers the decode guard
// on the reload path: a source_id without the delimiter cannot be attributed to
// a catalog, so OnSucceeded returns without touching the registry.
func TestCatalogSource_OnSucceeded_MalformedSourceIDIsNoOp(_ *testing.T) {
	s := &catalogSource{registry: registry.NewRegistry()}
	s.OnSucceeded("no-delimiter")
}

// TestCatalogSource_OnSucceeded_WithRegistry covers the reload path: with a
// registered api-gateway toolkit, OnSucceeded walks the registry and invokes
// the toolkit's connection reload (a no-op with zero connections, but the loop
// and type assertion run).
func TestCatalogSource_OnSucceeded_WithRegistry(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	if err := reg.Register(apigatewaykit.New("test")); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &catalogSource{registry: reg}
	s.OnSucceeded(catalogindex.EncodeSourceID("cat", "spec")) // must not panic; reloads the catalog
}

// fakeEnumerator is a ToolEnumerator returning a fixed corpus (or an error) for
// the toolsSource tests, so the source is exercised without an MCP server.
type fakeEnumerator struct {
	tools []*mcp.Tool
	err   error
}

func (f fakeEnumerator) EnumerateGlobalTools(context.Context) ([]*mcp.Tool, error) {
	return f.tools, f.err
}

// TestToolsSource_LoadItems proves the Source enumerates the corpus via the
// injected enumerator, builds embed text, and excludes the discovery tool.
func TestToolsSource_LoadItems(t *testing.T) {
	t.Parallel()
	enum := fakeEnumerator{tools: []*mcp.Tool{
		{Name: "alpha", Description: "do the alpha thing"},
		{Name: "platform_find_tools", Description: "discovery"},
	}}
	s := &toolsSource{enum: enum, discoveryToolName: "platform_find_tools"}

	if s.Kind() != toolsindex.SourceKind {
		t.Errorf("Kind() = %q; want %q", s.Kind(), toolsindex.SourceKind)
	}

	items, err := s.LoadItems(context.Background(), toolsindex.SourceID)
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1 (discovery tool excluded): %+v", len(items), items)
	}
	if items[0].ItemID != "alpha" {
		t.Errorf("item id = %q; want alpha", items[0].ItemID)
	}
	if items[0].Text == "" {
		t.Error("embed text should not be empty")
	}

	s.OnSucceeded("x") // no-op; must not panic
}

// TestToolsSource_LoadItems_EnumeratorError proves an enumeration failure is
// wrapped and surfaced (the worker retries on it).
func TestToolsSource_LoadItems_EnumeratorError(t *testing.T) {
	t.Parallel()
	s := &toolsSource{enum: fakeEnumerator{err: errors.New("boom")}, discoveryToolName: "platform_find_tools"}
	if _, err := s.LoadItems(context.Background(), toolsindex.SourceID); err == nil {
		t.Fatal("expected enumerator error to surface")
	}
}

// TestToolsSource_LoadItems_NilEnumerator proves the guard for a source built
// without an enumerator: LoadItems errors rather than panicking.
func TestToolsSource_LoadItems_NilEnumerator(t *testing.T) {
	t.Parallel()
	s := &toolsSource{discoveryToolName: "platform_find_tools"}
	if _, err := s.LoadItems(context.Background(), toolsindex.SourceID); err == nil {
		t.Fatal("expected error when no enumerator is injected")
	}
}

func TestToolParamSummary(t *testing.T) {
	t.Parallel()
	if got := toolParamSummary(nil); got != "" {
		t.Errorf("nil schema = %q; want empty", got)
	}
	schema := map[string]any{
		"properties": map[string]any{
			"query": map[string]any{"description": "the query"},
			"limit": map[string]any{},
		},
	}
	got := toolParamSummary(schema)
	want := "limit, query (the query)" // sorted by name
	if got != want {
		t.Errorf("summary = %q; want %q", got, want)
	}
	// Unmarshalable value (chan) -> marshal error -> empty.
	if s := toolParamSummary(make(chan int)); s != "" {
		t.Errorf("unmarshalable schema = %q; want empty", s)
	}
	// Non-object schema -> unmarshal into the properties struct fails -> empty.
	if s := toolParamSummary(42); s != "" {
		t.Errorf("non-object schema = %q; want empty", s)
	}
}

func TestToolEmbedText(t *testing.T) {
	t.Parallel()
	if got := toolEmbedText(&mcp.Tool{Name: "x"}); got != "x" {
		t.Errorf("name-only = %q; want x", got)
	}
	got := toolEmbedText(&mcp.Tool{Name: "trino_query", Description: "run SQL"})
	if got != "trino_query\nrun SQL" {
		t.Errorf("name+desc = %q", got)
	}
}

// errStore is a catalog.Store stub that returns the supplied error from every
// method. Used to exercise error-handling branches without standing up the
// postgres or memory store machinery.
type errStore struct{ err error }

func (s *errStore) CreateCatalog(_ context.Context, _ apigatewaycatalog.Catalog) error { return s.err }
func (s *errStore) GetCatalog(_ context.Context, _ string) (*apigatewaycatalog.Catalog, error) {
	return nil, s.err
}

func (s *errStore) ListCatalogs(_ context.Context) ([]apigatewaycatalog.Catalog, error) {
	return nil, s.err
}

func (s *errStore) UpdateCatalog(_ context.Context, _ string, _ apigatewaycatalog.Update) error {
	return s.err
}
func (s *errStore) DeleteCatalog(_ context.Context, _ string) error { return s.err }
func (s *errStore) UpsertSpec(_ context.Context, _ string, _ apigatewaycatalog.SpecEntry) error {
	return s.err
}

func (s *errStore) GetSpec(_ context.Context, _, _ string) (*apigatewaycatalog.SpecEntry, error) {
	return nil, s.err
}

func (s *errStore) ListSpecs(_ context.Context, _ string) ([]apigatewaycatalog.SpecEntry, error) {
	return nil, s.err
}
func (s *errStore) DeleteSpec(_ context.Context, _, _ string) error { return s.err }
func (s *errStore) ReferencingConnections(_ context.Context, _ string) ([]apigatewaycatalog.ConnectionRef, error) {
	return nil, s.err
}

func (s *errStore) UpsertOperationEmbeddings(_ context.Context, _, _ string, _ []apigatewaycatalog.OperationEmbedding) error {
	return s.err
}

func (s *errStore) UpsertOperationEmbeddingsBatch(_ context.Context, _, _ string, _ []apigatewaycatalog.OperationEmbedding) error {
	return s.err
}

func (s *errStore) ListOperationEmbeddings(_ context.Context, _, _ string) ([]apigatewaycatalog.OperationEmbedding, error) {
	return nil, s.err
}

func (s *errStore) DeleteOperationEmbeddings(_ context.Context, _, _ string) error {
	return s.err
}

func (s *errStore) SetOperationCount(_ context.Context, _, _ string, _ int) error {
	return s.err
}

func (s *errStore) ListEmbeddingGaps(_ context.Context) ([]apigatewaycatalog.SpecKey, error) {
	return nil, s.err
}

func (s *errStore) EmbeddingCoverage(_ context.Context) (indexed, expected int, err error) {
	return 0, 0, s.err
}
