package promptindex

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

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

// pgVecLiteral renders a []float32 in the pgvector text format so sqlmock's
// driver value round-trips through the pgvector.Vector scanner.
func pgVecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestStore_GetIndexText(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT display_name, name, description, content, tags FROM prompts").
		WithArgs("p-1").
		WillReturnRows(sqlmock.NewRows([]string{"display_name", "name", "description", "content", "tags"}).
			AddRow("Daily Sales", "daily-sales", "Summarize sales", "Analyze {date}", pq.Array([]string{"sales"})))

	got, err := st.GetIndexText(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("GetIndexText: %v", err)
	}
	want := "Daily Sales\nSummarize sales\nAnalyze {date}\nsales"
	if got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestStore_GetIndexText_NotIndexable(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT display_name, name, description, content, tags FROM prompts").
		WithArgs("gone").WillReturnError(sql.ErrNoRows)

	_, err := st.GetIndexText(context.Background(), "gone")
	if !errors.Is(err, errNotIndexable) {
		t.Errorf("err = %v, want errNotIndexable", err)
	}
}

func TestStore_GetIndexText_QueryError(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT display_name, name, description, content, tags FROM prompts").
		WithArgs("p-1").WillReturnError(errors.New("boom"))

	if _, err := st.GetIndexText(context.Background(), "p-1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_ListVectors(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	vec := []float32{0.1, 0.2, 0.3}
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM prompts").
		WithArgs("p-1").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral(vec), "nomic-embed-text", []byte("hash")))

	got, err := st.ListVectors(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	v, ok := got["p-1"]
	if !ok {
		t.Fatal("missing p-1 vector")
	}
	if v.Model != "nomic-embed-text" || v.Dim != 3 || len(v.Embedding) != 3 {
		t.Errorf("vector = %+v", v)
	}
}

func TestStore_ListVectors_NoEmbedding(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM prompts").
		WithArgs("p-1").WillReturnError(sql.ErrNoRows)

	got, err := st.ListVectors(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestStore_UpsertVectors(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("UPDATE prompts").
		WithArgs("p-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("hash")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := st.UpsertVectors(context.Background(), "p-1", []indexjobs.Vector{
		{ItemID: "p-1", Embedding: []float32{0.1, 0.2}, Model: "nomic-embed-text", TextHash: []byte("hash")},
	})
	if err != nil {
		t.Fatalf("UpsertVectors: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestStore_UpsertVectors_Empty(t *testing.T) {
	st, _, done := newMockStore(t)
	defer done()
	// No DB call expected for an empty row set.
	if err := st.UpsertVectors(context.Background(), "p-1", nil); err != nil {
		t.Errorf("UpsertVectors(nil) = %v", err)
	}
}

func TestStore_FindGaps(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT id\\s+FROM prompts").
		WithArgs("nomic-embed-text").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("p-1").AddRow("p-2"))

	ids, err := st.FindGaps(context.Background(), "nomic-embed-text")
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("ids = %v", ids)
	}
}

func TestStore_ListVectors_Error(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM prompts").
		WithArgs("p-1").WillReturnError(errors.New("boom"))
	if _, err := st.ListVectors(context.Background(), "p-1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_UpsertVectors_Error(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE prompts").
		WithArgs("p-1", sqlmock.AnyArg(), "m", []byte("h")).
		WillReturnError(errors.New("boom"))
	err := st.UpsertVectors(context.Background(), "p-1", []indexjobs.Vector{
		{ItemID: "p-1", Embedding: []float32{0.1}, Model: "m", TextHash: []byte("h")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_FindGaps_Error(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT id\\s+FROM prompts").
		WithArgs("m").WillReturnError(errors.New("boom"))
	if _, err := st.FindGaps(context.Background(), "m"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_FindGaps_ScanError(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	// A row whose value cannot scan into a string id exercises the scan-error
	// branch.
	rows := sqlmock.NewRows([]string{"id"}).AddRow("ok").RowError(0, errors.New("row boom"))
	mock.ExpectQuery("SELECT id\\s+FROM prompts").WithArgs("m").WillReturnRows(rows)
	if _, err := st.FindGaps(context.Background(), "m"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_Coverage_Error(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("boom"))
	if _, _, err := st.Coverage(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_Coverage(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))

	indexed, expected, err := st.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if indexed != 3 || expected != 5 {
		t.Errorf("indexed=%d expected=%d", indexed, expected)
	}
}
