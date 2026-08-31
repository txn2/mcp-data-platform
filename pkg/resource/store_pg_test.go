package resource

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestPostgresStore_Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	mock.ExpectExec("INSERT INTO resources").
		WithArgs(
			"id-1", "global", sqlmock.AnyArg(), "samples", "test.csv", "Test",
			"A test resource", "text/csv", int64(100), "s3/key", "mcp://global/samples/test.csv",
			pq.Array([]string{"tag1"}), "sub-1", "user@example.com",
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	r := Resource{
		ID: "id-1", Scope: ScopeGlobal, Path: "samples", Filename: "test.csv",
		DisplayName: "Test", Description: "A test resource", MIMEType: "text/csv",
		SizeBytes: 100, S3Key: "s3/key", URI: "mcp://global/samples/test.csv",
		Tags: []string{"tag1"}, UploaderSub: "sub-1", UploaderEmail: "user@example.com",
	}
	if err := store.Insert(context.Background(), r); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_Get(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}).AddRow(
		"id-1", "global", nil, "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://global/samples/test.csv",
		pq.Array([]string{"t1"}), "sub-1", "user@example.com", now, now, nil, "", "", nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM resources WHERE id = \\$1").
		WithArgs("id-1").
		WillReturnRows(rows)

	r, err := store.Get(context.Background(), "id-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r.ID != "id-1" || r.DisplayName != "Test" || r.Scope != ScopeGlobal {
		t.Errorf("unexpected resource: %+v", r)
	}
	if len(r.Tags) != 1 || r.Tags[0] != "t1" {
		t.Errorf("tags = %v", r.Tags)
	}
}

func TestPostgresStore_GetByURI(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}).AddRow(
		"id-1", "user", "sub-1", "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://user/sub-1/samples/test.csv",
		pq.Array([]string{}), "sub-1", "user@example.com", now, now, nil, "", "", nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM resources WHERE uri = \\$1").
		WithArgs("mcp://user/sub-1/samples/test.csv").
		WillReturnRows(rows)

	r, err := store.GetByURI(context.Background(), "mcp://user/sub-1/samples/test.csv")
	if err != nil {
		t.Fatalf("GetByURI: %v", err)
	}
	if r.ScopeID != "sub-1" {
		t.Errorf("ScopeID = %q", r.ScopeID)
	}
}

func TestPostgresStore_List(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	now := time.Now()

	// Count query
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Select query
	rows := sqlmock.NewRows([]string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}).AddRow(
		"id-1", "global", nil, "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://global/samples/test.csv",
		pq.Array([]string{}), "sub-1", "user@example.com", now, now, nil, "", "", nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM resources WHERE").WillReturnRows(rows)

	resources, total, err := store.List(context.Background(), Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(resources) != 1 {
		t.Errorf("total=%d, len=%d", total, len(resources))
	}
}

func TestPostgresStore_List_ClampsLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// A client-supplied limit above MaxListLimit must reach the SELECT clamped to
	// MaxListLimit (the scope arg, then the clamped limit, then the offset).
	rows := sqlmock.NewRows([]string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	})
	mock.ExpectQuery("SELECT .+ FROM resources WHERE").
		WithArgs(string(ScopeGlobal), MaxListLimit, 7).
		WillReturnRows(rows)

	if _, _, err := store.List(context.Background(), Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Limit:  MaxListLimit + 5000,
		Offset: 7,
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_List_EmptyScopes(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	resources, total, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 || resources != nil {
		t.Errorf("expected empty, got total=%d resources=%v", total, resources)
	}
}

// An unrestricted listing has no scopes and must still run: the short-circuit
// on an empty scope set is for a caller who named a library they may not read,
// not for an administrator listing every library (#1553).
func TestPostgresStore_List_EveryLibraryRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	_, total, err := store.List(context.Background(), Filter{AllScopes: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("an unrestricted listing did not run its count: %v", err)
	}
}

func TestPostgresStore_List_ZeroCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	resources, total, err := store.List(context.Background(), Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 || resources != nil {
		t.Errorf("expected empty for zero count")
	}
}

func TestPostgresStore_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	name := "Updated Name"
	mock.ExpectExec("UPDATE resources SET").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "id-1", Update{DisplayName: &name})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestPostgresStore_Update_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	name := "Updated"
	mock.ExpectExec("UPDATE resources SET").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Update(context.Background(), "missing", Update{DisplayName: &name})
	if err == nil {
		t.Fatal("expected error for not-found update")
	}
}

func TestPostgresStore_Delete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	mock.ExpectExec("DELETE FROM resources WHERE id = \\$1").
		WithArgs("id-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Delete(context.Background(), "id-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPostgresStore_Delete_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	mock.ExpectExec("DELETE FROM resources WHERE id = \\$1").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = store.Delete(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for not-found delete")
	}
}

func TestPostgresStore_Update_AllFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	name := "New Name"
	desc := "New desc"
	cat := "references"
	tags := []string{"a", "b"}

	mock.ExpectExec("UPDATE resources SET").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.Update(context.Background(), "id-1", Update{
		DisplayName: &name,
		Description: &desc,
		Path:        &cat,
		Tags:        tags,
	})
	if err != nil {
		t.Fatalf("Update all fields: %v", err)
	}
}

// A row whose tags column is NULL scans to an empty slice, not nil, so the JSON
// encoding is [] and callers never have to nil-check.
func TestPostgresStore_Get_NullTagsAndScopeID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery("SELECT .+ FROM resources WHERE id = \\$1").
		WithArgs("id-null").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "scope", "scope_id", "path", "filename", "display_name", "description",
			"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
			"created_at", "updated_at", "last_read_at",
			"thumbnail_s3_key", "thumbnail_dark_s3_key",
			"thumbnail_captured_at", "thumbnail_dark_captured_at",
		}).AddRow(
			"id-null", "global", nil, "samples", "t.csv", "T", "d",
			"text/csv", int64(1), "k", "mcp://global/samples/t.csv",
			nil, "sub", "u@example.com", now, now, nil, "", "", nil, nil,
		))

	got, err := NewPostgresStore(db).Get(context.Background(), "id-null")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Tags == nil || len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want an empty slice", got.Tags)
	}
	if got.ScopeID != "" {
		t.Errorf("ScopeID = %q, want empty for a global resource", got.ScopeID)
	}
}

// resourceRow builds one result row in the column order resourceScan expects,
// so a bulk read scans the same shape a single read does.
func resourceRow(id, name string) []driver.Value {
	now := time.Now()
	return []driver.Value{
		id, "global", nil, "samples", name + ".csv", name, "desc",
		"text/csv", int64(50), "resources/" + id + "/" + name + ".csv",
		"mcp://global/samples/" + name + ".csv",
		pq.Array([]string{"t1"}), "sub-1", "user@example.com", now, now, nil,
		// No capture taken, which is every resource until a portal tab takes
		// one (#1554).
		"", "", nil, nil,
	}
}

// TestPostgresStore_GetByIDs is the read a listing over records that point at
// resources uses: one query for the page, keyed back by id.
func TestPostgresStore_GetByIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cols := []string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}
	mock.ExpectQuery("SELECT .+ FROM resources WHERE id = ANY").
		WithArgs(pq.Array([]string{"id-1", "id-2", "gone"})).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(resourceRow("id-1", "First")...).
			AddRow(resourceRow("id-2", "Second")...))

	got, err := NewPostgresStore(db).GetByIDs(context.Background(), []string{"id-1", "id-2", "gone"})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	if got["id-1"].DisplayName != "First" || got["id-2"].DisplayName != "Second" {
		t.Errorf("unexpected resources: %+v", got)
	}
	// An id with no row is absent rather than an error: the caller is reading
	// a set of references, and a reference to something deleted is an answer.
	if _, ok := got["gone"]; ok {
		t.Errorf("an id with no row must not appear in the result")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostgresStore_GetByIDsNoIDs asks nothing of the database rather than
// running a query that matches everything.
func TestPostgresStore_GetByIDsNoIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := NewPostgresStore(db).GetByIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d resources, want none", len(got))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostgresStore_GetByIDsReportsAFailedRead, so a caller can tell a store
// that is not answering from a page whose sources are all gone.
func TestPostgresStore_GetByIDsReportsAFailedRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT .+ FROM resources WHERE id = ANY").
		WillReturnError(errors.New("connection refused"))

	if _, err := NewPostgresStore(db).GetByIDs(context.Background(), []string{"id-1"}); err == nil {
		t.Fatal("a failed read must be reported")
	}
}

// A caller who named a library they may not read has no folders and no tags,
// and the store answers without running a statement (#1555).
func TestPostgresStore_FacetsShortCircuitOnNoScopes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	folders, err := store.Folders(context.Background(), Filter{})
	if err != nil || folders != nil {
		t.Errorf("Folders = %v, %v; want nil, nil", folders, err)
	}
	tags, err := store.Tags(context.Background(), Filter{})
	if err != nil || tags != nil {
		t.Errorf("Tags = %v, %v; want nil, nil", tags, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a short-circuit ran a statement: %v", err)
	}
}

// The capture writes and the pending read (#1554). The real numbers are proved
// against PostgreSQL in the integration gate; what these pin is the statement
// each one runs and the not-found contract, which sqlmock can answer for.
func TestPostgresStore_SetAndClearThumbnail(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewPostgresStore(db)
	now := time.Now()

	// A capture writes the key and the moment, and nothing else: bumping
	// updated_at here would mark the capture behind the row it came from.
	mock.ExpectExec("UPDATE resources SET thumbnail_s3_key = \\$1, thumbnail_captured_at = \\$2 WHERE id = \\$3").
		WithArgs("k/light.png", now, "id-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.SetThumbnail(context.Background(), "id-1", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/light.png", CapturedAt: now,
	}); err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}

	// The dark variant writes its own pair.
	mock.ExpectExec("UPDATE resources SET thumbnail_dark_s3_key = \\$1, thumbnail_dark_captured_at = \\$2 WHERE id = \\$3").
		WithArgs("k/dark.png", now, "id-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.SetThumbnail(context.Background(), "id-1", ThumbnailCapture{
		Variant: ThumbnailVariantDark, S3Key: "k/dark.png", CapturedAt: now,
	}); err != nil {
		t.Fatalf("SetThumbnail dark: %v", err)
	}

	mock.ExpectExec("UPDATE resources SET thumbnail_s3_key = '', thumbnail_captured_at = NULL WHERE id = \\$1").
		WithArgs("id-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.ClearThumbnail(context.Background(), "id-1", ThumbnailVariantLight); err != nil {
		t.Fatalf("ClearThumbnail: %v", err)
	}

	// A row that is not there is an error, not a silent success: the caller
	// would otherwise report a capture nothing recorded.
	mock.ExpectExec("UPDATE resources SET").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.SetThumbnail(context.Background(), "gone", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k", CapturedAt: now,
	}); err == nil {
		t.Error("SetThumbnail on a missing resource reported success")
	}
	mock.ExpectExec("UPDATE resources SET").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.ClearThumbnail(context.Background(), "gone", ThumbnailVariantLight); err == nil {
		t.Error("ClearThumbnail on a missing resource reported success")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_PendingThumbnails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	cols := []string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}
	mock.ExpectQuery("SELECT .+ FROM resources WHERE .+ thumbnail_captured_at").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(resourceRow("id-1", "First")...))

	got, err := NewPostgresStore(db).PendingThumbnails(context.Background(),
		Filter{Scopes: []ScopeFilter{{Scope: ScopeGlobal}}}, 25)
	if err != nil {
		t.Fatalf("PendingThumbnails: %v", err)
	}
	if len(got) != 1 || got[0].ID != "id-1" {
		t.Errorf("pending = %v", got)
	}
}

// A caller who named a library they may not read has nothing pending, and the
// store answers without running a statement.
func TestPostgresStore_PendingThumbnailsShortCircuits(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := NewPostgresStore(db).PendingThumbnails(context.Background(), Filter{}, 25)
	if err != nil || got != nil {
		t.Errorf("PendingThumbnails = %v, %v; want nil, nil", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a short-circuit ran a statement: %v", err)
	}
}
