//go:build integration

package portalstore

// Real-Postgres tests for the two reads bounded by #1623: the summary every
// listing carries in the captures' place, and the page that reaches the
// captures a single read leaves out.
//
// Both are jsonb expressions -- jsonb_build_object over a correlated
// jsonb_array_elements for the summary, jsonb_agg over WITH ORDINALITY for the
// page. sqlmock matches them as strings and returns whatever a test supplies,
// so only a real PostgreSQL says whether the summary counts what it claims to
// and whether the page is cut where it says.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// seedProvenance files an asset and appends one capture per version listed,
// each carrying calls many calls.
func seedProvenance(t *testing.T, store *postgresAssetStore, id string, versions int, calls int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.Insert(ctx, provAsset(id)))
	for v := 1; v <= versions; v++ {
		eventIDs := make([]string, 0, calls)
		for c := range calls {
			eventIDs = append(eventIDs, "evt-"+string(rune('a'+c)))
		}
		require.NoError(t, store.AppendProvenanceCapture(ctx, id, capture("manage_asset", v, eventIDs...)))
	}
}

// The listing carries a summary that counts what the asset holds, and no
// captures at all.
func TestListingProvenanceSummary_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	seedProvenance(t, store, "sum-1", 25, 2)

	assets, _, err := store.List(context.Background(), portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner(provOwner, ""),
	})
	require.NoError(t, err)
	require.Len(t, assets, 1)

	row := assets[0]
	assert.Empty(t, row.Provenance.Captures, "a listing row carries no captures")
	require.NotNil(t, row.ProvenanceSummary)
	assert.Equal(t, 25, row.ProvenanceSummary.Captures)
	assert.Equal(t, 50, row.ProvenanceSummary.Calls, "two calls in each of twenty-five captures")
	assert.Equal(t, "manage_asset", row.ProvenanceSummary.LastTool)
	assert.Equal(t, "dps_test", row.ProvenanceSummary.LastSessionID)
	require.NotNil(t, row.ProvenanceSummary.FirstCapturedAt)
	require.NotNil(t, row.ProvenanceSummary.LastCapturedAt)
	assert.False(t, row.ProvenanceSummary.LastCapturedAt.Before(*row.ProvenanceSummary.FirstCapturedAt))
}

// An asset that recorded nothing, and one carrying only the pre-#1320 shape,
// both summarize as zero rather than failing the listing.
func TestListingProvenanceSummary_RealDB_EmptyAndLegacy(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()
	require.NoError(t, store.Insert(ctx, provAsset("sum-empty")))

	legacy := provAsset("sum-legacy")
	legacy.Provenance = portaldomain.Provenance{
		ToolCalls: []portaldomain.ProvenanceToolCall{{ToolName: "trino_query"}},
	}
	require.NoError(t, store.Insert(ctx, legacy))

	assets, _, err := store.List(ctx, portaldomain.AssetFilter{
		Owner: portaldomain.NewAssetOwner(provOwner, ""),
	})
	require.NoError(t, err)
	require.Len(t, assets, 2)
	for _, row := range assets {
		require.NotNil(t, row.ProvenanceSummary, "asset %s", row.ID)
		assert.Zero(t, row.ProvenanceSummary.Captures, "asset %s", row.ID)
		assert.Zero(t, row.ProvenanceSummary.Calls, "asset %s", row.ID)
		assert.Nil(t, row.ProvenanceSummary.FirstCapturedAt, "asset %s", row.ID)
		assert.Empty(t, row.ProvenanceSummary.LastTool, "asset %s", row.ID)
	}
}

// The page is newest first and cut where it says, with no overlap and no gap
// between consecutive pages.
func TestListProvenanceCaptures_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	seedProvenance(t, store, "page-1", 25, 1)
	ctx := context.Background()

	first, total, err := store.ListProvenanceCaptures(ctx, "page-1", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, 25, total)
	require.Len(t, first, 20)
	assert.Equal(t, 25, first[0].Version, "newest first")
	assert.Equal(t, 6, first[19].Version)

	second, total, err := store.ListProvenanceCaptures(ctx, "page-1", 20, 20)
	require.NoError(t, err)
	assert.Equal(t, 25, total)
	require.Len(t, second, 5, "the page is bounded by what is left")
	assert.Equal(t, 5, second[0].Version, "no overlap and no gap against the first page")
	assert.Equal(t, 1, second[4].Version)

	// The captures come back whole: the page carries what each write recorded,
	// not just its heading.
	require.Len(t, second[4].Calls, 1)
	assert.Equal(t, portaldomain.ProvenanceKindSQL, second[4].Calls[0].Kind)

	past, total, err := store.ListProvenanceCaptures(ctx, "page-1", 100, 20)
	require.NoError(t, err)
	assert.Equal(t, 25, total)
	assert.Empty(t, past, "an offset past the history is an empty page, not an error")
}

// An asset carrying only the pre-#1320 shape has no captures to page, and a
// deleted or unknown asset is reported as absent rather than as empty.
func TestListProvenanceCaptures_RealDB_LegacyAndMissing(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	legacy := provAsset("page-legacy")
	legacy.Provenance = portaldomain.Provenance{
		ToolCalls: []portaldomain.ProvenanceToolCall{{ToolName: "trino_query"}},
	}
	require.NoError(t, store.Insert(ctx, legacy))

	captures, total, err := store.ListProvenanceCaptures(ctx, "page-legacy", 0, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, captures)

	require.NoError(t, store.SoftDelete(ctx, "page-legacy"))
	_, _, err = store.ListProvenanceCaptures(ctx, "page-legacy", 0, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset not found or deleted")

	_, _, err = store.ListProvenanceCaptures(ctx, "no-such-asset", 0, 20)
	require.Error(t, err)
}
