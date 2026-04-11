package resource

import (
	"context"
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
		ID: "id-1", Scope: ScopeGlobal, Category: "samples", Filename: "test.csv",
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
		"id", "scope", "scope_id", "category", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at",
	}).AddRow(
		"id-1", "global", nil, "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://global/samples/test.csv",
		pq.Array([]string{"t1"}), "sub-1", "user@example.com", now, now,
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
		"id", "scope", "scope_id", "category", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at",
	}).AddRow(
		"id-1", "user", "sub-1", "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://user/sub-1/samples/test.csv",
		pq.Array([]string{}), "sub-1", "user@example.com", now, now,
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
		"id", "scope", "scope_id", "category", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at",
	}).AddRow(
		"id-1", "global", nil, "samples", "test.csv", "Test", "desc",
		"text/csv", int64(50), "s3/key", "mcp://global/samples/test.csv",
		pq.Array([]string{}), "sub-1", "user@example.com", now, now,
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
		Category:    &cat,
		Tags:        tags,
	})
	if err != nil {
		t.Fatalf("Update all fields: %v", err)
	}
}
