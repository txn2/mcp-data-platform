package scriptindex

import (
	"context"
	"errors"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestKindIsTheScriptsSourceKind pins the string the queue routes on: a Source
// and a Sink that disagree would fail registration, and a kind that drifts
// would strand every job already in the table.
func TestKindIsTheScriptsSourceKind(t *testing.T) {
	assert.Equal(t, "scripts", SourceKind)
	assert.Equal(t, SourceKind, NewSource(nil, 6000).Kind())
	assert.Equal(t, SourceKind, NewSink(nil, "m").Kind())
}

// TestOnSucceededIsANoOp documents why: ranked search reads the chunk table on
// every query, so a backfill has no cache to refresh.
func TestOnSucceededIsANoOp(t *testing.T) {
	assert.NotPanics(t, func() { NewSource(nil, 6000).OnSucceeded("scr-1") })
}

func TestRegisterConsumerRegistersTheScriptsPair(t *testing.T) {
	reg := &fakeRegistry{}
	require.NoError(t, RegisterConsumer(reg, nil, "m", 6000))
	require.Len(t, reg.sources, 1)
	assert.Equal(t, SourceKind, reg.sources[0].Kind())

	reg.err = errors.New("taken")
	require.Error(t, RegisterConsumer(reg, nil, "m", 6000))
}

type fakeRegistry struct {
	sources []indexjobs.Source
	err     error
}

func (f *fakeRegistry) Register(src indexjobs.Source, _ indexjobs.Sink) error {
	if f.err != nil {
		return f.err
	}
	f.sources = append(f.sources, src)
	return nil
}

// TestLoadItemsYieldsTheCardThenTheSource proves a script is indexed on its
// source as well as its card (#2027): the card is the first item and the
// source follows, each within the input, and a phrase found only in a comment
// reaches an item.
func TestLoadItemsYieldsTheCardThenTheSource(t *testing.T) {
	store, mock := newMock(t)
	source := "# churn counts a customer gone ninety days\n" + strings.Repeat("x = 1\n", 200)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow(
			"Daily Sales Report", "daily-sales", "Summarize sales",
			"", pq.Array([]string{}), []byte(`[]`), "active", "", false, source),
	)

	items, err := NewSource(store, 400).LoadItems(context.Background(), "scr-1")
	require.NoError(t, err)
	require.Greater(t, len(items), 2)
	assert.Equal(t, "scr-1:0", items[0].ItemID)
	assert.Contains(t, items[0].Text, "Daily Sales Report")
	assert.Equal(t, "scr-1:1", items[1].ItemID)
	assert.Contains(t, items[1].Text, "ninety days")
	for _, it := range items {
		assert.LessOrEqual(t, len(it.Text), 400)
	}

	v, ok := store.loaded.Load("scr-1")
	require.True(t, ok, "the hash of what was read is kept for the stamp")
	sc := &script.Script{
		DisplayName: "Daily Sales Report", Name: "daily-sales", Description: "Summarize sales",
		Tags: []string{}, Params: []script.Param{}, Status: "active", Enabled: true, Source: source,
	}
	assert.Equal(t, indexjobs.TextHash(Corpus(sc)), v)
}

func TestLoadItemsTreatsAMissingScriptAsNothingToIndex(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(sqlmock.NewRows(textColumns))

	items, err := NewSource(store, 6000).LoadItems(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestLoadItemsSurfacesAReadFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnError(errors.New("boom"))

	_, err := NewSource(store, 6000).LoadItems(context.Background(), "scr-1")
	require.Error(t, err)
}

func TestSinkReadsAndWritesThroughTheChunkTable(t *testing.T) {
	store, mock := newMock(t)
	sink := NewSink(store, "nomic-embed-text")
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "scr-1"}

	mock.ExpectQuery("FROM script_embedding_chunks").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}).
			AddRow(0, []byte("h0"), pgVecLiteral([]float32{0.5}), "nomic-embed-text").
			AddRow(1, []byte("h1"), pgVecLiteral([]float32{0.25}), "nomic-embed-text"),
	)
	existing, err := sink.ListExisting(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, existing, 2)
	assert.Equal(t, []byte("h1"), existing["scr-1:1"].TextHash)
	assert.Equal(t, 1, existing["scr-1:1"].Dim)

	rows := []indexjobs.Vector{
		{ItemID: "scr-1:0", Embedding: []float32{0.5}, Model: "nomic-embed-text", TextHash: []byte("h0")},
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO script_embedding_chunks").
		WithArgs("scr-1", 0, []byte("h0"), sqlmock.AnyArg(), "nomic-embed-text", 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM script_embedding_chunks").
		WithArgs("scr-1", pq.Array([]int{0})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, sink.Upsert(context.Background(), key, rows))

	mock.ExpectExec("INSERT INTO script_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, rows))
}

// TestUpsertClearsAScriptWithNoChunks is how a disabled script's vectors go:
// an empty set deletes every chunk.
func TestUpsertClearsAScriptWithNoChunks(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM script_embedding_chunks").
		WithArgs("scr-1", pq.Array([]int{})).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	require.NoError(t, store.ReplaceVectors(context.Background(), "scr-1", nil))
}

func TestReplaceAndUpsertRefuseAnItemOfAnotherScript(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectRollback()
	err := store.ReplaceVectors(context.Background(), "scr-1", []indexjobs.Vector{{ItemID: "scr-2:0"}})
	require.ErrorContains(t, err, "is not a chunk of script")

	require.ErrorContains(t, store.UpsertVectors(context.Background(), "scr-1",
		[]indexjobs.Vector{{ItemID: "scr-1:x"}}), "has no chunk index")
}

func TestReplaceSurfacesWriteFailures(t *testing.T) {
	store, mock := newMock(t)
	rows := []indexjobs.Vector{{ItemID: "scr-1:0", Embedding: []float32{1}}}

	mock.ExpectBegin().WillReturnError(errors.New("no conn"))
	require.Error(t, store.ReplaceVectors(context.Background(), "scr-1", rows))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO script_embedding_chunks").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	require.Error(t, store.ReplaceVectors(context.Background(), "scr-1", rows))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO script_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM script_embedding_chunks").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	require.Error(t, store.ReplaceVectors(context.Background(), "scr-1", rows))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO script_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM script_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(errors.New("boom"))
	require.Error(t, store.ReplaceVectors(context.Background(), "scr-1", rows))

	mock.ExpectExec("INSERT INTO script_embedding_chunks").WillReturnError(errors.New("boom"))
	require.Error(t, store.UpsertVectors(context.Background(), "scr-1", rows))
}

// TestStampExpectedRecordsWhatWasEmbedded pins the convergence marker: the
// model and the hash of the text LoadItems read, never a re-read of the row,
// so a save that lands mid-pass leaves the script owing an embedding.
func TestStampExpectedRecordsWhatWasEmbedded(t *testing.T) {
	store, mock := newMock(t)
	sink := NewSink(store, "m")
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "scr-1"}

	// Nothing was read for this script (it was disabled): nothing to record.
	require.NoError(t, sink.StampExpected(context.Background(), key, 0))

	store.loaded.Store("scr-1", []byte("hash"))
	mock.ExpectExec("UPDATE scripts").WithArgs("scr-1", "m", []byte("hash")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.StampExpected(context.Background(), key, 2))
	_, still := store.loaded.Load("scr-1")
	assert.False(t, still, "a stamp consumes the hash it records")

	store.loaded.Store("scr-1", []byte("hash"))
	mock.ExpectExec("UPDATE scripts").WillReturnError(errors.New("boom"))
	require.Error(t, sink.StampExpected(context.Background(), key, 2))
}

func TestSinkFindGapsDelegatesWithTheCurrentModel(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("index_embedded_hash IS DISTINCT FROM index_text_hash").WithArgs("nomic-embed-text").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow("scr-1"),
	)

	ids, err := NewSink(store, "nomic-embed-text").FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"scr-1"}, ids)
}

func TestSinkCoverageReportsAKnownExpectation(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("m").WillReturnRows(
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

// TestLoadItemsOfAScriptGoneClearsAnEarlierHash keeps a hash a failed pass
// read from being stamped by a later pass that built nothing.
func TestLoadItemsOfAScriptGoneClearsAnEarlierHash(t *testing.T) {
	store, mock := newMock(t)
	store.loaded.Store("scr-1", []byte("stale"))
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(sqlmock.NewRows(textColumns))

	_, err := NewSource(store, 6000).LoadItems(context.Background(), "scr-1")
	require.NoError(t, err)
	_, held := store.loaded.Load("scr-1")
	assert.False(t, held)
	require.NoError(t, NewSink(store, "m").StampExpected(context.Background(),
		indexjobs.Key{SourceKind: SourceKind, SourceID: "scr-1"}, 0), "nothing to stamp, no write")
}
