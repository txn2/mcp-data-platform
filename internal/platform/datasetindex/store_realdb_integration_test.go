//go:build integration

package datasetindex

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
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

	indexed, expected, err := store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, indexed)
	assert.Equal(t, 2, expected)

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
	_, expected, err = store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, expected, "the pruned row is gone entirely, not just its vector")

	// An empty enumeration clears the mirror.
	require.NoError(t, store.ReplaceVectors(ctx, nil))
	_, expected, err = store.Coverage(ctx)
	require.NoError(t, err)
	assert.Zero(t, expected)

	// One sync row per deployment: the CHECK (id) primary key makes that a
	// constraint rather than a convention the code remembers.
	require.NoError(t, store.StampSync(ctx))
	var syncRows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_dataset_sync`).Scan(&syncRows))
	assert.Equal(t, 1, syncRows)
}

// TestCatalogIndexSyncUpdatesRealDB proves a re-sync updates a dataset's text
// in place (keyed on the URN) rather than duplicating it, which is what lets a
// description edited in DataHub become searchable on the next sweep.
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
