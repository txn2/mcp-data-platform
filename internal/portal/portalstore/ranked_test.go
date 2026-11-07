package portalstore

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// assetSearchCols is the listing projection, in assetScanDest order. sqlmock
// scans positionally.
var assetSearchCols = []string{
	"id", "owner_id", "owner_email", "name", "description", "content_type",
	"s3_bucket", "s3_key", "thumbnail_s3_key", "thumbnail_dark_s3_key", "thumbnail_version", "thumbnail_dark_version", "thumbnail_renderer", "thumbnail_failure", "thumbnail_failed_version", "size_bytes", "tags", "provenance",
	"session_id", "current_version", "created_at", "updated_at", "deleted_at", "idempotency_key", "max_versions", "released_version",
}

func addAssetRow(rows *sqlmock.Rows, id, name string, extra ...driverValueList) {
	now := time.Now()
	base := []driver.Value{
		id, "u1", "alice@example.com", name, "desc", "text/html",
		"bucket", "key", "", "", 0, 0, 0, "", 0, int64(10), []byte(`["t"]`), []byte(`{}`),
		"", 1, now, now, nil, "", nil, 0,
	}
	for _, e := range extra {
		base = append(base, e...)
	}
	rows.AddRow(base...)
}

// driverValueList lets addAssetRow append further columns.
type driverValueList []driver.Value

// expectEmptyCollections mocks the populateCollections query a non-empty
// listing runs, returning no collection associations.
func expectEmptyCollections(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("FROM portal_collection_items").
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "id", "name"}))
}

func rankedRows() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "score", "total"}) }

// A ranked page is read in rank order through the listing, which returns rows
// in its own order; each row keeps its score.
func TestSearchReach_ReadsThePageInRankOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("WITH c AS MATERIALIZED").
		WillReturnRows(rankedRows().AddRow("a2", 0.9, 7).AddRow("a1", 0.6, 7))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	rows := sqlmock.NewRows(assetSearchCols)
	addAssetRow(rows, "a1", "one")
	addAssetRow(rows, "a2", "two")
	mock.ExpectQuery("FROM portal_assets").WillReturnRows(rows)
	expectEmptyCollections(mock)

	scored, total, err := store.SearchReach(context.Background(), portaldomain.RankedAssetQuery{
		QueryText: "revenue", Reach: portaldomain.NewReach("u1", "alice@example.com", "", portaldomain.ScopeAll),
		Embedding: []float32{0.1},
	})
	require.NoError(t, err)
	assert.Equal(t, 7, total)
	require.Len(t, scored, 2)
	assert.Equal(t, "a2", scored[0].Asset.ID)
	assert.InDelta(t, 0.9, scored[0].Score, 1e-9)
	assert.Equal(t, "a1", scored[1].Asset.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchReach_NobodyOrNoQueryRanksNothing(t *testing.T) {
	store := &postgresAssetStore{}
	for _, q := range []portaldomain.RankedAssetQuery{
		{QueryText: "x"},
		{QueryText: "  ", Reach: portaldomain.NewReach("u1", "", "", portaldomain.ScopeAll)},
	} {
		scored, total, err := store.SearchReach(context.Background(), q)
		require.NoError(t, err)
		assert.Empty(t, scored)
		assert.Zero(t, total)
	}
}

func TestSearchAssets_RanksAnOwnersAssetsAndReportsErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("WITH c AS MATERIALIZED").WithArgs("u1", "revenue").WillReturnRows(rankedRows())
	scored, err := store.SearchAssets(context.Background(), portaldomain.AssetSearchQuery{
		QueryText: "revenue", Owner: portaldomain.NewAssetOwner("u1", ""),
	})
	require.NoError(t, err)
	assert.Empty(t, scored)
	assert.NotNil(t, scored, "an empty result is a list")

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnError(assert.AnError)
	_, err = store.SearchAssets(context.Background(), portaldomain.AssetSearchQuery{
		QueryText: "revenue", Owner: portaldomain.NewAssetOwner("u1", ""),
	})
	require.Error(t, err)

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnRows(rankedRows().AddRow("a1", 0.5, 1))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(assert.AnError)
	_, err = store.SearchAssets(context.Background(), portaldomain.AssetSearchQuery{
		QueryText: "revenue", Owner: portaldomain.NewAssetOwner("u1", ""),
	})
	require.Error(t, err, "a failed row read fails the search")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAssetFacets_ReadsBothFacetsOverTheReach(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("jsonb_array_elements_text").
		WillReturnRows(sqlmock.NewRows([]string{"tag", "count"}).AddRow("finance", 3))
	mock.ExpectQuery("LOWER\\(a.owner_email\\)").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "count"}).AddRow("b@example.com", 3))

	facets, err := store.AssetFacets(context.Background(), portaldomain.AssetFilter{
		Reach: portaldomain.NewReach("u1", "alice@example.com", "", portaldomain.ScopeAll),
		Tags:  []string{"finance"}, Owners: []string{"b@example.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, []portaldomain.FacetCount{{Value: "finance", Count: 3}}, facets.Tags)
	assert.Equal(t, []portaldomain.FacetCount{{Value: "b@example.com", Count: 3}}, facets.Authors)
	require.NoError(t, mock.ExpectationsWereMet())

	none, err := store.AssetFacets(context.Background(), portaldomain.AssetFilter{})
	require.NoError(t, err)
	assert.Empty(t, none.Tags)
	assert.NotNil(t, none.Authors)
}

// The tag facet ignores the tag filter and the author facet the author
// filter, so a picker lists the values a person can switch to.
func TestBuildFacets_EachFacetDropsItsOwnFilter(t *testing.T) {
	st, err := buildFacets(portaldomain.AssetFilter{
		Reach: portaldomain.NewReach("u1", "", "", portaldomain.ScopeMine),
		Tags:  []string{"finance"}, Owners: []string{"b@example.com"},
	})
	require.NoError(t, err)
	assert.NotContains(t, st.tags, "tags @>")
	assert.Contains(t, st.tags, "LOWER(owner_email) = ANY")
	assert.Contains(t, st.authors, "tags @>")
	assert.NotContains(t, st.authors, "LOWER(owner_email) = ANY")
	assert.NotEmpty(t, st.tagArgs)
	assert.NotEmpty(t, st.authorArgs)
}

func TestSearchCollections_RanksAndReadsThroughTheListing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnRows(rankedRows().AddRow("c1", 0.8, 1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now()
	mock.ExpectQuery("FROM portal_collections").WillReturnRows(sqlmock.NewRows([]string{
		"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key", "config", "created_at", "updated_at", "deleted_at",
	}).AddRow("c1", "u1", "a@example.com", "Q4", "", "", []byte(`{}`), now, now, nil))
	mock.ExpectQuery("portal_collection_items").WillReturnRows(sqlmock.NewRows([]string{"collection_id", "tags"}))

	scored, err := store.SearchCollections(context.Background(), portaldomain.CollectionSearchQuery{QueryText: "q4", OwnerID: "u1"})
	require.NoError(t, err)
	require.Len(t, scored, 1)
	assert.Equal(t, "c1", scored[0].Collection.ID)

	empty, err := store.SearchCollections(context.Background(), portaldomain.CollectionSearchQuery{QueryText: "q4"})
	require.NoError(t, err)
	assert.Empty(t, empty)

	mock.ExpectQuery("WITH c AS MATERIALIZED").WillReturnError(assert.AnError)
	_, _, err = store.SearchCollectionReach(context.Background(), portaldomain.RankedCollectionQuery{
		QueryText: "q4", Reach: portaldomain.NewReach("u1", "", "", portaldomain.ScopeAll),
	})
	require.Error(t, err)

	none, total, err := store.SearchCollectionReach(context.Background(), portaldomain.RankedCollectionQuery{QueryText: "q4"})
	require.NoError(t, err)
	assert.Empty(t, none)
	assert.Zero(t, total)
}

func TestCollectionAuthors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	mock.ExpectQuery("LOWER\\(a.owner_email\\)").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "count"}).AddRow("b@example.com", 2))
	got, err := store.CollectionAuthors(context.Background(), portaldomain.NewReach("u1", "", "", portaldomain.ScopeShared))
	require.NoError(t, err)
	assert.Equal(t, []portaldomain.FacetCount{{Value: "b@example.com", Count: 2}}, got)

	none, err := store.CollectionAuthors(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

// The collection listing takes the author filter and a set of ids, on both
// its count and its page.
func TestCollectionListAuthorAndIDFilters(t *testing.T) {
	count, sel := (*postgresCollectionStore)(nil).buildListQueries(portaldomain.CollectionFilter{
		Reach:  portaldomain.NewReach("u1", "", "", portaldomain.ScopeAll),
		Owners: []string{"B@example.com"}, IDs: []string{"c1"},
	}, 10)
	for _, qb := range []interface{ ToSql() (string, []any, error) }{count, sel} {
		q, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, q, "LOWER(owner_email) = ANY")
		assert.Contains(t, q, "portal_collections.id = ANY")
		assert.Contains(t, q, "portal_shares ps")
	}
}
