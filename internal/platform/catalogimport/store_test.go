package catalogimport

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

var errBoom = errors.New("boom")

func TestStoreMappingWithMock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := NewStore(db)

	mock.ExpectQuery(regexp.QuoteMeta(selectMap)).WillReturnRows(sqlmock.NewRows([]string{"urn", "reference"}).AddRow(termURN, "mcp:glossary_term:t"))
	got, err := s.Map(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{termURN: "mcp:glossary_term:t"}, got)

	mock.ExpectQuery(regexp.QuoteMeta(selectMap)).WillReturnError(errBoom)
	_, err = s.Map(context.Background())
	assert.ErrorIs(t, err, errBoom)
	mock.ExpectQuery(regexp.QuoteMeta(selectMap)).WillReturnRows(sqlmock.NewRows([]string{"urn"}).AddRow("x"))
	_, err = s.Map(context.Background())
	assert.Error(t, err)

	mock.ExpectExec(regexp.QuoteMeta(upsertMap)).WithArgs(termURN, "mcp:glossary_term:t").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Record(context.Background(), termURN, "mcp:glossary_term:t"))
	mock.ExpectExec(regexp.QuoteMeta(upsertMap)).WillReturnError(errBoom)
	assert.ErrorIs(t, s.Record(context.Background(), termURN, "x"), errBoom)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreRunsWithMock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := NewStore(db)
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(insertRun)).WithArgs("http://dh", "ana").
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at"}).AddRow("r1", now))
	run, err := s.begin(context.Background(), "http://dh", "ana")
	require.NoError(t, err)
	assert.Equal(t, Run{ID: "r1", SourceURL: "http://dh", StartedBy: "ana", StartedAt: now, Running: true}, run)
	mock.ExpectQuery(regexp.QuoteMeta(insertRun)).WillReturnError(errBoom)
	_, err = s.begin(context.Background(), "http://dh", "ana")
	assert.ErrorIs(t, err, errBoom)

	mock.ExpectExec(regexp.QuoteMeta(finishRun)).WithArgs("r1", sqlmock.AnyArg(), "").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.finish(context.Background(), "r1", nil, ""))
	mock.ExpectExec(regexp.QuoteMeta(finishRun)).WillReturnError(errBoom)
	assert.ErrorIs(t, s.finish(context.Background(), "r1", newReport(), "x"), errBoom)

	cols := []string{"id", "source_url", "started_by", "started_at", "finished_at", "report", "error"}
	mock.ExpectQuery(regexp.QuoteMeta(selectRuns)).WithArgs(10).WillReturnRows(sqlmock.NewRows(cols).
		AddRow("r3", "u", "a", now, nil, "{}", "").
		AddRow("r2", "u", "a", now, nil, "{}", "").
		AddRow("r1", "u", "a", now, now, `{"aspects_written":4}`, ""))
	runs, err := s.Runs(context.Background(), 10, true)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	assert.True(t, runs[0].Running, "the newest unfinished run holds the lock")
	assert.False(t, runs[1].Running, "an older unfinished run was cut off")
	assert.NotEmpty(t, runs[1].Error)
	assert.Equal(t, 4, runs[2].Report.AspectsWritten)

	mock.ExpectQuery(regexp.QuoteMeta(selectRuns)).WillReturnRows(sqlmock.NewRows(cols).AddRow("r1", "u", "a", now, now, `not json`, ""))
	_, err = s.Runs(context.Background(), 10, false)
	assert.Error(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(selectRuns)).WillReturnError(errBoom)
	_, err = s.Runs(context.Background(), 10, false)
	assert.ErrorIs(t, err, errBoom)
	mock.ExpectQuery(regexp.QuoteMeta(selectRuns)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("r1"))
	_, err = s.Runs(context.Background(), 10, false)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunnerWithMock(t *testing.T) {
	open := func(string, string) (Source, error) { return &fakeSource{}, nil }

	t.Run("a source that cannot be opened", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		rn := NewRunner(db, nil, nil, func(string, string) (Source, error) { return nil, errBoom })
		_, err = rn.Start(context.Background(), "http://dh", "t", "ana")
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("the lock is held elsewhere", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("pg_try_advisory_lock").WithArgs(LockKey).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
		_, err = NewRunner(db, nil, nil, open).Start(context.Background(), "http://dh", "t", "ana")
		assert.ErrorIs(t, err, ErrRunning)
	})

	t.Run("the lock cannot be read", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnError(errBoom)
		_, err = NewRunner(db, nil, nil, open).Start(context.Background(), "http://dh", "t", "ana")
		assert.ErrorIs(t, err, errBoom)
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnError(errBoom)
		_, err = NewRunner(db, nil, nil, open).Runs(context.Background(), 10)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("the start cannot be recorded", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(insertRun)).WillReturnError(errBoom)
		mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
		_, err = NewRunner(db, nil, nil, open).Start(context.Background(), "http://dh", "t", "ana")
		assert.ErrorIs(t, err, errBoom)
		assert.NoError(t, mock.ExpectationsWereMet(), "the lock is let go")
	})

	t.Run("the lock is free when nothing runs", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
		mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(regexp.QuoteMeta(selectRuns)).WillReturnRows(sqlmock.NewRows(
			[]string{"id", "source_url", "started_by", "started_at", "finished_at", "report", "error"}).
			AddRow("r1", "u", "a", time.Now(), nil, "{}", ""))
		runs, err := NewRunner(db, nil, nil, open).Runs(context.Background(), 10)
		require.NoError(t, err)
		assert.False(t, runs[0].Running)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a run that fails records its error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.MatchExpectationsInOrder(false)
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(insertRun)).WillReturnRows(sqlmock.NewRows([]string{"id", "started_at"}).AddRow("r1", time.Now()))
		mock.ExpectExec(regexp.QuoteMeta(finishRun)).WithArgs("r1", sqlmock.AnyArg(), "reading the import's mapping: boom").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
		im := New(nil, failingMapping{}, Options{})
		rn := NewRunner(db, im, nil, open)
		_, err = rn.Start(context.Background(), "http://dh", "t", "ana")
		require.NoError(t, err)
		rn.Stop()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// failingMapping cannot be read.
type failingMapping struct{}

func (failingMapping) Map(context.Context) (map[string]string, error) { return nil, errBoom }
func (failingMapping) Record(context.Context, string, string) error   { return errBoom }
