package sitestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

// The statements a sweep of one site makes, in order.
const (
	lockSQL    = "SELECT 1 FROM sites WHERE id = $1 FOR UPDATE"
	aloneSQL   = "SELECT EXISTS (SELECT 1 FROM site_jobs"
	linksSQL   = "FROM site_releases WHERE site_id = $1 ORDER BY number"
	keptSQL    = "UNION SELECT release_number FROM site_environments"
	treeSQL    = "WITH RECURSIVE chain AS"
	objectsSQL = "GROUP BY 1, 2, f.stored_in"
	clearSQL   = "DELETE FROM site_release_files WHERE release_id = $1"
	insertSQL  = "INSERT INTO site_release_files"
	relinkSQL  = "UPDATE site_releases SET manifest_parent = NULLIF($2, ''), manifest_depth = $3 WHERE id = $1"
	deleteSQL  = "DELETE FROM site_release_files WHERE release_id = ANY($1)"
	markSQL    = "UPDATE site_releases SET pruned_at = NOW() WHERE id = ANY($1)"
)

var linkCols = []string{"id", "number", "parent", "depth", "pruned"}

func files(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows(fileCols)
	for _, r := range rows {
		out.AddRow(r...)
	}
	return out
}

// file is one row: its path, digest and holding release, then its size and
// its gzip and brotli sizes.
func file(path, sum, stored string, sizes ...int64) []driver.Value {
	sz := append(sizes, 0, 0, 0)[:3]
	return []driver.Value{path, sum, "text/html", sz[0], sz[1], sz[2], stored}
}

func expectHead(mock sqlmock.Sqlmock, busy, keepsAll bool) {
	mock.ExpectBegin()
	mock.ExpectQuery(q(lockSQL)).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	mock.ExpectQuery(q(aloneSQL)).WithArgs("site_1", siteconfig.DefaultKeepReleases, jobFresh.Seconds()).
		WillReturnRows(sqlmock.NewRows([]string{"busy", "all"}).AddRow(busy, keepsAll))
}

// expectChain is three releases, 1 <- 2 <- 3, of which 2 is pruned and 3 is
// amended onto 1.
func expectChain(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(q(linksSQL)).WithArgs("site_1").WillReturnRows(sqlmock.NewRows(linkCols).
		AddRow("a", 1, "", 0, false).AddRow("b", 2, "a", 1, false).AddRow("c", 3, "b", 2, false))
	mock.ExpectQuery(q(keptSQL)).WithArgs("site_1", siteconfig.DefaultKeepReleases).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1).AddRow(3))
	// 3's own trees and 1's, output then source then render.
	mock.ExpectQuery(q(treeSQL)).WithArgs("c", site.TreeOutput).WillReturnRows(files(file("index.html", "s3", "", 10, 0, 0), file("shared.css", "sh", "", 5, 0, 0)))
	mock.ExpectQuery(q(treeSQL)).WithArgs("a", site.TreeOutput).WillReturnRows(files(
		file("index.html", "s1", "", 10, 0, 0), file("old.txt", "so", "", 3, 0, 0), file("shared.css", "sh", "", 5, 0, 0)))
	for _, kind := range []string{site.TreeSource, site.TreeRender} {
		mock.ExpectQuery(q(treeSQL)).WithArgs("c", kind).WillReturnRows(files())
		mock.ExpectQuery(q(treeSQL)).WithArgs("a", kind).WillReturnRows(files())
	}
	// What 1 names, then what the pruned 2 names.
	mock.ExpectQuery(q(objectsSQL)).WillReturnRows(files(file("", "s1", "", 10, 0, 0), file("", "sh", "", 5, 0, 0), file("", "so", "", 3, 0, 0)))
	mock.ExpectQuery(q(objectsSQL)).WillReturnRows(files(file("", "s2", "", 10, 4, 3), file("", "sh", "", 5, 0, 0),
		file("legacy.txt", "sl", "srel_old", 7, 0, 0)))
}

func TestPrune_ReportDecidesAndWritesNothing(t *testing.T) {
	s, mock := newMock(t)
	expectHead(mock, false, false)
	expectChain(mock)
	mock.ExpectRollback()

	res, err := s.Prune(context.Background(), "site_1", false)
	require.NoError(t, err)
	assert.Equal(t, []int{2}, res.Releases)
	assert.Equal(t, 1, res.Rewritten, "3 read its files through 2")
	require.Len(t, res.Objects, 2, "the digest only 2 named, and a file stored under an old release's prefix that only 2 named")
	assert.Equal(t, "s2", res.Objects[0].SHA256)
	assert.Equal(t, "srel_old", res.Objects[1].StoredIn)
	assert.Equal(t, int64(10+4+3+7), res.Bytes, "a file and its compressed forms")
}

func TestPrune_DeleteRewritesVerifiesAndMarks(t *testing.T) {
	s, mock := newMock(t)
	expectHead(mock, false, false)
	expectChain(mock)
	mock.ExpectExec(q(clearSQL)).WithArgs("c").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(q(insertSQL)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(q(relinkSQL)).WithArgs("c", "a", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(q(treeSQL)).WithArgs("c", site.TreeOutput).WillReturnRows(files(file("index.html", "s3", "", 10, 0, 0), file("shared.css", "sh", "", 5, 0, 0)))
	mock.ExpectQuery(q(treeSQL)).WithArgs("c", site.TreeSource).WillReturnRows(files())
	mock.ExpectQuery(q(treeSQL)).WithArgs("c", site.TreeRender).WillReturnRows(files())
	mock.ExpectExec(q(deleteSQL)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(q(markSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	res, err := s.Prune(context.Background(), "site_1", true)
	require.NoError(t, err)
	assert.Equal(t, []int{2}, res.Releases)
}

func TestPrune_ARewriteThatChangesWhatAReleaseServesRollsBack(t *testing.T) {
	s, mock := newMock(t)
	expectHead(mock, false, false)
	expectChain(mock)
	mock.ExpectExec(q(clearSQL)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(q(insertSQL)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(q(relinkSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(q(treeSQL)).WithArgs("c", site.TreeOutput).WillReturnRows(files(file("index.html", "s3", "", 10, 0, 0)))
	mock.ExpectRollback()

	_, err := s.Prune(context.Background(), "site_1", true)
	require.ErrorContains(t, err, "would no longer serve the files it held")
}

func TestPrune_AReleaseWithNoKeptAncestorIsWrittenWhole(t *testing.T) {
	s, mock := newMock(t)
	expectHead(mock, false, false)
	mock.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "a", 1, false))
	mock.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeOutput).WillReturnRows(files(file("index.html", "s2", "", 10, 0, 0)))
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeSource).WillReturnRows(files())
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeRender).WillReturnRows(files())
	mock.ExpectQuery(q(objectsSQL)).WillReturnRows(files(file("", "s1", "", 10, 0, 0)))
	mock.ExpectExec(q(clearSQL)).WithArgs("b").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(insertSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(relinkSQL)).WithArgs("b", "", 0).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeOutput).WillReturnRows(files(file("index.html", "s2", "", 10, 0, 0)))
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeSource).WillReturnRows(files())
	mock.ExpectQuery(q(treeSQL)).WithArgs("b", site.TreeRender).WillReturnRows(files())
	mock.ExpectExec(q(deleteSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(markSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	res, err := s.Prune(context.Background(), "site_1", true)
	require.NoError(t, err)
	assert.Equal(t, []int{1}, res.Releases)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "s1", res.Objects[0].SHA256)
}

func TestPrune_SitesItLeavesAlone(t *testing.T) {
	ctx := context.Background()
	t.Run("a job running", func(t *testing.T) {
		s, mock := newMock(t)
		expectHead(mock, true, false)
		mock.ExpectRollback()
		res, err := s.Prune(ctx, "site_1", true)
		require.NoError(t, err)
		assert.True(t, res.Skipped)
	})
	t.Run("every environment keeps every release", func(t *testing.T) {
		s, mock := newMock(t)
		expectHead(mock, false, true)
		mock.ExpectRollback()
		res, err := s.Prune(ctx, "site_1", true)
		require.NoError(t, err)
		assert.Empty(t, res.Releases)
	})
	t.Run("nothing past the limits", func(t *testing.T) {
		s, mock := newMock(t)
		expectHead(mock, false, false)
		mock.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "a", 1, true))
		mock.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectRollback()
		res, err := s.Prune(ctx, "site_1", true)
		require.NoError(t, err)
		assert.Empty(t, res.Releases, "a release pruned before is not pruned again")
	})
	t.Run("no such site", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q(lockSQL)).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		_, err := s.Prune(ctx, "site_1", true)
		require.ErrorIs(t, err, site.ErrNotFound)
	})
}

func TestPrune_Failures(t *testing.T) {
	ctx := context.Background()
	steps := []struct {
		name string
		set  func(sqlmock.Sqlmock)
		want string
	}{
		{"begin", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errDown) }, "begin"},
		{"lock", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery(q(lockSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "lock site"},
		{"limits", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery(q(lockSQL)).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
			m.ExpectQuery(q(aloneSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "jobs and limits"},
		{"releases", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "list releases"},
		{"kept", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false))
			m.ExpectQuery(q(keptSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "releases to keep"},
		{"tree", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "a", 1, false))
			m.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
			m.ExpectQuery(q(treeSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "list files"},
		{"objects", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "", 0, false))
			m.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
			m.ExpectQuery(q(objectsSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "stored files"},
		{"mark", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "", 0, false))
			m.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
			m.ExpectQuery(q(objectsSQL)).WillReturnRows(files())
			m.ExpectQuery(q(objectsSQL)).WillReturnRows(files())
			m.ExpectExec(q(deleteSQL)).WillReturnError(errDown)
			m.ExpectRollback()
		}, "delete pruned files"},
		{"commit", func(m sqlmock.Sqlmock) {
			expectHead(m, false, false)
			m.ExpectQuery(q(linksSQL)).WillReturnRows(sqlmock.NewRows(linkCols).AddRow("a", 1, "", 0, false).AddRow("b", 2, "", 0, false))
			m.ExpectQuery(q(keptSQL)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
			m.ExpectQuery(q(objectsSQL)).WillReturnRows(files())
			m.ExpectQuery(q(objectsSQL)).WillReturnRows(files())
			m.ExpectExec(q(deleteSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(q(markSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit().WillReturnError(errDown)
		}, "commit"},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			s, mock := newMock(t)
			step.set(mock)
			_, err := s.Prune(ctx, "site_1", true)
			require.ErrorContains(t, err, step.want)
		})
	}
}

func TestAmend_IsWhatTurnsOneFileSetIntoAnother(t *testing.T) {
	rows := &fileRows{}
	from := []site.File{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "2"}, {Path: "c", SHA256: "3"}}
	own := []site.File{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "9"}, {Path: "d", SHA256: "4"}}
	amend(rows, site.TreeOutput, from, own)
	assert.Equal(t, []string{"b", "d", "c"}, rows.paths, "the changed, the new, then the removed")
	assert.Equal(t, []bool{false, false, true}, rows.removed)
}

func TestSiteRefsAndSweeps(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery(q("SELECT id, name FROM sites ORDER BY name")).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow("site_1", "marketing"))
	refs, err := s.SiteRefs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []SiteRef{{ID: "site_1", Name: "marketing"}}, refs)
	mock.ExpectQuery(q("SELECT id, name FROM sites")).WillReturnError(errDown)
	_, err = s.SiteRefs(ctx)
	require.Error(t, err)

	started := time.Unix(1700000000, 0).UTC()
	w := &Sweep{Mode: siteconfig.ModeDelete, Sites: 2, Releases: 3, Files: 4, Bytes: 5, StartedAt: started}
	mock.ExpectBegin()
	mock.ExpectQuery(q("INSERT INTO site_retention_sweeps")).WillReturnRows(sqlmock.NewRows([]string{"finished_at"}).AddRow(rowTime))
	mock.ExpectExec(q("DELETE FROM site_retention_sweeps WHERE id NOT IN")).WithArgs(keptSweeps).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.NoError(t, s.RecordSweep(ctx, w))
	assert.Equal(t, rowTime, w.FinishedAt)

	mock.ExpectBegin()
	mock.ExpectQuery(q("INSERT INTO site_retention_sweeps")).WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.RecordSweep(ctx, w), "record sweep")

	cols := []string{"mode", "run_by", "scope", "sites", "releases", "rewritten", "files", "bytes", "orphans", "orphan_bytes", "skipped", "refused", "failed", "started_at", "finished_at"}
	mock.ExpectQuery(q("FROM site_retention_sweeps ORDER BY id DESC LIMIT $1")).WithArgs(keptSweeps).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("report", "admin@example.com", "marketing", 1, 2, 0, 3, 4, 0, 0, 0, 0, 0, started, rowTime))
	got, err := s.Sweeps(ctx, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "marketing", got[0].Scope)
	mock.ExpectQuery(q("FROM site_retention_sweeps")).WillReturnError(errDown)
	_, err = s.Sweeps(ctx, 5)
	require.Error(t, err)
}

func TestKeptBy(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery(q("SELECT DISTINCT n, why FROM")).WithArgs("site_1", siteconfig.DefaultKeepReleases).
		WillReturnRows(sqlmock.NewRows([]string{"n", "why"}).AddRow(2, "dev").AddRow(2, "prod").AddRow(3, "approval"))
	got, err := s.KeptBy(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, map[int][]string{2: {"dev", "prod"}, 3: {"approval"}}, got)

	mock.ExpectQuery(q("SELECT DISTINCT n, why FROM")).WillReturnError(errDown)
	_, err = s.KeptBy(ctx, "site_1")
	require.ErrorContains(t, err, "what keeps each release")
}
