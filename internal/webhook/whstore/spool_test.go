package whstore

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var spoolCols = []string{
	"replica", "dir", "bytes", "events", "files", "oldest_at", "max_bytes", "max_age_seconds",
	"drained_events", "drain_failures", "last_drain_at", "last_error", "started_at", "updated_at",
	"fs_total_bytes", "fs_free_bytes", "consecutive_failures", "failing_since",
}

func TestRecordSpool(t *testing.T) {
	st, mock := newMock(t)
	oldest := start.Add(-time.Minute)
	state := SpoolState{
		Replica: "mcp-0", Dir: "/s", Bytes: 10, Events: 2, Files: 1, Oldest: &oldest, MaxBytes: 100,
		MaxAge: time.Hour, StartedAt: start, UpdatedAt: start, FsTotal: new(int64(1000)), FsFree: new(int64(400)),
		ConsecutiveFailures: 3, FailingSince: &oldest,
	}
	mock.ExpectExec(`INSERT INTO webhook_spool_state`).
		WithArgs("mcp-0", "/s", int64(10), int64(2), 1, oldest, int64(100), int64(3600), int64(0), int64(0), nil, "", start, start,
			new(int64(1000)), new(int64(400)), int64(3), oldest).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_spool_state WHERE updated_at <`).
		WithArgs(start.Add(-spoolStateKept)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, st.RecordSpool(ctx, state))

	mock.ExpectExec(`INSERT INTO webhook_spool_state`).WillReturnError(errDown)
	assert.ErrorIs(t, st.RecordSpool(ctx, state), errDown)
	mock.ExpectExec(`INSERT INTO webhook_spool_state`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_spool_state`).WillReturnError(errDown)
	assert.ErrorIs(t, st.RecordSpool(ctx, state), errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSpoolStates(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectQuery(`FROM webhook_spool_state ORDER BY replica`).WillReturnRows(sqlmock.NewRows(spoolCols).
		AddRow("a", "/s", 10, 2, 1, start, 100, 3600, 5, 1, start, "boom", start, start, 1000, 400, 4, start).
		AddRow("b", "/s", 0, 0, 0, nil, 100, 60, 0, 0, nil, "", start, start, nil, nil, 0, nil))
	got, err := st.SpoolStates(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, time.Hour, got[0].MaxAge)
	require.NotNil(t, got[0].Oldest)
	require.NotNil(t, got[0].LastDrainAt)
	assert.Equal(t, "boom", got[0].LastError)
	assert.Nil(t, got[1].Oldest)
	assert.Nil(t, got[1].LastDrainAt)
	require.NotNil(t, got[0].FsTotal)
	assert.Equal(t, int64(1000), *got[0].FsTotal)
	assert.Equal(t, int64(400), *got[0].FsFree)
	assert.Nil(t, got[1].FsTotal, "an unread volume stays unknown")
	assert.Equal(t, int64(4), got[0].ConsecutiveFailures)
	require.NotNil(t, got[0].FailingSince)
	assert.Nil(t, got[1].FailingSince)

	mock.ExpectQuery(`FROM webhook_spool_state`).WillReturnRows(sqlmock.NewRows(spoolCols))
	none, err := st.SpoolStates(ctx)
	require.NoError(t, err)
	assert.NotNil(t, none, "no state is an empty list")

	mock.ExpectQuery(`FROM webhook_spool_state`).WillReturnError(errDown)
	_, err = st.SpoolStates(ctx)
	assert.ErrorIs(t, err, errDown)
	mock.ExpectQuery(`FROM webhook_spool_state`).WillReturnRows(sqlmock.NewRows([]string{"replica"}).AddRow("a"))
	_, err = st.SpoolStates(ctx)
	assert.Error(t, err)
}
