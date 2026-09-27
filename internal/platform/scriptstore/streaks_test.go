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
)

func TestFailureStreaks_CountsNewestFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	success := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	failedAt := success.Add(3 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(failureStreaksQuery)).WillReturnRows(
		sqlmock.NewRows([]string{"script_id", "id", "version", "status", "failure_cause", "finished_at", "last_line", "last_success"}).
			AddRow("a", "r6", 6, "failed", "upstream", failedAt, "E: 500", success).
			AddRow("a", "r5", 1, "failed", "", nil, "E: 500", success).
			AddRow("a", "r4", 1, "failed", "", nil, "E: bad row", success).
			AddRow("a", "r3", 1, "failed", "", nil, "E: 500", success).
			AddRow("a", "r2", 1, "succeeded", "", nil, nil, success).
			AddRow("a", "r1", 1, "failed", "", nil, "E: older", success).
			AddRow("b", "r7", 1, "succeeded", "", nil, nil, nil))

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
	assert.Zero(t, got["b"].Failed)
	assert.Nil(t, got["b"].LastSuccessAt)
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
				sqlmock.NewRows([]string{"script_id", "id", "version", "status", "failure_cause", "finished_at", "last_line", "last_success"}).
					AddRow("a", "r1", 1, "failed", "", nil, "x", nil).RowError(0, errors.New("broken")))
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
