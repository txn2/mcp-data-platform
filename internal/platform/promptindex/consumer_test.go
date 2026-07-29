package promptindex

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestSourceKindAndHooks(t *testing.T) {
	src := NewSource(nil)
	if src.Kind() != SourceKind || SourceKind != "prompts" {
		t.Errorf("kind = %q", src.Kind())
	}
	src.OnSucceeded("p-1") // no-op, must not panic
}

func TestSource_LoadItems(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	src := NewSource(st)

	mock.ExpectQuery("SELECT display_name, name, description, content, tags FROM prompts").
		WithArgs("p-1").
		WillReturnRows(sqlmock.NewRows([]string{"display_name", "name", "description", "content", "tags"}).
			AddRow("Title", "name", "Desc", "Body", pq.Array([]string{})))

	items, err := src.LoadItems(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 || items[0].ItemID != "p-1" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Text == "" {
		t.Error("empty item text")
	}
}

func TestSource_LoadItems_NotIndexable(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	src := NewSource(st)

	mock.ExpectQuery("SELECT display_name, name, description, content, tags FROM prompts").
		WithArgs("gone").WillReturnError(sql.ErrNoRows)

	items, err := src.LoadItems(context.Background(), "gone")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if items != nil {
		t.Errorf("want nil items for a non-indexable prompt, got %+v", items)
	}
}

func TestSink_CoverageError(t *testing.T) {
	st, mock, done := newMockStore(t)
	defer done()
	sink := NewSink(st, "m")
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("boom"))
	if _, err := sink.Coverage(context.Background()); err == nil {
		t.Fatal("expected error")
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

	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "p-1"}

	// ListExisting -> ListVectors
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM prompts").
		WithArgs("p-1").WillReturnError(sql.ErrNoRows)
	if _, err := sink.ListExisting(context.Background(), key); err != nil {
		t.Errorf("ListExisting = %v", err)
	}

	// Upsert -> UpsertVectors
	mock.ExpectExec("UPDATE prompts").
		WithArgs("p-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	rows := []indexjobs.Vector{{ItemID: "p-1", Embedding: []float32{0.1}, Model: "nomic-embed-text", TextHash: []byte("h")}}
	if err := sink.Upsert(context.Background(), key, rows); err != nil {
		t.Errorf("Upsert = %v", err)
	}

	// UpsertBatch -> UpsertVectors
	mock.ExpectExec("UPDATE prompts").
		WithArgs("p-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.UpsertBatch(context.Background(), key, rows); err != nil {
		t.Errorf("UpsertBatch = %v", err)
	}

	// FindGaps -> store.FindGaps
	mock.ExpectQuery("SELECT id\\s+FROM prompts").
		WithArgs("nomic-embed-text").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("p-1"))
	gaps, err := sink.FindGaps(context.Background())
	if err != nil || len(gaps) != 1 {
		t.Errorf("FindGaps = %v, %v", gaps, err)
	}

	// Coverage -> store.Coverage
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(1, 2))
	cov, err := sink.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage = %v", err)
	}
	if cov.Indexed != 1 || cov.Expected != 2 || !cov.ExpectedKnown {
		t.Errorf("coverage = %+v", cov)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
