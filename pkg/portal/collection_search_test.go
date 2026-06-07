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

// collectionSearchCols matches collectionColumns / collectionScanDest order.
var collectionSearchCols = []string{
	"id", "owner_id", "owner_email", "name", "description", "thumbnail_s3_key",
	"config", "created_at", "updated_at", "deleted_at",
}

func addCollectionRow(rows *sqlmock.Rows, id, name string, extra ...driver.Value) {
	now := time.Now()
	base := make([]driver.Value, 0, 10+len(extra))
	base = append(base, id, "u1", "alice@example.com", name, "desc", "", []byte(`{}`), now, now, nil)
	base = append(base, extra...)
	rows.AddRow(base...)
}

// expectEmptyAssetTags mocks the populateAssetTags query after a non-empty
// collection search.
func expectEmptyAssetTags(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("FROM portal_collection_sections").
		WillReturnRows(sqlmock.NewRows([]string{"collection_id", "array_agg"}))
}

func TestSearchCollections_Hybrid(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, collectionSearchCols...), "vec_score", "lex_match"))
	addCollectionRow(rows, "c-1", "Quarterly review", 0.8, true)
	mock.ExpectQuery("UNION ALL").
		WithArgs(sqlmock.AnyArg(), "quarterly", "alice@example.com").
		WillReturnRows(rows)
	expectEmptyAssetTags(mock)

	scored, err := store.SearchCollections(context.Background(), CollectionSearchQuery{
		Embedding: []float32{0.1, 0.2, 0.3},
		QueryText: "quarterly",
		OwnerID:   "alice@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, scored, 1)
	assert.Equal(t, "c-1", scored[0].Collection.ID)
	// cosine 0.8 -> semantic 0.9; with lexical match: 0.6*0.9 + 0.4 = 0.94
	assert.InDelta(t, 0.94, scored[0].Score, 1e-9)
}

func TestSearchCollections_Lexical(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, collectionSearchCols...), "lex_rank"))
	addCollectionRow(rows, "c-2", "Notes", 0.33)
	mock.ExpectQuery("ORDER BY lex_rank DESC").
		WithArgs("notes", "alice@example.com").
		WillReturnRows(rows)
	expectEmptyAssetTags(mock)

	scored, err := store.SearchCollections(context.Background(), CollectionSearchQuery{
		QueryText: "notes", OwnerID: "alice@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, scored, 1)
	assert.Equal(t, "c-2", scored[0].Collection.ID)
	assert.InDelta(t, 0.33, scored[0].Score, 1e-9)
}

func TestSearchCollections_HybridQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	mock.ExpectQuery("UNION ALL").WillReturnError(assert.AnError)
	_, err = store.SearchCollections(context.Background(), CollectionSearchQuery{
		Embedding: []float32{0.1}, QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchCollections_LexicalQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	mock.ExpectQuery("ORDER BY lex_rank DESC").WillReturnError(assert.AnError)
	_, err = store.SearchCollections(context.Background(), CollectionSearchQuery{
		QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchCollections_TagPopulateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	rows := sqlmock.NewRows(append(append([]string{}, collectionSearchCols...), "lex_rank"))
	addCollectionRow(rows, "c-1", "x", 0.5)
	mock.ExpectQuery("ORDER BY lex_rank DESC").WillReturnRows(rows)
	mock.ExpectQuery("FROM portal_collection_sections").WillReturnError(assert.AnError)

	_, err = store.SearchCollections(context.Background(), CollectionSearchQuery{
		QueryText: "x", OwnerID: "alice@example.com",
	})
	require.Error(t, err)
}

func TestSearchCollections_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresCollectionStore{db: db}

	mock.ExpectQuery("ORDER BY lex_rank DESC").
		WillReturnRows(sqlmock.NewRows(append(append([]string{}, collectionSearchCols...), "lex_rank")))

	scored, err := store.SearchCollections(context.Background(), CollectionSearchQuery{
		QueryText: "none", OwnerID: "alice@example.com",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, scored)
}
