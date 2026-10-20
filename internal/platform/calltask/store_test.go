package calltask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

var errDown = errors.New("db down")

// Each statement passes its arguments and reports the store's failure.
func TestPostgresStoreStatements(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s, mock := mockStore(t)

	mock.ExpectExec("INSERT INTO call_tasks").
		WithArgs("dpt_1", "trino_export", "jane", msgWorking, now, now.Add(time.Hour)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Create(ctx, Task{
		ID: "dpt_1", ToolName: "trino_export", UserID: "jane", StatusMessage: msgWorking,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))
	mock.ExpectExec("INSERT INTO call_tasks").WillReturnError(errDown)
	require.ErrorIs(t, s.Create(ctx, Task{}), errDown)

	mock.ExpectExec("UPDATE call_tasks").
		WithArgs("dpt_1", "completed", "done", []byte(`{"content":[]}`), nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Finish(ctx, "dpt_1", Ending{Status: "completed", Message: "done", Result: json.RawMessage(`{"content":[]}`)}))
	mock.ExpectExec("UPDATE call_tasks").WillReturnError(errDown)
	require.ErrorIs(t, s.Finish(ctx, "dpt_1", Ending{Status: "failed", Message: "x"}), errDown)

	mock.ExpectExec("SET cancel_requested_at").WithArgs("dpt_1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RequestCancel(ctx, "dpt_1"))
	mock.ExpectExec("SET cancel_requested_at").WillReturnError(errDown)
	require.ErrorIs(t, s.RequestCancel(ctx, "dpt_1"), errDown)

	mock.ExpectExec("heartbeat_at < \\$2").WithArgs("dpt_1", now, msgReplicaGone, []byte(`{}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	failed, err := s.FailStale(ctx, "dpt_1", now, msgReplicaGone, json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, failed)
	mock.ExpectExec("heartbeat_at < \\$2").WillReturnResult(sqlmock.NewResult(0, 0))
	failed, err = s.FailStale(ctx, "dpt_1", now, msgReplicaGone, nil)
	require.NoError(t, err)
	assert.False(t, failed, "a task still reporting is left alone")
	mock.ExpectExec("heartbeat_at < \\$2").WillReturnError(errDown)
	_, err = s.FailStale(ctx, "dpt_1", now, msgReplicaGone, nil)
	require.ErrorIs(t, err, errDown)
	mock.ExpectExec("heartbeat_at < \\$2").WillReturnResult(sqlmock.NewErrorResult(errDown))
	_, err = s.FailStale(ctx, "dpt_1", now, msgReplicaGone, nil)
	require.ErrorIs(t, err, errDown)

	require.NoError(t, mock.ExpectationsWereMet())
}

// Get reads a row, reports a missing one as not found, and passes a read
// failure through.
func TestPostgresStoreGet(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s, mock := mockStore(t)
	cols := []string{
		"id", "tool_name", "user_id", "status", "status_message", "result", "error",
		"created_at", "updated_at", "heartbeat_at", "cancel_requested", "expires_at",
		"idempotency_key", "background", "progress",
	}
	mock.ExpectQuery("FROM call_tasks").WithArgs("dpt_1").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("dpt_1", "trino_export", "jane", "completed", "done", []byte(`{"content":[]}`), nil, now, now, now, true, now.Add(time.Hour),
			"", false, nil))
	task, err := s.Get(ctx, "dpt_1")
	require.NoError(t, err)
	assert.Equal(t, "jane", task.UserID)
	assert.JSONEq(t, `{"content":[]}`, string(task.Result))
	assert.Empty(t, task.Error)
	assert.True(t, task.CancelRequested)

	mock.ExpectQuery("FROM call_tasks").WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx, "dpt_x")
	require.ErrorIs(t, err, errTaskNotFound)
	mock.ExpectQuery("FROM call_tasks").WillReturnError(errDown)
	_, err = s.Get(ctx, "dpt_x")
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Heartbeat stamps the named working tasks and returns the ones a cancel was
// asked for.
func TestPostgresStoreHeartbeat(t *testing.T) {
	ctx := context.Background()
	s, mock := mockStore(t)
	mock.ExpectQuery("SET heartbeat_at").WillReturnRows(sqlmock.NewRows([]string{"id", "cancel"}).
		AddRow("dpt_1", false).AddRow("dpt_2", true))
	cancel, err := s.Heartbeat(ctx, []string{"dpt_1", "dpt_2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"dpt_2"}, cancel)

	mock.ExpectQuery("SET heartbeat_at").WillReturnError(errDown)
	_, err = s.Heartbeat(ctx, []string{"dpt_1"})
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery("SET heartbeat_at").WillReturnRows(sqlmock.NewRows([]string{"id", "cancel"}).AddRow("dpt_1", "not a bool"))
	_, err = s.Heartbeat(ctx, []string{"dpt_1"})
	require.Error(t, err)

	mock.ExpectQuery("SET heartbeat_at").WillReturnRows(sqlmock.NewRows([]string{"id", "cancel"}).
		AddRow("dpt_1", false).RowError(0, errDown))
	_, err = s.Heartbeat(ctx, []string{"dpt_1"})
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}
