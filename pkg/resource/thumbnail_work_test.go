package resource

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// The statements are held to PostgreSQL by the real-database suite and the SQL
// gate; what is held here is the Go around them.

var errWorkDB = errors.New("db down")

func workStore(t *testing.T, db *sql.DB) *postgresStore {
	t.Helper()
	s, ok := NewPostgresStore(db).(*postgresStore)
	if !ok {
		t.Fatal("NewPostgresStore is not the Postgres store")
	}
	return s
}

var resourceCols = []string{
	"id", "scope", "scope_id", "path", "filename", "display_name", "description",
	"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
	"created_at", "updated_at", "last_read_at",
	"thumbnail_s3_key", "thumbnail_dark_s3_key",
	"thumbnail_captured_at", "thumbnail_dark_captured_at", "thumbnail_renderer", "thumbnail_failure", "thumbnail_failed_at",
}

func TestBuildThumbnailClaim_BindsWhatTheStatementNames(t *testing.T) {
	_, args := buildThumbnailClaim(1, 2*time.Minute, 4)
	if len(args) != 7 {
		t.Fatalf("bound %d values, the statement names $1..$7", len(args))
	}
	if args[0] != 120.0 || args[3] != 1 || args[5] != 4 {
		t.Errorf("lease, renderer and limit bound as %v, %v, %v", args[0], args[3], args[5])
	}
}

func TestClaimThumbnailWork_ReturnsTheClaimedResources(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows(resourceCols).AddRow(resourceRow("r1", "First")...).AddRow(resourceRow("r2", "Second")...)
	mock.ExpectQuery("UPDATE resources SET thumbnail_claimed_until").WillReturnRows(rows)

	got, err := workStore(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 4)
	if err != nil {
		t.Fatalf("ClaimThumbnailWork: %v", err)
	}
	if len(got) != 2 || got[0].ID != "r1" || got[1].ID != "r2" {
		t.Fatalf("claimed %+v", got)
	}

	// No room claims nothing and runs nothing.
	if got, err := workStore(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 0); err != nil || got != nil {
		t.Errorf("limit 0 = %v, %v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestClaimThumbnailWork_EachFailureIsReported(t *testing.T) {
	for _, tt := range []struct {
		name   string
		expect func(sqlmock.Sqlmock)
	}{
		{"the claim", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("UPDATE resources").WillReturnError(errWorkDB)
		}},
		{"a row", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("UPDATE resources").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
		}},
		{"the cursor", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("UPDATE resources").WillReturnRows(sqlmock.NewRows(resourceCols).AddRow(resourceRow("r1", "First")...).RowError(0, errWorkDB))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tt.expect(mock)
			if _, err := workStore(t, db).ClaimThumbnailWork(context.Background(), 1, time.Minute, 4); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// A failure is recorded against the file as it stood and ends the lease.
func TestRecordThumbnailFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := workStore(t, db)
	at := time.Now()

	mock.ExpectExec("UPDATE resources\\s+SET thumbnail_failure = \\$1, thumbnail_failed_at = \\$2, thumbnail_claimed_until = NULL").
		WithArgs("the image could not be decoded", at, "r1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.RecordThumbnailFailure(context.Background(), "r1", "the image could not be decoded", at); err != nil {
		t.Fatalf("RecordThumbnailFailure: %v", err)
	}

	mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.RecordThumbnailFailure(context.Background(), "gone", "x", at); err == nil {
		t.Error("a resource that is not there must be an error")
	}

	mock.ExpectExec("UPDATE resources").WillReturnError(errWorkDB)
	if err := store.RecordThumbnailFailure(context.Background(), "r1", "x", at); err == nil {
		t.Error("a failed write must be reported")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
