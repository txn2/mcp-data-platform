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
	"approved_by", "approved_at", "auto_approved", "grants", "created_at",
}

// versionRow returns one full version row in versionColumns order.
func versionRow(version int, source, status string, paramsJSON []byte) []driver.Value {
	return []driver.Value{
		"sver_1", "script_1", version, "Daily", "A daily report", "",
		source, paramsJSON, pq.Array([]string{}), "jane@example.com",
		pq.Array([]string{"analyst"}), status, "", nil, false, []byte("{}"), rowTime,
	}
}

// versionRowAuthorIndex is the author column's position in versionSelectColumns,
// named so a column added ahead of it moves one constant rather than every
// numeric index in this file.
const (
	versionRowAuthorIndex     = 9
	versionRowApprovedByIndex = 12
	versionRowGrantsIndex     = 15
)

// versionRowBy returns a version row written by a named author, for the rules
// that turn on WHO wrote a version rather than on what it contains.
func versionRowBy(author, status string) []driver.Value {
	row := versionRow(2, "print(1)", status, []byte("[]"))
	row[versionRowAuthorIndex] = author
	return row
}

// versionRowWithoutRoles returns a version row whose author held no roles, the
// shape a version written before the author-roles column existed carries.
func versionRowWithoutRoles(version int, status string) []driver.Value {
	row := versionRow(version, "print(1)", status, []byte("[]"))
	row[versionRowAuthorIndex+1] = pq.Array([]string{})
	return row
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
		require.NoError(t, s.UpdateWithVersion(context.Background(), sc, testAuthor, false))
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
		require.NoError(t, s.UpdateWithVersion(context.Background(), sc, testAuthor, false))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestUpdateWithVersion_RejectsAnEditRacingAnApproval is the row-lock
// re-validation the ticket calls for: the gate is decided from an unlocked read,
// so it is checked again against the row as locked, and an edit that would swap
// approved source out from under a live approval is refused as a conflict.
func TestUpdateWithVersion_RejectsAnEditRacingAnApproval(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	// The locked row now has an approved version, which the caller's earlier
	// unlocked read did not see.
	locked := scriptRow(rowSpec{id: "script_1", name: "daily", scope: "personal", owner: "jane@example.com", paramsJSON: emptyParams(t), approvedVersion: "sver_1"})
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(locked...))
	mock.ExpectRollback()

	sc := &script.Script{ID: "script_1", Name: "daily", Scope: script.ScopePersonal, Source: "print(2)"}
	err := s.UpdateWithVersion(context.Background(), sc, testAuthor, false)
	require.ErrorIs(t, err, script.ErrVersionConflict)
	assert.Contains(t, err.Error(), "re-read and retry")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_MissingScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(scriptSelectColumns))
	mock.ExpectRollback()

	err := s.UpdateWithVersion(context.Background(), &script.Script{ID: "gone"}, testAuthor, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateWithVersion_LockErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := s.UpdateWithVersion(context.Background(), &script.Script{ID: "script_1"}, testAuthor, false)
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
		testAuthor, false)
	assert.ErrorContains(t, err, "next version number")
}

// TestCreateDraftVersion_LeavesTheLiveRowAlone is the gate's other half: a
// deferred edit writes history and nothing else.
func TestCreateDraftVersion_LeavesTheLiveRowAlone(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectNextVersion(mock, 3)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	n, err := s.CreateDraftVersion(context.Background(), "script_1",
		&script.Script{Source: "print(2)"}, testAuthor)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDraftVersion_Errors(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(scriptSelectColumns))
	mock.ExpectRollback()

	_, err := s.CreateDraftVersion(context.Background(), "gone", &script.Script{}, testAuthor)
	assert.ErrorContains(t, err, "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListVersions(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE script_id = $1 ORDER BY version DESC")).
		WithArgs("script_1").
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(2, "print(2)", script.VersionStatusDraft, emptyParams(t))...).
			AddRow(versionRow(1, "print(1)", script.VersionStatusApplied, emptyParams(t))...))

	got, err := s.ListVersions(context.Background(), "script_1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, got[0].Version)
	assert.Equal(t, script.VersionStatusDraft, got[0].Status)
	assert.Equal(t, []script.Param{}, got[0].Params)
	assert.Equal(t, []string{}, got[0].Tags)
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
