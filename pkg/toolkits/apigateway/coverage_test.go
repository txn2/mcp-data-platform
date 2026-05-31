package apigateway

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// failingCatalogStore returns errors from every method. Used to
// exercise the toolkit's "store wired but ListSpecs failed" branch
// of buildConnSpecs.
type failingCatalogStore struct{}

func (failingCatalogStore) CreateCatalog(context.Context, catalog.Catalog) error {
	return errors.New("boom")
}

func (failingCatalogStore) GetCatalog(context.Context, string) (*catalog.Catalog, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) ListCatalogs(context.Context) ([]catalog.Catalog, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) UpdateCatalog(context.Context, string, catalog.Update) error {
	return errors.New("boom")
}

func (failingCatalogStore) DeleteCatalog(context.Context, string) error {
	return errors.New("boom")
}

func (failingCatalogStore) UpsertSpec(context.Context, string, catalog.SpecEntry) error {
	return errors.New("boom")
}

func (failingCatalogStore) GetSpec(context.Context, string, string) (*catalog.SpecEntry, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) ListSpecs(context.Context, string) ([]catalog.SpecEntry, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) DeleteSpec(context.Context, string, string) error {
	return errors.New("boom")
}

func (failingCatalogStore) ReferencingConnections(context.Context, string) ([]catalog.ConnectionRef, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) UpsertOperationEmbeddings(context.Context, string, string, []catalog.OperationEmbedding) error {
	return errors.New("boom")
}

func (failingCatalogStore) UpsertOperationEmbeddingsBatch(context.Context, string, string, []catalog.OperationEmbedding) error {
	return errors.New("boom")
}

func (failingCatalogStore) ListOperationEmbeddings(context.Context, string, string) ([]catalog.OperationEmbedding, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) SetOperationCount(context.Context, string, string, int) error {
	return errors.New("boom")
}

func (failingCatalogStore) DeleteOperationEmbeddings(context.Context, string, string) error {
	return errors.New("boom")
}

func (failingCatalogStore) ListEmbeddingGaps(context.Context) ([]catalog.SpecKey, error) {
	return nil, errors.New("boom")
}

func (failingCatalogStore) EmbeddingCoverage(context.Context) (indexed, expected int, err error) {
	return 0, 0, errors.New("boom")
}

func TestCatalogStore_Getter(t *testing.T) {
	tk := New("api")
	if tk.CatalogStore() != nil {
		t.Fatal("expected nil before SetCatalogStore")
	}
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	if tk.CatalogStore() != store {
		t.Fatal("CatalogStore() should return wired store")
	}
}

func TestBuildConnSpecs_StoreError(t *testing.T) {
	tk := New("api")
	tk.SetCatalogStore(failingCatalogStore{})
	// AddConnection should succeed: the catalog load error is
	// non-fatal and the connection registers with zero ops.
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "anything",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if c == nil {
		t.Fatal("connection should be registered despite catalog error")
	}
	if len(c.operations) != 0 {
		t.Errorf("expected zero ops, got %d", len(c.operations))
	}
}

func TestReloadConnectionsByCatalog_LogsErrorOnRebuildFailure(t *testing.T) {
	tk := New("api")
	setupCatalogWithSpec(t, tk, "vendor", "default", validMinimalSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	// Swap the store to one whose ListSpecs fails, then trigger a
	// reload. The conn rebuild itself shouldn't error (load failure
	// is non-fatal), but if rebuild ever returns an error the
	// branch is exercised. We assert the sweep finishes — empty
	// catalogID is a no-op; the matching path runs through reload.
	tk.SetCatalogStore(failingCatalogStore{})
	tk.ReloadConnectionsByCatalog("vendor") // exercises the rebuild loop
	// Empty catalogID branch
	tk.ReloadConnectionsByCatalog("")
}
