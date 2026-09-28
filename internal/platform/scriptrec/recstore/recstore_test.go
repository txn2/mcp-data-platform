package recstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
)

var rowColumns = []string{
	"run_id", "script_id", "script_name", "kind", "recorded_by", "version",
	"source_sha256", "succeeded", "reason", "bytes", "kept", "created_at", "data",
}

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestSaveWritesNullsForWhatARecordingDoesNotHave(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_recordings")).
		WithArgs("dpx_1", nil, "w", "draft", "jane@example.com", nil, "", true, "", 0, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Save(context.Background(), scriptrec.Stored{Meta: scriptrec.Meta{
		RunID: "dpx_1", ScriptName: "w", Kind: "draft", RecordedBy: "jane@example.com", Succeeded: true,
	}}))
	mock.ExpectExec("INSERT").WillReturnError(errors.New("boom"))
	assert.ErrorContains(t, s.Save(context.Background(), scriptrec.Stored{Meta: scriptrec.Meta{RunID: "x", ScriptID: "s", Version: 2}, Data: []byte{1}}), "save script recording")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAndRecentReadRows(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	row := []driver.Value{"srun_1", "s1", "w", "run", "jane@example.com", 1, "h", true, "", 2, false, now, []byte{1, 2}}
	mock.ExpectQuery("WHERE run_id = \\$1").WithArgs("srun_1").WillReturnRows(sqlmock.NewRows(rowColumns).AddRow(row...))
	got, err := s.Get(context.Background(), "srun_1")
	require.NoError(t, err)
	assert.Equal(t, "s1", got.ScriptID)
	assert.Equal(t, []byte{1, 2}, got.Data)

	mock.ExpectQuery("WHERE run_id").WillReturnRows(sqlmock.NewRows(rowColumns))
	_, err = s.Get(context.Background(), "x")
	assert.ErrorIs(t, err, scriptrec.ErrNotFound)
	mock.ExpectQuery("WHERE run_id").WillReturnError(errors.New("boom"))
	_, err = s.Get(context.Background(), "x")
	assert.ErrorContains(t, err, "get script recording")

	mock.ExpectQuery("ORDER BY created_at DESC").WithArgs("s1", 5).WillReturnRows(sqlmock.NewRows(rowColumns).AddRow(row...))
	recent, err := s.Recent(context.Background(), "s1", 5)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	mock.ExpectQuery("ORDER BY").WillReturnError(errors.New("boom"))
	_, err = s.Recent(context.Background(), "s1", 5)
	assert.ErrorContains(t, err, "list script recordings")
	mock.ExpectQuery("ORDER BY").WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("x"))
	_, err = s.Recent(context.Background(), "s1", 5)
	assert.ErrorContains(t, err, "scanning script recording")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKeepAttachesAndMarksInOneTransaction(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SET script_id = \\$1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET kept").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	require.NoError(t, s.Keep(context.Background(), "s1", "jane@example.com", nil))

	mock.ExpectBegin().WillReturnError(errors.New("boom"))
	assert.Error(t, s.Keep(context.Background(), "s1", "j", nil))
	mock.ExpectBegin()
	mock.ExpectExec("SET script_id").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, s.Keep(context.Background(), "s1", "j", nil), "attach")
	mock.ExpectBegin()
	mock.ExpectExec("SET script_id").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET kept").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, s.Keep(context.Background(), "s1", "j", nil), "keep script recordings")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeCountsWhatItSwept(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM script_recordings").WithArgs(3600).WillReturnResult(sqlmock.NewResult(0, 4))
	n, err := s.Purge(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 4, n)
	mock.ExpectExec("DELETE").WillReturnError(errors.New("boom"))
	_, err = s.Purge(context.Background(), time.Hour)
	assert.ErrorContains(t, err, "purge")
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
	_, err = s.Purge(context.Background(), time.Hour)
	assert.ErrorContains(t, err, "counting")
	require.NoError(t, mock.ExpectationsWereMet())
}
