//go:build integration

package portalstore

// Real-Postgres tests for the #1295 listing order. The ORDER BY column is
// spliced into SQL rather than bound, and the tie-breaker exists precisely for
// the paging behavior sqlmock cannot show: these run the composed statement
// against the real schema and assert the rows come back in the stated order.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const orderOwner = "550e8400-e29b-41d4-a716-446655440111"

// stamp forces a row's timestamps, which Insert leaves to the column defaults.
func stampAsset(t *testing.T, db *sql.DB, id string, created, updated time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_assets SET created_at = $1, updated_at = $2 WHERE id = $3`, created, updated, id)
	require.NoError(t, err)
}

func stampCollection(t *testing.T, db *sql.DB, id string, created, updated time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE portal_collections SET created_at = $1, updated_at = $2 WHERE id = $3`, created, updated, id)
	require.NoError(t, err)
}

func newAsset(id, name string, size int64) portaldomain.Asset {
	return portaldomain.Asset{
		ID: id, OwnerID: orderOwner, OwnerEmail: "u@example.com",
		Name: name, ContentType: "text/html", S3Bucket: "portal-assets", S3Key: "k-" + id,
		SizeBytes: size, Tags: []string{}, CurrentVersion: 1,
	}
}

func ids(assets []portaldomain.Asset) []string {
	out := make([]string, len(assets))
	for i, a := range assets {
		out[i] = a.ID
	}
	return out
}

// The complaint that opened #1295: an asset created in June and revised today
// belongs above one created yesterday and never touched since.
func TestAssetListOrder_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	jan := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, store.Insert(ctx, newAsset("asset_revised", "beta revised", 300)))
	require.NoError(t, store.Insert(ctx, newAsset("asset_untouched", "alpha untouched", 100)))
	stampAsset(t, db, "asset_revised", jan, june)
	stampAsset(t, db, "asset_untouched", may, may)

	t.Run("defaults to most recently touched", func(t *testing.T) {
		got, total, err := store.List(ctx, portaldomain.AssetFilter{OwnerID: orderOwner})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{"asset_revised", "asset_untouched"}, ids(got))
	})

	t.Run("created_at is still reachable, and disagrees", func(t *testing.T) {
		got, _, err := store.List(ctx, portaldomain.AssetFilter{
			OwnerID: orderOwner, SortBy: "created_at", SortDir: portaldomain.SortDesc,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"asset_untouched", "asset_revised"}, ids(got))
	})

	t.Run("name ascending", func(t *testing.T) {
		got, _, err := store.List(ctx, portaldomain.AssetFilter{
			OwnerID: orderOwner, SortBy: "name", SortDir: portaldomain.SortAsc,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"asset_untouched", "asset_revised"}, ids(got))
	})

	t.Run("size descending", func(t *testing.T) {
		got, _, err := store.List(ctx, portaldomain.AssetFilter{
			OwnerID: orderOwner, SortBy: "size_bytes", SortDir: portaldomain.SortDesc,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"asset_revised", "asset_untouched"}, ids(got))
	})

	t.Run("an unlisted column is refused by the query, not by Postgres", func(t *testing.T) {
		// If the column reached the statement at all, Postgres would answer
		// with an error rather than the default ordering.
		got, _, err := store.List(ctx, portaldomain.AssetFilter{
			OwnerID: orderOwner, SortBy: "s3_key; DROP TABLE portal_assets",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"asset_revised", "asset_untouched"}, ids(got))
	})
}

// The tie-breaker's whole purpose: with a non-unique sort key, LIMIT/OFFSET
// paging must not hand the same row to two pages or skip one between them.
func TestAssetListPagingIsStableOnATie_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresAssetStore{db: db}
	ctx := context.Background()

	same := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"asset_a", "asset_b", "asset_c"} {
		require.NoError(t, store.Insert(ctx, newAsset(id, "identical", 10)))
		stampAsset(t, db, id, same, same)
	}

	var walked []string
	for offset := 0; offset < 3; offset++ {
		page, _, err := store.List(ctx, portaldomain.AssetFilter{
			OwnerID: orderOwner, Limit: 1, Offset: offset,
		})
		require.NoError(t, err)
		require.Len(t, page, 1)
		walked = append(walked, page[0].ID)
	}

	// Every row exactly once, in the tie-breaker's direction.
	assert.Equal(t, []string{"asset_c", "asset_b", "asset_a"}, walked)
}

func TestCollectionListOrder_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := &postgresCollectionStore{db: db}
	ctx := context.Background()

	jan := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, store.Insert(ctx, portaldomain.Collection{
		ID: "coll_revised", OwnerID: orderOwner, OwnerEmail: "u@example.com", Name: "beta revised",
	}))
	require.NoError(t, store.Insert(ctx, portaldomain.Collection{
		ID: "coll_untouched", OwnerID: orderOwner, OwnerEmail: "u@example.com", Name: "alpha untouched",
	}))
	stampCollection(t, db, "coll_revised", jan, june)
	stampCollection(t, db, "coll_untouched", may, may)

	collIDs := func(cs []portaldomain.Collection) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.ID
		}
		return out
	}

	t.Run("defaults to most recently touched", func(t *testing.T) {
		got, total, err := store.List(ctx, portaldomain.CollectionFilter{OwnerID: orderOwner})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{"coll_revised", "coll_untouched"}, collIDs(got))
	})

	t.Run("name ascending", func(t *testing.T) {
		got, _, err := store.List(ctx, portaldomain.CollectionFilter{
			OwnerID: orderOwner, SortBy: "name", SortDir: portaldomain.SortAsc,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"coll_untouched", "coll_revised"}, collIDs(got))
	})

	t.Run("a column the collections table lacks falls back rather than erroring", func(t *testing.T) {
		got, _, err := store.List(ctx, portaldomain.CollectionFilter{
			OwnerID: orderOwner, SortBy: "size_bytes",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"coll_revised", "coll_untouched"}, collIDs(got))
	})
}

// The default ordering's index has to exist for the default page to be an index
// scan rather than a sort of the owner's whole library (000104).
func TestListOrderIndexesExist_RealDB(t *testing.T) {
	db := testdb.New(t)

	for _, idx := range []string{"idx_portal_assets_owner_updated", "idx_portal_collections_owner_updated"} {
		var name string
		err := db.QueryRowContext(context.Background(),
			`SELECT indexname FROM pg_indexes WHERE indexname = $1`, idx).Scan(&name)
		require.NoError(t, err, "migration 000104 must create %s", idx)
		assert.Equal(t, idx, name)
	}
}
