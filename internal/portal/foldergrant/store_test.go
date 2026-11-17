package foldergrant

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/grantsource"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func store(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func TestStore_Reads(t *testing.T) {
	s, mock := store(t)
	ctx := context.Background()
	mock.ExpectQuery("SELECT DISTINCT folder_path").WithArgs("sub", "a@example.com", pq.Array([]string{"finance"})).
		WillReturnRows(sqlmock.NewRows([]string{"folder_path"}).AddRow("reports").AddRow("finance/close"))
	got, err := s.Writable(ctx, grantsource.Grantee{Subject: "sub", Email: "a@example.com", Personas: []string{"finance"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"reports", "finance/close"}, got)

	mock.ExpectQuery("SELECT EXISTS").WithArgs("reports").WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	ok, err := s.FolderExists(ctx, "reports")
	require.NoError(t, err)
	assert.True(t, ok)

	mock.ExpectQuery("FROM site_media").WithArgs("reports/q3").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("marketing"))
	sites, err := s.Sites(ctx, "reports/q3")
	require.NoError(t, err)
	assert.Equal(t, []string{"marketing"}, sites)
	got, err = Source{Store: s, External: func(string) bool { return false }}.WritableFolders(ctx, grantsource.Grantee{})
	require.Error(t, err, "no expectation set: the read fails through")
	assert.Nil(t, got)
}

func TestStore_ReadFailures(t *testing.T) {
	s, mock := store(t)
	ctx := context.Background()
	down := errors.New("down")
	mock.ExpectQuery("FROM portal_shares").WillReturnError(down)
	_, err := s.List(ctx, "reports")
	require.Error(t, err)
	mock.ExpectQuery("SELECT DISTINCT folder_path").WillReturnError(down)
	_, err = s.Writable(ctx, grantsource.Grantee{})
	require.Error(t, err)
	mock.ExpectQuery("SELECT EXISTS").WillReturnError(down)
	_, err = s.FolderExists(ctx, "x")
	require.Error(t, err)
	mock.ExpectQuery("FROM site_media").WillReturnError(down)
	_, err = s.Sites(ctx, "x")
	require.Error(t, err)
	mock.ExpectQuery("SELECT id FROM portal_shares").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("INSERT INTO portal_shares").WillReturnError(down)
	require.Error(t, s.Add(ctx, &Grant{Folder: "x", Persona: "p"}))
	mock.ExpectQuery("SELECT DISTINCT folder_path").WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
	_, err = s.Writable(ctx, grantsource.Grantee{})
	require.Error(t, err, "a row that does not scan")
	mock.ExpectQuery("FROM portal_shares").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.List(ctx, "reports")
	require.Error(t, err)
	mock.ExpectQuery("FROM site_media").WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
	_, err = s.Sites(ctx, "x")
	require.Error(t, err)
}

func TestSource_AnExternalPersonaHoldsNone(t *testing.T) {
	s, _ := store(t)
	got, err := Source{Store: s, External: func(p string) bool { return p == "guests" }}.
		WritableFolders(context.Background(), grantsource.Grantee{Personas: []string{"analyst", "guests"}})
	require.NoError(t, err)
	assert.Equal(t, []string{}, got, "a person any of whose personas is external never sees Global")
}
