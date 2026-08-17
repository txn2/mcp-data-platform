package callindex

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// newMock returns a store over a mocked database plus the mock controller.
func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	return NewStore(db), mock
}

var textColumns = []string{"purpose", "statement", "method", "path", "operation_id"}

func TestGetTextComposesTheCatalogsOwnCorpus(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow(
			"Sizing Q3 revenue by region.", "SELECT region FROM sales.orders", "", "", ""),
	)

	got, err := store.GetText(context.Background(), "call-1")
	require.NoError(t, err)
	// The vector is computed from what the lexical arm matches, so the text is
	// composed by the catalog rather than assembled a second way here.
	assert.Contains(t, got, "Sizing Q3 revenue by region.")
	assert.Contains(t, got, "SELECT region FROM sales.orders")
}

func TestGetTextTreatsAnEmptyRecordAsNothingToIndex(t *testing.T) {
	store, mock := newMock(t)
	// Two reads: the store's own, and the Source's, which must turn the same
	// answer into a clean completion rather than a failed job.
	for range 2 {
		mock.ExpectQuery("FROM call_records").WillReturnRows(
			sqlmock.NewRows(textColumns).AddRow("", "", "", "", ""),
		)
	}

	_, err := store.GetText(context.Background(), "call-1")
	assert.ErrorIs(t, err, errNotIndexable)

	items, err := NewSource(store).LoadItems(context.Background(), "call-1")
	assert.NoError(t, err)
	assert.Empty(t, items, "a record with nothing to say writes no vector")
}

func TestGetTextTreatsAMissingRecordAsNothingToIndex(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnError(sqlNoRows())

	_, err := store.GetText(context.Background(), "gone")
	assert.ErrorIs(t, err, errNotIndexable)
}

func TestLoadItemsYieldsOneItemPerRecord(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow("Counting orders.", "SELECT 1", "", "", ""),
	)

	items, err := NewSource(store).LoadItems(context.Background(), "call-1")
	require.NoError(t, err)
	require.Len(t, items, 1, "a record is its own indexing unit")
	assert.Equal(t, "call-1", items[0].ItemID)
}

func TestLoadItemsReportsARealFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnError(errors.New("db down"))

	_, err := NewSource(store).LoadItems(context.Background(), "call-1")
	assert.Error(t, err, "an unreadable database is a failed job, not an empty one")
}

func TestListExistingReadsThePersistedVector(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnRows(
		sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgvector.NewVector([]float32{0.1, 0.2}), "nomic", []byte("hash")),
	)

	got, err := NewSink(store, "nomic").ListExisting(context.Background(), indexjobs.Key{SourceID: "call-1"})
	require.NoError(t, err)
	require.Contains(t, got, "call-1")
	assert.Equal(t, "nomic", got["call-1"].Model)
	assert.Equal(t, 2, got["call-1"].Dim)
}

func TestListExistingOnARecordWithNoVectorIsEmpty(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnError(sqlNoRows())

	got, err := NewSink(store, "nomic").ListExisting(context.Background(), indexjobs.Key{SourceID: "call-1"})
	require.NoError(t, err)
	assert.Empty(t, got, "no vector yet means the worker embeds it")
}

func TestUpsertWritesTheVectorBack(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("UPDATE call_records").WillReturnResult(sqlmock.NewResult(0, 1))

	err := NewSink(store, "nomic").Upsert(context.Background(),
		indexjobs.Key{SourceID: "call-1"},
		[]indexjobs.Vector{{ItemID: "call-1", Embedding: []float32{0.1}, Model: "nomic", TextHash: []byte("h")}})
	require.NoError(t, err)
}

func TestFindGapsSkipsWhatWasNeverMeantToBeEmbedded(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WithArgs("nomic").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("call-1"))

	got, err := NewSink(store, "nomic").FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"call-1"}, got)
}

func TestFindGapsReportsAFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnError(errors.New("db down"))

	_, err := NewSink(store, "nomic").FindGaps(context.Background())
	assert.Error(t, err)
}

func TestCoverageCountsTheSamePopulationTheGapQueryWalks(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnRows(
		sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(7, 10),
	)

	got, err := NewSink(store, "nomic").Coverage(context.Background())
	require.NoError(t, err)
	// A converged catalog must read as complete rather than as permanently
	// short by the records that were never meant to be embedded.
	assert.Equal(t, 7, got.Indexed)
	assert.Equal(t, 10, got.Expected)
	assert.True(t, got.ExpectedKnown)
}

func TestCoverageReportsAFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnError(errors.New("db down"))

	_, err := NewSink(store, "nomic").Coverage(context.Background())
	assert.Error(t, err)
}

// sqlNoRows is the driver's no-rows answer, which the store maps onto its own
// nothing-to-index contract.
func sqlNoRows() error { return sql.ErrNoRows }
