package scriptstore

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

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestRecordProgress_ReportsTheCancelRequest(t *testing.T) {
	s, mock := newMock(t)
	done := int64(2)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("RETURNING cancel_requested_at, cancel_requested_by")).
		WithArgs("dpx_1", "worker-a", 1, "log", false, "step", int64(2), at, nil, false).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at", "cancel_requested_by"}).AddRow(at, "jane@example.com"))

	requested, by, err := s.RecordProgress(context.Background(), testLease, script.RunLive{
		Progress: &script.RunProgress{Message: "step", Done: &done, At: at}, Log: "log",
	})
	require.NoError(t, err)
	assert.True(t, requested)
	assert.Equal(t, "jane@example.com", by)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordProgress_Failures(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("RETURNING cancel_requested_at")).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at", "cancel_requested_by"}))
	_, _, err := s.RecordProgress(context.Background(), testLease, script.RunLive{})
	require.ErrorIs(t, err, script.ErrLeaseLost)

	mock.ExpectQuery(regexp.QuoteMeta("RETURNING cancel_requested_at")).WillReturnError(errors.New("boom"))
	_, _, err = s.RecordProgress(context.Background(), testLease, script.RunLive{})
	require.ErrorContains(t, err, "record script run progress")
}

func TestCancelRun_ReturnsThePriorStatus(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("RETURNING prior.status")).WithArgs("dpx_1", "jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(script.RunStatusRunning))
	got, err := s.CancelRun(context.Background(), "dpx_1", "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusRunning, got)
}

func TestCancelRun_Failures(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("RETURNING prior.status")).WillReturnRows(sqlmock.NewRows([]string{"status"}))
	_, err := s.CancelRun(context.Background(), "dpx_1", "jane@example.com")
	require.ErrorIs(t, err, script.ErrRunNotFound)

	mock.ExpectQuery(regexp.QuoteMeta("RETURNING prior.status")).WillReturnError(errors.New("boom"))
	_, err = s.CancelRun(context.Background(), "dpx_1", "jane@example.com")
	require.ErrorContains(t, err, "cancel script run")
}

// TestScanRun_ReadsTheLiveColumns reads a row carrying a result and progress.
func TestScanRun_ReadsTheLiveColumns(t *testing.T) {
	s, mock := newMock(t)
	row := runRow(script.RunStatusRunning, 1, nil)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	// result, progress_message, progress_done, progress_total, progress_at,
	// cancel_requested_at, cancel_requested_by
	copy(row[25:32], []driver.Value{[]byte(`{"n":1}`), "step", int64(3), nil, at, at, "sam@example.com"})
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(row...))

	run, err := s.GetRun(context.Background(), "dpx_1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"n":1}`, string(run.Result))
	require.NotNil(t, run.Progress)
	assert.Equal(t, int64(3), *run.Progress.Done)
	assert.Nil(t, run.Progress.Total)
	require.NotNil(t, run.CancelRequestedAt)
	assert.Equal(t, "sam@example.com", run.CancelRequestedBy)
}

// TestFinish_WritesTheResultAsJSON binds a returned value as the JSONB it is
// stored as, and no value as NULL rather than as an empty string JSONB would
// refuse.
func TestFinish_WritesTheResultAsJSON(t *testing.T) {
	assert.Nil(t, nullJSON(nil))
	assert.Equal(t, []byte(`{"n":1}`), nullJSON([]byte(`{"n":1}`)))
}
