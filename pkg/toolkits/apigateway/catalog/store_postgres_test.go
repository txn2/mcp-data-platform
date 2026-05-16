package catalog

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

// anyArg is the sqlmock placeholder for "I don't care about this
// specific argument" — used for created_at/updated_at/last_fetched_at
// when the test focuses on which columns are written, not their
// values.
type anyArg struct{}

func (anyArg) Match(_ driver.Value) bool { return true }

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewPostgresStore(db), mock, func() { _ = db.Close() }
}

func TestCreateCatalog_Success(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalogs`)).
		WithArgs("salesforce-rest-2024-10", "salesforce-rest", "2024-10",
			"Salesforce REST", "Sobject and query APIs", "operator@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.CreateCatalog(context.Background(), Catalog{
		ID:          "salesforce-rest-2024-10",
		Name:        "salesforce-rest",
		Version:     "2024-10",
		DisplayName: "Salesforce REST",
		Description: "Sobject and query APIs",
		CreatedBy:   "operator@example.com",
	})
	if err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCatalog_InvalidID(t *testing.T) {
	t.Parallel()
	store, _, done := newMockStore(t)
	defer done()
	err := store.CreateCatalog(context.Background(), Catalog{ID: "BAD"})
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("CreateCatalog err=%v want ErrInvalidID", err)
	}
}

func TestCreateCatalog_Conflict(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalogs`)).
		WillReturnError(&pq.Error{Code: pgUniqueViolation})
	err := store.CreateCatalog(context.Background(), Catalog{
		ID:          "petstore",
		Name:        "petstore",
		DisplayName: "Petstore",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateCatalog err=%v want ErrConflict", err)
	}
}

func TestCreateCatalog_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalogs`)).
		WillReturnError(errors.New("boom"))
	err := store.CreateCatalog(context.Background(), Catalog{
		ID:          "petstore",
		Name:        "petstore",
		DisplayName: "Petstore",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetCatalog_Success(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs`)).
		WithArgs("petstore").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "display_name", "description",
			"created_by", "created_at", "updated_at",
		}).AddRow("petstore", "petstore", "", "Petstore", "", "", now, now))
	c, err := store.GetCatalog(context.Background(), "petstore")
	if err != nil {
		t.Fatalf("GetCatalog: %v", err)
	}
	if c.ID != "petstore" || c.DisplayName != "Petstore" {
		t.Fatalf("unexpected catalog: %+v", c)
	}
}

func TestGetCatalog_NotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs`)).
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "display_name", "description",
			"created_by", "created_at", "updated_at",
		}))
	_, err := store.GetCatalog(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestGetCatalog_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs`)).
		WillReturnError(errors.New("boom"))
	_, err := store.GetCatalog(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListCatalogs(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "version", "display_name", "description",
			"created_by", "created_at", "updated_at",
		}).
			AddRow("a", "a", "1", "A", "", "", now, now).
			AddRow("b", "b", "2", "B", "", "", now, now))
	cs, err := store.ListCatalogs(context.Background())
	if err != nil {
		t.Fatalf("ListCatalogs: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d catalogs, want 2", len(cs))
	}
}

func TestUpdateCatalog_Partial(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	dn := "New Display"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalogs`)).
		WithArgs("New Display", "petstore").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.UpdateCatalog(context.Background(), "petstore",
		Update{DisplayName: &dn})
	if err != nil {
		t.Fatalf("UpdateCatalog: %v", err)
	}
}

func TestUpdateCatalog_AllFields(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	n, v, dn, desc := "n2", "v2", "DN2", "Desc2"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalogs`)).
		WithArgs("n2", "v2", "DN2", "Desc2", "id").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.UpdateCatalog(context.Background(), "id",
		Update{Name: &n, Version: &v, DisplayName: &dn, Description: &desc})
	if err != nil {
		t.Fatalf("UpdateCatalog: %v", err)
	}
}

func TestUpdateCatalog_EmptyUpdateChecksExistence(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	// Empty Update probes the row existence: present → nil.
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs WHERE id = $1`)).
		WithArgs("id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("id"))
	if err := store.UpdateCatalog(context.Background(), "id", Update{}); err != nil {
		t.Fatalf("UpdateCatalog empty (present): %v", err)
	}
}

func TestUpdateCatalog_EmptyUpdateNotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	// Empty Update probes the row existence: missing → ErrNotFound,
	// mirroring the MemoryStore contract.
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs WHERE id = $1`)).
		WithArgs("ghost").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	err := store.UpdateCatalog(context.Background(), "ghost", Update{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestUpdateCatalog_EmptyUpdateDBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalogs WHERE id = $1`)).
		WillReturnError(errors.New("boom"))
	err := store.UpdateCatalog(context.Background(), "id", Update{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateCatalog_NotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	dn := "x"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalogs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := store.UpdateCatalog(context.Background(), "ghost",
		Update{DisplayName: &dn})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestUpdateCatalog_Conflict(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	dn := "x"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalogs`)).
		WillReturnError(&pq.Error{Code: pgUniqueViolation})
	err := store.UpdateCatalog(context.Background(), "id",
		Update{DisplayName: &dn})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
}

func TestUpdateCatalog_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	dn := "x"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalogs`)).
		WillReturnError(errors.New("boom"))
	err := store.UpdateCatalog(context.Background(), "id",
		Update{DisplayName: &dn})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteCatalog(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalogs`)).
		WithArgs("id").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.DeleteCatalog(context.Background(), "id"); err != nil {
		t.Fatalf("DeleteCatalog: %v", err)
	}
}

func TestDeleteCatalog_NotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalogs`)).
		WithArgs("ghost").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := store.DeleteCatalog(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestDeleteCatalog_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalogs`)).
		WillReturnError(errors.New("boom"))
	err := store.DeleteCatalog(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpsertSpec_Insert(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_specs`)).
		WithArgs("petstore", "default", "openapi: 3.0", SourceInline, "", "", "", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "default",
		Content:    "openapi: 3.0",
		SourceKind: SourceInline,
	})
	if err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

func TestUpsertSpec_WithFetchedAt(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	fetched := time.Now()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_specs`)).
		WithArgs("petstore", "default", "openapi: 3.0", SourceURL,
			"https://petstore3.swagger.io/api/v3/openapi.json", "etag-xyz", "", fetched).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:      "default",
		Content:       "openapi: 3.0",
		SourceKind:    SourceURL,
		SourceURL:     "https://petstore3.swagger.io/api/v3/openapi.json",
		ETag:          "etag-xyz",
		LastFetchedAt: fetched,
	})
	if err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// TestUpsertSpec_WithBasePath proves the operator-supplied base
// path round-trips through the INSERT (normalized at write time:
// trailing slash stripped, leading slash required).
func TestUpsertSpec_WithBasePath(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_specs`)).
		WithArgs("petstore", "default", "openapi: 3.0", SourceInline, "", "", "/v1", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "default",
		Content:    "openapi: 3.0",
		SourceKind: SourceInline,
		BasePath:   "/v1/", // trailing slash should be stripped
	})
	if err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// TestUpsertSpec_RejectsInvalidBasePath proves the validator stops
// a malformed base path before the SQL exec.
func TestUpsertSpec_RejectsInvalidBasePath(t *testing.T) {
	t.Parallel()
	store, _, done := newMockStore(t)
	defer done()
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "default",
		Content:    "openapi: 3.0",
		SourceKind: SourceInline,
		BasePath:   "v1", // missing leading slash
	})
	if err == nil {
		t.Fatal("expected error for missing leading slash")
	}
}

func TestUpsertSpec_InvalidName(t *testing.T) {
	t.Parallel()
	store, _, done := newMockStore(t)
	defer done()
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "BAD NAME",
		SourceKind: SourceInline,
	})
	if !errors.Is(err, ErrInvalidSpecName) {
		t.Fatalf("err=%v want ErrInvalidSpecName", err)
	}
}

func TestUpsertSpec_InvalidSourceKind(t *testing.T) {
	t.Parallel()
	store, _, done := newMockStore(t)
	defer done()
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "default",
		SourceKind: "bogus",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpsertSpec_CatalogMissing(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_specs`)).
		WillReturnError(&pq.Error{Code: pgForeignKeyViolation})
	err := store.UpsertSpec(context.Background(), "ghost", SpecEntry{
		SpecName:   "default",
		Content:    "x",
		SourceKind: SourceInline,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestUpsertSpec_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_specs`)).
		WillReturnError(errors.New("boom"))
	err := store.UpsertSpec(context.Background(), "petstore", SpecEntry{
		SpecName:   "default",
		Content:    "x",
		SourceKind: SourceInline,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetSpec_Success(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WithArgs("petstore", "default").
		WillReturnRows(sqlmock.NewRows([]string{
			"spec_name", "content", "source_kind", "source_url",
			"etag", "base_path", "last_fetched_at", "created_at", "updated_at",
		}).AddRow("default", "openapi: 3.0", "url",
			"https://x", "etag-1", "", now, now, now))
	s, err := store.GetSpec(context.Background(), "petstore", "default")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if s.LastFetchedAt.IsZero() {
		t.Fatal("expected last_fetched_at to be set")
	}
}

func TestGetSpec_NullFetchedAt(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WithArgs("petstore", "default").
		WillReturnRows(sqlmock.NewRows([]string{
			"spec_name", "content", "source_kind", "source_url",
			"etag", "base_path", "last_fetched_at", "created_at", "updated_at",
		}).AddRow("default", "openapi: 3.0", "inline",
			"", "", "", nil, now, now))
	s, err := store.GetSpec(context.Background(), "petstore", "default")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if !s.LastFetchedAt.IsZero() {
		t.Fatal("expected zero last_fetched_at")
	}
}

func TestGetSpec_NotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WithArgs("petstore", "missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"spec_name", "content", "source_kind", "source_url",
			"etag", "base_path", "last_fetched_at", "created_at", "updated_at",
		}))
	_, err := store.GetSpec(context.Background(), "petstore", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestGetSpec_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WillReturnError(errors.New("boom"))
	_, err := store.GetSpec(context.Background(), "x", "y")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListSpecs(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WithArgs("petstore").
		WillReturnRows(sqlmock.NewRows([]string{
			"spec_name", "content", "source_kind", "source_url",
			"etag", "base_path", "last_fetched_at", "created_at", "updated_at",
		}).
			AddRow("users", "openapi: 3.0", "inline", "", "", "", nil, now, now).
			AddRow("orders", "openapi: 3.0", "url", "https://x", "etag", "/v1", now, now, now))
	specs, err := store.ListSpecs(context.Background(), "petstore")
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if !specs[0].LastFetchedAt.IsZero() {
		t.Errorf("first row should have null fetched_at, got %v", specs[0].LastFetchedAt)
	}
	if specs[1].LastFetchedAt.IsZero() {
		t.Error("second row should have non-zero fetched_at")
	}
}

func TestListSpecs_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM api_catalog_specs`)).
		WillReturnError(errors.New("boom"))
	_, err := store.ListSpecs(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteSpec(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalog_specs`)).
		WithArgs("petstore", "gift").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.DeleteSpec(context.Background(), "petstore", "gift"); err != nil {
		t.Fatalf("DeleteSpec: %v", err)
	}
}

func TestDeleteSpec_NotFound(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalog_specs`)).
		WithArgs("petstore", "ghost").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := store.DeleteSpec(context.Background(), "petstore", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestDeleteSpec_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM api_catalog_specs`)).
		WillReturnError(errors.New("boom"))
	err := store.DeleteSpec(context.Background(), "petstore", "gift")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReferencingConnections(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM connection_instances`)).
		WithArgs("petstore").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name"}).
			AddRow("api", "petstore-prod").
			AddRow("api", "petstore-staging"))
	refs, err := store.ReferencingConnections(context.Background(), "petstore")
	if err != nil {
		t.Fatalf("ReferencingConnections: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if refs[0].Name != "petstore-prod" {
		t.Fatalf("first ref name=%q", refs[0].Name)
	}
}

func TestReferencingConnections_None(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM connection_instances`)).
		WithArgs("orphan").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name"}))
	refs, err := store.ReferencingConnections(context.Background(), "orphan")
	if err != nil {
		t.Fatalf("ReferencingConnections: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("got %d refs, want 0", len(refs))
	}
}

func TestReferencingConnections_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`FROM connection_instances`)).
		WillReturnError(errors.New("boom"))
	_, err := store.ReferencingConnections(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsPGCode_NotPQError(t *testing.T) {
	t.Parallel()
	if isPGCode(errors.New("plain error"), pgUniqueViolation) {
		t.Fatal("plain error matched pq code")
	}
	if isPGCode(nil, pgUniqueViolation) {
		t.Fatal("nil matched pq code")
	}
}

// silence unused-import warning when anyArg goes unused in a refactor.
var _ = anyArg{}
