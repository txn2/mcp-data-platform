package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// adminAuthor is the administrator making a transfer, and the authority a run
// of the script presents afterwards.
var adminAuthor = script.Author{Email: "admin@example.com", Roles: []string{"admin"}}

// transferOf is a request that leaves the outputs where they are, which is
// every transfer before #1588 and every one that says nothing about them.
func transferOf(id, to string) script.TransferRequest {
	return script.TransferRequest{ID: id, NewOwnerEmail: to}
}

// moveOf is a request that hands the script's outputs to the new owner too.
func moveOf(id, to string) script.TransferRequest {
	return script.TransferRequest{ID: id, NewOwnerEmail: to, Outputs: script.OutputsMove}
}

// expectTransferWrites queues the writes every transfer makes before the
// outputs are considered: the version row and the live row.
func expectTransferWrites(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectNextVersion(mock, 4)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectLiveRowUpdate(mock, false)
}

// TestTransfer_WritesTheVersionThatCarriesTheNewAuthority proves the transfer
// snapshots unconditionally, unlike an edit: the code is unchanged, and the
// version exists precisely because the roles a run presents come from it.
func TestTransfer_WritesTheVersionThatCarriesTheNewAuthority(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectNextVersion(mock, 4)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WithArgs("script_1", 4, "Daily", "A daily report", "", "print(1)", sqlmock.AnyArg(),
			pq.Array([]string{}), "admin@example.com", pq.Array([]string{"admin"}),
			script.VersionStatusApplied).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectLiveRowUpdate(mock, false)
	mock.ExpectCommit()

	_, err := s.Transfer(context.Background(), transferOf("script_1", "Admin@Example.com"), adminAuthor)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_RefusesANameTheNewOwnerAlreadyUses proves the receiving side
// decides: names are unique within an owner, so a collision is reported as
// something a caller can act on rather than as an internal failure.
func TestTransfer_RefusesANameTheNewOwnerAlreadyUses(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectNextVersion(mock, 4)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
		WillReturnError(&pq.Error{Code: pgUniqueViolation})
	mock.ExpectRollback()

	_, err := s.Transfer(context.Background(), transferOf("script_1", "admin@example.com"), adminAuthor)

	require.ErrorIs(t, err, script.ErrNameTaken)
	assert.Contains(t, err.Error(), "admin@example.com")
	assert.Contains(t, err.Error(), `"daily"`)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_SurfacesTheDomainRefusal proves the domain rule runs inside the
// transaction, so a refused transfer writes no version.
func TestTransfer_SurfacesTheDomainRefusal(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	mock.ExpectRollback()

	_, err := s.Transfer(context.Background(), transferOf("script_1", "jane@example.com"), adminAuthor)

	assert.ErrorContains(t, err, "already belongs to")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTransfer_MissingScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(scriptSelectColumns))
	mock.ExpectRollback()

	_, err := s.Transfer(context.Background(), transferOf("gone", "admin@example.com"), adminAuthor)

	assert.ErrorContains(t, err, "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_WriteFailureIsNotAConflict keeps the two apart: a database that
// is down must not be reported to a caller as a name they can change.
func TestTransfer_WriteFailureIsNotAConflict(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	expectNextVersion(mock, 4)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).WillReturnError(errors.New("down"))
	mock.ExpectRollback()

	_, err := s.Transfer(context.Background(), transferOf("script_1", "admin@example.com"), adminAuthor)

	require.Error(t, err)
	assert.NotErrorIs(t, err, script.ErrNameTaken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_VersionNumberFailure covers the allocation arm, which shares the
// transaction with everything after it.
func TestTransfer_VersionNumberFailure(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLockedScript(t, mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GREATEST")).WillReturnError(errors.New("down"))
	mock.ExpectRollback()

	_, err := s.Transfer(context.Background(), transferOf("script_1", "admin@example.com"), adminAuthor)
	assert.ErrorContains(t, err, "next version number")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_MovesTheOutputsInTheSameTransaction is #1588's store criterion:
// asked to move the outputs, the transfer rewrites the address on the assets
// and collections this script CREATED, bound to the NORMALIZED new address and
// the script's id, and reports how many rows each statement touched. The two
// updates sit inside the transfer's transaction, after the script's own row.
func TestTransfer_MovesTheOutputsInTheSameTransaction(t *testing.T) {
	s, mock := newMock(t)
	expectTransferWrites(t, mock)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets")).
		WithArgs("admin@example.com", "script_1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_collections")).
		WithArgs("admin@example.com", "script_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	moved, err := s.Transfer(context.Background(), moveOf("script_1", "Admin@Example.com"), adminAuthor)

	require.NoError(t, err)
	assert.Equal(t, script.Transferred{AssetsMoved: 2, CollectionsMoved: 1}, moved)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTransfer_KeepsTheOutputsUnlessAsked pins the other disposition and the
// unstated one: neither touches a file row.
func TestTransfer_KeepsTheOutputsUnlessAsked(t *testing.T) {
	for _, outputs := range []script.OutputDisposition{"", script.OutputsKeep} {
		t.Run(string(outputs), func(t *testing.T) {
			s, mock := newMock(t)
			expectTransferWrites(t, mock)
			mock.ExpectCommit()

			req := transferOf("script_1", "admin@example.com")
			req.Outputs = outputs
			moved, err := s.Transfer(context.Background(), req, adminAuthor)

			require.NoError(t, err)
			assert.Equal(t, script.Transferred{}, moved)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestTransfer_AFailedOutputMoveMovesNoScript is the reason the two writes
// share a transaction: a transfer that could not hand over the files does not
// hand over the script either, so the surfaces never report a half-move.
func TestTransfer_AFailedOutputMoveMovesNoScript(t *testing.T) {
	cases := []struct {
		name   string
		expect func(mock sqlmock.Sqlmock)
		want   string
	}{
		{"assets", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets")).WillReturnError(errors.New("down"))
		}, "moving the script's assets"},
		{"collections", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_collections")).WillReturnError(errors.New("down"))
		}, "moving the script's collections"},
		{"asset count", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets")).
				WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_collections")).WillReturnResult(sqlmock.NewResult(0, 0))
		}, "counting moved assets"},
		{"collection count", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_assets")).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE portal_collections")).
				WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
		}, "counting moved collections"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			expectTransferWrites(t, mock)
			tc.expect(mock)
			mock.ExpectRollback()

			_, err := s.Transfer(context.Background(), moveOf("script_1", "admin@example.com"), adminAuthor)

			assert.ErrorContains(t, err, tc.want)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
