package resource

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func movedRow() Move {
	return Move{
		Scope: ScopePersona, ScopeID: "ops",
		URI:     "mcp://persona/ops/templates/report.docx",
		FromURI: "mcp://user/sub-1/templates/report.docx",
	}
}

func TestPostgresStore_MoveWritesTheRowAndTheAliasTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m := movedRow()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").
		WithArgs("res-1", "persona", sqlmock.AnyArg(), m.URI, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").
		WithArgs(m.FromURI, "res-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The address the resource now holds stops being an alias of anything, so a
	// move back to where it came from does not leave a row claiming its own URI.
	mock.ExpectExec("DELETE FROM resource_uri_aliases").
		WithArgs(m.URI).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	if err := NewPostgresStore(db).Move(context.Background(), "res-1", m); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_MoveRecordsNoAliasWhenTheAddressIsUnchanged(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// A resource whose URI would not change has nothing to alias; recording one
	// would point the resource's own current address at itself.
	m := Move{Scope: ScopeGlobal, URI: "mcp://global/t/x.csv", FromURI: "mcp://global/t/x.csv"}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	if err := NewPostgresStore(db).Move(context.Background(), "res-1", m); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_MoveReportsATakenAddressAsAConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").
		WillReturnError(&pq.Error{Code: "23505", Constraint: "resources_uri_key"})
	mock.ExpectRollback()

	err = NewPostgresStore(db).Move(context.Background(), "res-1", movedRow())
	if !errors.Is(err, ErrURIConflict) {
		t.Fatalf("err = %v, want ErrURIConflict", err)
	}
}

func TestPostgresStore_MoveOfAMissingRowIsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = NewPostgresStore(db).Move(context.Background(), "res-1", movedRow())
	if err == nil || errors.Is(err, ErrURIConflict) {
		t.Fatalf("err = %v, want a not-found failure", err)
	}
}

func TestPostgresStore_MoveFailsWhenTheAliasCannotBeRecorded(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// The row's new address and the alias for its old one are the same fact
	// stated twice: committing only the first would leave every citation of the
	// old URI dangling.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	if err := NewPostgresStore(db).Move(context.Background(), "res-1", movedRow()); err == nil {
		t.Fatal("Move reported success with no alias written")
	}
}

func TestPostgresStore_MoveFailsWhenTheStaleAliasCannotBeCleared(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM resource_uri_aliases").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	if err := NewPostgresStore(db).Move(context.Background(), "res-1", movedRow()); err == nil {
		t.Fatal("Move reported success with a stale alias left behind")
	}
}

func TestPostgresStore_MoveReportsANonConflictWriteFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// Not every rejection is the address being taken; anything else has to reach
	// the caller as a failure rather than as a conflict they could act on.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnError(errors.New("connection refused"))
	mock.ExpectRollback()

	err = NewPostgresStore(db).Move(context.Background(), "res-1", movedRow())
	if err == nil || errors.Is(err, ErrURIConflict) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}

func TestPostgresStore_MoveReportsANonConflictCommitFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(errors.New("connection refused"))

	err = NewPostgresStore(db).Move(context.Background(), "res-1", movedRow())
	if err == nil || errors.Is(err, ErrURIConflict) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}

func TestPostgresStore_MoveCannotBegin(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin().WillReturnError(errors.New("connection refused"))
	if err := NewPostgresStore(db).Move(context.Background(), "res-1", movedRow()); err == nil {
		t.Fatal("Move reported success without a transaction")
	}
}

func TestPostgresStore_MoveConflictOnCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// A deferred constraint reports at commit; the caller must still see a
	// conflict rather than a 500.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE resources SET scope").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM resource_uri_aliases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(errors.New("duplicate key value violates unique constraint"))

	err = NewPostgresStore(db).Move(context.Background(), "res-1", movedRow())
	if !errors.Is(err, ErrURIConflict) {
		t.Fatalf("err = %v, want ErrURIConflict", err)
	}
}

func TestIsUniqueViolationReadsADriverWithoutPqErrors(t *testing.T) {
	if !isUniqueViolation(errors.New(`pq: duplicate key value violates unique constraint "resources_uri_key"`)) {
		t.Error("a message-only duplicate report was not recognized")
	}
	if isUniqueViolation(errors.New("connection refused")) {
		t.Error("an unrelated failure read as a unique violation")
	}
}

// --- GetByURI through an alias ---

func resourceRows(uri string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "scope", "scope_id", "category", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
	}).AddRow("res-1", "persona", "ops", "templates", "report.docx", "Report", "desc",
		"text/plain", int64(9), "s3/key", uri, pq.Array([]string{}), "sub-1", "me@example.com",
		now, now, nil)
}

func TestPostgresStore_GetByURIResolvesAVacatedAddress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	old := "mcp://user/sub-1/templates/report.docx"
	mock.ExpectQuery("FROM resources WHERE uri = ").WithArgs(old).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("JOIN resource_uri_aliases").WithArgs(old).
		WillReturnRows(resourceRows("mcp://persona/ops/templates/report.docx"))

	res, err := NewPostgresStore(db).GetByURI(context.Background(), old)
	if err != nil {
		t.Fatalf("GetByURI: %v", err)
	}
	// The resource answers to the old address and reports the one it holds now,
	// which is what lets a caller tell a live hit from an alias hit.
	if res.URI != "mcp://persona/ops/templates/report.docx" {
		t.Errorf("URI = %q", res.URI)
	}
}

func TestPostgresStore_GetByURIPrefersALiveAddress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	uri := "mcp://user/sub-1/templates/report.docx"
	mock.ExpectQuery("FROM resources WHERE uri = ").WithArgs(uri).WillReturnRows(resourceRows(uri))

	if _, err := NewPostgresStore(db).GetByURI(context.Background(), uri); err != nil {
		t.Fatalf("GetByURI: %v", err)
	}
	// No alias query was expected: an alias must never shadow whichever resource
	// occupies that address now.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_GetByURIDoesNotFallBackOnARealFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	uri := "mcp://global/t/x.csv"
	mock.ExpectQuery("FROM resources WHERE uri = ").WithArgs(uri).
		WillReturnError(errors.New("connection refused"))

	if _, err := NewPostgresStore(db).GetByURI(context.Background(), uri); err == nil {
		t.Fatal("a failed read was reported as a miss")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_GetByURIUnknownEverywhere(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	uri := "mcp://global/t/gone.csv"
	mock.ExpectQuery("FROM resources WHERE uri = ").WithArgs(uri).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("JOIN resource_uri_aliases").WithArgs(uri).WillReturnError(sql.ErrNoRows)

	_, err = NewPostgresStore(db).GetByURI(context.Background(), uri)
	if !IsNotFound(err) {
		t.Fatalf("err = %v, want a not-found", err)
	}
}
