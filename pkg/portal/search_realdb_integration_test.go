//go:build integration

package portal

// Real-Postgres tests for the #587 lexical-ranking fix: asset and collection
// lexical search must differentiate two single-match records of different
// lengths instead of collapsing both to the flat weight-D 0.1. They run against
// the real schema/FTS functions (portal_asset_fts, portal_collection_fts) that
// the sqlmock unit tests cannot exercise.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const realdbOwner = "550e8400-e29b-41d4-a716-446655440000"

func TestSearchAssetsLexical_RealDB_Differentiates(t *testing.T) {
	store := &postgresAssetStore{db: testdb.New(t)}
	ctx := context.Background()

	short := Asset{
		ID: "asset_short", OwnerID: realdbOwner, OwnerEmail: "u@example.com",
		Name: "short asset", Description: "revenue", ContentType: "text/html",
		S3Bucket: "portal-assets", S3Key: "k1", Tags: []string{}, CurrentVersion: 1,
	}
	long := Asset{
		ID: "asset_long", OwnerID: realdbOwner, OwnerEmail: "u@example.com",
		Name:        "long asset",
		Description: "Quarterly revenue grew across every region this year compared with the prior period.",
		ContentType: "text/html", S3Bucket: "portal-assets", S3Key: "k2", Tags: []string{}, CurrentVersion: 1,
	}
	require.NoError(t, store.Insert(ctx, short))
	require.NoError(t, store.Insert(ctx, long))

	results, err := store.SearchAssets(ctx, AssetSearchQuery{QueryText: "revenue", OwnerID: realdbOwner, Limit: 10})
	require.NoError(t, err)

	scores := map[string]float64{}
	for _, r := range results {
		scores[r.Asset.ID] = r.Score
	}
	require.Contains(t, scores, short.ID)
	require.Contains(t, scores, long.ID)
	assertDifferentiatedScores(t, scores[short.ID], scores[long.ID])
}

func TestSearchCollectionsLexical_RealDB_Differentiates(t *testing.T) {
	store := &postgresCollectionStore{db: testdb.New(t)}
	ctx := context.Background()

	short := Collection{
		ID: "col_short", OwnerID: realdbOwner, OwnerEmail: "u@example.com",
		Name: "short collection", Description: "revenue",
	}
	long := Collection{
		ID: "col_long", OwnerID: realdbOwner, OwnerEmail: "u@example.com",
		Name:        "long collection",
		Description: "Quarterly revenue grew across every region this year compared with the prior period.",
	}
	require.NoError(t, store.Insert(ctx, short))
	require.NoError(t, store.Insert(ctx, long))

	results, err := store.SearchCollections(ctx, CollectionSearchQuery{QueryText: "revenue", OwnerID: realdbOwner, Limit: 10})
	require.NoError(t, err)

	scores := map[string]float64{}
	for _, r := range results {
		scores[r.Collection.ID] = r.Score
	}
	require.Contains(t, scores, short.ID)
	require.Contains(t, scores, long.ID)
	assertDifferentiatedScores(t, scores[short.ID], scores[long.ID])
}

// assertDifferentiatedScores checks the #587 invariant: two single-match records
// of different lengths get clearly different lexical scores (a clear margin in
// either direction; the FTS functions are weighted so direction is not fixed),
// not the flat weight-D value, and both stay in (0,1).
func assertDifferentiatedScores(t *testing.T, a, b float64) {
	t.Helper()
	hi, lo := a, b
	if lo > hi {
		hi, lo = lo, hi
	}
	assert.Greater(t, hi, 1.2*lo, "single-match records of different lengths must get clearly different scores")
	assert.Greater(t, lo, 0.0, "scores must be positive")
	assert.Less(t, hi, 1.0, "scores must be < 1")
}
