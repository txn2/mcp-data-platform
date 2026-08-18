package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// approvalGrant is the grant a reviewer submits. Roles are absent on purpose:
// the store fills them from the version's author, and a test that supplied them
// would not notice if that stopped happening.
func approvalGrant() script.Grants {
	return script.Grants{
		Connections:  []string{"warehouse"},
		Capabilities: []string{script.CapabilityQuery, script.CapabilityExport},
		Destinations: []script.Destination{script.PortalDestination()},
	}
}

// expectVersionLock queues the FOR UPDATE version read every approval starts
// with, returning a row in the given status.
func expectVersionLock(mock sqlmock.Sqlmock, status string) {
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE script_id = $1 AND version = $2 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRow(2, "print(1)", status, []byte("[]"))...))
}

// expectApprovedReread queues the post-write re-read, returning the row with
// the grant the store just bound.
func expectApprovedReread(mock sqlmock.Sqlmock, grantsJSON []byte) {
	row := versionRow(2, "print(1)", script.VersionStatusApplied, []byte("[]"))
	row[len(row)-2] = driver.Value(grantsJSON)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).AddRow(row...))
}

// TestApproveVersion_BindsTheAuthorsRolesNotTheRequest is the security property
// the whole grant model rests on: approving cannot hand a script authority its
// author did not hold, because the roles come from the version rather than from
// the caller.
func TestApproveVersion_BindsTheAuthorsRolesNotTheRequest(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectVersionLock(mock, script.VersionStatusApplied)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).
		WithArgs("sver_1", "admin@example.com", sqlmock.AnyArg(), script.VersionStatusApplied).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectLiveRowUpdate(mock, false)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectApprovedReread(mock, []byte(`{"roles":["analyst"],"connections":["warehouse"],"capabilities":["platform.query"],"destinations":["portal"]}`))
	mock.ExpectCommit()

	// The caller tries to grant itself an extra role; the store must ignore it.
	requested := approvalGrant()
	requested.Roles = []string{"admin"}

	approved, err := s.ApproveVersion(context.Background(), "script_1", 2, "admin@example.com", requested)
	require.NoError(t, err)
	assert.Equal(t, []string{"analyst"}, approved.Grants.Roles,
		"the approved grant carries the version author's roles, never the approver's request")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApproveVersion_AppliesTheApprovedSnapshot pins that approving makes the
// approved version the served code and resolves the other drafts, so the
// version being executed and the version being read are the same one — whether
// a draft is being promoted or an earlier version approved back into service.
func TestApproveVersion_AppliesTheApprovedSnapshot(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectVersionLock(mock, script.VersionStatusDraft)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnResult(sqlmock.NewResult(0, 1))
	expectLiveRowUpdate(mock, false)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).
		WithArgs("script_1", "sver_1", script.VersionStatusSuperseded, script.VersionStatusDraft).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectApprovedReread(mock, []byte(`{}`))
	mock.ExpectCommit()

	_, err := s.ApproveVersion(context.Background(), "script_1", 2, "admin@example.com", approvalGrant())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApproveVersion_RefusesVersionsThatMustNotExecute covers the states an
// approval must not resolve.
func TestApproveVersion_RefusesVersionsThatMustNotExecute(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr string
	}{
		{"rejected", script.VersionStatusRejected, "cannot be approved"},
		{"superseded", script.VersionStatusSuperseded, "cannot be approved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			expectLockedScript(t, mock)
			expectVersionLock(mock, tt.status)
			mock.ExpectRollback()

			_, err := s.ApproveVersion(context.Background(), "script_1", 2, "admin@example.com", approvalGrant())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.ErrorIs(t, err, script.ErrVersionConflict)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestApproveVersion_RefusesAnInvalidGrant covers the approval-time validation
// that keeps an unusable capability set from reaching a run.
func TestApproveVersion_RefusesAnInvalidGrant(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE script_id = $1 AND version = $2 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRowWithoutRoles(2, script.VersionStatusApplied)...))
	mock.ExpectRollback()

	_, err := s.ApproveVersion(context.Background(), "script_1", 2, "admin@example.com", approvalGrant())
	require.Error(t, err)
	assert.ErrorIs(t, err, script.ErrInvalidGrant, "the REST surface answers 400 on this sentinel")
	assert.Contains(t, err.Error(), "held no roles",
		"a version whose author held no roles would resolve to the deny-all persona")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApproveVersion_MissingVersionIsAConflict covers the reviewer naming a
// version that is not there.
func TestApproveVersion_MissingVersionIsAConflict(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(versionSelectColumns))
	mock.ExpectRollback()

	_, err := s.ApproveVersion(context.Background(), "script_1", 9, "admin@example.com", approvalGrant())
	require.Error(t, err)
	assert.ErrorIs(t, err, script.ErrVersionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApproveVersion_StoreFailuresRollBack covers the write paths' error
// handling, each of which must abandon the transaction rather than leave a
// half-approved script.
func TestApproveVersion_StoreFailuresRollBack(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(sqlmock.Sqlmock)
		wantErr string
	}{
		{"stamp fails", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnError(errors.New("boom"))
		}, "stamp script version approval"},
		{"live row write fails", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).WillReturnError(errors.New("boom"))
		}, "update script"},
		{"superseding the other drafts fails", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnResult(sqlmock.NewResult(0, 1))
			expectLiveRowUpdate(mock, false)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).WillReturnError(errors.New("boom"))
		}, "supersede pending script drafts"},
		{"gate write fails", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnResult(sqlmock.NewResult(0, 1))
			expectLiveRowUpdate(mock, false)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id")).WillReturnError(errors.New("boom"))
		}, "point script execution gate"},
		{"re-read fails", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).WillReturnResult(sqlmock.NewResult(0, 1))
			expectLiveRowUpdate(mock, false)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE id = $1")).WillReturnError(errors.New("boom"))
		}, "re-read approved script version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			expectLockedScript(t, mock)
			expectVersionLock(mock, script.VersionStatusApplied)
			tt.arrange(mock)
			mock.ExpectRollback()

			_, err := s.ApproveVersion(context.Background(), "script_1", 2, "admin@example.com", approvalGrant())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
