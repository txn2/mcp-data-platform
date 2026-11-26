package sitetypecheck

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var checkCols = []string{"id", "site_id", "release_number", "environment", "tsconfig", "status", "result", "requested_by", "created_at", "started_at", "finished_at"}

// unsupportedSrc is a release whose tsconfig a check refuses before it runs
// the compiler, so a queue test needs no worker.
var unsupportedSrc = fstest.MapFS{"tsconfig.json": {Data: []byte(`{"compilerOptions":{"baseUrl":"."}}`)}}

func newMockQueue(t *testing.T, source Source) (*Queue, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	if source == nil {
		source = func(context.Context, string, int) (fs.FS, error) { return unsupportedSrc, nil }
	}
	return NewQueue(db, NewChecker(Config{Dir: t.TempDir()}, &fakeGetter{}, Cache{}), source), mock
}

func checkRow(status string, result any) *sqlmock.Rows {
	var raw []byte
	if result != nil {
		raw, _ = json.Marshal(result)
	}
	now := time.Now()
	return sqlmock.NewRows(checkCols).AddRow(int64(7), "site_1", 3, "dev", "tsconfig.json", status, raw, "pat@example.com", now, nil, nil)
}

func TestQueue_EnqueueWakesTheWorker(t *testing.T) {
	q, mock := newMockQueue(t, nil)
	mock.ExpectQuery(`INSERT INTO site_typechecks`).WithArgs("site_1", 3, "dev", "tsconfig.json", "pat@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	id, err := q.Enqueue(context.Background(), Request{SiteID: "site_1", Release: 3, Environment: "dev", Tsconfig: "tsconfig.json", By: "pat@example.com"})
	require.NoError(t, err)
	assert.Equal(t, int64(9), id)
	select {
	case <-q.wake:
	default:
		t.Error("the enqueue did not wake this replica's worker")
	}

	mock.ExpectQuery(`INSERT INTO site_typechecks`).WillReturnError(errors.New("down"))
	_, err = q.Enqueue(context.Background(), Request{SiteID: "site_1"})
	require.ErrorContains(t, err, "queueing a type check")
}

func TestQueue_Latest(t *testing.T) {
	q, mock := newMockQueue(t, nil)
	mock.ExpectQuery(`FROM site_typechecks`).WillReturnError(sql.ErrNoRows)
	c, err := q.Latest(context.Background(), "site_1", 3)
	require.NoError(t, err)
	assert.Nil(t, c, "a release never checked has no check")

	mock.ExpectQuery(`FROM site_typechecks`).WillReturnRows(checkRow(StatusQueued, nil))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM site_typechecks`).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
	c, err = q.Latest(context.Background(), "site_1", 3)
	require.NoError(t, err)
	assert.Equal(t, 2, c.Ahead, "a queued check says how many run first")

	mock.ExpectQuery(`FROM site_typechecks`).WillReturnRows(checkRow(StatusErrors, Result{Status: StatusErrors, Diagnostics: []Diagnostic{{File: "a.ts", Code: "TS2322"}}}))
	c, err = q.Latest(context.Background(), "site_1", 3)
	require.NoError(t, err)
	require.NotNil(t, c.Result)
	assert.Equal(t, "TS2322", c.Result.Diagnostics[0].Code)

	mock.ExpectQuery(`FROM site_typechecks`).WillReturnError(errors.New("down"))
	_, err = q.Latest(context.Background(), "site_1", 3)
	require.Error(t, err)
}

func TestQueue_Summaries(t *testing.T) {
	q, mock := newMockQueue(t, nil)
	mock.ExpectQuery(`SELECT DISTINCT ON \(release_number\)`).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows([]string{"n", "status", "errors"}).AddRow(3, StatusErrors, 2).AddRow(4, StatusClean, 0))
	got, err := q.Summaries(context.Background(), "site_1")
	require.NoError(t, err)
	assert.Equal(t, map[int]Summary{3: {Status: StatusErrors, Errors: 2}, 4: {Status: StatusClean}}, got)

	mock.ExpectQuery(`SELECT DISTINCT ON`).WillReturnError(errors.New("down"))
	_, err = q.Summaries(context.Background(), "site_1")
	require.Error(t, err)

	mock.ExpectQuery(`SELECT DISTINCT ON`).WillReturnRows(sqlmock.NewRows([]string{"n", "status", "errors"}).AddRow("x", StatusClean, 0))
	_, err = q.Summaries(context.Background(), "site_1")
	require.Error(t, err)
}

// TestQueue_RunQueued claims the oldest check, runs it, records its result,
// and stops when nothing is left.
func TestQueue_RunQueued(t *testing.T) {
	q, mock := newMockQueue(t, nil)
	mock.ExpectQuery(`UPDATE site_typechecks SET status = 'running'`).WillReturnRows(checkRow(StatusRunning, nil))
	mock.ExpectQuery(`SELECT tsbuildinfo FROM site_typecheck_state`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`UPDATE site_typechecks SET status = \$2`).WithArgs(int64(7), StatusUnsupported, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`UPDATE site_typechecks SET status = 'running'`).WillReturnError(sql.ErrNoRows)
	require.NoError(t, q.runQueued(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery(`UPDATE site_typechecks SET status = 'running'`).WillReturnError(errors.New("down"))
	require.ErrorContains(t, q.runQueued(context.Background()), "claiming a type check")
}

// TestQueue_RunOneRecordsAFailedRead: a release whose files cannot be read
// ends failed, saying so.
func TestQueue_RunOneRecordsAFailedRead(t *testing.T) {
	q, mock := newMockQueue(t, func(context.Context, string, int) (fs.FS, error) { return nil, errors.New("gone") })
	mock.ExpectExec(`UPDATE site_typechecks SET status = \$2`).WithArgs(int64(7), StatusFailed, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, q.runOne(context.Background(), &Check{ID: 7, SiteID: "site_1", ReleaseNumber: 3}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestQueue_FinishKeepsTheState: the environment's incremental state is kept
// when the check left one under the bound, and a failed write is reported.
func TestQueue_FinishKeepsTheState(t *testing.T) {
	q, mock := newMockQueue(t, nil)
	c := &Check{ID: 7, SiteID: "site_1", Environment: "dev"}
	mock.ExpectExec(`UPDATE site_typechecks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO site_typecheck_state`).WithArgs("site_1", "dev", []byte("state")).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, q.finish(context.Background(), c, Result{Status: StatusClean}, []byte("state")))

	mock.ExpectExec(`UPDATE site_typechecks`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, q.finish(context.Background(), c, Result{Status: StatusClean}, make([]byte, maxStateBytes+1)), "an oversized state is not kept")

	mock.ExpectExec(`UPDATE site_typechecks`).WillReturnError(errors.New("down"))
	require.ErrorContains(t, q.finish(context.Background(), c, Result{Status: StatusClean}, nil), "recording a type check")

	mock.ExpectExec(`UPDATE site_typechecks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO site_typecheck_state`).WillReturnError(errors.New("down"))
	require.ErrorContains(t, q.finish(context.Background(), c, Result{Status: StatusClean}, []byte("state")), "incremental state")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestLogFinished: a finished check is logged with its whole time and its
// compile time (#2833); one that could not use the compilation cache is a
// warning naming why.
func TestLogFinished(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	c := &Check{ID: 7, SiteID: "s1"}

	logFinished(context.Background(), c, Result{Status: StatusClean, DurationMS: 162140, CompileMS: 103727})
	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "INFO", line["level"])
	assert.Equal(t, "site type check finished", line["msg"])
	assert.InDelta(t, 162140, line["duration_ms"], 0)
	assert.InDelta(t, 103727, line["compile_ms"], 0)

	buf.Reset()
	logFinished(context.Background(), c, Result{Status: StatusClean, CacheError: "write /cache/x: no space left on device"})
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "WARN", line["level"])
	assert.Contains(t, line["error"], "no space left on device")
}
