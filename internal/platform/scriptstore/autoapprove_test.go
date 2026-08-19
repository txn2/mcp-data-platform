package scriptstore

import (
	"context"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// widenedScript is the personal script of expectWidenedLock with its scope
// changed and every versioned field left exactly as the locked row carries it,
// so the edit under test is the scope change and nothing else.
func widenedScript() *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily", DisplayName: "Daily",
		Description: "A daily report", Scope: script.ScopeGlobal,
		OwnerEmail: "jane@example.com", Source: "print(1)", Status: script.StatusActive,
		Params: []script.Param{}, Tags: []string{}, Version: 1, ApprovedVersionID: "sver_1",
	}
}

// expectWidenedLock queues the FOR UPDATE read of a personal script whose
// execution gate points at an approved version.
func expectWidenedLock(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(scriptRow(rowSpec{
			id: "script_1", name: "daily", scope: "personal", owner: "jane@example.com",
			paramsJSON: emptyParams(t), approvedVersion: "sver_1",
		})...))
}

// TestAutoApproveVersion_RecordsAnApprovalNobodyReviewed pins the one fact that
// separates an automatic approval from a reviewed one, and pins that everything
// else about it is identical — including that the roles bound are the version
// author's own and not the request's.
func TestAutoApproveVersion_RecordsAnApprovalNobodyReviewed(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectVersionLock(mock, script.VersionStatusApplied)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).
		WithArgs("sver_1", "jane@example.com", sqlmock.AnyArg(), script.VersionStatusApplied, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectLiveRowUpdate(mock, false)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions SET status = $3")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectApprovedReread(mock, []byte(`{"roles":["analyst"],"connections":["warehouse"]}`))
	mock.ExpectCommit()

	requested := approvalGrant()
	requested.Roles = []string{"admin"}

	approved, err := s.AutoApproveVersion(context.Background(), "script_1", 2, "jane@example.com", requested)
	require.NoError(t, err)
	assert.Equal(t, []string{"analyst"}, approved.Grants.Roles,
		"an automatic approval binds the author's own roles, exactly as a reviewed one does")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAutoApproveVersion_RefusesAVersionItsOwnerDidNotWrite is the invariant
// that keeps a script off authority its owner never held. The grant's roles are
// taken from the version's AUTHOR, so an automatic approval of somebody else's
// version — an administrator's edit to a person's script, say — would bind that
// administrator's roles with nobody reviewing it.
func TestAutoApproveVersion_RefusesAVersionItsOwnerDidNotWrite(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	// versionRow's author is jane@example.com; the locked script is hers, so the
	// refusal has to come from a version somebody else wrote.
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE script_id = $1 AND version = $2 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(versionRowBy("admin@example.com", script.VersionStatusApplied)...))
	mock.ExpectRollback()

	_, err := s.AutoApproveVersion(context.Background(), "script_1", 2, "jane@example.com", approvalGrant())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not own this personal script")
	assert.ErrorIs(t, err, script.ErrVersionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAutoApproveVersion_RefusesASharedScript keeps the exemption to the shape
// it was reasoned about: a script with an audience is reviewed.
func TestAutoApproveVersion_RefusesASharedScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(scriptRow(rowSpec{
			id: "script_1", name: "daily", scope: "global", owner: "jane@example.com",
			paramsJSON: emptyParams(t),
		})...))
	expectVersionLock(mock, script.VersionStatusApplied)
	mock.ExpectRollback()

	_, err := s.AutoApproveVersion(context.Background(), "script_1", 2, "jane@example.com", approvalGrant())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not own this personal script")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithVersion_WithdrawsAnAutomaticApprovalOnWidening is the rule that
// keeps the automatic path from becoming the shared path by an edit: the version
// was approved because its only caller was its author, and a widened script has
// an audience that agreed to nothing.
func TestUpdateWithVersion_WithdrawsAnAutomaticApprovalOnWidening(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectWidenedLock(t, mock)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).
		WithArgs("sver_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scripts SET approved_version_id = NULL")).
		WithArgs("script_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectLiveRowUpdate(mock, false)
	mock.ExpectCommit()

	widened := widenedScript()
	require.NoError(t, s.UpdateWithVersion(context.Background(), widened,
		script.Author{Email: "admin@example.com", Roles: []string{"admin"}}, false))

	assert.Empty(t, widened.ApprovedVersionID, "the execution gate is cleared with the approval")
	assert.Equal(t, script.StatusDraft, widened.Status,
		"a script the platform will not execute must not claim it is active")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithVersion_KeepsAnApprovalAPersonMadeOnWidening pins the predicate
// that separates the two: a reviewer decided, and widening the audience does not
// un-decide it. The version update matches no row, so the gate stays put.
func TestUpdateWithVersion_KeepsAnApprovalAPersonMadeOnWidening(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectWidenedLock(t, mock)
	// auto_approved is false on the row, so the predicated update matches nothing.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_versions")).
		WithArgs("sver_1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectLiveRowUpdate(mock, false)
	mock.ExpectCommit()

	widened := widenedScript()
	require.NoError(t, s.UpdateWithVersion(context.Background(), widened,
		script.Author{Email: "admin@example.com", Roles: []string{"admin"}}, false))

	assert.Equal(t, "sver_1", widened.ApprovedVersionID)
	assert.Equal(t, script.StatusActive, widened.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}
