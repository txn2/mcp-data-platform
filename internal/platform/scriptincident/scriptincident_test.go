package scriptincident

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockStore(t *testing.T) (Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

func TestPostgresStore_Open(t *testing.T) {
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	for _, opened := range []bool{true, false} {
		s, mock := mockStore(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_failure_incidents")).
			WithArgs("s1", at, "run-1", "upstream", "timeout").
			WillReturnRows(sqlmock.NewRows([]string{"opened"}).AddRow(opened))
		got, err := s.Open(context.Background(), Opening{ScriptID: "s1", RunID: "run-1", Cause: "upstream", Error: "timeout", FailedAt: at})
		require.NoError(t, err)
		assert.Equal(t, opened, got)
		require.NoError(t, mock.ExpectationsWereMet())
	}
	s, mock := mockStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_failure_incidents")).WillReturnError(errors.New("down"))
	_, err := s.Open(context.Background(), Opening{ScriptID: "s1"})
	assert.ErrorContains(t, err, "recording a failed automation run")
}

func TestPostgresStore_Close(t *testing.T) {
	opened := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	s, mock := mockStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("DELETE FROM script_failure_incidents")).WithArgs("s1").
		WillReturnRows(sqlmock.NewRows([]string{"opened_at", "opening_run_id", "opening_cause", "opening_error", "failed_runs", "last_failed_at"}).
			AddRow(opened, "run-1", "upstream", "timeout", 4, opened.Add(time.Hour)))
	in, err := s.Close(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, &Incident{
		OpenedAt: opened, OpeningRunID: "run-1", OpeningCause: "upstream", OpeningError: "timeout",
		FailedRuns: 4, LastFailedAt: opened.Add(time.Hour),
	}, in)

	mock.ExpectQuery(regexp.QuoteMeta("DELETE FROM script_failure_incidents")).WillReturnRows(sqlmock.NewRows([]string{"opened_at"}))
	in, err = s.Close(context.Background(), "s1")
	require.NoError(t, err)
	assert.Nil(t, in, "no incident open")

	mock.ExpectQuery(regexp.QuoteMeta("DELETE FROM script_failure_incidents")).WillReturnError(errors.New("down"))
	_, err = s.Close(context.Background(), "s1")
	assert.ErrorContains(t, err, "closing an automation's failure incident")
}
