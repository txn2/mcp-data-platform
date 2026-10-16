package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

func TestFailureStreaks_CountsNewestFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	success := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	failedAt := success.Add(3 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"script_id", "id", "version", "status", "failure_cause", "finished_at", "last_line", "last_success", "checkpoint"}).
			AddRow("a", "r6", 6, "failed", "upstream", failedAt, "E: 500", success, []byte(`{"through":"07:00"}`)).
			AddRow("a", "r5", 1, "failed", "", nil, "E: 500", success, nil).
			AddRow("a", "r4", 1, "failed", "", nil, "E: bad row", success, nil).
			AddRow("a", "r3", 1, "failed", "", nil, "E: 500", success, nil).
			AddRow("a", "r2", 1, "succeeded", "", nil, nil, success, nil).
			AddRow("a", "r1", 1, "failed", "", nil, "E: older", success, nil).
			AddRow("b", "r7", 1, "succeeded", "", nil, nil, nil, nil))
	started := failedAt.Add(5 * time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta(latestRunsQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"script_id", "id", "status", "created_at", "started_at"}).
			AddRow("a", "r8", "running", started, started).
			AddRow("b", "r7", "succeeded", success, nil).
			AddRow("c", "r9", "pending", success, nil))

	got, err := New(db).FailureStreaks(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, 4, got["a"].Failed)
	assert.Equal(t, 2, got["a"].SameError)
	assert.Equal(t, "E: 500", got["a"].LastError)
	assert.Equal(t, success, *got["a"].LastSuccessAt)
	assert.Equal(t, "r6", got["a"].LastFailedRunID)
	assert.Equal(t, failedAt, *got["a"].LastFailedAt)
	assert.Equal(t, 6, got["a"].LastFailedVersion)
	assert.Equal(t, "upstream", got["a"].LastCause)
	assert.Equal(t, map[string]any{"through": "07:00"}, got["a"].LastCheckpoint, "the newest failure committed a checkpoint")
	assert.Zero(t, got["b"].Failed)
	assert.Nil(t, got["b"].LastSuccessAt)
	assert.Equal(t, &runstate.LatestRun{ID: "r8", Status: "running", CreatedAt: started, StartedAt: &started}, got["a"].Latest,
		"the newest run whatever its status, a run newer than every finished one")
	assert.Equal(t, &runstate.LatestRun{ID: "r7", Status: "succeeded", CreatedAt: success}, got["b"].Latest)
	assert.NotContains(t, got, "c", "a script with no finished run stays absent")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFailureStreaks_Failures(t *testing.T) {
	got, err := New(nil).FailureStreaks(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	for name, setup := range map[string]func(sqlmock.Sqlmock){
		"query": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnError(errors.New("down"))
		},
		"scan": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow("a"))
		},
		"iterate": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnRows(
				sqlmock.NewRows([]string{"script_id", "id", "version", "status", "failure_cause", "finished_at", "last_line", "last_success", "checkpoint"}).
					AddRow("a", "r1", 1, "failed", "", nil, "x", nil, nil).RowError(0, errors.New("broken")))
		},
		"latest query": func(m sqlmock.Sqlmock) {
			expectOneStreak(m)
			m.ExpectQuery(regexp.QuoteMeta(latestRunsQuery)).WillReturnError(errors.New("down"))
		},
		"latest scan": func(m sqlmock.Sqlmock) {
			expectOneStreak(m)
			m.ExpectQuery(regexp.QuoteMeta(latestRunsQuery)).WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow("a"))
		},
		"latest iterate": func(m sqlmock.Sqlmock) {
			expectOneStreak(m)
			m.ExpectQuery(regexp.QuoteMeta(latestRunsQuery)).WillReturnRows(
				sqlmock.NewRows([]string{"script_id", "id", "status", "created_at", "started_at"}).
					AddRow("a", "r1", "failed", time.Now(), nil).RowError(0, errors.New("broken")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			setup(mock)
			_, err = New(db).FailureStreaks(context.Background(), []string{"a"})
			assert.Error(t, err)
		})
	}
}

// expectOneStreak answers the streak query with one failed run of script a.
func expectOneStreak(m sqlmock.Sqlmock) {
	m.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"script_id", "id", "version", "status", "failure_cause", "finished_at", "last_line", "last_success", "checkpoint"}).
			AddRow("a", "r1", 1, "failed", "", nil, "x", nil, nil))
}
