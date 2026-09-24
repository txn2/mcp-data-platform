package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func folderStorePG(t *testing.T) (*postgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s, ok := NewPostgresStore(db).(*postgresStore)
	require.True(t, ok)
	return s, mock
}

var persona = ScopeFilter{Scope: ScopePersona, ScopeID: "ops"}

func countRow(n int) *sqlmock.Rows { return sqlmock.NewRows([]string{"count"}).AddRow(n) }

func TestFolderChain(t *testing.T) {
	assert.Equal(t, []string{"a", "a/b", "a/b/c"}, folderChain("a/b/c"))
	assert.Equal(t, []string{"a"}, folderChain("a"))
}

func TestPostgresCreateFolder(t *testing.T) {
	t.Run("records the chain", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM resources").
			WithArgs("persona", "ops", "data/new", "data/new/%").WillReturnRows(countRow(0))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM resource_folders").WillReturnRows(countRow(0))
		mock.ExpectExec("INSERT INTO resource_folders").
			WithArgs("persona", sqlmock.AnyArg(), "data", "me@example.com", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO resource_folders").
			WithArgs("persona", sqlmock.AnyArg(), "data/new", "me@example.com", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, s.CreateFolder(context.Background(), persona, "data/new", "me@example.com"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("a folder in use exists", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(2))
		assert.ErrorIs(t, s.CreateFolder(context.Background(), persona, "data", ""), ErrFolderExists)
	})
	t.Run("a stored folder exists", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(0))
		mock.ExpectQuery("FROM resource_folders").WillReturnRows(countRow(1))
		assert.ErrorIs(t, s.CreateFolder(context.Background(), persona, "data", ""), ErrFolderExists)
	})
	t.Run("the read fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.CreateFolder(context.Background(), persona, "data", ""), "counting resources")
	})
	t.Run("the write fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(0))
		mock.ExpectQuery("FROM resource_folders").WillReturnRows(countRow(0))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.CreateFolder(context.Background(), persona, "data", ""), "recording folder")
	})
}

func TestPostgresDeleteFolder(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(0))
		mock.ExpectExec("DELETE FROM resource_folders").
			WithArgs("persona", "ops", "data", "data/%").WillReturnResult(sqlmock.NewResult(0, 3))
		require.NoError(t, s.DeleteFolder(context.Background(), persona, "data"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("holds files", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(1))
		assert.ErrorIs(t, s.DeleteFolder(context.Background(), persona, "data"), ErrFolderNotEmpty)
	})
	t.Run("names nothing", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(0))
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, s.DeleteFolder(context.Background(), persona, "data"), ErrFolderEmpty)
	})
	t.Run("the count fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnError(errors.New("down"))
		assert.Error(t, s.DeleteFolder(context.Background(), persona, "data"))
	})
	t.Run("the delete fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectQuery("FROM resources").WillReturnRows(countRow(0))
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.DeleteFolder(context.Background(), persona, "data"), "deleting folder")
	})
}

func TestPostgresFolderExists(t *testing.T) {
	s, mock := folderStorePG(t)
	mock.ExpectQuery("FROM resource_folders").WillReturnRows(countRow(1))
	ok, err := s.FolderExists(context.Background(), ScopeFilter{Scope: ScopeGlobal}, "brand")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPostgresMoveFolderTree(t *testing.T) {
	tree := FolderRename{Library: persona, From: "a/b", To: "a"}
	folderRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"path", "created_by", "created_at"}).
			AddRow("a/b", "x@example.com", time.Unix(0, 0)).
			AddRow("a/b/b", "", time.Unix(0, 0))
	}
	t.Run("the folders take the new prefix", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT path, created_by, created_at FROM resource_folders").WillReturnRows(folderRows())
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnResult(sqlmock.NewResult(0, 2))
		// The destination's chain, then each moved row at its new path: a/b
		// becomes a, and a/b/b becomes a/b.
		mock.ExpectExec("INSERT INTO resource_folders").WithArgs("persona", sqlmock.AnyArg(), "a", "", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO resource_folders").WithArgs("persona", sqlmock.AnyArg(), "a", "x@example.com", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO resource_folders").WithArgs("persona", sqlmock.AnyArg(), "a/b", "", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.MoveFolderTree(context.Background(), tree, nil))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("cannot begin", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin().WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.MoveFolderTree(context.Background(), tree, nil), "beginning folder move")
	})
	t.Run("a conflict at commit", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT path").WillReturnRows(sqlmock.NewRows([]string{"path", "created_by", "created_at"}))
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("duplicate key value violates unique constraint"))
		assert.ErrorIs(t, s.MoveFolderTree(context.Background(), tree, nil), ErrURIConflict)
	})
	t.Run("the folder read fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT path").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.MoveFolderTree(context.Background(), tree, nil), "reading folders")
	})
	t.Run("the clear fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT path").WillReturnRows(folderRows())
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.MoveFolderTree(context.Background(), tree, nil), "clearing moved folders")
	})
	t.Run("a moved row cannot be written", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT path").WillReturnRows(folderRows())
		mock.ExpectExec("DELETE FROM resource_folders").WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.MoveFolderTree(context.Background(), tree, nil), "writing moved folder")
	})
	t.Run("a resource move fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE resources SET").WillReturnError(errors.New("down"))
		err := s.MoveFolderTree(context.Background(), tree, []Move{movedRow()})
		assert.ErrorContains(t, err, "moving resource")
	})
}

func TestPostgresPeople(t *testing.T) {
	s, mock := folderStorePG(t)
	mock.ExpectQuery("SELECT scope_id, SUM").WillReturnRows(
		sqlmock.NewRows([]string{"scope_id", "count", "email"}).
			AddRow("sub-1", 4, "one@example.com").
			AddRow("two@example.com", 1, nil).
			AddRow("sub-3", 0, nil))
	people, err := s.People(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Person{
		{ScopeID: "sub-1", Email: "one@example.com", Count: 4},
		{ScopeID: "two@example.com", Email: "two@example.com", Count: 1},
		{ScopeID: "sub-3", Email: "", Count: 0},
	}, people)

	s, mock = folderStorePG(t)
	mock.ExpectQuery("SELECT scope_id").WillReturnError(errors.New("down"))
	_, err = s.People(context.Background())
	assert.ErrorContains(t, err, "listing people")
}

// TestPostgresInsertRecordsTheFolderOrNothing: the folder is written in the
// file's transaction, so a failure recording it or committing both is a
// failed insert (#1872).
func TestPostgresInsertRecordsTheFolderOrNothing(t *testing.T) {
	r := Resource{ID: "id-1", Scope: ScopeGlobal, Path: "samples", Filename: "x.csv", URI: "mcp://global/samples/x.csv"}
	t.Run("the folder cannot be recorded", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.Insert(context.Background(), r), "recording folder")
	})
	t.Run("the commit fails", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO resource_folders").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit().WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.Insert(context.Background(), r), "committing insert")
	})
	t.Run("cannot begin", func(t *testing.T) {
		s, mock := folderStorePG(t)
		mock.ExpectBegin().WillReturnError(errors.New("down"))
		assert.ErrorContains(t, s.Insert(context.Background(), r), "beginning insert")
	})
}
