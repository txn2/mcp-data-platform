package datasetindex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

func vec() []float32 { return []float32{0.1, 0.2} }

func TestSyncNormalizesNilTags(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectBegin()
	// A nil tag slice must bind an empty array, not SQL NULL: the column is NOT
	// NULL, so binding nil is the error class the real-DB gate exists for.
	mock.ExpectExec("INSERT INTO catalog_datasets").
		WithArgs("urn:a", "orders", "desc", sqlmock.AnyArg(), "Revenue").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, store.Sync(context.Background(), []Entry{
		{URN: "urn:a", Name: "orders", Description: "desc", Domain: "Revenue"},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncEmptyIsNoop(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	require.NoError(t, store.Sync(context.Background(), nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncErrorPaths(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	boom := errors.New("boom")
	entries := []Entry{{URN: "urn:a"}}

	mock.ExpectBegin().WillReturnError(boom)
	require.Error(t, store.Sync(ctx, entries))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO catalog_datasets").WillReturnError(boom)
	require.Error(t, store.Sync(ctx, entries))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(boom)
	require.Error(t, store.Sync(ctx, entries))

	mock.ExpectExec("INSERT INTO catalog_dataset_sync").WillReturnError(boom)
	require.Error(t, store.StampSync(ctx))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListVectors(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("FROM catalog_datasets").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "embedding_text_hash", "embedding", "embedding_model"}).
			AddRow("urn:a", []byte("hash"), pgvector.NewVector(vec()), "model-x"))

	got, err := store.ListVectors(context.Background())
	require.NoError(t, err)
	require.Contains(t, got, "urn:a")
	assert.Equal(t, 2, got["urn:a"].Dim, "dim is derived from the stored vector so the dedup pass can compare it")
	assert.Equal(t, "model-x", got["urn:a"].Model)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListVectorsErrorPaths(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	boom := errors.New("boom")

	mock.ExpectQuery("FROM catalog_datasets").WillReturnError(boom)
	_, err := store.ListVectors(context.Background())
	require.Error(t, err)

	mock.ExpectQuery("FROM catalog_datasets").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "embedding_text_hash", "embedding", "embedding_model"}).
			AddRow("urn:a", []byte("h"), "not-a-vector", "m"))
	_, err = store.ListVectors(context.Background())
	require.Error(t, err, "a malformed vector column is a scan error, not a silent empty result")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertVectors(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	rows := []indexjobs.Vector{{ItemID: "urn:a", Embedding: vec(), Model: "model-x", TextHash: []byte("h")}}

	require.NoError(t, store.UpsertVectors(ctx, nil), "an empty chunk writes nothing")

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE catalog_datasets").
		WithArgs("urn:a", pgvector.NewVector(vec()), "model-x", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, store.UpsertVectors(ctx, rows))

	boom := errors.New("boom")
	mock.ExpectBegin().WillReturnError(boom)
	require.Error(t, store.UpsertVectors(ctx, rows))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnError(boom)
	require.Error(t, store.UpsertVectors(ctx, rows))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(boom)
	require.Error(t, store.UpsertVectors(ctx, rows))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceVectorsPrunes(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets WHERE NOT").
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, store.ReplaceVectors(ctx, []indexjobs.Vector{
		{ItemID: "urn:a", Embedding: vec(), Model: "m", TextHash: []byte("h")},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceVectorsEmptySetClearsMirror(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	// The Source fails the job rather than reporting a partial corpus, so an
	// empty set means the catalog is empty and the mirror must follow.
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	require.NoError(t, store.ReplaceVectors(context.Background(), nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceVectorsErrorPaths(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	boom := errors.New("boom")
	rows := []indexjobs.Vector{{ItemID: "urn:a", Embedding: vec()}}

	mock.ExpectBegin().WillReturnError(boom)
	require.Error(t, store.ReplaceVectors(ctx, rows))

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets WHERE NOT").WillReturnError(boom)
	require.Error(t, store.ReplaceVectors(ctx, rows))

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets").WillReturnError(boom)
	require.Error(t, store.ReplaceVectors(ctx, nil))

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets WHERE NOT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(boom)
	require.Error(t, store.ReplaceVectors(ctx, rows))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNeedsSweep(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()

	mock.ExpectQuery("catalog_dataset_sync").
		WithArgs(1800.0, "model-x").
		WillReturnRows(sqlmock.NewRows([]string{"needs"}).AddRow(true))
	needs, err := store.NeedsSweep(ctx, "model-x", 30*time.Minute)
	require.NoError(t, err)
	assert.True(t, needs)

	mock.ExpectQuery("catalog_dataset_sync").WillReturnError(errors.New("boom"))
	_, err = store.NeedsSweep(ctx, "model-x", time.Minute)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCoverage(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))
	indexed, expected, err := store.Coverage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, indexed)
	assert.Equal(t, 5, expected)

	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
	_, _, err = store.Coverage(context.Background())
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSinkDelegates(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	sink := NewSink(store, "model-x", 30*time.Minute)
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: SourceID}

	mock.ExpectQuery("FROM catalog_datasets").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "embedding_text_hash", "embedding", "embedding_model"}))
	existing, err := sink.ListExisting(ctx, key)
	require.NoError(t, err)
	assert.Empty(t, existing)

	require.NoError(t, sink.StampExpected(ctx, key, 7), "the expected count lives in DataHub, not in a stamp")

	rows := []indexjobs.Vector{{ItemID: "urn:a", Embedding: vec(), Model: "model-x", TextHash: []byte("h")}}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, sink.UpsertBatch(ctx, key, rows))

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM catalog_datasets WHERE NOT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, sink.Upsert(ctx, key, rows))

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(1, 1))
	cov, err := sink.Coverage(ctx)
	require.NoError(t, err)
	assert.True(t, cov.ExpectedKnown)
	assert.Equal(t, 1, cov.Indexed)

	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
	_, err = sink.Coverage(ctx)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSinkFindGaps(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	sink := NewSink(store, "model-x", time.Hour)

	// A corpus that owes work is reported as the one corpus unit; this is also
	// how the periodic re-enumeration is scheduled, with no goroutine of its own.
	mock.ExpectQuery("catalog_dataset_sync").
		WillReturnRows(sqlmock.NewRows([]string{"needs"}).AddRow(true))
	gaps, err := sink.FindGaps(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{SourceID}, gaps)

	mock.ExpectQuery("catalog_dataset_sync").
		WillReturnRows(sqlmock.NewRows([]string{"needs"}).AddRow(false))
	gaps, err = sink.FindGaps(ctx)
	require.NoError(t, err)
	assert.Empty(t, gaps, "a fresh, fully embedded corpus is not re-enumerated")

	mock.ExpectQuery("catalog_dataset_sync").WillReturnError(errors.New("boom"))
	_, err = sink.FindGaps(ctx)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchCatalogIndexLexical(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("ts_rank_cd").
		WithArgs("refund policy").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "name", "description", "lex_rank"}).
			AddRow("urn:a", "sales.orders", "Refunds are netted at close of month.", 0.42))

	hits, err := store.SearchCatalogIndex(context.Background(), knowledge.CatalogIndexQuery{
		QueryText: "refund policy",
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "urn:a", hits[0].URN)
	assert.InDelta(t, 0.42, hits[0].Score, 0.0001)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchCatalogIndexHybridFusesAndDedups(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	// The same dataset comes back from both UNION arms; it must be emitted once,
	// at the higher fused score.
	mock.ExpectQuery("UNION ALL").
		WithArgs(pgvector.NewVector(vec()), "refund policy").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "name", "description", "vec_score", "lex_match"}).
			AddRow("urn:a", "sales.orders", "Refunds are netted.", 0.9, false).
			AddRow("urn:a", "sales.orders", "Refunds are netted.", 0.9, true).
			AddRow("urn:b", "sales.other", "", 0.2, false))

	hits, err := store.SearchCatalogIndex(context.Background(), knowledge.CatalogIndexQuery{
		QueryText: "refund policy",
		Embedding: vec(),
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "urn:a", hits[0].URN)
	assert.Greater(t, hits[0].Score, hits[1].Score)
	// alpha*((0.9+1)/2) + (1-alpha)*1 with alpha = 0.6.
	assert.InDelta(t, 0.97, hits[0].Score, 0.0001)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchCatalogIndexLimitClamped(t *testing.T) {
	t.Parallel()
	assert.Equal(t, defaultSearchLimit, effectiveLimit(0))
	assert.Equal(t, defaultSearchLimit, effectiveLimit(maxSearchLimit+1))
	assert.Equal(t, 5, effectiveLimit(5))
}

func TestSearchCatalogIndexTruncatesToLimit(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	rows := sqlmock.NewRows([]string{"urn", "name", "description", "vec_score", "lex_match"}).
		AddRow("urn:a", "a", "", 0.9, true).
		AddRow("urn:b", "b", "", 0.8, true)
	mock.ExpectQuery("UNION ALL").WillReturnRows(rows)

	hits, err := store.SearchCatalogIndex(context.Background(), knowledge.CatalogIndexQuery{
		QueryText: "q", Embedding: vec(), Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "urn:a", hits[0].URN)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchCatalogIndexErrorPaths(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	boom := errors.New("boom")

	mock.ExpectQuery("UNION ALL").WillReturnError(boom)
	_, err := store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "q", Embedding: vec()})
	require.Error(t, err)

	mock.ExpectQuery("UNION ALL").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "name", "description", "vec_score", "lex_match"}).
			AddRow("urn:a", "a", "", "not-a-float", true))
	_, err = store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "q", Embedding: vec()})
	require.Error(t, err)

	mock.ExpectQuery("ts_rank_cd").WillReturnError(boom)
	_, err = store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "q"})
	require.Error(t, err)

	mock.ExpectQuery("ts_rank_cd").
		WillReturnRows(sqlmock.NewRows([]string{"urn", "name", "description", "lex_rank"}).
			AddRow("urn:a", "a", "", "not-a-float"))
	_, err = store.SearchCatalogIndex(ctx, knowledge.CatalogIndexQuery{QueryText: "q"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
