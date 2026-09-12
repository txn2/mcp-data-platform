package graphqlstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"

	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

func newStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
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
	return New(db), mock
}

// gzipped renders SDL the way PutSchema stores it, so a read test can be
// seeded with a row of the real shape.
func gzipped(t *testing.T, sdl string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(sdl)); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestPutAndGetSchemaRoundTrip(t *testing.T) {
	store, mock := newStore(t)
	const sdl = "type Query { health: String }"
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	mock.ExpectExec("INSERT INTO graphql_connection_schemas").
		WithArgs("gql", "abc", sqlmock.AnyArg(), graphqlkit.SchemaSourceIntrospection, at, "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.PutSchema(context.Background(), graphqlkit.StoredSchema{
		Connection: "gql", Hash: "abc", SDL: sdl,
		Source: graphqlkit.SchemaSourceIntrospection, FetchedAt: at,
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	mock.ExpectQuery("SELECT schema_hash, sdl_gzip, source, fetched_at").
		WithArgs("gql").
		WillReturnRows(sqlmock.NewRows([]string{"schema_hash", "sdl_gzip", "source", "fetched_at", "read_error"}).
			AddRow("abc", gzipped(t, sdl), graphqlkit.SchemaSourceIntrospection, at, "HTTP 302"))
	got, err := store.GetSchema(context.Background(), "gql")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SDL != sdl || got.Hash != "abc" || got.Source != graphqlkit.SchemaSourceIntrospection {
		t.Errorf("schema = %+v", got)
	}
	if got.ReadError != "HTTP 302" {
		t.Errorf("read error = %q; the refusal stored beside the schema was not read back", got.ReadError)
	}
	if !got.FetchedAt.Equal(at) {
		t.Errorf("fetched at = %v", got.FetchedAt)
	}
}

func TestGetSchemaReportsTheAbsentCaseDistinctly(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("SELECT schema_hash").WithArgs("gql").WillReturnError(sql.ErrNoRows)
	_, err := store.GetSchema(context.Background(), "gql")
	if !errors.Is(err, graphqlkit.ErrSchemaNotFound) {
		t.Errorf("err = %v; a connection with no schema is not a failure", err)
	}
}

func TestGetSchemaReportsAReadFailure(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("SELECT schema_hash").WithArgs("gql").WillReturnError(errors.New("connection refused"))
	if _, err := store.GetSchema(context.Background(), "gql"); err == nil {
		t.Error("a read failure was swallowed")
	}
}

func TestGetSchemaRefusesACorruptedRow(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("SELECT schema_hash").WithArgs("gql").
		WillReturnRows(sqlmock.NewRows([]string{"schema_hash", "sdl_gzip", "source", "fetched_at", "read_error"}).
			AddRow("abc", []byte("not gzip"), "upload", time.Now(), ""))
	if _, err := store.GetSchema(context.Background(), "gql"); err == nil {
		t.Error("a row that is not compressed schema text was accepted")
	}
}

func TestPutSchemaReportsAWriteFailure(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("INSERT INTO graphql_connection_schemas").WillReturnError(errors.New("disk full"))
	err := store.PutSchema(context.Background(), graphqlkit.StoredSchema{Connection: "gql", SDL: "type Query { a: String }"})
	if err == nil {
		t.Error("a write failure was swallowed")
	}
}

func TestRecordReadErrorLeavesTheSchemaAlone(t *testing.T) {
	store, mock := newStore(t)
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(`UPDATE graphql_connection_schemas\s+SET read_error = \$4, updated_at = NOW\(\)\s+WHERE connection = \$1 AND schema_hash = \$2 AND fetched_at = \$3`).
		WithArgs("gql", "abc", at, "HTTP 302").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.RecordReadError(context.Background(), graphqlkit.StoredSchema{
		Connection: "gql", Hash: "abc", FetchedAt: at, ReadError: "HTTP 302",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestRecordReadErrorReportsAWriteFailure(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("UPDATE graphql_connection_schemas").WillReturnError(errors.New("disk full"))
	if err := store.RecordReadError(context.Background(), graphqlkit.StoredSchema{Connection: "gql"}); err == nil {
		t.Error("a write failure was swallowed")
	}
}

func TestDeleteSchemaAlsoDropsTheIndexBuiltFromIt(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("DELETE FROM graphql_operation_embeddings").WithArgs("gql").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM graphql_connection_schemas").WithArgs("gql").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.DeleteSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestDeleteSchemaReportsEitherFailure(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("DELETE FROM graphql_operation_embeddings").WillReturnError(errors.New("boom"))
	if err := store.DeleteSchema(context.Background(), "gql"); err == nil {
		t.Error("an embeddings delete failure was swallowed")
	}

	store2, mock2 := newStore(t)
	mock2.ExpectExec("DELETE FROM graphql_operation_embeddings").WillReturnResult(sqlmock.NewResult(0, 0))
	mock2.ExpectExec("DELETE FROM graphql_connection_schemas").WillReturnError(errors.New("boom"))
	if err := store2.DeleteSchema(context.Background(), "gql"); err == nil {
		t.Error("a schema delete failure was swallowed")
	}
}

func TestLoadVectorsKeysByOperation(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("SELECT operation_id, embedding").
		WithArgs("gql", "hash1").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "embedding"}).
			AddRow("query:a", pgvector.NewVector([]float32{1, 0})).
			AddRow("query:b", pgvector.NewVector([]float32{0, 1})))
	got, err := store.LoadVectors(context.Background(), "gql", "hash1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || len(got["query:a"]) != 2 {
		t.Errorf("vectors = %v", got)
	}
}

func TestLoadVectorsReportsAFailure(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("SELECT operation_id, embedding").WillReturnError(errors.New("boom"))
	if _, err := store.LoadVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("a query failure was swallowed")
	}

	store2, mock2 := newStore(t)
	mock2.ExpectQuery("SELECT operation_id, embedding").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "embedding"}).
			AddRow("query:a", "not a vector"))
	if _, err := store2.LoadVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("an unscannable row was swallowed")
	}

	store3, mock3 := newStore(t)
	mock3.ExpectQuery("SELECT operation_id, embedding").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "embedding"}).
			AddRow("query:a", pgvector.NewVector([]float32{1})).RowError(0, errors.New("stream broke")))
	if _, err := store3.LoadVectors(context.Background(), "gql", "h"); err == nil {
		t.Error("a row-iteration failure was swallowed")
	}
}

func TestDecompressBoundsWhatItReadsBack(t *testing.T) {
	// A row this package wrote is bounded by the schema it came from; the
	// cap is the guard on a row that is not.
	sdl, err := decompress(gzipped(t, "type Query { a: String }"))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if sdl != "type Query { a: String }" {
		t.Errorf("sdl = %q", sdl)
	}
	if _, err := decompress([]byte{0x1f, 0x8b, 0x00}); err == nil {
		t.Error("a truncated gzip stream was accepted")
	}
}

// TestDecompressRefusesARowPastItsBound proves the guard is a refusal rather
// than a silent cut: a schema handed back half-read would fail to parse later
// with no mention of the cap that did it.
func TestDecompressRefusesARowPastItsBound(t *testing.T) {
	big := gzipped(t, strings.Repeat("a", maxSDLBytes+1))
	if _, err := decompress(big); err == nil {
		t.Fatal("an oversized row was accepted")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v; want the bound named", err)
	}
	// A row at the bound is still read whole.
	atBound := gzipped(t, strings.Repeat("a", maxSDLBytes))
	if _, err := decompress(atBound); err != nil {
		t.Errorf("a row at the bound was refused: %v", err)
	}
}
