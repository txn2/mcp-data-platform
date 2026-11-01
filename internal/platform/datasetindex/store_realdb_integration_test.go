//go:build integration

package datasetindex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogsemantic"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// embedDim matches the vector(768) column migration 000096 declares. A vector
// of any other width is rejected by Postgres, which is one of the things this
// gate proves the code agrees with.
const embedDim = 768

// unitVector returns a 768-dimension vector pointing mostly along axis i, so
// two calls with different i are far apart under cosine distance and a call
// with the same i is identical.
func unitVector(i int) []float32 {
	v := make([]float32, embedDim)
	v[i%embedDim] = 1
	return v
}

// TestCatalogIndexRealDB proves the indexing path against the real schema: the
// mirror write binds a NOT NULL TEXT[] correctly, the atomic replace prunes a
// dataset the catalog stopped returning, gap detection reads the sweep marker
// and the model column as intended, and the ranked search returns a dataset by
// its DESCRIPTION for a query that names neither the dataset nor its table.
// None of that is decidable under sqlmock, which does not parse SQL.
func TestCatalogIndexRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	orders := Entry{
		URN:         "urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)",
		Name:        "sales.orders",
		Description: "Refunds are subtracted before revenue is recognized.",
		Tags:        []string{"finance"},
		Domain:      "Revenue",
	}
	shipments := Entry{
		URN:  "urn:li:dataset:(urn:li:dataPlatform:trino,ops.shipments,PROD)",
		Name: "ops.shipments",
		// No tags: a nil slice must bind as an empty array, not SQL NULL.
		Description: "One row per parcel handed to a carrier.",
	}
	require.NoError(t, store.Sync(ctx, []Entry{orders, shipments}))

	// Before any sweep marker exists the corpus owes work, which is what makes
	// a fresh deployment index itself without a bootstrap enqueue.
	needs, err := store.NeedsSweep(ctx, "model-x", time.Hour)
	require.NoError(t, err)
	assert.True(t, needs)

	require.NoError(t, store.StampSync(ctx))
	needs, err = store.NeedsSweep(ctx, "model-x", time.Hour)
	require.NoError(t, err)
	assert.True(t, needs, "a stamped sweep with unembedded rows still owes the embedding half")

	// Embed both rows the way the worker would.
	rows := []indexjobs.Vector{
		{ItemID: orders.URN, Embedding: unitVector(0), Model: "model-x", TextHash: []byte("h1")},
		{ItemID: shipments.URN, Embedding: unitVector(1), Model: "model-x", TextHash: []byte("h2")},
	}
	require.NoError(t, store.ReplaceVectors(ctx, rows))

	needs, err = store.NeedsSweep(ctx, "model-x", time.Hour)
	require.NoError(t, err)
	assert.False(t, needs, "a fresh, fully embedded corpus is not re-enumerated")

	needs, err = store.NeedsSweep(ctx, "model-y", time.Hour)
	require.NoError(t, err)
	assert.True(t, needs, "a model swap invalidates the stored vectors")

	needs, err = store.NeedsSweep(ctx, "model-x", time.Nanosecond)
	require.NoError(t, err)
	assert.True(t, needs, "an aged-out sweep is how the periodic re-enumeration is scheduled")

	indexed, expected, err := store.Coverage(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, indexed)
	assert.Zero(t, expected, "the expected half is the platform catalog's count, and this catalog is empty")

	// The dedup pass reads back exactly what was written, including the
	// dimension it derives from the stored vector.
	existing, err := store.ListVectors(ctx)
	require.NoError(t, err)
	require.Contains(t, existing, orders.URN)
	assert.Equal(t, embedDim, existing[orders.URN].Dim)
	assert.Equal(t, []byte("h1"), existing[orders.URN].TextHash)

	// The point of the whole feature: a topical query that names neither the
	// dataset nor its table finds it through the description an agent applied.
	hits, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{
		QueryText: "how are refunds treated in revenue",
		Embedding: unitVector(0),
		Limit:     10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, orders.URN, hits[0].URN)
	assert.Equal(t, orders.Description, hits[0].Description,
		"the hit carries the description so search renders it without a catalog round trip")

	// Lexical-only ranking (no query vector) still answers, which is the path a
	// search takes when the intent could not be embedded.
	lex, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "parcel carrier"})
	require.NoError(t, err)
	require.Len(t, lex, 1)
	assert.Equal(t, shipments.URN, lex[0].URN)

	// A dataset the catalog stops returning is pruned by the next replace, so a
	// deleted dataset cannot linger as a hit whose fetch would 404.
	require.NoError(t, store.ReplaceVectors(ctx, rows[:1]))
	after, err := store.ListVectors(ctx)
	require.NoError(t, err)
	assert.NotContains(t, after, shipments.URN)
	indexed, _, err = store.Coverage(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, indexed, "the pruned row is gone entirely, not just its vector")

	// An empty enumeration clears the mirror.
	require.NoError(t, store.ReplaceVectors(ctx, nil))
	indexed, _, err = store.Coverage(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, indexed)

	// One sync row per deployment: the CHECK (id) primary key makes that a
	// constraint rather than a convention the code remembers.
	require.NoError(t, store.StampSync(ctx))
	var syncRows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_dataset_sync`).Scan(&syncRows))
	assert.Equal(t, 1, syncRows)
}

// TestCatalogIndexSyncUpdatesRealDB proves a re-sync updates a dataset's text
// in place (keyed on the URN) rather than duplicating it, which is what lets a
// description edited in the catalog become searchable on the next sweep.
func TestCatalogIndexSyncUpdatesRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	urn := "urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"
	require.NoError(t, store.Sync(ctx, []Entry{{URN: urn, Name: "sales.orders", Description: "Orders."}}))
	require.NoError(t, store.ReplaceVectors(ctx, []indexjobs.Vector{
		{ItemID: urn, Embedding: unitVector(0), Model: "model-x", TextHash: []byte("h1")},
	}))

	require.NoError(t, store.Sync(ctx, []Entry{{
		URN:         urn,
		Name:        "sales.orders",
		Description: "Refunds are subtracted before revenue is recognized.",
		Tags:        []string{"finance"},
	}}))

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_datasets`).Scan(&count))
	assert.Equal(t, 1, count)

	hits, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "refunds revenue"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Contains(t, hits[0].Description, "Refunds", "the mirror carries the edited description")

	// The vector survives the text update (the sync touches no embedding
	// column), so the worker's hash comparison — not this write — decides
	// whether a re-embed is owed.
	existing, err := store.ListVectors(ctx)
	require.NoError(t, err)
	require.Contains(t, existing, urn)
	assert.Equal(t, []byte("h1"), existing[urn].TextHash)
}

// TestCatalogIndexNoticesACatalogChangeRealDB proves a dataset declared or
// curated after the last sync owes a sweep at once, rather than when the sync
// interval runs out (#1919).
func TestCatalogIndexNoticesACatalogChangeRealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	store := NewStore(db)
	cat := catalogstore.New(db)
	require.NoError(t, store.StampSync(ctx))
	needs, err := store.NeedsSweep(ctx, "m", time.Hour)
	require.NoError(t, err)
	require.False(t, needs, "a fresh sync over an empty catalog owes nothing")

	ref := catalog.DatasetRef("trino", "w", "hive", "sales", "orders")
	_, err = cat.PutDataset(ctx, catalog.DatasetInput{Ref: ref, Type: catalog.DatasetTable})
	require.NoError(t, err)
	needs, err = store.NeedsSweep(ctx, "m", time.Hour)
	require.NoError(t, err)
	assert.True(t, needs, "a dataset declared after the sync is read on the next check")

	require.NoError(t, store.StampSync(ctx))
	needs, err = store.NeedsSweep(ctx, "m", time.Hour)
	require.NoError(t, err)
	require.False(t, needs)

	_, err = cat.WriteAspect(ctx, ref, catalog.AspectDescription, json.RawMessage(`"new"`), "a")
	require.NoError(t, err)
	needs, err = store.NeedsSweep(ctx, "m", time.Hour)
	require.NoError(t, err)
	assert.True(t, needs, "a description written after the sync is read on the next check")
}

// TestCatalogIndexKeepsTablesWhenTheCapBindsRealDB is #2381 against the real
// catalog and its real listing: API operations sort before every Trino table
// by reference, yet with the cap binding the index keeps the tables, then the
// GraphQL operations, then the API operations; the platform-admin
// connection's operations are never mirrored; and coverage is held to the
// catalog's own total less them, so a capped corpus reads short.
func TestCatalogIndexKeepsTablesWhenTheCapBindsRealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cat := catalogstore.New(db)
	put := func(ref catalog.Ref, typ catalog.DatasetType) {
		t.Helper()
		_, err := cat.PutDataset(ctx, catalog.DatasetInput{Ref: ref, Type: typ})
		require.NoError(t, err)
	}
	for _, op := range []string{"listOrders", "getOrder", "createOrder"} {
		put(catalog.DatasetRef("api", "crm", op), catalog.DatasetAPIOperation)
	}
	for _, op := range []string{"listPersonas", "getPersona"} {
		put(catalog.DatasetRef("api", "platform-admin", op), catalog.DatasetAPIOperation)
	}
	put(catalog.DatasetRef("graphql", "gh", "query.repository"), catalog.DatasetGraphQLOperation)
	orders := catalog.DatasetRef("trino", "w", "hive", "sales", "orders")
	refunds := catalog.DatasetRef("trino", "w", "hive", "sales", "refunds")
	put(orders, catalog.DatasetTable)
	put(refunds, catalog.DatasetTable)
	gone := catalog.DatasetRef("trino", "w", "hive", "sales", "retired")
	put(gone, catalog.DatasetTable)
	require.NoError(t, cat.MarkDatasetRemoved(ctx, gone))

	lister := catalogsemantic.New(cat, nil)
	excluded := []Connection{{Kind: "api", Name: "platform-admin"}}
	store := NewStore(db)
	sink := NewSink(store, "m", time.Hour).Excluding(excluded...)

	items, err := NewSource(store, lister, 3).Excluding(excluded...).LoadItems(ctx, SourceID)
	require.NoError(t, err)
	got := make([]string, 0, len(items))
	for _, it := range items {
		got = append(got, it.ItemID)
	}
	assert.Equal(t, []string{orders.String(), refunds.String(), catalog.DatasetRef("graphql", "gh", "query.repository").String()}, got,
		"with room for three, the tables are kept, then the GraphQL operation")
	embed(t, store, items)
	cov, err := sink.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, indexjobs.Coverage{Indexed: 3, Expected: 6, ExpectedKnown: true}, cov,
		"expected is every live dataset less platform-admin's, so the capped corpus reads short")

	items, err = NewSource(store, lister, 100).Excluding(excluded...).LoadItems(ctx, SourceID)
	require.NoError(t, err)
	require.Len(t, items, 6)
	for _, it := range items {
		assert.NotContains(t, it.ItemID, "platform-admin", "the platform's own admin operations are not mirrored")
	}
	embed(t, store, items)
	cov, err = sink.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, indexjobs.Coverage{Indexed: 6, Expected: 6, ExpectedKnown: true}, cov)
}

// embed stores a vector for every item, as the worker would after a sweep.
func embed(t *testing.T, store *Store, items []indexjobs.Item) {
	t.Helper()
	rows := make([]indexjobs.Vector, 0, len(items))
	for i, it := range items {
		rows = append(rows, indexjobs.Vector{ItemID: it.ItemID, Embedding: unitVector(i), Model: "m", TextHash: []byte(it.ItemID)})
	}
	require.NoError(t, store.ReplaceVectors(context.Background(), rows))
}
