package connstored

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// record is the store's record type in these tests, standing in for the
// platform's ConnectionInstance (which this package cannot import).
type record struct {
	kind, name, description, catalogID string
}

// project is the accessor pkg/platform supplies.
func project(r record) Row {
	return Row{Kind: r.kind, Name: r.name, Description: r.description, CatalogID: r.catalogID}
}

// fakeStore is an Inventory over a fixed set of records.
type fakeStore struct {
	records    []record
	err        error
	persistent bool
	calls      int
}

func (f *fakeStore) List(_ context.Context) ([]record, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

func (f *fakeStore) Persistent() bool { return f.persistent }

// countingCatalogs counts ListSpecs calls, so the memoization is observable.
type countingCatalogs struct {
	apicatalog.Store
	calls int
	err   error
}

func (c *countingCatalogs) ListSpecs(ctx context.Context, catalogID string) ([]apicatalog.SpecEntry, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	specs, err := c.Store.ListSpecs(ctx, catalogID)
	if err != nil {
		return nil, fmt.Errorf("listing specs: %w", err)
	}
	return specs, nil
}

// catalogWithSpecs returns a memory catalog store holding one catalog whose
// specs expose the given operation counts.
func catalogWithSpecs(t *testing.T, catalogID string, counts ...int) func() apicatalog.Store {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	require.NoError(t, store.CreateCatalog(context.Background(), apicatalog.Catalog{ID: catalogID, Name: catalogID}))
	for i, n := range counts {
		require.NoError(t, store.UpsertSpec(context.Background(), catalogID, apicatalog.SpecEntry{
			SpecName: "spec" + string(rune('a'+i)), Content: "{}", SourceKind: apicatalog.SourceInline,
			OperationCount: n,
		}))
	}
	return func() apicatalog.Store { return store }
}

// fixed returns a catalog store as the accessor the inventory asks.
func fixed(store apicatalog.Store) func() apicatalog.Store {
	return func() apicatalog.Store { return store }
}

func TestNew_NilWhereThereIsNoSharedInventory(t *testing.T) {
	assert.Nil(t, New[record](nil, fixed(nil), project), "no store is no inventory to read")
	assert.Nil(t, New(&fakeStore{persistent: false}, fixed(nil), project),
		"a store that does not outlive the process holds nothing another replica wrote")
	assert.Nil(t, New(&fakeStore{persistent: true}, fixed(nil), nil), "no projection is no way to read a record")
}

// TestListStoredConnections_ReportsEveryRowWithItsCatalogSurface is what the
// enumeration is built on: every saved connection, with the operation count its
// catalog's specs sum to, which is the same number on every replica.
func TestListStoredConnections_ReportsEveryRowWithItsCatalogSurface(t *testing.T) {
	store := &fakeStore{persistent: true, records: []record{
		{kind: "api", name: "billing", description: "Billing API", catalogID: "cat-1"},
		{kind: "trino", name: "warehouse", description: "The warehouse"},
	}}

	got, err := New(store, catalogWithSpecs(t, "cat-1", 3, 4), project).ListStoredConnections(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "api", got[0].Kind)
	assert.Equal(t, "billing", got[0].Name)
	assert.Equal(t, "Billing API", got[0].Description)
	assert.Equal(t, "cat-1", got[0].CatalogID)
	assert.Equal(t, 7, got[0].OperationCount, "summed across the catalog's specs")
	assert.Equal(t, "warehouse", got[1].Name)
	assert.Zero(t, got[1].OperationCount, "a connection that names no catalog has no catalog surface")
}

// TestListStoredConnections_ReadsOneCatalogOnce: two connections on one catalog
// is the point of a catalog, and an inventory that read it per connection would
// cost a query per connection.
func TestListStoredConnections_ReadsOneCatalogOnce(t *testing.T) {
	store := &fakeStore{persistent: true, records: []record{
		{kind: "api", name: "one", catalogID: "shared"},
		{kind: "graphql", name: "two", catalogID: "shared"},
	}}
	catalogs := &countingCatalogs{Store: catalogWithSpecs(t, "shared", 2)()}

	got, err := New(store, fixed(catalogs), project).ListStoredConnections(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, got[0].OperationCount)
	assert.Equal(t, 2, got[1].OperationCount)
	assert.Equal(t, 1, catalogs.calls, "the shared catalog is read once")
}

// TestListStoredConnections_ACatalogThatCannotBeReadStillReportsTheConnection:
// the connection exists whether or not its surface can be measured, and a
// refusal here would hide it from an operator who just saved it.
func TestListStoredConnections_ACatalogThatCannotBeReadStillReportsTheConnection(t *testing.T) {
	store := &fakeStore{persistent: true, records: []record{{kind: "api", name: "billing", catalogID: "cat-1"}}}
	catalogs := &countingCatalogs{Store: apicatalog.NewMemoryStore(), err: errors.New("the catalog is unreachable")}

	got, err := New(store, fixed(catalogs), project).ListStoredConnections(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "billing", got[0].Name)
	assert.Zero(t, got[0].OperationCount)
}

// TestListStoredConnections_StoreErrorIsReported hands the failure up, where
// the enumeration decides to degrade to what this process serves.
func TestListStoredConnections_StoreErrorIsReported(t *testing.T) {
	store := &fakeStore{persistent: true, err: errors.New("the database is unreachable")}

	_, err := New(store, fixed(nil), project).ListStoredConnections(context.Background())

	require.Error(t, err)
}

// TestListStoredConnections_NoCatalogStoreReportsNoSurface covers a deployment
// with no api gateway wired: the connections are still the inventory.
func TestListStoredConnections_NoCatalogStoreReportsNoSurface(t *testing.T) {
	store := &fakeStore{persistent: true, records: []record{{kind: "api", name: "billing", catalogID: "cat-1"}}}

	got, err := New(store, fixed(nil), project).ListStoredConnections(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Zero(t, got[0].OperationCount)
}
