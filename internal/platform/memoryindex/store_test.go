package memoryindex

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewStore(db), mock, func() { _ = db.Close() }
}

// pgVecLiteral renders a []float32 in the pgvector text format so
// sqlmock's driver value round-trips through the pgvector.Vector scanner.
func pgVecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestStore_GetContent(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT content FROM memory_records").WithArgs("mem-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("the daily_sales table"))
	got, err := st.GetContent(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	if got != "the daily_sales table" {
		t.Errorf("content = %q", got)
	}
}

func TestStore_GetContent_ArchivedOrMissing(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT content FROM memory_records").WithArgs("gone").
		WillReturnError(sql.ErrNoRows)
	_, err := st.GetContent(context.Background(), "gone")
	if !errors.Is(err, errArchivedOrMissing) {
		t.Errorf("err = %v; want errArchivedOrMissing", err)
	}
}

func TestStore_GetContent_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT content FROM memory_records").
		WillReturnError(errors.New("boom"))
	if _, err := st.GetContent(context.Background(), "x"); err == nil {
		t.Error("GetContent should surface query error")
	}
}

func TestStore_ListVectors(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("FROM memory_records").WithArgs("mem-1").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral([]float32{1, 2, 3}), "nomic-embed-text", []byte("h1")))

	got, err := st.ListVectors(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	v, ok := got["mem-1"]
	if !ok {
		t.Fatalf("vectors = %+v; want key mem-1", got)
	}
	if v.Model != "nomic-embed-text" || v.Dim != 3 || v.ItemID != "mem-1" {
		t.Errorf("vector = %+v", v)
	}
}

func TestStore_ListVectors_NoEmbedding(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("FROM memory_records").WithArgs("mem-1").
		WillReturnError(sql.ErrNoRows)
	got, err := st.ListVectors(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map for an un-embedded record, got %+v", got)
	}
}

func TestStore_ListVectors_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("FROM memory_records").WillReturnError(errors.New("boom"))
	if _, err := st.ListVectors(context.Background(), "x"); err == nil {
		t.Error("ListVectors should surface query error")
	}
}

func TestStore_UpsertVectors(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("UPDATE memory_records").
		WithArgs("mem-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("h1")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rows := []indexjobs.Vector{{ItemID: "mem-1", Embedding: []float32{1, 2, 3}, Model: "nomic-embed-text", TextHash: []byte("h1"), Dim: 3}}
	if err := st.UpsertVectors(context.Background(), "mem-1", rows); err != nil {
		t.Fatalf("UpsertVectors: %v", err)
	}
}

func TestStore_UpsertVectors_EmptyNoop(t *testing.T) {
	t.Parallel()
	st, _, done := newMockStore(t)
	defer done()
	// No DB expectation: an empty row set must not issue an UPDATE.
	if err := st.UpsertVectors(context.Background(), "mem-1", nil); err != nil {
		t.Fatalf("UpsertVectors(nil): %v", err)
	}
}

func TestStore_UpsertVectors_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE memory_records").WillReturnError(errors.New("boom"))
	rows := []indexjobs.Vector{{ItemID: "mem-1", Embedding: []float32{1}}}
	if err := st.UpsertVectors(context.Background(), "mem-1", rows); err == nil {
		t.Error("UpsertVectors should surface exec error")
	}
}

func TestStore_FindGaps(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("FROM memory_records").WithArgs("nomic-embed-text").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("mem-null").AddRow("mem-stale"))
	ids, err := st.FindGaps(context.Background(), "nomic-embed-text")
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(ids) != 2 || ids[0] != "mem-null" || ids[1] != "mem-stale" {
		t.Errorf("ids = %v", ids)
	}
}

func TestStore_FindGaps_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("FROM memory_records").WillReturnError(errors.New("boom"))
	if _, err := st.FindGaps(context.Background(), "m"); err == nil {
		t.Error("FindGaps should surface query error")
	}
}

func TestStore_Coverage(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("FROM memory_records").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(7, 10))
	indexed, expected, err := st.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if indexed != 7 || expected != 10 {
		t.Errorf("indexed=%d expected=%d; want 7/10", indexed, expected)
	}
}

func TestStore_Coverage_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("FROM memory_records").WillReturnError(errors.New("boom"))
	if _, _, err := st.Coverage(context.Background()); err == nil {
		t.Error("Coverage should surface query error")
	}
}
