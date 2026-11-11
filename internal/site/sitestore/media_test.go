package sitestore

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

func TestMedia(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	cols := []string{"prefix", "scope", "scope_id", "folder", "created_by", "created_at"}
	mock.ExpectQuery(q("FROM site_media WHERE site_id = $1 ORDER BY prefix")).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("/media/", "global", "", "sites/m", "jane@example.com", rowTime))
	got, err := s.Media(ctx, "site_1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sites/m", got[0].Folder)

	mock.ExpectQuery(q("FROM site_media")).WillReturnError(errDown)
	_, err = s.Media(ctx, "site_1")
	require.ErrorContains(t, err, "list media")

	mounts := []site.Mount{{Prefix: "/media/", Scope: "global", Folder: "sites/m"}, {Prefix: "/v/", Scope: "persona", ScopeID: "web", Folder: "v"}}
	mock.ExpectBegin()
	mock.ExpectExec(q("DELETE FROM site_media WHERE site_id = $1 AND NOT (prefix = ANY($2))")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q("INSERT INTO site_media")).WithArgs("site_1", "/media/", "global", "", "sites/m", "jane@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q("INSERT INTO site_media")).WithArgs("site_1", "/v/", "persona", "web", "v", "jane@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.SetMedia(ctx, "site_1", mounts, "jane@example.com"))

	mock.ExpectBegin()
	mock.ExpectExec(q("DELETE FROM site_media")).WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetMedia(ctx, "site_1", mounts, "x"), "remove media")

	mock.ExpectBegin()
	mock.ExpectExec(q("DELETE FROM site_media")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(q("INSERT INTO site_media")).WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetMedia(ctx, "site_1", mounts, "x"), "mount /media/")
}
