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

	require.NoError(t, s.Transfer(context.Background(), "script_1", "Admin@Example.com", adminAuthor))
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

	err := s.Transfer(context.Background(), "script_1", "admin@example.com", adminAuthor)

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

	err := s.Transfer(context.Background(), "script_1", "jane@example.com", adminAuthor)

	assert.ErrorContains(t, err, "already belongs to")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTransfer_MissingScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE")).WillReturnRows(sqlmock.NewRows(scriptSelectColumns))
	mock.ExpectRollback()

	err := s.Transfer(context.Background(), "gone", "admin@example.com", adminAuthor)

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

	err := s.Transfer(context.Background(), "script_1", "admin@example.com", adminAuthor)

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

	assert.ErrorContains(t, s.Transfer(context.Background(), "script_1", "admin@example.com", adminAuthor),
		"next version number")
	require.NoError(t, mock.ExpectationsWereMet())
}
