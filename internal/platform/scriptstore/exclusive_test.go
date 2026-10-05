package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/openrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// errExclusiveConflict is the error the one-open-run index raises (#1986).
var errExclusiveConflict = &pq.Error{Code: pgUniqueViolation, Constraint: exclusiveOpenIndex}

func exclusiveRun() *script.Run {
	return &script.Run{ID: "dpx_2", ScriptID: "script_1", VersionID: "sver_1", Version: 3, Trigger: script.TriggerTool}
}

// TestEnqueue_AnExclusiveScriptWithARunOpenIsRefusedNamingIt is #1986's
// run_script and portal criterion at the store: nothing is queued, and the
// error names the open run, what started it and since when.
func TestEnqueue_AnExclusiveScriptWithARunOpenIsRefusedNamingIt(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errExclusiveConflict)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WithArgs("script_1", "").
		WillReturnRows(sqlmock.NewRows(openRunColumns).
			AddRow("run_open", script.TriggerSchedule, script.RunStatusRunning, rowTime, rowTime))

	err := s.Enqueue(context.Background(), exclusiveRun())

	var openErr *openrun.Error
	require.ErrorAs(t, err, &openErr)
	require.ErrorIs(t, err, openrun.ErrOpen)
	assert.Equal(t, "run_open", openErr.Open.ID)
	assert.Contains(t, err.Error(), "run run_open (started by its schedule, running since 2023-11-14T22:13:20Z)")
	assert.Contains(t, err.Error(), "nothing was queued")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnqueue_RetriesOnceWhenTheOpenRunHasJustEnded covers the open run
// finishing between the refused insert and the read that names it: the run is
// queued on the second try.
func TestEnqueue_RetriesOnceWhenTheOpenRunHasJustEnded(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errExclusiveConflict)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnRows(sqlmock.NewRows(openRunColumns))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
		WillReturnRows(sqlmock.NewRows(enqueueReturning).
			AddRow(rowTime, rowTime, int64(0), []byte("{}"), rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 1))

	run := exclusiveRun()
	require.NoError(t, s.Enqueue(context.Background(), run))
	assert.Equal(t, script.RunStatusPending, run.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnqueue_ASecondConflictWithNothingToNameIsStillARefusal covers a run
// opened and closed again around both reads: the caller is still told the
// script runs one at a time rather than handed an internal failure.
func TestEnqueue_ASecondConflictWithNothingToNameIsStillARefusal(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errExclusiveConflict)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnRows(sqlmock.NewRows(openRunColumns))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errExclusiveConflict)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnRows(sqlmock.NewRows(openRunColumns))

	err := s.Enqueue(context.Background(), exclusiveRun())
	require.ErrorIs(t, err, openrun.ErrOpen)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnqueue_AFailedReadOfTheOpenRunIsReturned keeps a database fault from
// reading as the refusal.
func TestEnqueue_AFailedReadOfTheOpenRunIsReturned(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errExclusiveConflict)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnError(errors.New("boom"))

	err := s.Enqueue(context.Background(), exclusiveRun())
	require.Error(t, err)
	assert.NotErrorIs(t, err, openrun.ErrOpen)
	assert.Contains(t, err.Error(), "reading the open run")
}

// TestEnqueue_AnotherUniqueViolationIsNotTheRefusal keeps the refusal to the
// one index that means it.
func TestEnqueue_AnotherUniqueViolationIsNotTheRefusal(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
		WillReturnError(&pq.Error{Code: pgUniqueViolation, Constraint: "script_runs_pkey"})

	err := s.Enqueue(context.Background(), exclusiveRun())
	require.Error(t, err)
	assert.NotErrorIs(t, err, openrun.ErrOpen)
	assert.Contains(t, err.Error(), "enqueue script run")
}

// TestUpdate_TurningExclusiveOnWithTwoRunsOpenIsRefused is the save half: the
// re-stamp of the open runs violates the index, and the save is refused with
// the reason rather than leaving two open runs the setting does not hold.
func TestUpdate_TurningExclusiveOnWithTwoRunsOpenIsRefused(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"changed"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_runs SET exclusive")).
		WithArgs("script_1", true).WillReturnError(errExclusiveConflict)
	mock.ExpectRollback()

	err := s.Update(context.Background(), &script.Script{ID: "script_1", Name: "x", Exclusive: true})
	require.ErrorIs(t, err, openrun.ErrOpen)
	assert.Contains(t, err.Error(), openrun.BlockedMessage)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdate_ARestampFaultIsWrapped keeps a database fault distinct from the
// refusal.
func TestUpdate_ARestampFaultIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"changed"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_runs SET exclusive")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := s.Update(context.Background(), &script.Script{ID: "script_1", Name: "x"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, openrun.ErrOpen)
	assert.Contains(t, err.Error(), "stamping the script's open runs")
}
