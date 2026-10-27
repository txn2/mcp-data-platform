package scriptevents

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

var errDown = errors.New("db down")

func mockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func runFor(id string) *script.Run {
	return &script.Run{
		ID: "dpx_" + id, ScriptID: "s1", VersionID: "v1", Version: 2, Trigger: runstate.TriggerEvent,
		RequestedBy: "jane@example.com", EventSource: "shop", EventID: id, Event: map[string]any{"id": id},
	}
}

func TestScriptsForSource(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery(regexp.QuoteMeta(scriptsForSourceSQL)).WithArgs("shop").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("s1").AddRow("s2"))
	ids, err := s.ScriptsForSource(t.Context(), "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"s1", "s2"}, ids)

	mock.ExpectQuery(regexp.QuoteMeta(scriptsForSourceSQL)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	ids, err = s.ScriptsForSource(t.Context(), "none")
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids, "no script is an empty list")

	mock.ExpectQuery(regexp.QuoteMeta(scriptsForSourceSQL)).WillReturnError(errDown)
	_, err = s.ScriptsForSource(t.Context(), "shop")
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery(regexp.QuoteMeta(scriptsForSourceSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("s1").RowError(0, errDown))
	_, err = s.ScriptsForSource(t.Context(), "shop")
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectEnqueue expects the lock and the count of pending event runs.
func expectEnqueue(mock sqlmock.Sqlmock, pending int) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(lockScriptSQL)).WithArgs("s1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(pendingEventRunsSQL)).WithArgs("s1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(pending))
}

// #2332: an event's run is pending while the script is under the bound,
// skipped past it, and nothing when the event already has a run.
func TestEnqueueEvents(t *testing.T) {
	s, mock := mockStore(t)
	expectEnqueue(mock, 1)
	insert := regexp.QuoteMeta("INSERT INTO script_runs")
	mock.ExpectExec(insert).WithArgs("dpx_a", "s1", "v1", 2, script.RunStatusPending, sqlmock.AnyArg(), "jane@example.com",
		"shop", "a", sqlmock.AnyArg(), "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insert).WithArgs("dpx_b", "s1", "v1", 2, runstate.StatusSkippedBacklog, sqlmock.AnyArg(), "jane@example.com",
		"shop", "b", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insert).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 0))

	got, err := s.EnqueueEvents(t.Context(), []*script.Run{runFor("a"), runFor("b"), runFor("c")}, 2)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{Enqueued: 1, Skipped: 1, Already: 1}, got)
	require.NoError(t, mock.ExpectationsWereMet())

	got, err = s.EnqueueEvents(t.Context(), nil, 2)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{}, got)
}

func TestEnqueueEvents_Failures(t *testing.T) {
	for name, setup := range map[string]func(mock sqlmock.Sqlmock){
		"begin": func(mock sqlmock.Sqlmock) { mock.ExpectBegin().WillReturnError(errDown) },
		"lock": func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(lockScriptSQL)).WillReturnError(errDown)
			mock.ExpectRollback()
		},
		"count": func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(lockScriptSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(regexp.QuoteMeta(pendingEventRunsSQL)).WillReturnError(errDown)
			mock.ExpectRollback()
		},
		"insert": func(mock sqlmock.Sqlmock) {
			expectEnqueue(mock, 0)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errDown)
			mock.ExpectRollback()
		},
		"rows affected": func(mock sqlmock.Sqlmock) {
			expectEnqueue(mock, 0)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnResult(sqlmock.NewErrorResult(errDown))
			mock.ExpectRollback()
		},
		"commit": func(mock sqlmock.Sqlmock) {
			expectEnqueue(mock, 0)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit().WillReturnError(errDown)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, mock := mockStore(t)
			setup(mock)
			_, err := s.EnqueueEvents(t.Context(), []*script.Run{runFor("a")}, 10)
			require.ErrorIs(t, err, errDown)
		})
	}

	s, mock := mockStore(t)
	expectEnqueue(mock, 0)
	mock.ExpectRollback()
	bad := runFor("a")
	bad.EventID = ""
	_, err := s.EnqueueEvents(t.Context(), []*script.Run{bad}, 10)
	require.ErrorContains(t, err, "needs a run id and an event id")

	s, mock = mockStore(t)
	expectEnqueue(mock, 0)
	mock.ExpectRollback()
	unencodable := runFor("a")
	unencodable.Event = map[string]any{"x": make(chan int)}
	_, err = s.EnqueueEvents(t.Context(), []*script.Run{unencodable}, 10)
	require.ErrorContains(t, err, "marshal run event")
}

func TestEventSummary(t *testing.T) {
	s, mock := mockStore(t)
	since := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WithArgs("s1", since).
		WillReturnRows(sqlmock.NewRows([]string{"pending", "skipped", "last_run", "last_skip"}).AddRow(3, 1, last, nil))
	got, err := s.EventSummary(t.Context(), "s1", since)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Pending)
	assert.Equal(t, 1, got.SkippedSince)
	assert.Equal(t, &last, got.LastEventRun)
	assert.Nil(t, got.LastSkippedAt)

	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnError(errDown)
	_, err = s.EventSummary(t.Context(), "s1", since)
	require.ErrorIs(t, err, errDown)
}
