package graphqlindex

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func vectorStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		_ = db.Close()
	})
	return NewStore(db), mock
}

func vectorRow() []indexjobs.Vector {
	return []indexjobs.Vector{{
		ItemID: "query:a", TextHash: []byte{1, 2}, Embedding: []float32{1, 0}, Model: "m", Dim: 2,
	}}
}

func TestListVectorsKeysByOperation(t *testing.T) {
	store, mock := vectorStore(t)
	mock.ExpectQuery("SELECT operation_id, text_hash, embedding, model, dim").
		WithArgs("gql", "h1").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"}).
			AddRow("query:a", []byte{1}, pgvector.NewVector([]float32{1, 0}), "m", 2))
	got, err := store.ListVectors(context.Background(), "gql", "h1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got["query:a"].Model != "m" {
		t.Errorf("vectors = %+v", got)
	}
}

func TestListVectorsReportsEveryFailureShape(t *testing.T) {
	store, mock := vectorStore(t)
	mock.ExpectQuery("SELECT operation_id").WillReturnError(errors.New("boom"))
	if _, err := store.ListVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("a query failure was swallowed")
	}

	store2, mock2 := vectorStore(t)
	mock2.ExpectQuery("SELECT operation_id").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"}).
			AddRow("query:a", []byte{1}, "not a vector", "m", 2))
	if _, err := store2.ListVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("an unscannable row was swallowed")
	}

	store3, mock3 := vectorStore(t)
	mock3.ExpectQuery("SELECT operation_id").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"}).
			AddRow("query:a", []byte{1}, pgvector.NewVector([]float32{1}), "m", 1).
			RowError(0, errors.New("stream broke")))
	if _, err := store3.ListVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("a row-iteration failure was swallowed")
	}
}

func TestReplaceSwapsOneSchemaVersionsSetAtomically(t *testing.T) {
	store, mock := vectorStore(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM graphql_operation_embeddings").
		WithArgs("gql", "h1").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO graphql_operation_embeddings").
		WithArgs("gql", "h1", "query:a", []byte{1, 2}, sqlmock.AnyArg(), "m", 2).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.Replace(context.Background(), "gql", "h1", vectorRow()); err != nil {
		t.Fatalf("replace: %v", err)
	}
}

func TestReplaceReportsEveryFailureShape(t *testing.T) {
	cases := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{"begin fails", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errors.New("boom")) }},
		{"delete fails", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM").WillReturnError(errors.New("boom"))
			m.ExpectRollback()
		}},
		{"insert fails", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec("INSERT INTO").WillReturnError(errors.New("boom"))
			m.ExpectRollback()
		}},
		{"commit fails", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit().WillReturnError(errors.New("boom"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, mock := vectorStore(t)
			c.setup(mock)
			if err := store.Replace(context.Background(), "gql", "h1", vectorRow()); err == nil {
				t.Error("the failure was swallowed")
			}
		})
	}
}

func TestUpsertBatchWritesInPlace(t *testing.T) {
	store, mock := vectorStore(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO graphql_operation_embeddings").
		WithArgs("gql", "h1", "query:a", []byte{1, 2}, sqlmock.AnyArg(), "m", 2).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.UpsertBatch(context.Background(), "gql", "h1", vectorRow()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

func TestUpsertBatchOfNothingTouchesNothing(t *testing.T) {
	store, _ := vectorStore(t)
	if err := store.UpsertBatch(context.Background(), "gql", "h1", nil); err != nil {
		t.Errorf("an empty batch = %v", err)
	}
}

func TestUpsertBatchReportsItsFailures(t *testing.T) {
	store, mock := vectorStore(t)
	mock.ExpectBegin().WillReturnError(errors.New("boom"))
	if err := store.UpsertBatch(context.Background(), "gql", "h1", vectorRow()); err == nil {
		t.Error("a begin failure was swallowed")
	}

	store2, mock2 := vectorStore(t)
	mock2.ExpectBegin()
	mock2.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock2.ExpectCommit().WillReturnError(errors.New("boom"))
	if err := store2.UpsertBatch(context.Background(), "gql", "h1", vectorRow()); err == nil {
		t.Error("a commit failure was swallowed")
	}
}

func TestSinkReadsAndWritesTheConnectionsCurrentVersion(t *testing.T) {
	tk := newToolkit(t, "gql", "flat")
	source := NewSource(lister(tk))
	hash, items, ok := tk.IndexItems("gql")
	if !ok {
		t.Fatal("no schema")
	}
	store, mock := vectorStore(t)
	sink := NewSink(store, source)
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "gql"}

	mock.ExpectQuery("SELECT operation_id").WithArgs("gql", hash).
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"}))
	if _, err := sink.ListExisting(context.Background(), key); err != nil {
		t.Fatalf("list: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WithArgs("gql", hash).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := sink.Upsert(context.Background(), key, vectorRow()); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := sink.UpsertBatch(context.Background(), key, vectorRow()); err != nil {
		t.Fatalf("upsert batch: %v", err)
	}
	if len(items) == 0 {
		t.Error("the fixture exposes no operations")
	}
}

func TestFindGapsReportsAConnectionWhoseIndexHasDrifted(t *testing.T) {
	tk := newToolkit(t, "gql", "flat")
	source := NewSource(lister(tk))
	hash, items, _ := tk.IndexItems("gql")
	store, mock := vectorStore(t)
	sink := NewSink(store, source)

	// Nothing indexed yet: every operation is a gap.
	mock.ExpectQuery("SELECT operation_id").WithArgs("gql", hash).
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"}))
	gaps, err := sink.FindGaps(context.Background())
	if err != nil {
		t.Fatalf("find gaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != "gql" {
		t.Errorf("gaps = %v", gaps)
	}

	// Every operation indexed at its current text: no gap.
	rows := sqlmock.NewRows([]string{"operation_id", "text_hash", "embedding", "model", "dim"})
	for id, text := range items {
		rows.AddRow(id, indexjobs.TextHash(text), pgvector.NewVector([]float32{1}), "", 1)
	}
	mock.ExpectQuery("SELECT operation_id").WithArgs("gql", hash).WillReturnRows(rows)
	gaps, err = sink.FindGaps(context.Background())
	if err != nil {
		t.Fatalf("find gaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v; an index in step reports none", gaps)
	}
}

func TestFindGapsSkipsAConnectionWithNoSchemaAndReportsAReadFailure(t *testing.T) {
	source := NewSource(lister(newToolkit(t, "gql", "")))
	store, _ := vectorStore(t)
	gaps, err := NewSink(store, source).FindGaps(context.Background())
	if err != nil || len(gaps) != 0 {
		t.Errorf("a connection with no schema gave (%v, %v)", gaps, err)
	}

	tk := newToolkit(t, "gql", "flat")
	store2, mock2 := vectorStore(t)
	mock2.ExpectQuery("SELECT operation_id").WillReturnError(errors.New("boom"))
	if _, err := NewSink(store2, NewSource(lister(tk))).FindGaps(context.Background()); err == nil {
		t.Error("a read failure was swallowed")
	}
}
