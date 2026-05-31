package toolsindex

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestStore_ErrorPaths(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()
	boom := errors.New("boom")

	mock.ExpectQuery("FROM tool_embeddings").WillReturnError(boom)
	if _, err := st.ListVectors(ctx, SourceID); err == nil {
		t.Error("ListVectors should surface query error")
	}
	mock.ExpectQuery("ORDER BY embedding").WillReturnError(boom)
	if _, err := st.RankBySimilarity(ctx, SourceID, vec()); err == nil {
		t.Error("RankBySimilarity should surface query error")
	}
	mock.ExpectBegin().WillReturnError(boom)
	if err := st.Replace(ctx, SourceID, []indexjobs.Vector{{ItemID: "x", Embedding: vec()}}); err == nil {
		t.Error("Replace should surface begin error")
	}

	// Insert failure inside Replace's transaction (covers insertVectors
	// error path).
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM tool_embeddings").WithArgs(SourceID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnError(boom)
	if err := st.Replace(ctx, SourceID, []indexjobs.Vector{{ItemID: "x", Embedding: vec()}}); err == nil {
		t.Error("Replace should surface insert error")
	}

	// UpsertBatch begin + insert error paths.
	mock.ExpectBegin().WillReturnError(boom)
	if err := st.UpsertBatch(ctx, SourceID, []indexjobs.Vector{{ItemID: "x", Embedding: vec()}}); err == nil {
		t.Error("UpsertBatch should surface begin error")
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnError(boom)
	if err := st.UpsertBatch(ctx, SourceID, []indexjobs.Vector{{ItemID: "x", Embedding: vec()}}); err == nil {
		t.Error("UpsertBatch should surface insert error")
	}
}

func TestStore_Coverage(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("COUNT.*FROM tool_embeddings").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
	got, err := st.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if got != 42 {
		t.Errorf("Coverage = %d; want 42", got)
	}
}

func TestStore_CoverageError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("COUNT.*FROM tool_embeddings").WillReturnError(errors.New("boom"))
	if _, err := st.Coverage(context.Background()); err == nil {
		t.Error("Coverage should surface query error")
	}
}

func TestSink_Coverage(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("COUNT.*FROM tool_embeddings").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	cov, err := NewSink(st, nil).Coverage(context.Background())
	if err != nil {
		t.Fatalf("Sink.Coverage: %v", err)
	}
	if cov.Indexed != 5 || cov.ExpectedKnown {
		t.Errorf("coverage = %+v; want {Indexed 5, ExpectedKnown false}", cov)
	}
}

func TestSink_CoverageError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("COUNT.*FROM tool_embeddings").WillReturnError(errors.New("boom"))
	if _, err := NewSink(st, nil).Coverage(context.Background()); err == nil {
		t.Error("Sink.Coverage should surface store error")
	}
}

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewStore(db), mock, func() { _ = db.Close() }
}

// vec returns a fixed 3-element test vector.
func vec() []float32 { return []float32{1, 2, 3} }

func TestSink_KindAndDelegation(t *testing.T) {
	t.Parallel()
	s := NewSink(NewStore(nil), nil)
	if s.Kind() != SourceKind {
		t.Errorf("Kind() = %q; want %q", s.Kind(), SourceKind)
	}
}

func TestStore_ListVectors(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("FROM tool_embeddings").WithArgs(SourceID).
		WillReturnRows(sqlmock.NewRows([]string{"tool_name", "text_hash", "embedding", "model", "dim"}).
			AddRow("trino_query", []byte("h1"), pgVecLiteral(vec()), "m", 3).
			AddRow("s3_list_objects", []byte("h2"), pgVecLiteral(vec()), "m", 3))

	got, err := st.ListVectors(context.Background(), SourceID)
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	if len(got) != 2 || got["trino_query"].Model != "m" || got["trino_query"].Dim != 3 {
		t.Errorf("vectors = %+v", got)
	}
}

func TestStore_Replace(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM tool_embeddings").WithArgs(SourceID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rows := []indexjobs.Vector{{ItemID: "trino_query", TextHash: []byte("h"), Embedding: vec(), Model: "m", Dim: 3}}
	if err := st.Replace(context.Background(), SourceID, rows); err != nil {
		t.Fatalf("Replace: %v", err)
	}
}

func TestStore_UpsertBatchEmptyNoop(t *testing.T) {
	t.Parallel()
	st, _, done := newMockStore(t)
	defer done()
	// No DB expectations: an empty batch must not open a transaction.
	if err := st.UpsertBatch(context.Background(), SourceID, nil); err != nil {
		t.Fatalf("UpsertBatch(nil): %v", err)
	}
}

func TestStore_UpsertBatch(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	rows := []indexjobs.Vector{{ItemID: "x", TextHash: []byte("h"), Embedding: vec(), Model: "m", Dim: 3}}
	if err := st.UpsertBatch(context.Background(), SourceID, rows); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
}

// itemsFunc returns a CurrentItemsFunc yielding the given items, for
// the content-diff FindGaps tests.
func itemsFunc(items ...indexjobs.Item) CurrentItemsFunc {
	return func(context.Context) ([]indexjobs.Item, error) { return items, nil }
}

// vectorRows builds the mock tool_embeddings result for the given
// name->text pairs, hashing each text the same way the worker does so
// the gap check sees a match.
func vectorRows(pairs map[string]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"tool_name", "text_hash", "embedding", "model", "dim"})
	for name, text := range pairs {
		rows.AddRow(name, indexjobs.TextHash(text), pgVecLiteral(vec()), "m", 3)
	}
	return rows
}

func TestSink_FindGaps_InSyncReturnsEmpty(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	// Live corpus matches the persisted vectors by name and text hash:
	// no gap, so the reconciler enqueues nothing (issue #511).
	mock.ExpectQuery("FROM tool_embeddings").WithArgs(SourceID).
		WillReturnRows(vectorRows(map[string]string{"trino_query": "run sql", "s3_list": "list objects"}))
	sink := NewSink(st, itemsFunc(
		indexjobs.Item{ItemID: "trino_query", Text: "run sql"},
		indexjobs.Item{ItemID: "s3_list", Text: "list objects"},
	))
	gaps, err := sink.FindGaps(context.Background())
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v; want empty (in sync)", gaps)
	}
}

func TestSink_FindGaps_DetectsDrift(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		persisted map[string]string
		live      []indexjobs.Item
	}{
		{
			name:      "added tool",
			persisted: map[string]string{"a": "ta"},
			live:      []indexjobs.Item{{ItemID: "a", Text: "ta"}, {ItemID: "b", Text: "tb"}},
		},
		{
			name:      "removed tool",
			persisted: map[string]string{"a": "ta", "b": "tb"},
			live:      []indexjobs.Item{{ItemID: "a", Text: "ta"}},
		},
		{
			name:      "changed description",
			persisted: map[string]string{"a": "old text"},
			live:      []indexjobs.Item{{ItemID: "a", Text: "new text"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, mock, done := newMockStore(t)
			defer done()
			mock.ExpectQuery("FROM tool_embeddings").WithArgs(SourceID).
				WillReturnRows(vectorRows(tc.persisted))
			sink := NewSink(st, itemsFunc(tc.live...))
			gaps, err := sink.FindGaps(context.Background())
			if err != nil {
				t.Fatalf("FindGaps: %v", err)
			}
			if len(gaps) != 1 || gaps[0] != SourceID {
				t.Errorf("gaps = %v; want [%s] (drift detected)", gaps, SourceID)
			}
		})
	}
}

func TestSink_FindGaps_NilItemsFuncResyncs(t *testing.T) {
	t.Parallel()
	// A nil items provider is a wiring fault, not a steady state, so
	// FindGaps fails safe by re-syncing rather than reporting no gap.
	sink := NewSink(NewStore(nil), nil)
	gaps, err := sink.FindGaps(context.Background())
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != SourceID {
		t.Errorf("gaps = %v; want [%s] (nil-items fallback)", gaps, SourceID)
	}
}

func TestSink_FindGaps_ErrorPaths(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	t.Run("load items error", func(t *testing.T) {
		t.Parallel()
		st, _, done := newMockStore(t)
		defer done()
		sink := NewSink(st, func(context.Context) ([]indexjobs.Item, error) { return nil, boom })
		if _, err := sink.FindGaps(context.Background()); err == nil {
			t.Error("FindGaps should surface a load-items error")
		}
	})
	t.Run("list vectors error", func(t *testing.T) {
		t.Parallel()
		st, mock, done := newMockStore(t)
		defer done()
		mock.ExpectQuery("FROM tool_embeddings").WillReturnError(boom)
		sink := NewSink(st, itemsFunc(indexjobs.Item{ItemID: "a", Text: "ta"}))
		if _, err := sink.FindGaps(context.Background()); err == nil {
			t.Error("FindGaps should surface a list-vectors error")
		}
	})
}

func TestStore_RankBySimilarity(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("ORDER BY embedding").
		WillReturnRows(sqlmock.NewRows([]string{"tool_name", "score"}).
			AddRow("trino_query", 0.91).
			AddRow("s3_list_objects", 0.42))
	got, err := st.RankBySimilarity(context.Background(), SourceID, vec())
	if err != nil {
		t.Fatalf("RankBySimilarity: %v", err)
	}
	if len(got) != 2 || got[0].ToolName != "trino_query" || got[0].Score <= got[1].Score {
		t.Errorf("ranked = %+v; want trino_query first with higher score", got)
	}
}

// pgVecLiteral renders a []float32 in the pgvector text format
// ("[1,2,3]") so sqlmock's driver value round-trips through the
// pgvector.Vector scanner.
func pgVecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestSink_Delegation(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	sink := NewSink(st, nil)
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: SourceID}
	ctx := context.Background()

	mock.ExpectQuery("FROM tool_embeddings").WithArgs(SourceID).
		WillReturnRows(sqlmock.NewRows([]string{"tool_name", "text_hash", "embedding", "model", "dim"}))
	if _, err := sink.ListExisting(ctx, key); err != nil {
		t.Fatalf("ListExisting: %v", err)
	}

	rows := []indexjobs.Vector{{ItemID: "x", TextHash: []byte("h"), Embedding: vec(), Model: "m", Dim: 3}}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM tool_embeddings").WithArgs(SourceID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := sink.Upsert(ctx, key, rows); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO tool_embeddings").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := sink.UpsertBatch(ctx, key, rows); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	// StampExpected is a no-op (no DB). FindGaps here uses the nil-items
	// fallback (this sink was built with nil currentItems), so it
	// re-syncs without touching the DB; the content-diff paths are
	// covered by the dedicated TestSink_FindGaps_* tests.
	if err := sink.StampExpected(ctx, key, 5); err != nil {
		t.Fatalf("StampExpected: %v", err)
	}
	gaps, err := sink.FindGaps(ctx)
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != SourceID {
		t.Errorf("FindGaps = %v; want [%s]", gaps, SourceID)
	}
}
