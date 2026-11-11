package sitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

var (
	rowTime   = time.Unix(1700000000, 0).UTC()
	errDown   = errors.New("down")
	errDupKey = &pq.Error{Code: uniqueViolation}
)

var (
	siteCols    = []string{"id", "name", "description", "owner_email", "main_environment", "created_by", "created_at", "updated_at"}
	envCols     = []string{"name", "release_number", "requires_approval", "public", "internal_view", "access_changed_at", "updated_by", "updated_at", "keep_releases", "kept"}
	releaseCols = []string{"id", "site_id", "number", "source", "note", "file_count", "total_bytes", "manifest_sha256", "redirects", "created_by", "created_at", "base_number", "manifest_parent", "manifest_depth", "rendered", "pruned_at"}
	fileCols    = []string{"path", "sha256", "content_type", "size", "gzip_size", "brotli_size", "stored_in"}
	deployCols  = []string{"environment", "action", "from_release", "to_release", "actor", "created_at"}
	domainCols  = []string{"hostname", "site_id", "name", "environment", "application_id", "application", "created_by", "created_at"}
	routeCols   = []string{"id", "name", "env", "number", "private", "part_of"}
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return New(db), mock
}

func q(s string) string { return regexp.QuoteMeta(s) }

func siteRows(name string) *sqlmock.Rows {
	return sqlmock.NewRows(siteCols).AddRow("site_1", name, "words", "jane@example.com", "dev", "jane@example.com", rowTime, rowTime)
}

func releaseRow(n int, redirects string) []driver.Value {
	return []driver.Value{"srel_1", "site_1", n, "ci", "note", 2, int64(30), "digest", []byte(redirects), "jane@example.com", rowTime, 0, "", 0, false, nil}
}

func domainRow(host string) []driver.Value {
	return []driver.Value{host, "site_1", "marketing", "prod", "", "", "admin@example.com", rowTime}
}

func TestIDs(t *testing.T) {
	assert.True(t, strings.HasPrefix(NewSiteID(), "site_"), "the service tells an id from a name by this prefix")
	assert.True(t, strings.HasPrefix(NewReleaseID(), "srel_"))
	assert.NotEqual(t, NewSiteID(), NewSiteID())
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	st := func() *site.Site {
		return &site.Site{
			ID: "site_1", Name: "marketing", Description: "d", OwnerEmail: "jane@example.com", CreatedBy: "jane@example.com",
			Environments: []site.Environment{{Name: "dev"}, {Name: "prod", RequiresApproval: true}},
		}
	}
	t.Run("writes the site and each environment", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("INSERT INTO sites")).WithArgs("site_1", "marketing", "d", "jane@example.com", "jane@example.com", "").
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(rowTime, rowTime))
		mock.ExpectExec(q("INSERT INTO site_environments")).WithArgs("site_1", "dev", false, false, false, "jane@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_environments")).WithArgs("site_1", "prod", true, false, false, "jane@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		got := st()
		require.NoError(t, s.Create(ctx, got))
		assert.Equal(t, rowTime, got.CreatedAt)
	})
	t.Run("a name taken", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("INSERT INTO sites")).WillReturnError(errDupKey)
		mock.ExpectRollback()
		require.ErrorIs(t, s.Create(ctx, st()), site.ErrNameTaken)
	})
	t.Run("another failure", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("INSERT INTO sites")).WillReturnError(errDown)
		mock.ExpectRollback()
		err := s.Create(ctx, st())
		require.ErrorContains(t, err, "create site")
		assert.NotErrorIs(t, err, site.ErrNameTaken)
	})
	t.Run("an environment that cannot be written rolls the site back", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("INSERT INTO sites")).WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(rowTime, rowTime))
		mock.ExpectExec(q("INSERT INTO site_environments")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.Create(ctx, st()), "create environment dev")
	})
	t.Run("the transaction will not open", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin().WillReturnError(errDown)
		require.ErrorContains(t, s.Create(ctx, st()), "begin")
	})
	t.Run("the commit fails", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("INSERT INTO sites")).WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(rowTime, rowTime))
		mock.ExpectExec(q("INSERT INTO site_environments")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_environments")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errDown)
		require.ErrorContains(t, s.Create(ctx, st()), "commit")
	})
}

func TestGetAndGetByName(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	env := func() *sqlmock.Rows {
		return sqlmock.NewRows(envCols).AddRow("dev", 3, false, false, false, rowTime, "jane@example.com", rowTime, nil, 0).AddRow("prod", 0, true, true, false, rowTime, "", rowTime, 5, 3)
	}

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WithArgs("site_1").WillReturnRows(siteRows("marketing"))
	mock.ExpectQuery(q("FROM site_environments e WHERE e.site_id = $1")).WithArgs("site_1", siteconfig.DefaultKeepReleases).WillReturnRows(env())
	mock.ExpectQuery(q("FROM site_media WHERE site_id = $1")).WithArgs("site_1").WillReturnRows(
		sqlmock.NewRows([]string{"prefix", "scope", "scope_id", "folder", "created_by", "created_at"}).AddRow("/media/", "global", "", "sites/m", "jane@example.com", rowTime))
	got, err := s.Get(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, "marketing", got.Name)
	assert.Equal(t, "dev", got.MainEnvironment, "the environment the main address serves is read back")
	assert.True(t, got.Environment("prod").Public, "each environment's access is read back")
	require.Len(t, got.Environments, 2)
	assert.Equal(t, 3, got.Environment("dev").Release)
	assert.True(t, got.Environment("prod").RequiresApproval)
	require.Len(t, got.Media, 1, "a read of one site carries its media")
	assert.Equal(t, "/media/", got.Media[0].Prefix)
	dev, prod := got.Environment("dev"), got.Environment("prod")
	assert.True(t, dev.KeepDefault, "no limit of its own follows the deployment's default")
	assert.Equal(t, siteconfig.DefaultKeepReleases, dev.Keep)
	assert.Nil(t, dev.KeepReleases)
	assert.False(t, prod.KeepDefault)
	assert.Equal(t, 5, prod.Keep, "a limit of its own is the limit in force")
	require.NotNil(t, prod.KeepReleases)
	assert.Equal(t, 3, prod.Kept, "how many releases its window keeps now")

	mock.ExpectQuery(q("FROM sites WHERE name = $1")).WithArgs("marketing").WillReturnRows(siteRows("marketing"))
	mock.ExpectQuery(q("FROM site_environments")).WillReturnRows(sqlmock.NewRows(envCols))
	mock.ExpectQuery(q("FROM site_media")).WillReturnRows(sqlmock.NewRows([]string{"prefix", "scope", "scope_id", "folder", "created_by", "created_at"}))
	got, err = s.GetByName(ctx, "marketing")
	require.NoError(t, err)
	assert.NotNil(t, got.Environments, "no environment is [], never null")
	assert.Empty(t, got.Environments)
	assert.NotNil(t, got.Media, "no media is [], never null")

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx, "none")
	require.ErrorIs(t, err, site.ErrNotFound)

	mock.ExpectQuery(q("FROM sites WHERE name = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.GetByName(ctx, "none")
	require.ErrorIs(t, err, site.ErrNotFound)

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WillReturnError(errDown)
	_, err = s.Get(ctx, "site_1")
	require.ErrorContains(t, err, "read site")

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WillReturnRows(siteRows("marketing"))
	mock.ExpectQuery(q("FROM site_environments")).WillReturnError(errDown)
	_, err = s.Get(ctx, "site_1")
	require.ErrorContains(t, err, "list environments")

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WillReturnRows(siteRows("marketing"))
	mock.ExpectQuery(q("FROM site_environments")).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("dev"))
	_, err = s.Get(ctx, "site_1")
	require.ErrorContains(t, err, "read environment")

	mock.ExpectQuery(q("FROM sites WHERE id = $1")).WillReturnRows(siteRows("marketing"))
	mock.ExpectQuery(q("FROM site_environments")).WillReturnRows(
		sqlmock.NewRows(envCols).AddRow("dev", 1, false, false, false, rowTime, "", rowTime, nil, 0).RowError(0, errDown))
	_, err = s.Get(ctx, "site_1")
	require.ErrorContains(t, err, "list environments")
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnRows(
		sqlmock.NewRows(siteCols).
			AddRow("site_1", "a", "", "o", false, "o", rowTime, rowTime).
			AddRow("site_2", "b", "", "o", false, "o", rowTime, rowTime))
	mock.ExpectQuery(q("FROM site_environments")).WithArgs("site_1", siteconfig.DefaultKeepReleases).WillReturnRows(sqlmock.NewRows(envCols).AddRow("dev", 1, false, false, false, rowTime, "", rowTime, nil, 0))
	mock.ExpectQuery(q("FROM site_environments")).WithArgs("site_2", siteconfig.DefaultKeepReleases).WillReturnRows(sqlmock.NewRows(envCols))
	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Len(t, got[0].Environments, 1)
	assert.Empty(t, got[1].Environments)

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnRows(sqlmock.NewRows(siteCols))
	got, err = s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []site.Site{}, got, "no site is [], never null")

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnError(errDown)
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "list sites")

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "read site")

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnRows(
		sqlmock.NewRows(siteCols).AddRow("site_1", "a", "", "o", false, "o", rowTime, rowTime).RowError(0, errDown))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "list sites")

	mock.ExpectQuery(q("FROM sites ORDER BY name")).WillReturnRows(siteRows("a"))
	mock.ExpectQuery(q("FROM site_environments")).WillReturnError(errDown)
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "list environments")
}

func TestUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	st := &site.Site{ID: "site_1", Description: "new", OwnerEmail: "o@example.com"}

	mock.ExpectExec(q("UPDATE sites")).WithArgs("site_1", "new", "o@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Update(ctx, st))
	mock.ExpectExec(q("UPDATE sites")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Update(ctx, st), site.ErrNotFound)
	mock.ExpectExec(q("UPDATE sites")).WillReturnError(errDown)
	require.ErrorContains(t, s.Update(ctx, st), "update site")
	mock.ExpectExec(q("UPDATE sites")).WillReturnResult(sqlmock.NewErrorResult(errDown))
	require.ErrorContains(t, s.Update(ctx, st), "rows affected")

	mock.ExpectExec(q("DELETE FROM sites")).WithArgs("site_1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(ctx, "site_1"))
	mock.ExpectExec(q("DELETE FROM sites")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Delete(ctx, "none"), site.ErrNotFound)
	mock.ExpectExec(q("DELETE FROM sites")).WillReturnError(errDown)
	require.ErrorContains(t, s.Delete(ctx, "site_1"), "delete site")
}

func TestSetEnvironment(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	yes := true
	set := site.EnvironmentSettings{RequiresApproval: &yes}
	mock.ExpectExec(q("ON CONFLICT (site_id, name) DO UPDATE")).WithArgs("site_1", "qa", &yes, nil, nil, "jane@example.com", sql.NullInt64{}, false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetEnvironment(ctx, "site_1", "qa", set, "jane@example.com"))
	five := 5
	mock.ExpectExec(q("keep_releases = CASE WHEN $8 THEN NULL ELSE COALESCE($7::int, site_environments.keep_releases) END")).
		WithArgs("site_1", "qa", nil, nil, nil, "jane@example.com", sql.NullInt64{Int64: 5, Valid: true}, false).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetEnvironment(ctx, "site_1", "qa", site.EnvironmentSettings{Keep: &five}, "jane@example.com"), "a limit is written")
	mock.ExpectExec(q("INSERT INTO site_environments")).
		WithArgs("site_1", "qa", nil, nil, nil, "jane@example.com", sql.NullInt64{}, true).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetEnvironment(ctx, "site_1", "qa", site.EnvironmentSettings{KeepDefault: true}, "jane@example.com"), "back to the default")
	mock.ExpectExec(q("INSERT INTO site_environments")).WillReturnError(errDown)
	require.ErrorContains(t, s.SetEnvironment(ctx, "site_1", "qa", set, "x"), "set environment")
}

func newRelease() *site.Release {
	return &site.Release{
		ID: "srel_1", SiteID: "site_1", Number: 2, Source: "ci", Note: "n", FileCount: 2, TotalBytes: 30, ManifestSHA256: "digest",
		Redirects: []site.Redirect{{From: "/a", To: "/b", Status: 301}}, CreatedBy: "jane@example.com",
		Files: []site.File{
			{Path: "a.css", SHA256: "aa", ContentType: "text/css", Size: 10, GzipSize: 4, BrotliSize: 3},
			{Path: "b.png", SHA256: "bb", ContentType: "image/png", Size: 20},
		},
	}
}

func TestRecordRelease(t *testing.T) {
	ctx := context.Background()
	lock := func(mock sqlmock.Sqlmock) {
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT 1 FROM sites WHERE id = $1 FOR UPDATE")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	}
	t.Run("writes the release and its files under the site's lock", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).
			WithArgs("srel_1", "site_1", 2, "ci", "n", 2, int64(30), "digest", []byte(`[{"from":"/a","to":"/b","status":301}]`), "jane@example.com", 0, "", 0, false).
			WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		mock.ExpectExec(q("INSERT INTO site_release_files")).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectCommit()
		r := newRelease()
		require.NoError(t, s.RecordRelease(ctx, r, nil))
		assert.Equal(t, rowTime, r.CreatedAt)
	})
	t.Run("no such site", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("FOR UPDATE")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.RecordRelease(ctx, newRelease(), nil), site.ErrNotFound)
	})
	t.Run("the lock fails", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("FOR UPDATE")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.RecordRelease(ctx, newRelease(), nil), "lock site")
	})
	t.Run("the number was taken by another publish", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnError(errDupKey)
		mock.ExpectRollback()
		require.ErrorIs(t, s.RecordRelease(ctx, newRelease(), nil), site.ErrStale)
	})
	t.Run("the insert fails", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.RecordRelease(ctx, newRelease(), nil), "record release")
	})
	t.Run("the files cannot be written", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		mock.ExpectExec(q("INSERT INTO site_release_files")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.RecordRelease(ctx, newRelease(), nil), "record files")
	})
	t.Run("a release with nothing of its own writes no file rows", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		mock.ExpectCommit()
		r := newRelease()
		r.Files = nil
		require.NoError(t, s.RecordRelease(ctx, r, nil))
	})
	t.Run("a move lands in the same transaction and fills in the number", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(7))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		mock.ExpectExec(q("INSERT INTO site_release_files")).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectQuery(q("FOR SHARE")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		mock.ExpectQuery(q("FOR UPDATE OF e")).WillReturnRows(sqlmock.NewRows([]string{"n", "pruned"}).AddRow(6, false))
		mock.ExpectExec(q("UPDATE site_environments")).WithArgs("site_1", "default", 7, "a@b").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WithArgs("site_1", "default", "publish", 6, 7, "a@b").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.RecordRelease(ctx, newRelease(), &site.Pointer{SiteID: "site_1", Environment: "default", After: 6, Action: "publish", Actor: "a@b"}))
	})
	t.Run("a move that lost the race undoes the release with it", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("SELECT COALESCE(MAX(number), 0) + 1")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(7))
		mock.ExpectQuery(q("INSERT INTO site_releases")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
		mock.ExpectExec(q("INSERT INTO site_release_files")).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectQuery(q("FOR SHARE")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		mock.ExpectQuery(q("FOR UPDATE OF e")).WillReturnRows(sqlmock.NewRows([]string{"n", "pruned"}).AddRow(9, false))
		mock.ExpectRollback()
		require.ErrorIs(t, s.RecordRelease(ctx, newRelease(), &site.Pointer{SiteID: "site_1", Environment: "default", After: 6, Action: "publish", Actor: "a@b"}), site.ErrStale)
	})
}

func TestReleaseReads(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	rows := func(v []driver.Value) *sqlmock.Rows { return sqlmock.NewRows(releaseCols).AddRow(v...) }

	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WithArgs("site_1", 2).
		WillReturnRows(rows(releaseRow(2, `[{"from":"/a","to":"/b","status":302}]`)))
	r, err := s.Release(ctx, "site_1", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Number)
	assert.Equal(t, []site.Redirect{{From: "/a", To: "/b", Status: 302}}, r.Redirects)

	mock.ExpectQuery(q("FROM site_releases WHERE id = $1")).WithArgs("srel_1").WillReturnRows(rows(releaseRow(2, `[]`)))
	r, err = s.ReleaseByID(ctx, "srel_1")
	require.NoError(t, err)
	assert.Equal(t, "digest", r.ManifestSHA256)
	assert.Equal(t, []site.Redirect{}, r.Redirects, "no redirect is [], never null")

	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WillReturnError(sql.ErrNoRows)
	_, err = s.Release(ctx, "site_1", 9)
	require.ErrorIs(t, err, site.ErrNoRelease)

	mock.ExpectQuery(q("FROM site_releases WHERE id = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.ReleaseByID(ctx, "none")
	require.ErrorIs(t, err, site.ErrNoRelease)

	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WillReturnError(errDown)
	_, err = s.Release(ctx, "site_1", 1)
	require.ErrorContains(t, err, "read release")

	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WillReturnRows(rows(releaseRow(1, `{`)))
	_, err = s.Release(ctx, "site_1", 1)
	require.ErrorContains(t, err, "decode redirects")
}

func TestReleases(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("ORDER BY number DESC")).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows(releaseCols).AddRow(releaseRow(2, `[]`)...).AddRow(releaseRow(1, `[]`)...))
	got, err := s.Releases(ctx, "site_1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, got[0].Number)

	mock.ExpectQuery(q("ORDER BY number DESC")).WillReturnRows(sqlmock.NewRows(releaseCols))
	got, err = s.Releases(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Release{}, got)

	mock.ExpectQuery(q("ORDER BY number DESC")).WillReturnError(errDown)
	_, err = s.Releases(ctx, "site_1")
	require.ErrorContains(t, err, "list releases")

	mock.ExpectQuery(q("ORDER BY number DESC")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.Releases(ctx, "site_1")
	require.ErrorContains(t, err, "read release")

	mock.ExpectQuery(q("ORDER BY number DESC")).WillReturnRows(sqlmock.NewRows(releaseCols).AddRow(releaseRow(1, `[]`)...).RowError(0, errDown))
	_, err = s.Releases(ctx, "site_1")
	require.ErrorContains(t, err, "list releases")
}

func TestFiles(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("WITH RECURSIVE chain")).WithArgs("srel_1", site.TreeOutput).
		WillReturnRows(sqlmock.NewRows(fileCols).AddRow("a.css", "aa", "text/css", int64(10), int64(4), int64(3), ""))
	got, err := s.Files(ctx, "srel_1")
	require.NoError(t, err)
	assert.Equal(t, []site.File{{Path: "a.css", SHA256: "aa", ContentType: "text/css", Size: 10, GzipSize: 4, BrotliSize: 3}}, got)

	mock.ExpectQuery(q("FROM site_release_files")).WillReturnRows(sqlmock.NewRows(fileCols))
	got, err = s.Files(ctx, "srel_1")
	require.NoError(t, err)
	assert.Equal(t, []site.File{}, got)

	mock.ExpectQuery(q("FROM site_release_files")).WillReturnError(errDown)
	_, err = s.Files(ctx, "srel_1")
	require.ErrorContains(t, err, "list files")

	mock.ExpectQuery(q("FROM site_release_files")).WillReturnRows(sqlmock.NewRows([]string{"path"}).AddRow("a"))
	_, err = s.Files(ctx, "srel_1")
	require.ErrorContains(t, err, "read file")

	mock.ExpectQuery(q("FROM site_release_files")).WillReturnRows(sqlmock.NewRows(fileCols).AddRow("a", "b", "c", int64(1), int64(0), int64(0), "").RowError(0, errDown))
	_, err = s.Files(ctx, "srel_1")
	require.ErrorContains(t, err, "list files")
}

func TestStoredFiles(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery(q("SELECT DISTINCT CASE WHEN f.stored_in")).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows(fileCols).AddRow("", "aa", "", int64(0), int64(4), int64(0), ""))
	got, err := s.StoredFiles(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.File{{SHA256: "aa", GzipSize: 4}}, got)

	mock.ExpectQuery(q("SELECT DISTINCT CASE WHEN f.stored_in")).WillReturnError(errDown)
	_, err = s.StoredFiles(ctx, "site_1")
	require.ErrorContains(t, err, "list stored files")
}

func TestPoint(t *testing.T) {
	ctx := context.Background()
	p := site.Pointer{SiteID: "site_1", Environment: "prod", After: 1, To: 2, Action: site.ActionPromote, Actor: "a@example.com"}
	shared := func(mock sqlmock.Sqlmock) {
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT 1 FROM sites WHERE id = $1 FOR SHARE")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	}
	currentPruned := func(mock sqlmock.Sqlmock, v any, pruned bool) {
		shared(mock)
		mock.ExpectQuery(q("SELECT e.release_number")).WithArgs("site_1", "prod", 2).
			WillReturnRows(sqlmock.NewRows([]string{"release_number", "pruned"}).AddRow(v, pruned))
	}
	current := func(mock sqlmock.Sqlmock, v any) { currentPruned(mock, v, false) }

	t.Run("moves the pointer and records the deployment", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, int64(1))
		mock.ExpectExec(q("UPDATE site_environments SET release_number")).WithArgs("site_1", "prod", 2, "a@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WithArgs("site_1", "prod", site.ActionPromote, 1, 2, "a@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.Point(ctx, p))
	})
	t.Run("an environment serving nothing is at release 0", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, nil)
		mock.ExpectExec(q("UPDATE site_environments")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		first := p
		first.After = 0
		require.NoError(t, s.Point(ctx, first))
	})
	t.Run("another move got there first", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, int64(5))
		mock.ExpectRollback()
		require.ErrorIs(t, s.Point(ctx, p), site.ErrStale)
	})
	t.Run("a null pointer is stale for a move decided against a release", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, nil)
		mock.ExpectRollback()
		require.ErrorIs(t, s.Point(ctx, p), site.ErrStale)
	})
	t.Run("no such environment", func(t *testing.T) {
		s, mock := newMock(t)
		shared(mock)
		mock.ExpectQuery(q("SELECT e.release_number")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.Point(ctx, p), site.ErrNoEnvironment)
	})
	t.Run("the lock fails", func(t *testing.T) {
		s, mock := newMock(t)
		shared(mock)
		mock.ExpectQuery(q("SELECT e.release_number")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.Point(ctx, p), "lock environment")
	})
	t.Run("a release a sweep pruned is never pointed at", func(t *testing.T) {
		s, mock := newMock(t)
		currentPruned(mock, int64(1), true)
		mock.ExpectRollback()
		require.ErrorIs(t, s.Point(ctx, p), site.ErrPruned)
	})
	t.Run("no such site", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("FOR SHARE")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.Point(ctx, p), site.ErrNotFound)
	})
	t.Run("the site's lock fails", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("FOR SHARE")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.Point(ctx, p), "lock site")
	})
	t.Run("the move fails", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, int64(1))
		mock.ExpectExec(q("UPDATE site_environments")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.Point(ctx, p), "move environment")
	})
	t.Run("the record fails and the move goes with it", func(t *testing.T) {
		s, mock := newMock(t)
		current(mock, int64(1))
		mock.ExpectExec(q("UPDATE site_environments")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.Point(ctx, p), "record deployment")
	})
}

func TestDeployments(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("FROM site_deployments")).WithArgs("site_1", "prod", defaultListing).
		WillReturnRows(sqlmock.NewRows(deployCols).AddRow("prod", site.ActionRollback, 3, 2, "a", rowTime))
	got, err := s.Deployments(ctx, "site_1", "prod", 0)
	require.NoError(t, err)
	assert.Equal(t, []site.Deployment{{Environment: "prod", Action: site.ActionRollback, From: 3, To: 2, Actor: "a", CreatedAt: rowTime}}, got)

	mock.ExpectQuery(q("FROM site_deployments")).WithArgs("site_1", "", 5).WillReturnRows(sqlmock.NewRows(deployCols))
	got, err = s.Deployments(ctx, "site_1", "", 5)
	require.NoError(t, err)
	assert.Equal(t, []site.Deployment{}, got)

	mock.ExpectQuery(q("FROM site_deployments")).WillReturnError(errDown)
	_, err = s.Deployments(ctx, "site_1", "", 5)
	require.ErrorContains(t, err, "list deployments")

	mock.ExpectQuery(q("FROM site_deployments")).WillReturnRows(sqlmock.NewRows([]string{"environment"}).AddRow("x"))
	_, err = s.Deployments(ctx, "site_1", "", 5)
	require.ErrorContains(t, err, "read deployment")

	mock.ExpectQuery(q("FROM site_deployments")).WillReturnRows(sqlmock.NewRows(deployCols).AddRow("p", "a", 1, 2, "x", rowTime).RowError(0, errDown))
	_, err = s.Deployments(ctx, "site_1", "", 5)
	require.ErrorContains(t, err, "list deployments")
}

func TestAddDomain(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	d := &site.Domain{Hostname: "www.example.com", SiteID: "site_1", Environment: "prod", CreatedBy: "a"}

	mock.ExpectQuery(q("INSERT INTO site_domains")).WithArgs("www.example.com", "site_1", "prod", "", "a").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
	require.NoError(t, s.AddDomain(ctx, d))
	assert.Equal(t, rowTime, d.CreatedAt)

	mock.ExpectQuery(q("INSERT INTO site_domains")).WillReturnError(errDupKey)
	require.ErrorIs(t, s.AddDomain(ctx, d), site.ErrDomainTaken)

	mock.ExpectQuery(q("INSERT INTO site_domains")).WillReturnError(errDown)
	err := s.AddDomain(ctx, d)
	require.ErrorContains(t, err, "add domain")
	assert.NotErrorIs(t, err, site.ErrDomainTaken)
}

func TestDomainAndDomains(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("WHERE d.hostname = $1")).WithArgs("www.example.com").
		WillReturnRows(sqlmock.NewRows(domainCols).AddRow(domainRow("www.example.com")...))
	d, err := s.Domain(ctx, "www.example.com")
	require.NoError(t, err)
	assert.Equal(t, "marketing", d.Site)
	assert.Equal(t, rowTime, d.CreatedAt)

	mock.ExpectQuery(q("WHERE d.hostname = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.Domain(ctx, "none.example.com")
	require.ErrorIs(t, err, site.ErrNoDomain)

	mock.ExpectQuery(q("WHERE d.hostname = $1")).WillReturnError(errDown)
	_, err = s.Domain(ctx, "x.example.com")
	require.ErrorContains(t, err, "read domain")

	mock.ExpectQuery(q("ORDER BY d.hostname")).WillReturnRows(
		sqlmock.NewRows(domainCols).AddRow(domainRow("a.example.com")...).AddRow(domainRow("b.example.com")...))
	list, err := s.Domains(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "b.example.com", list[1].Hostname)

	mock.ExpectQuery(q("ORDER BY d.hostname")).WillReturnRows(sqlmock.NewRows(domainCols))
	list, err = s.Domains(ctx)
	require.NoError(t, err)
	assert.Equal(t, []site.Domain{}, list)

	mock.ExpectQuery(q("ORDER BY d.hostname")).WillReturnError(errDown)
	_, err = s.Domains(ctx)
	require.ErrorContains(t, err, "list domains")

	mock.ExpectQuery(q("ORDER BY d.hostname")).WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("x"))
	_, err = s.Domains(ctx)
	require.ErrorContains(t, err, "read domain")

	mock.ExpectQuery(q("ORDER BY d.hostname")).WillReturnRows(sqlmock.NewRows(domainCols).AddRow(domainRow("a.example.com")...).RowError(0, errDown))
	_, err = s.Domains(ctx)
	require.ErrorContains(t, err, "list domains")
}

func TestSiteDomains(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("WHERE d.site_id = $1 ORDER BY d.hostname")).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows(domainCols).AddRow(domainRow("a.example.com")...))
	got, err := s.SiteDomains(ctx, "site_1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a.example.com", got[0].Hostname)

	mock.ExpectQuery(q("WHERE d.site_id = $1")).WillReturnRows(sqlmock.NewRows(domainCols))
	got, err = s.SiteDomains(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, []site.Domain{}, got, "none is [], never null")

	mock.ExpectQuery(q("WHERE d.site_id = $1")).WillReturnError(errDown)
	_, err = s.SiteDomains(ctx, "site_1")
	require.ErrorContains(t, err, "list domains")
}

func TestRemoveDomain(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectExec(q("DELETE FROM site_domains")).WithArgs("www.example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RemoveDomain(ctx, "www.example.com"))
	mock.ExpectExec(q("DELETE FROM site_domains")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.RemoveDomain(ctx, "none.example.com"), site.ErrNoDomain)
	mock.ExpectExec(q("DELETE FROM site_domains")).WillReturnError(errDown)
	require.ErrorContains(t, s.RemoveDomain(ctx, "x.example.com"), "remove domain")
}

func TestRouteHost(t *testing.T) {
	ctx := context.Background()
	domain := func(mock sqlmock.Sqlmock, v []driver.Value) {
		mock.ExpectQuery(q("WHERE d.hostname = $1")).WillReturnRows(sqlmock.NewRows(domainCols).AddRow(v...))
	}
	t.Run("an added site hostname serves its environment's release", func(t *testing.T) {
		s, mock := newMock(t)
		domain(mock, domainRow("www.example.com"))
		mock.ExpectQuery(q("WHERE s.id = $1 AND e.name = $2")).WithArgs("site_1", "prod").
			WillReturnRows(sqlmock.NewRows(routeCols).AddRow("site_1", "marketing", "prod", 2, false, ""))
		mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WithArgs("site_1", 2).
			WillReturnRows(sqlmock.NewRows(releaseCols).AddRow(releaseRow(2, `[]`)...))
		r, err := s.RouteHost(ctx, "www.example.com")
		require.NoError(t, err)
		assert.Equal(t, "marketing", r.Site)
		assert.Equal(t, "prod", r.Environment)
		require.NotNil(t, r.Release)
		assert.Equal(t, 2, r.Release.Number)
		assert.Empty(t, r.Application)
		assert.False(t, r.Private, "the environment row says it is public")
	})
	t.Run("no such hostname", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(q("WHERE d.hostname = $1")).WillReturnError(sql.ErrNoRows)
		_, err := s.RouteHost(ctx, "none.example.com")
		require.ErrorIs(t, err, site.ErrNoDomain)
	})
	t.Run("an application's hostname names the application alone", func(t *testing.T) {
		s, mock := newMock(t)
		domain(mock, []driver.Value{"shop.example.com", "", "", "", "app_1", "shop", "a", rowTime})
		r, err := s.RouteHost(ctx, "shop.example.com")
		require.NoError(t, err)
		assert.Equal(t, &site.Route{Application: "shop", Hostname: "shop.example.com"}, r)
	})
	t.Run("an environment serving nothing has no release", func(t *testing.T) {
		s, mock := newMock(t)
		domain(mock, domainRow("www.example.com"))
		mock.ExpectQuery(q("WHERE s.id = $1 AND e.name = $2")).WillReturnRows(sqlmock.NewRows(routeCols).AddRow("site_1", "marketing", "prod", 0, false, ""))
		r, err := s.RouteHost(ctx, "www.example.com")
		require.NoError(t, err)
		assert.Nil(t, r.Release)
	})
}

func TestRouteOwn(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	mock.ExpectQuery(q("WHERE s.name = $1 AND e.name = s.main_environment")).WithArgs("marketing").
		WillReturnRows(sqlmock.NewRows(routeCols).AddRow("site_1", "marketing", site.EnvDev, 1, true, ""))
	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WillReturnRows(sqlmock.NewRows(releaseCols).AddRow(releaseRow(1, `[]`)...))
	r, err := s.RouteOwn(ctx, "marketing")
	require.NoError(t, err)
	assert.Equal(t, 1, r.Release.Number)
	assert.Equal(t, site.EnvDev, r.Environment)
	assert.True(t, r.Private, "a private environment answers only to those who may open it")

	mock.ExpectQuery(q("WHERE s.name = $1 AND e.name = s.main_environment")).WillReturnError(sql.ErrNoRows)
	_, err = s.RouteOwn(ctx, "none")
	require.ErrorIs(t, err, site.ErrNoDomain)

	mock.ExpectQuery(q("WHERE s.name = $1 AND e.name = s.main_environment")).WillReturnError(errDown)
	_, err = s.RouteOwn(ctx, "marketing")
	require.ErrorContains(t, err, "read route")

	mock.ExpectQuery(q("WHERE s.name = $1 AND e.name = s.main_environment")).WillReturnRows(sqlmock.NewRows(routeCols).AddRow("site_1", "marketing", site.EnvDev, 9, true, ""))
	mock.ExpectQuery(q("WHERE site_id = $1 AND number = $2")).WillReturnError(sql.ErrNoRows)
	_, err = s.RouteOwn(ctx, "marketing")
	require.ErrorIs(t, err, site.ErrNoRelease, "a pointer at a release that is gone is told as that")
}

func TestHostsAndApplicationHosts(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		match  string
		read   func(*Store) ([]string, error)
		errMsg string
	}{
		{"hosts", "SELECT hostname FROM site_domains", func(s *Store) ([]string, error) { return s.Hosts(ctx) }, "list hostnames"},
		{"application hosts", "FROM application_parts p", func(s *Store) ([]string, error) { return s.ApplicationHosts(ctx, "app_1", "pages.example.net") }, "list application hosts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectQuery(q(tc.match)).WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("a.example.com").AddRow("b.example.com"))
			got, err := tc.read(s)
			require.NoError(t, err)
			assert.Equal(t, []string{"a.example.com", "b.example.com"}, got)

			mock.ExpectQuery(q(tc.match)).WillReturnRows(sqlmock.NewRows([]string{"hostname"}))
			got, err = tc.read(s)
			require.NoError(t, err)
			assert.Equal(t, []string{}, got, "none is [], never null")

			mock.ExpectQuery(q(tc.match)).WillReturnError(errDown)
			_, err = tc.read(s)
			require.ErrorContains(t, err, tc.errMsg)

			mock.ExpectQuery(q(tc.match)).WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
			_, err = tc.read(s)
			require.ErrorContains(t, err, "read hostname")

			mock.ExpectQuery(q(tc.match)).WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("x").RowError(0, errDown))
			_, err = tc.read(s)
			require.ErrorContains(t, err, tc.errMsg)
		})
	}
}

func TestApplicationHosts_PassesTheApplicationAndPagesDomain(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q("p.kind = 'site'")).WithArgs("app_1", "pages.example.net").WillReturnRows(sqlmock.NewRows([]string{"hostname"}))
	_, err := s.ApplicationHosts(context.Background(), "app_1", "pages.example.net")
	require.NoError(t, err)
}

func TestHandoffs(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	exp := rowTime.Add(time.Minute)

	mock.ExpectExec(q("INSERT INTO page_handoffs")).WithArgs("hash", exp).WillReturnResult(sqlmock.NewResult(0, 1))
	first, err := s.UseHandoff(ctx, "hash", exp)
	require.NoError(t, err)
	assert.True(t, first, "the first use passes")

	mock.ExpectExec(q("INSERT INTO page_handoffs")).WillReturnResult(sqlmock.NewResult(0, 0))
	again, err := s.UseHandoff(ctx, "hash", exp)
	require.NoError(t, err)
	assert.False(t, again, "a code is used once")

	mock.ExpectExec(q("INSERT INTO page_handoffs")).WillReturnError(errDown)
	used, err := s.UseHandoff(ctx, "hash", exp)
	require.ErrorContains(t, err, "record handoff")
	assert.False(t, used)

	mock.ExpectExec(q("INSERT INTO page_handoffs")).WillReturnResult(sqlmock.NewErrorResult(errDown))
	used, err = s.UseHandoff(ctx, "hash", exp)
	require.ErrorContains(t, err, "record handoff")
	assert.False(t, used)

	mock.ExpectExec(q("DELETE FROM page_handoffs WHERE expires_at < $1")).WithArgs(rowTime).WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, s.ExpireHandoffs(ctx, rowTime))
	mock.ExpectExec(q("DELETE FROM page_handoffs")).WillReturnError(errDown)
	require.ErrorContains(t, s.ExpireHandoffs(ctx, rowTime), "expire handoffs")
}
