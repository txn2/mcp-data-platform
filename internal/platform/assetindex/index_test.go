package assetindex

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

func pgVecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestSourceKindAndHooks(t *testing.T) {
	src := NewSource(nil)
	if src.Kind() != SourceKind || SourceKind != "portal-assets" {
		t.Errorf("kind = %q", src.Kind())
	}
	src.OnSucceeded("a-1") // no-op, must not panic
}

func TestStore_GetIndexText(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT name, description, tags FROM portal_assets").
		WithArgs("a-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "description", "tags"}).
			AddRow("Q4 Dashboard", "revenue", []byte(`["sales","q4"]`)))

	got, err := st.GetIndexText(context.Background(), "a-1")
	if err != nil {
		t.Fatalf("GetIndexText: %v", err)
	}
	if want := "Q4 Dashboard\nrevenue\nsales q4"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestStore_GetIndexText_NotIndexable(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT name, description, tags FROM portal_assets").
		WithArgs("gone").WillReturnError(sql.ErrNoRows)
	if _, err := st.GetIndexText(context.Background(), "gone"); !errors.Is(err, errNotIndexable) {
		t.Errorf("err = %v, want errNotIndexable", err)
	}
}

func TestStore_GetIndexText_BadTags(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT name, description, tags FROM portal_assets").
		WithArgs("a-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "description", "tags"}).
			AddRow("n", "d", []byte(`not json`)))
	if _, err := st.GetIndexText(context.Background(), "a-1"); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestSource_LoadItems(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT name, description, tags FROM portal_assets").
		WithArgs("a-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "description", "tags"}).
			AddRow("Name", "Desc", []byte(`[]`)))
	items, err := NewSource(st).LoadItems(context.Background(), "a-1")
	if err != nil || len(items) != 1 || items[0].ItemID != "a-1" || items[0].Text == "" {
		t.Fatalf("items = %+v, err = %v", items, err)
	}
}

func TestSource_LoadItems_NotIndexable(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT name, description, tags FROM portal_assets").
		WithArgs("gone").WillReturnError(sql.ErrNoRows)
	items, err := NewSource(st).LoadItems(context.Background(), "gone")
	if err != nil || items != nil {
		t.Errorf("want (nil,nil), got (%v,%v)", items, err)
	}
}

func TestSink_Delegates(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	sink := NewSink(st, "nomic-embed-text")
	if sink.Kind() != SourceKind {
		t.Errorf("kind = %q", sink.Kind())
	}
	if err := sink.StampExpected(context.Background(), indexjobs.Key{}, 3); err != nil {
		t.Errorf("StampExpected = %v", err)
	}
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "a-1"}

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM portal_assets").
		WithArgs("a-1").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral([]float32{0.1, 0.2}), "nomic-embed-text", []byte("h")))
	got, err := sink.ListExisting(context.Background(), key)
	if err != nil || got["a-1"].Dim != 2 {
		t.Errorf("ListExisting = %+v, %v", got, err)
	}

	mock.ExpectExec("UPDATE portal_assets").
		WithArgs("a-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	rows := []indexjobs.Vector{{ItemID: "a-1", Embedding: []float32{0.1}, Model: "nomic-embed-text", TextHash: []byte("h")}}
	if err := sink.Upsert(context.Background(), key, rows); err != nil {
		t.Errorf("Upsert = %v", err)
	}

	mock.ExpectExec("UPDATE portal_assets").
		WithArgs("a-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.UpsertBatch(context.Background(), key, rows); err != nil {
		t.Errorf("UpsertBatch = %v", err)
	}

	mock.ExpectQuery("SELECT id\\s+FROM portal_assets").
		WithArgs("nomic-embed-text").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a-1").AddRow("a-2"))
	gaps, err := sink.FindGaps(context.Background())
	if err != nil || len(gaps) != 2 {
		t.Errorf("FindGaps = %v, %v", gaps, err)
	}

	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))
	cov, err := sink.Coverage(context.Background())
	if err != nil || cov.Indexed != 3 || cov.Expected != 5 || !cov.ExpectedKnown {
		t.Errorf("coverage = %+v, %v", cov, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestStore_UpsertVectors_Empty(t *testing.T) {
	st, _, done := newMockStore(t)
	defer done()
	if err := st.UpsertVectors(context.Background(), "a-1", nil); err != nil {
		t.Errorf("UpsertVectors(nil) = %v", err)
	}
}

func TestSink_CoverageError(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("boom"))
	if _, err := NewSink(st, "m").Coverage(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_ErrorPaths(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM portal_assets").
		WithArgs("a-1").WillReturnError(errors.New("boom"))
	if _, err := st.ListVectors(context.Background(), "a-1"); err == nil {
		t.Error("ListVectors: expected error")
	}

	mock.ExpectQuery("SELECT id\\s+FROM portal_assets").
		WithArgs("m").WillReturnError(errors.New("boom"))
	if _, err := st.FindGaps(context.Background(), "m"); err == nil {
		t.Error("FindGaps: expected error")
	}

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("boom"))
	if _, _, err := st.Coverage(context.Background()); err == nil {
		t.Error("Coverage: expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
