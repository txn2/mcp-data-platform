package scriptindex

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// TestKindIsTheScriptsSourceKind pins the string the queue routes on: a Source
// and a Sink that disagree would fail registration, and a kind that drifts
// would strand every job already in the table.
func TestKindIsTheScriptsSourceKind(t *testing.T) {
	assert.Equal(t, "scripts", SourceKind)
	assert.Equal(t, SourceKind, NewSource(nil).Kind())
	assert.Equal(t, SourceKind, NewSink(nil, "m").Kind())
}

// TestOnSucceededIsANoOp documents why: ranked search reads the vector from the
// scripts row on every query, so a backfill has no cache to refresh.
func TestOnSucceededIsANoOp(t *testing.T) {
	assert.NotPanics(t, func() { NewSource(nil).OnSucceeded("scr-1") })
}

func TestLoadItemsYieldsOneItemPerScript(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow(
			"Daily Sales Report", "daily-sales", "Summarize sales",
			"", pq.Array([]string{}), []byte(`[]`), ""),
	)

	items, err := NewSource(store).LoadItems(context.Background(), "scr-1")
	require.NoError(t, err)
	require.Len(t, items, 1, "a script is its own indexing unit; there is nothing to chunk")
	assert.Equal(t, "scr-1", items[0].ItemID)
	assert.Contains(t, items[0].Text, "Daily Sales Report")
}

func TestLoadItemsSurfacesAReadFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnError(errors.New("boom"))

	_, err := NewSource(store).LoadItems(context.Background(), "scr-1")
	require.Error(t, err)
}

func TestSinkReadsAndWritesThroughTheStore(t *testing.T) {
	store, mock := newMock(t)
	sink := NewSink(store, "nomic-embed-text")
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "scr-1"}

	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral([]float32{0.5}), "nomic-embed-text", []byte("hash")),
	)
	existing, err := sink.ListExisting(context.Background(), key)
	require.NoError(t, err)
	assert.Contains(t, existing, "scr-1")

	rows := []indexjobs.Vector{{ItemID: "scr-1", Embedding: []float32{0.5}, Model: "nomic-embed-text", TextHash: []byte("hash")}}
	mock.ExpectExec("UPDATE scripts").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.Upsert(context.Background(), key, rows))

	// UpsertBatch is the same write for a single-item unit: there are no sibling
	// rows outside the batch to preserve.
	mock.ExpectExec("UPDATE scripts").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, rows))
}

// TestStampExpectedRecordsNothing pins the condition-based gap model: coverage
// is a count over the table, so no per-unit expectation is stored.
func TestStampExpectedRecordsNothing(t *testing.T) {
	store, _ := newMock(t)
	// No DB call is expected; the mock would fail the test if one were made.
	require.NoError(t, NewSink(store, "m").StampExpected(context.Background(), indexjobs.Key{}, 3))
}

func TestSinkFindGapsDelegatesWithTheCurrentModel(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("nomic-embed-text").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow("scr-1"),
	)

	ids, err := NewSink(store, "nomic-embed-text").FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"scr-1"}, ids)
}

func TestSinkCoverageReportsAKnownExpectation(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnRows(
		sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(2, 4),
	)

	cov, err := NewSink(store, "m").Coverage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, indexjobs.Coverage{Indexed: 2, Expected: 4, ExpectedKnown: true}, cov)
}

func TestSinkCoverageSurfacesAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))

	_, err := NewSink(store, "m").Coverage(context.Background())
	require.Error(t, err)
}
