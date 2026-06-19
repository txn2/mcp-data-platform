package portal

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assetSearchCols is the asset projection the search scanners read, matching
// assetSearchColumns / assetScanDest order. sqlmock scans positionally.
var assetSearchCols = []string{
	"id", "owner_id", "owner_email", "name", "description", "content_type",
	"s3_bucket", "s3_key", "thumbnail_s3_key", "thumbnail_dark_s3_key", "size_bytes", "tags", "provenance",
	"session_id", "current_version", "created_at", "updated_at", "deleted_at", "idempotency_key",
}

func addAssetRow(rows *sqlmock.Rows, id, name string, extra ...driverValueList) {
	now := time.Now()
	base := []driver.Value{
		id, "u1", "alice@example.com", name, "desc", "text/html",
		"bucket", "key", "", "", int64(10), []byte(`["t"]`), []byte(`{}`),
		"", 1, now, now, nil, "",
	}
	for _, e := range extra {
		base = append(base, e...)
	}
	rows.AddRow(base...)
}

// driverValueList lets addAssetRow append the per-arm score columns.
type driverValueList []driver.Value

// expectEmptyCollections mocks the populateCollections query that runs after a
// non-empty asset search, returning no collection associations.
func expectEmptyCollections(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("FROM portal_collection_items").
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "id", "name"}))
}

func TestSearchAssets_Hybrid(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "vec_score", "lex_match"))
	addAssetRow(rows, "a-1", "Cohort retention", driverValueList{0.9, true})
	mock.ExpectQuery("UNION ALL").
		WithArgs(sqlmock.AnyArg(), "retention", "alice@example.com").
		WillReturnRows(rows)
	expectEmptyCollections(mock)

	scored, err := store.SearchAssets(context.Background(), AssetSearchQuery{
		Embedding: []float32{0.1, 0.2, 0.3},
		QueryText: "retention",
		OwnerID:   "alice@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, scored, 1)
	assert.Equal(t, "a-1", scored[0].Asset.ID)
	// cosine 0.9 -> semantic 0.95; with lexical match: 0.6*0.95 + 0.4 = 0.97
	assert.InDelta(t, 0.97, scored[0].Score, 1e-9)
}

func TestSearchAssets_HybridDedupKeepsHigher(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	// The same asset appears in both arms; the higher fused score must win.
	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "vec_score", "lex_match"))
	addAssetRow(rows, "a-1", "Cohort", driverValueList{1.0, false}) // vec arm: 0.6
	addAssetRow(rows, "a-1", "Cohort", driverValueList{1.0, true})  // lex arm: 1.0
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)
	expectEmptyCollections(mock)

	scored, err := store.SearchAssets(context.Background(), AssetSearchQuery{
		Embedding: []float32{0.1}, QueryText: "x", OwnerID: "alice@example.com",
	})
	require.NoError(t, err)
	require.Len(t, scored, 1)
	assert.InDelta(t, 1.0, scored[0].Score, 1e-9)
}

func TestSearchAssets_Lexical(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "lex_rank"))
	addAssetRow(rows, "a-2", "Notes", driverValueList{0.42})
	mock.ExpectQuery("ORDER BY lex_rank DESC").
		WithArgs("notes", "alice@example.com").
		WillReturnRows(rows)
	expectEmptyCollections(mock)

	scored, err := store.SearchAssets(context.Background(), AssetSearchQuery{
		QueryText: "notes", OwnerID: "alice@example.com", // nil embedding -> lexical
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, scored, 1)
	assert.Equal(t, "a-2", scored[0].Asset.ID)
	assert.InDelta(t, 0.42, scored[0].Score, 1e-9)
}

func TestSearchAssets_HybridQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("UNION ALL").WillReturnError(assert.AnError)
	_, err = store.SearchAssets(context.Background(), AssetSearchQuery{
		Embedding: []float32{0.1}, QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchAssets_LexicalQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("ORDER BY lex_rank DESC").WillReturnError(assert.AnError)
	_, err = store.SearchAssets(context.Background(), AssetSearchQuery{
		QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchAssets_CollectionPopulateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "lex_rank"))
	addAssetRow(rows, "a-1", "x", driverValueList{0.5})
	mock.ExpectQuery("ORDER BY lex_rank DESC").WillReturnRows(rows)
	mock.ExpectQuery("FROM portal_collection_items").WillReturnError(assert.AnError)

	_, err = store.SearchAssets(context.Background(), AssetSearchQuery{
		QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

// TestSearchAssets_HybridScanError covers the per-row finish error branch: a row
// whose tags column is not valid JSON fails unmarshal during scanning.
func TestSearchAssets_HybridScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	now := time.Now()
	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "vec_score", "lex_match")).
		AddRow("a-1", "u1", "alice@example.com", "n", "d", "text/html",
			"b", "k", "", "", int64(1), []byte("not json"), []byte(`{}`),
			"", 1, now, now, nil, "", 0.5, true)
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	_, err = store.SearchAssets(context.Background(), AssetSearchQuery{
		Embedding: []float32{0.1}, QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchAssets_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresAssetStore{db: db}

	mock.ExpectQuery("ORDER BY lex_rank DESC").
		WillReturnRows(sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "lex_rank")))
	// No populate query: zero results.

	scored, err := store.SearchAssets(context.Background(), AssetSearchQuery{
		QueryText: "none", OwnerID: "alice@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, scored)
}
