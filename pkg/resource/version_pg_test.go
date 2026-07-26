package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// versionRows builds a result set in the projection every version read shares.
func versionRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"resource_id", "version", "mime_type", "size_bytes", "s3_key",
		"uploader_sub", "uploader_email", "restored_from", "created_at",
	})
}

func TestBuildRevisionS3Key(t *testing.T) {
	tests := []struct {
		name                      string
		scope                     Scope
		scopeID, resID, revID, fn string
		want                      string
	}{
		{
			name: "persona scope keys by persona", scope: ScopePersona, scopeID: "analyst",
			resID: "r1", revID: "rev9", fn: "runbook.md",
			want: "resources/persona/analyst/r1/v/rev9/runbook.md",
		},
		{
			name: "global scope keys under global", scope: ScopeGlobal,
			resID: "r1", revID: "rev9", fn: "runbook.md",
			want: "resources/global/global/r1/v/rev9/runbook.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildRevisionS3Key(tt.scope, tt.scopeID, tt.resID, tt.revID, tt.fn); got != tt.want {
				t.Errorf("BuildRevisionS3Key = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("a revision never lands on the create-time key", func(t *testing.T) {
		head := BuildS3Key(ScopeGlobal, "", "r1", "runbook.md")
		if got := BuildRevisionS3Key(ScopeGlobal, "", "r1", "rev1", "runbook.md"); got == head {
			t.Fatal("revision key collides with the head key: a revision would overwrite the version it replaces")
		}
	})
}

func TestNormalizeMaxVersions(t *testing.T) {
	tests := map[int]int{
		0:  DefaultMaxVersions,
		-5: DefaultMaxVersions,
		1:  MinMaxVersions,
		2:  2,
		25: 25,
	}
	for in, want := range tests {
		if got := NormalizeMaxVersions(in); got != want {
			t.Errorf("NormalizeMaxVersions(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestAddRevision(t *testing.T) {
	t.Run("records the revision and moves the head in one transaction", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		now := time.Now().UTC()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM resources WHERE id = \\$1 FOR UPDATE").
			WithArgs("r1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
		mock.ExpectQuery("INSERT INTO resource_versions").
			WithArgs("r1", "text/csv", int64(12), "k/v/rev1/f.csv", "sub", "u@example.com", nil, sqlmock.AnyArg()).
			WillReturnRows(versionRows().AddRow("r1", 3, "text/csv", int64(12), "k/v/rev1/f.csv", "sub", "u@example.com", nil, now))
		mock.ExpectExec("UPDATE resources").
			WithArgs("text/csv", int64(12), "k/v/rev1/f.csv", sqlmock.AnyArg(), "r1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		v, err := store.AddRevision(context.Background(), Revision{
			ResourceID: "r1", MIMEType: "text/csv", SizeBytes: 12,
			S3Key: "k/v/rev1/f.csv", UploaderSub: "sub", UploaderEmail: "u@example.com",
		})
		if err != nil {
			t.Fatalf("AddRevision: %v", err)
		}
		if v.Version != 3 {
			t.Errorf("version = %d, want the number the store assigned (3)", v.Version)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	t.Run("a head that vanished mid-revision rolls the trail back", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM resources WHERE id = \\$1 FOR UPDATE").
			WithArgs("r1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
		mock.ExpectQuery("INSERT INTO resource_versions").
			WillReturnRows(versionRows().AddRow("r1", 1, "text/csv", int64(1), "k", "sub", "", nil, time.Now()))
		mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()

		if _, err := store.AddRevision(context.Background(), Revision{ResourceID: "r1"}); err == nil {
			t.Fatal("AddRevision succeeded against a deleted resource; the version row would outlive its resource")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	t.Run("restored_from is carried through", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		from := 2
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM resources WHERE id = \\$1 FOR UPDATE").
			WithArgs("r1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
		mock.ExpectQuery("INSERT INTO resource_versions").
			WithArgs("r1", "", int64(0), "", "", "", int64(2), sqlmock.AnyArg()).
			WillReturnRows(versionRows().AddRow("r1", 5, "", int64(0), "", "", "", 2, time.Now()))
		mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		v, err := store.AddRevision(context.Background(), Revision{ResourceID: "r1", RestoredFrom: &from})
		if err != nil {
			t.Fatalf("AddRevision: %v", err)
		}
		if v.RestoredFrom == nil || *v.RestoredFrom != 2 {
			t.Errorf("restored_from = %v, want 2", v.RestoredFrom)
		}
	})
}

func TestListAndGetVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, _ := NewPostgresStore(db).(*postgresStore)
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT .+ FROM resource_versions").
		WithArgs("r1").
		WillReturnRows(versionRows().
			AddRow("r1", 2, "text/csv", int64(20), "k2", "sub", "u@example.com", 1, now).
			AddRow("r1", 1, "text/csv", int64(10), "k1", "sub", "u@example.com", nil, now))

	versions, err := store.ListVersions(context.Background(), "r1")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("versions = %+v, want two rows newest first", versions)
	}
	if versions[0].RestoredFrom == nil || *versions[0].RestoredFrom != 1 {
		t.Errorf("restored_from = %v, want 1", versions[0].RestoredFrom)
	}
	if versions[1].RestoredFrom != nil {
		t.Errorf("restored_from = %v, want nil for a fresh upload", versions[1].RestoredFrom)
	}

	mock.ExpectQuery("SELECT .+ FROM resource_versions").
		WithArgs("r1", 1).
		WillReturnRows(versionRows().AddRow("r1", 1, "text/csv", int64(10), "k1", "sub", "u@example.com", nil, now))
	v, err := store.GetVersion(context.Background(), "r1", 1)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v.S3Key != "k1" {
		t.Errorf("s3_key = %q, want k1", v.S3Key)
	}
}

func TestGetVersion_MissingRowIsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, _ := NewPostgresStore(db).(*postgresStore)

	mock.ExpectQuery("SELECT .+ FROM resource_versions").
		WithArgs("r1", 9).
		WillReturnRows(versionRows())

	_, err = store.GetVersion(context.Background(), "r1", 9)
	if err == nil {
		t.Fatal("GetVersion on a missing row returned no error")
	}
	if !IsNotFound(err) {
		t.Errorf("error = %v, want one IsNotFound recognizes so the handler answers 404, not 500", err)
	}
}

func TestPruneVersions(t *testing.T) {
	t.Run("returns the pruned rows so their blobs can be removed", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		mock.ExpectQuery("DELETE FROM resource_versions").
			WithArgs("r1", 10).
			WillReturnRows(versionRows().AddRow("r1", 1, "text/csv", int64(10), "old-key", "sub", "", nil, time.Now()))

		pruned, err := store.PruneVersions(context.Background(), "r1", 10)
		if err != nil {
			t.Fatalf("PruneVersions: %v", err)
		}
		if len(pruned) != 1 || pruned[0].S3Key != "old-key" {
			t.Fatalf("pruned = %+v, want the dropped row with its key", pruned)
		}
	})

	t.Run("a cap below the floor is raised", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		mock.ExpectQuery("DELETE FROM resource_versions").
			WithArgs("r1", MinMaxVersions).
			WillReturnRows(versionRows())

		if _, err := store.PruneVersions(context.Background(), "r1", 1); err != nil {
			t.Fatalf("PruneVersions: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("a cap of 1 was not raised to the floor: %v", err)
		}
	})

	t.Run("a query failure surfaces", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		mock.ExpectQuery("DELETE FROM resource_versions").WillReturnError(errors.New("db down"))
		if _, err := store.PruneVersions(context.Background(), "r1", 10); err == nil {
			t.Fatal("PruneVersions swallowed a query failure")
		}
	})
}

func TestTouchRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, _ := NewPostgresStore(db).(*postgresStore)

	at := time.Now()
	mock.ExpectExec("UPDATE resources SET last_read_at").
		WithArgs(at.UTC(), "r1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.TouchRead(context.Background(), "r1", at); err != nil {
		t.Fatalf("TouchRead: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}

	mock.ExpectExec("UPDATE resources SET last_read_at").WillReturnError(errors.New("db down"))
	if err := store.TouchRead(context.Background(), "r1", at); err == nil {
		t.Error("TouchRead swallowed a write failure; the caller logs it, so it must be reported")
	}
}

func TestCurrentVersion(t *testing.T) {
	versions := []Version{{Version: 3, S3Key: "c"}, {Version: 2, S3Key: "b"}, {Version: 1, S3Key: "a"}}
	if got := currentVersion(versions, "b"); got != 2 {
		t.Errorf("currentVersion = %d, want 2", got)
	}
	if got := currentVersion(versions, "unknown"); got != 0 {
		t.Errorf("currentVersion with no matching key = %d, want 0", got)
	}
	if got := currentVersion(nil, "a"); got != 0 {
		t.Errorf("currentVersion with no trail = %d, want 0", got)
	}
}

func TestListVersions_ReadFailures(t *testing.T) {
	t.Run("a query failure surfaces", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		mock.ExpectQuery("SELECT .+ FROM resource_versions").WillReturnError(errors.New("db down"))
		if _, err := store.ListVersions(context.Background(), "r1"); err == nil {
			t.Fatal("ListVersions swallowed a query failure")
		}
	})

	t.Run("a malformed row surfaces", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		store, _ := NewPostgresStore(db).(*postgresStore)

		// A version number the driver cannot convert to int: the scan must fail
		// loudly rather than yield a half-populated trail.
		mock.ExpectQuery("SELECT .+ FROM resource_versions").
			WillReturnRows(versionRows().AddRow("r1", "not-a-number", "text/csv", int64(1), "k", "sub", "", nil, time.Now()))
		if _, err := store.ListVersions(context.Background(), "r1"); err == nil {
			t.Fatal("ListVersions returned rows it could not scan")
		}
	})
}

func TestAddRevision_MissingResourceIsRefusedBeforeAnyWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store, _ := NewPostgresStore(db).(*postgresStore)

	// The lock read is also the existence check: a resource deleted between the
	// blob write and the revision must not leave a version row behind.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM resources WHERE id = \\$1 FOR UPDATE").
		WithArgs("gone").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	if _, err := store.AddRevision(context.Background(), Revision{ResourceID: "gone"}); err == nil {
		t.Fatal("AddRevision succeeded for a resource that does not exist")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a version row was written for a missing resource: %v", err)
	}
}
