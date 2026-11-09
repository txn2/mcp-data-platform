package sitestore

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

var grantCols = []string{"email", "level", "granted_by", "created_at"}

func TestGrants(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("FROM site_grants WHERE site_id = $1 ORDER BY level, email")).WithArgs("site_1").WillReturnRows(
		sqlmock.NewRows(grantCols).AddRow("mo@example.com", site.LevelManage, "jane@example.com", rowTime))
	got, err := s.Grants(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Grant{{Email: "mo@example.com", Level: site.LevelManage, GrantedBy: "jane@example.com", CreatedAt: rowTime}}, got)

	mock.ExpectQuery(q("FROM site_grants")).WillReturnRows(sqlmock.NewRows(grantCols))
	got, err = s.Grants(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Grant{}, got, "none is [], never null")

	mock.ExpectQuery(q("FROM site_grants")).WillReturnError(errDown)
	_, err = s.Grants(ctx, "site_1")
	require.ErrorContains(t, err, "list grants")

	mock.ExpectQuery(q("FROM site_grants")).WillReturnRows(sqlmock.NewRows([]string{"email"}).AddRow("x"))
	_, err = s.Grants(ctx, "site_1")
	require.ErrorContains(t, err, "read grant")

	mock.ExpectQuery(q("FROM site_grants")).WillReturnRows(
		sqlmock.NewRows(grantCols).AddRow("a@example.com", site.LevelView, "b", rowTime).RowError(0, errDown))
	_, err = s.Grants(ctx, "site_1")
	require.ErrorContains(t, err, "list grants")
}

func TestGrantLevel(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("SELECT level FROM site_grants WHERE site_id = $1 AND email = $2")).WithArgs("site_1", "a@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"level"}).AddRow(site.LevelView))
	level, err := s.GrantLevel(ctx, "site_1", "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, site.LevelView, level)

	mock.ExpectQuery(q("SELECT level")).WillReturnError(sql.ErrNoRows)
	level, err = s.GrantLevel(ctx, "site_1", "none@example.com")
	require.NoError(t, err, "no row is no grant, not a failure")
	assert.Empty(t, level)

	mock.ExpectQuery(q("SELECT level")).WillReturnError(errDown)
	_, err = s.GrantLevel(ctx, "site_1", "a@example.com")
	require.ErrorContains(t, err, "read grant")
}

func TestSetGrant(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	g := site.Grant{Email: "a@example.com", Level: site.LevelManage, GrantedBy: "jane@example.com"}

	mock.ExpectExec(q("ON CONFLICT (site_id, email) DO UPDATE")).WithArgs("site_1", g.Email, g.Level, g.GrantedBy).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetGrant(ctx, "site_1", g))

	mock.ExpectExec(q("INSERT INTO site_grants")).WillReturnError(errDown)
	require.ErrorContains(t, s.SetGrant(ctx, "site_1", g), "set grant")
}

func TestRemoveGrant(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectExec(q("DELETE FROM site_grants WHERE site_id = $1 AND email = $2")).WithArgs("site_1", "a@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RemoveGrant(ctx, "site_1", "a@example.com"))

	mock.ExpectExec(q("DELETE FROM site_grants")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.RemoveGrant(ctx, "site_1", "a@example.com"), site.ErrNoGrant)

	mock.ExpectExec(q("DELETE FROM site_grants")).WillReturnError(errDown)
	require.ErrorContains(t, s.RemoveGrant(ctx, "site_1", "a@example.com"), "remove grant")
}

func TestSiteAccess(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	cols := []string{"owner_email", "internal_view", "level"}

	mock.ExpectQuery(q("LEFT JOIN site_grants g ON g.site_id = s.id AND g.email = $2")).
		WithArgs("site_1", "pat@client.example"). // normalized before it is matched
		WillReturnRows(sqlmock.NewRows(cols).AddRow("jane@example.com", true, site.LevelView))
	got, err := s.SiteAccess(ctx, "site_1", "  PAT@Client.Example ")
	require.NoError(t, err)
	assert.Equal(t, &site.Access{OwnerEmail: "jane@example.com", InternalView: true, Level: site.LevelView}, got)

	mock.ExpectQuery(q("FROM sites s")).WillReturnError(sql.ErrNoRows)
	_, err = s.SiteAccess(ctx, "site_none", "a@example.com")
	require.ErrorIs(t, err, site.ErrNotFound)

	mock.ExpectQuery(q("FROM sites s")).WillReturnError(errDown)
	_, err = s.SiteAccess(ctx, "site_1", "a@example.com")
	require.ErrorContains(t, err, "read access")
}
