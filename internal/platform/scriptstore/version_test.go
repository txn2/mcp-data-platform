package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// versionSelectColumns is the result-set shape a version SELECT mock must
// return, in versionColumns order.
var versionSelectColumns = []string{
	"id", "script_id", "version", "display_name", "description", "category",
	"source_code", "params", "tags", "author", "author_roles", "status",
	"created_at",
}

// versionRow returns one full version row in versionColumns order.
func versionRow(version int, source, status string, paramsJSON []byte) []driver.Value {
	return []driver.Value{
		"sver_1", "script_1", version, "Daily", "A daily report", "",
		source, paramsJSON, pq.Array([]string{}), "jane@example.com",
		pq.Array([]string{"analyst"}), status, rowTime,
	}
}

// expectLockedScript queues the FOR UPDATE read every version write starts with.
func expectLockedScript(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	row := scriptRow(rowSpec{
		id: "script_1", name: "daily", scope: "personal",
		owner: "jane@example.com", paramsJSON: emptyParams(t), source: "print(1)",
	})
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(row...))
}

// expectNextVersion queues the version-number allocation.
func expectNextVersion(mock sqlmock.Sqlmock, n int) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GREATEST")).
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(n))
}

// TestUpdateWithVersion_SnapshotsOnlyWhenTheSubstanceMoved keeps the history
// from filling with rows that record nothing.
func TestUpdateWithVersion_SnapshotsOnlyWhenTheSubstanceMoved(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		expectLockedScript(t, mock)
		expectNextVersion(mock, 2)
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
			WillReturnResult(sqlmock.NewResult(1, 1))
		expectLiveRowUpdate(mock, true)
		mock.ExpectCommit()

		sc := &script.Script{ID: "script_1", Name: "daily", Scope: script.ScopePersonal, Source: "print(2)"}
		require.NoError(t, s.UpdateWithVersion(context.Background(), sc, testAuthor))
		assert.Equal(t, 2, sc.Version)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unchanged", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		expectLockedScript(t, mock)
		expectLiveRowUpdate(mock, true)
		mock.ExpectCommit()

		sc := &script.Script{
			ID: "script_1", Name: "daily", Scope: script.ScopePersonal, Source: "print(1)",
			DisplayName: "Daily", Description: "A daily report", Params: []script.Param{}, Tags: []string{},
		}
		require.NoError(t, s.UpdateWithVersion(context.Background(), sc, testAuthor))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateWithVersion_MissingScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(scriptSelectColumns))
	mock.ExpectRollback()

	err := s.UpdateWithVersion(context.Background(), &script.Script{ID: "gone"}, testAuthor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_LockErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := s.UpdateWithVersion(context.Background(), &script.Script{ID: "script_1"}, testAuthor)
	assert.ErrorContains(t, err, "lock script")
}

func TestUpdateWithVersion_VersionNumberErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GREATEST")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := s.UpdateWithVersion(context.Background(),
		&script.Script{ID: "script_1", Name: "daily", Scope: script.ScopePersonal, Source: "print(2)"},
		testAuthor)
	assert.ErrorContains(t, err, "next version number")
}

func TestListVersions(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE script_id = $1 ORDER BY version DESC")).
		WithArgs("script_1").
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(2, "print(2)", script.VersionStatusApplied, emptyParams(t))...).
			AddRow(versionRow(1, "print(1)", script.VersionStatusSuperseded, emptyParams(t))...))

	got, err := s.ListVersions(context.Background(), "script_1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, got[0].Version)
	assert.Equal(t, script.VersionStatusApplied, got[0].Status)
	assert.Equal(t, []script.Param{}, got[0].Params)
	assert.Equal(t, []string{}, got[0].Tags)
	assert.Equal(t, []string{"analyst"}, got[0].AuthorRoles,
		"the snapshot carries the authority a run of this version presents")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery("FROM script_versions").WillReturnError(errors.New("boom"))
	_, err = s.ListVersions(context.Background(), "script_1")
	assert.ErrorContains(t, err, "list script versions")
}

func TestListVersions_ScanErrorPropagates(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM script_versions").
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(1, "print(1)", script.VersionStatusApplied, []byte("{bad"))...))

	_, err := s.ListVersions(context.Background(), "script_1")
	assert.ErrorContains(t, err, "unmarshal version params")
}

func TestGetVersion(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 AND version = $2")).
		WithArgs("script_1", 1).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(1, "print(1)", script.VersionStatusApplied, emptyParams(t))...))

	got, err := s.GetVersion(context.Background(), "script_1", 1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "print(1)", got.Source)

	mock.ExpectQuery("FROM script_versions").WillReturnRows(sqlmock.NewRows(versionSelectColumns))
	got, err = s.GetVersion(context.Background(), "script_1", 9)
	require.NoError(t, err)
	assert.Nil(t, got, "VersionStore contract: a missing version is nil, nil")

	mock.ExpectQuery("FROM script_versions").WillReturnError(errors.New("boom"))
	_, err = s.GetVersion(context.Background(), "script_1", 1)
	assert.ErrorContains(t, err, "get script version")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestScanVersion_NormalizesNullColumns pins the read-side contract for rows
// whose array columns come back NULL and whose params JSON is a literal null:
// every slice field is an empty slice, so JSON output carries [] and pq.Array
// on a later write cannot bind NULL into a NOT NULL column.
func TestScanVersion_NormalizesNullColumns(t *testing.T) {
	s, mock := newMock(t)
	row := versionRow(1, "print(1)", script.VersionStatusApplied, []byte(`null`))
	row[8] = nil  // tags
	row[10] = nil // author_roles
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 AND version = $2")).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).AddRow(row...))

	got, err := s.GetVersion(context.Background(), "script_1", 1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []script.Param{}, got.Params)
	assert.Equal(t, []string{}, got.Tags)
	assert.Equal(t, []string{}, got.AuthorRoles)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreate_SnapshotsAnAuthorWithNoRoles covers the write-side half of the
// same rule: an author who holds no roles still produces a valid snapshot row,
// with an empty array rather than a NULL bound into author_roles.
func TestCreate_SnapshotsAnAuthorWithNoRoles(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	sc := &script.Script{Name: "daily", Scope: script.ScopePersonal, Source: "print(1)"}
	require.NoError(t, s.Create(context.Background(), sc, script.Author{Email: "jane@example.com"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetVersionByID covers the worker's read: a queued run names the exact
// snapshot it executes by id, and only an id survives later saves unchanged.
func TestGetVersionByID(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE id = $1")).
		WithArgs("sver_1").
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(2, "print(2)", script.VersionStatusApplied, emptyParams(t))...))

	got, err := s.GetVersionByID(context.Background(), "sver_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "print(2)", got.Source)

	mock.ExpectQuery("FROM script_versions").WillReturnRows(sqlmock.NewRows(versionSelectColumns))
	got, err = s.GetVersionByID(context.Background(), "gone")
	require.NoError(t, err)
	assert.Nil(t, got, "VersionStore contract: a missing version is nil, nil")

	mock.ExpectQuery("FROM script_versions").WillReturnError(errors.New("boom"))
	_, err = s.GetVersionByID(context.Background(), "sver_1")
	assert.ErrorContains(t, err, "get script version by id")
	require.NoError(t, mock.ExpectationsWereMet())
}
