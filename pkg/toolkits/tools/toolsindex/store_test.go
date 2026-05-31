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
	s := NewSink(NewStore(nil))
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

func TestStore_FindGaps_AlwaysReturnsSource(t *testing.T) {
	t.Parallel()
	st, _, done := newMockStore(t)
	defer done()
	// No DB query: tools always re-syncs, so FindGaps unconditionally
	// returns the single source.
	gaps, err := st.FindGaps(context.Background())
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != SourceID {
		t.Errorf("gaps = %v; want [%s]", gaps, SourceID)
	}
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
	sink := NewSink(st)
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

	// StampExpected is a no-op (no DB); FindGaps always returns the source.
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
