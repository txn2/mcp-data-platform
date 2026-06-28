package knowledgepage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errBoom is a sentinel error for store error-path tests.
var errBoom = errors.New("boom")

func kpRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "slug", "title", "summary", "body", "tags",
		"created_by", "created_email", "updated_by", "current_version",
		"created_at", "updated_at", "deleted_at",
	})
}

// TestNewPostgresStoreSearcher proves the combined constructor returns a working
// store: a SemanticSearch through it actually queries the database and returns the
// ranked row (#705). The apply path needs this combined Store+Searcher+prober value.
func TestNewPostgresStoreSearcher(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	ss := NewPostgresStoreSearcher(db)

	cols := append([]string{
		"id", "slug", "title", "summary", "body", "tags",
		"created_by", "created_email", "updated_by", "current_version",
		"created_at", "updated_at", "deleted_at",
	}, "cos")
	mock.ExpectQuery("ORDER BY embedding").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("kp1", "s", "T", "", "b", []byte(`[]`), "", "", "", 1, time.Now(), time.Now(), nil, 0.88))

	out, err := ss.SemanticSearch(context.Background(), []float32{0.1, 0.2}, 5)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "kp1", out[0].Page.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_pages").
		WithArgs("kp1", "fiscal", "Fiscal", "sum", "body", sqlmock.AnyArg(), "alice@example.com", "alice@example.com", "alice@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").
		WithArgs(sqlmock.AnyArg(), "kp1", 1, "Fiscal", "sum", "body", sqlmock.AnyArg(), "alice@example.com", "Initial version").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.Insert(context.Background(), Page{
		ID: "kp1", Slug: "fiscal", Title: "Fiscal", Summary: "sum", Body: "body",
		Tags: []string{"finance"}, CreatedBy: "alice@example.com", CreatedEmail: "alice@example.com",
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_Get(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(kpRows().AddRow("kp1", "fiscal", "Fiscal", "sum", "# body", []byte(`["finance"]`),
			"alice@example.com", "alice@example.com", "bob@example.com", 2, time.Now(), time.Now(), nil))

	page, err := store.Get(context.Background(), "kp1")
	require.NoError(t, err)
	assert.Equal(t, "Fiscal", page.Title)
	assert.Equal(t, []string{"finance"}, page.Tags)
	assert.Equal(t, 2, page.CurrentVersion)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_GetNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").WithArgs("missing").WillReturnRows(kpRows())
	_, err = store.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStore_GetBySlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").
		WithArgs("fiscal").
		WillReturnRows(kpRows().AddRow("kp1", "fiscal", "Fiscal", "", "", []byte(`[]`),
			"", "", "", 1, time.Now(), time.Now(), nil))
	page, err := store.GetBySlug(context.Background(), "fiscal")
	require.NoError(t, err)
	assert.Equal(t, "kp1", page.ID)

	mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").WithArgs("nope").WillReturnRows(kpRows())
	_, err = store.GetBySlug(context.Background(), "nope")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStore_List(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").
		WillReturnRows(kpRows().AddRow("kp1", "", "Fiscal", "", "", []byte(`[]`),
			"", "", "", 1, time.Now(), time.Now(), nil))

	pages, total, err := store.List(context.Background(), Filter{Tag: "finance", Query: "fis", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, pages, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT title, summary, body, tags, current_version").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "summary", "body", "tags", "current_version"}).
			AddRow("Old", "olds", "oldbody", []byte(`["a"]`), 1))
	mock.ExpectExec("UPDATE portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO portal_knowledge_page_versions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	newTitle, newBody := "New", "newbody"
	newTags := []string{"b"}
	err = store.Update(context.Background(), "kp1", Update{
		Title: &newTitle, Body: &newBody, Tags: &newTags, UpdatedBy: "bob@example.com", ChangeSummary: "edit",
	})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_UpdateNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT title, summary, body, tags, current_version").
		WithArgs("missing").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	title := "X"
	err = store.Update(context.Background(), "missing", Update{Title: &title})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStore_SoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").
		WithArgs("kp1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.SoftDelete(context.Background(), "kp1"))

	mock.ExpectExec("UPDATE portal_knowledge_pages SET deleted_at").
		WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, store.SoftDelete(context.Background(), "missing"), ErrNotFound)
}

func TestStore_Versions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT COUNT").WithArgs("kp1").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery("SELECT id, page_id, version").WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "page_id", "version", "title", "summary", "body", "tags", "created_by", "change_summary", "created_at"}).
			AddRow("v1", "kp1", 1, "T", "", "b", []byte(`[]`), "alice", "init", time.Now()))
	versions, total, err := store.ListVersions(context.Background(), "kp1", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, versions, 1)

	mock.ExpectQuery("SELECT id, page_id, version").WithArgs("kp1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "page_id", "version", "title", "summary", "body", "tags", "created_by", "change_summary", "created_at"}).
			AddRow("v1", "kp1", 1, "T", "", "b", []byte(`[]`), "alice", "init", time.Now()))
	v, err := store.GetVersion(context.Background(), "kp1", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, v.Version)
}

func TestStore_ErrorPaths(t *testing.T) {
	t.Run("insert page error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		store := NewPostgresStore(db)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO portal_knowledge_pages").WillReturnError(errBoom)
		mock.ExpectRollback()
		err := store.Insert(context.Background(), Page{ID: "kp1", Title: "T"})
		assert.Error(t, err)
	})
	t.Run("update exec error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		store := NewPostgresStore(db)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT title, summary, body, tags, current_version").WithArgs("kp1").
			WillReturnRows(sqlmock.NewRows([]string{"title", "summary", "body", "tags", "current_version"}).
				AddRow("Old", "", "b", []byte(`[]`), 1))
		mock.ExpectExec("UPDATE portal_knowledge_pages").WillReturnError(errBoom)
		mock.ExpectRollback()
		title := "New"
		assert.Error(t, store.Update(context.Background(), "kp1", Update{Title: &title}))
	})
	t.Run("list count error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		store := NewPostgresStore(db)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errBoom)
		_, _, err := store.List(context.Background(), Filter{})
		assert.Error(t, err)
	})
	t.Run("get malformed tags", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		store := NewPostgresStore(db)
		mock.ExpectQuery("SELECT .* FROM portal_knowledge_pages").WithArgs("kp1").
			WillReturnRows(kpRows().AddRow("kp1", "", "T", "", "", []byte(`{not an array}`),
				"", "", "", 1, time.Now(), time.Now(), nil))
		_, err := store.Get(context.Background(), "kp1")
		assert.Error(t, err)
	})
	t.Run("get version not found", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		store := NewPostgresStore(db)
		mock.ExpectQuery("SELECT id, page_id, version").WithArgs("kp1", 9).
			WillReturnRows(sqlmock.NewRows([]string{"id", "page_id", "version", "title", "summary", "body", "tags", "created_by", "change_summary", "created_at"}))
		_, err := store.GetVersion(context.Background(), "kp1", 9)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestIndexTextAndHelpers(t *testing.T) {
	assert.Equal(t, "Title\nbody\ntag1 tag2", IndexText("Title", "body", []string{"tag1", "tag2"}))
	assert.Equal(t, "Title", IndexText("Title", "", nil))
	assert.Nil(t, nullableSlug("  "))
	assert.Equal(t, "x", nullableSlug(" x "))
	assert.NotEmpty(t, NewID())
	assert.NotEmpty(t, NewVersionID())
}
