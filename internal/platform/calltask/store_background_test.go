package calltask

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// taskRow is one row of taskColumns.
func taskRow(id string, at time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tool_name", "user_id", "status", "status_message", "result", "error",
		"created_at", "updated_at", "heartbeat_at", "cancel_requested", "expires_at",
		"idempotency_key", "background", "progress",
	}).AddRow(id, "api_export", "jane", "working", msgWorking, nil, nil,
		at, at, at, false, at.Add(time.Hour), "k1", true, []byte(`{"pages":2}`))
}

// A background export is inserted once per caller, tool and key: a second
// start under a working key reads back the export already working.
func TestPostgresStoreCreateBackground(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s, mock := mockStore(t)
	task := Task{
		ID: "dpt_2", ToolName: "api_export", UserID: "jane", StatusMessage: msgWorking,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), IdempotencyKey: "k1",
	}

	mock.ExpectExec("INSERT INTO call_tasks").
		WithArgs("dpt_2", "api_export", "jane", msgWorking, now, now.Add(time.Hour), "k1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	got, created, err := s.CreateBackground(ctx, task)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "dpt_2", got.ID)

	mock.ExpectExec("INSERT INTO call_tasks").
		WillReturnError(&pq.Error{Code: pgUniqueViolation, Constraint: workingKeyIndex})
	mock.ExpectQuery("idempotency_key = \\$3").WithArgs("jane", "api_export", "k1").
		WillReturnRows(taskRow("dpt_1", now))
	got, created, err = s.CreateBackground(ctx, task)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "dpt_1", got.ID)
	assert.JSONEq(t, `{"pages":2}`, string(got.Progress))

	mock.ExpectExec("INSERT INTO call_tasks").
		WillReturnError(&pq.Error{Code: pgUniqueViolation, Constraint: workingKeyIndex})
	mock.ExpectQuery("idempotency_key = \\$3").WillReturnError(errDown)
	_, _, err = s.CreateBackground(ctx, task)
	require.ErrorIs(t, err, errDown)

	mock.ExpectExec("INSERT INTO call_tasks").WillReturnError(errDown)
	_, _, err = s.CreateBackground(ctx, task)
	require.ErrorIs(t, err, errDown)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStoreProgressAndEnded(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s, mock := mockStore(t)

	mock.ExpectExec("SET progress").WithArgs("dpt_1", []byte(`{"pages":3}`)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetProgress(ctx, "dpt_1", json.RawMessage(`{"pages":3}`)))
	mock.ExpectExec("SET progress").WillReturnError(errDown)
	require.ErrorIs(t, s.SetProgress(ctx, "dpt_1", nil), errDown)

	mock.ExpectQuery("background AND status <> 'working'").WithArgs("jane", now, 5).
		WillReturnRows(taskRow("dpt_1", now))
	ended, err := s.EndedBackground(ctx, "jane", now, 5)
	require.NoError(t, err)
	require.Len(t, ended, 1)
	assert.Equal(t, "k1", ended[0].IdempotencyKey)

	mock.ExpectQuery("background AND status <> 'working'").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("dpt_1"))
	_, err = s.EndedBackground(ctx, "jane", now, 5)
	require.Error(t, err, "a row that does not scan")

	mock.ExpectQuery("background AND status <> 'working'").
		WillReturnRows(taskRow("dpt_1", now).RowError(0, errDown))
	_, err = s.EndedBackground(ctx, "jane", now, 5)
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery("background AND status <> 'working'").WillReturnError(errDown)
	_, err = s.EndedBackground(ctx, "jane", now, 5)
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery("background AND status <> 'working'").WillReturnRows(sqlmock.NewRows(nil))
	ended, err = s.EndedBackground(ctx, "jane", now, 5)
	require.NoError(t, err)
	assert.NotNil(t, ended, "no ended export is an empty list")
	assert.NoError(t, mock.ExpectationsWereMet())
}
