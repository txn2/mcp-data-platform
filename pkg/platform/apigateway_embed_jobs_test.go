package platform

import (
	"context"
	"errors"
	"testing"

	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// twoOpSpec is a minimal two-operation OpenAPI document the
// catalogSource tests parse into items.
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

// seedCatalogStore inserts a catalog and a single spec used by the
// catalogSource tests below.
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

// TestCatalogSource_Kind pins the source kind the Source advertises;
// it must match catalogindex.SourceKind and the Sink it is paired
// with in the registry.
func TestCatalogSource_Kind(t *testing.T) {
	t.Parallel()
	s := &catalogSource{}
	if s.Kind() != catalogindex.SourceKind {
		t.Errorf("Kind() = %q; want %q", s.Kind(), catalogindex.SourceKind)
	}
}

// TestCatalogSource_LoadItems proves the Source decodes the
// source_id, fetches the spec content, and parses it into one item
// per operation with the synthesized operation ids as item ids.
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

// TestCatalogSource_LoadItems_MalformedSourceID covers the decode
// guard: a source_id without the delimiter cannot be attributed to a
// catalog/spec, so LoadItems errors rather than querying with empty
// keys.
func TestCatalogSource_LoadItems_MalformedSourceID(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: apigatewaycatalog.NewMemoryStore()}
	if _, err := s.LoadItems(context.Background(), "no-delimiter"); err == nil {
		t.Fatal("expected error on malformed source_id")
	}
}

// TestCatalogSource_LoadItems_MissingSpec proves a vanished spec
// surfaces as a wrapped error; the worker treats it as terminal (the
// spec was deleted between enqueue and claim).
func TestCatalogSource_LoadItems_MissingSpec(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: apigatewaycatalog.NewMemoryStore()}
	if _, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("missing", "missing")); err == nil {
		t.Fatal("expected error for missing spec")
	}
}

// TestCatalogSource_LoadItems_ParseError proves malformed spec
// content surfaces as a wrapped build-items error.
func TestCatalogSource_LoadItems_ParseError(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", "this is not yaml at all: [")

	s := &catalogSource{store: store}
	if _, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("cat1", "spec1")); err == nil {
		t.Fatal("expected parse error")
	}
}

// TestCatalogSource_LoadItems_StoreError proves a store read failure
// is surfaced (not swallowed). errStore returns from every method.
func TestCatalogSource_LoadItems_StoreError(t *testing.T) {
	t.Parallel()
	s := &catalogSource{store: &errStore{err: errors.New("db down")}}
	if _, err := s.LoadItems(context.Background(), catalogindex.EncodeSourceID("c", "s")); err == nil {
		t.Fatal("expected store error to surface")
	}
}

// TestCatalogSource_OnSucceeded_NilRegistryIsNoOp proves OnSucceeded
// returns silently when no toolkit registry is wired (the platform
// may wire the queue before any toolkits register).
func TestCatalogSource_OnSucceeded_NilRegistryIsNoOp(_ *testing.T) {
	s := &catalogSource{registry: nil}
	s.OnSucceeded(catalogindex.EncodeSourceID("cat1", "spec1"))
}

// errStore is a catalog.Store stub that returns the supplied error
// from every method. Used to exercise error-handling branches
// without standing up the postgres or memory store machinery.
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
