package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// stateSelectColumns is the result-set shape a script_state SELECT mock must
// return, in stateColumns order.
var stateSelectColumns = []string{"script_id", "state", "revision", "run_id", "updated_by", "updated_at"}

// stateRow is one state row at the given revision, written by a run.
func stateRow(revision int64) []driver.Value {
	return []driver.Value{"script_1", []byte(`{"synced_through":"2026-08-28"}`), revision, "dpx_9", "", rowTime}
}

func TestStateColumnsMatchTheScanOrder(t *testing.T) {
	assert.Len(t, splitTopLevel(stateColumns), len(stateSelectColumns))
}

func TestGetState(t *testing.T) {
	t.Run("no row is revision 0 and an empty object", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_state WHERE script_id = $1")).
			WithArgs("script_1").WillReturnRows(sqlmock.NewRows(stateSelectColumns))

		st, err := s.GetState(context.Background(), "script_1")
		require.NoError(t, err)
		assert.Equal(t, "script_1", st.ScriptID)
		assert.Zero(t, st.Revision)
		assert.Equal(t, map[string]any{}, st.Value, "a reader always gets an object, never nil")
		assert.Equal(t, "nobody", st.WrittenBy())
	})

	t.Run("a row reads back with its writer", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_state")).
			WillReturnRows(sqlmock.NewRows(stateSelectColumns).AddRow(stateRow(3)...))

		st, err := s.GetState(context.Background(), "script_1")
		require.NoError(t, err)
		assert.Equal(t, int64(3), st.Revision)
		assert.Equal(t, "2026-08-28", st.Value["synced_through"])
		assert.Equal(t, "run dpx_9", st.WrittenBy())
	})

	t.Run("a read failure is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_state")).WillReturnError(errors.New("boom"))

		_, err := s.GetState(context.Background(), "script_1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get script state")
	})

	t.Run("a malformed stored object is reported", func(t *testing.T) {
		s, mock := newMock(t)
		row := stateRow(1)
		row[1] = []byte("{not json")
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_state")).
			WillReturnRows(sqlmock.NewRows(stateSelectColumns).AddRow(row...))

		_, err := s.GetState(context.Background(), "script_1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal script state")
	})
}

// TestSetState_IsAnUnconditionalUpsertByAPerson pins the reset: one statement
// that inserts at revision 1 or bumps the existing revision, recording the
// person and clearing the run that wrote the previous revision.
func TestSetState_IsAnUnconditionalUpsertByAPerson(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (script_id) DO UPDATE")).
		WithArgs("script_1", []byte(`{"synced_through":"2026-08-01"}`), "jane@example.com").
		WillReturnRows(sqlmock.NewRows(stateSelectColumns).
			AddRow("script_1", []byte(`{"synced_through":"2026-08-01"}`), int64(4), "", "jane@example.com", rowTime))

	st, err := s.SetState(context.Background(), "script_1",
		map[string]any{"synced_through": "2026-08-01"}, "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, int64(4), st.Revision)
	assert.Equal(t, "jane@example.com", st.WrittenBy())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetState_ClearsWithAnEmptyObject(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_state")).
		WithArgs("script_1", []byte(`{}`), "jane@example.com").
		WillReturnRows(sqlmock.NewRows(stateSelectColumns).
			AddRow("script_1", []byte(`{}`), int64(5), "", "jane@example.com", rowTime))

	st, err := s.SetState(context.Background(), "script_1", nil, "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, st.Value)
	assert.Equal(t, int64(5), st.Revision, "a clear moves the revision, so a run in flight fails at its write")
}

func TestSetState_RefusesAnOversizedObject(t *testing.T) {
	s, mock := newMock(t)
	_, err := s.SetState(context.Background(), "script_1",
		map[string]any{"blob": strings.Repeat("x", script.MaxStateBytes)}, "jane@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the")
	require.NoError(t, mock.ExpectationsWereMet(), "nothing reaches the database")
}

func TestSetState_WriteFailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_state")).WillReturnError(errors.New("boom"))

	_, err := s.SetState(context.Background(), "script_1", map[string]any{}, "jane@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set script state")
}

// The state write inside Finish (#1537).

// TestFinish_AppliesStagedStateInTheSuccessTransaction pins the whole of the
// concurrency guarantee at the statement level: the run row is read under its
// lease, the state is upserted WHERE revision = the revision the run read, and
// the run row records what it wrote and the revision that produced, all under
// one transaction.
func TestFinish_AppliesStagedStateInTheSuccessTransaction(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision FROM script_runs")).
		WithArgs("dpx_1", "worker-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "state_revision"}).AddRow("script_1", int64(2)))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_state.revision = $4")).
		WithArgs("script_1", []byte(`{"synced_through":"2026-08-28"}`), "dpx_1", int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(3)))
	mock.ExpectExec(regexp.QuoteMeta("state_written = $9, state_revision_written = $10")).
		WithArgs("dpx_1", "worker-a", 1, script.RunStatusSucceeded, "", "", false, sqlmock.AnyArg(),
			[]byte(`{"synced_through":"2026-08-28"}`), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).
		WithArgs(NotifyChannel, "dpx_1").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, s.Finish(context.Background(), testLease, script.RunResult{
		Status: script.RunStatusSucceeded,
		State:  &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-28"}},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFinish_ARefusedStateWriteFailsTheRunNamingTheWriter pins the conflict
// arm: the guarded upsert returns no row, the current row is read to name who
// moved the state, and the run is recorded FAILED with that message in the same
// transaction, its state columns left NULL.
func TestFinish_ARefusedStateWriteFailsTheRunNamingTheWriter(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision FROM script_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "state_revision"}).AddRow("script_1", int64(2)))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_state.revision = $4")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_state WHERE script_id = $1")).
		WithArgs("script_1").
		WillReturnRows(sqlmock.NewRows(stateSelectColumns).AddRow(stateRow(3)...))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_runs")).
		WithArgs("dpx_1", "worker-a", 1, script.RunStatusFailed, sqlmock.AnyArg(), "", false, sqlmock.AnyArg(), nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, s.Finish(context.Background(), testLease, script.RunResult{
		Status: script.RunStatusSucceeded,
		State:  &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-29"}},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFinish_AFailedRunNeverTouchesTheState pins that a failed run's staged
// state is dropped on the floor: no transaction, no state statement, the plain
// terminal update with NULL state columns.
func TestFinish_AFailedRunNeverTouchesTheState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_runs")).
		WithArgs("dpx_1", "worker-a", 1, script.RunStatusFailed, "boom", "", false, sqlmock.AnyArg(), nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, s.Finish(context.Background(), testLease, script.RunResult{
		Status: script.RunStatusFailed, Error: "boom",
		State: &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-29"}},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFinish_AStateWriteUnderALostLeaseIsRefused pins that the lease is checked
// before the state row is touched: a reclaimed run's stale worker must not
// move the state.
func TestFinish_AStateWriteUnderALostLeaseIsRefused(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision FROM script_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "state_revision"}))
	mock.ExpectRollback()

	err := s.Finish(context.Background(), testLease, script.RunResult{
		Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{}},
	})
	assert.ErrorIs(t, err, script.ErrLeaseLost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinish_StateWriteFailuresAreWrapped(t *testing.T) {
	t.Run("reading the run", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision")).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()

		err := s.Finish(context.Background(), testLease, script.RunResult{
			Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "state revision")
	})

	t.Run("the upsert", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision")).
			WillReturnRows(sqlmock.NewRows([]string{"script_id", "state_revision"}).AddRow("script_1", int64(0)))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_state")).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()

		err := s.Finish(context.Background(), testLease, script.RunResult{
			Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "write run state")
	})

	t.Run("naming the writer", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT script_id, state_revision")).
			WillReturnRows(sqlmock.NewRows([]string{"script_id", "state_revision"}).AddRow("script_1", int64(0)))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_state")).WillReturnRows(sqlmock.NewRows([]string{"revision"}))
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_state WHERE script_id")).WillReturnError(errors.New("boom"))
		mock.ExpectRollback()

		err := s.Finish(context.Background(), testLease, script.RunResult{
			Status: script.RunStatusSucceeded, State: &script.StateWrite{Value: map[string]any{}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refused a run's write")
	})
}

// TestScanRun_ReadsTheStateColumns pins the two NULL-vs-object distinctions a
// reader depends on: a run that saved nothing carries no written state, and a
// run that saved {} carries an empty object.
func TestScanRun_ReadsTheStateColumns(t *testing.T) {
	t.Run("saved nothing", func(t *testing.T) {
		s, mock := newMock(t)
		row := runRow(script.RunStatusSucceeded, 1, nil)
		row[21], row[22] = int64(2), []byte(`{"synced_through":"2026-08-27"}`)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
			WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(row...))

		run, err := s.GetRun(context.Background(), "dpx_1")
		require.NoError(t, err)
		assert.Equal(t, int64(2), run.StateRevision)
		assert.Equal(t, "2026-08-27", run.StateRead["synced_through"])
		assert.Nil(t, run.StateWritten)
		assert.Zero(t, run.StateRevisionWritten)
	})

	t.Run("saved an empty object", func(t *testing.T) {
		s, mock := newMock(t)
		row := runRow(script.RunStatusSucceeded, 1, nil)
		row[23], row[24] = []byte(`{}`), int64(3)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
			WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(row...))

		run, err := s.GetRun(context.Background(), "dpx_1")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{}, run.StateWritten, "{} is a write; nil is no write")
		assert.Equal(t, int64(3), run.StateRevisionWritten)
	})

	t.Run("malformed state is reported", func(t *testing.T) {
		for _, tc := range []struct {
			name, want string
			col        int
		}{
			{"read", "state read", 22}, {"written", "state written", 23},
		} {
			s, mock := newMock(t)
			row := runRow(script.RunStatusSucceeded, 1, nil)
			row[tc.col] = []byte("{not json")
			mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
				WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(row...))

			_, err := s.GetRun(context.Background(), "dpx_1")
			require.Error(t, err, tc.name)
			assert.Contains(t, err.Error(), tc.want)
		}
	})
}

// TestEnqueue_PinsTheStateReadInTheInsert pins that the revision and the object
// a run reads are taken in the insert itself and handed back on the run.
func TestEnqueue_PinsTheStateReadInTheInsert(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("COALESCE((SELECT revision FROM script_state WHERE script_id = $2), 0)")).
		WillReturnRows(sqlmock.NewRows(enqueueReturning).
			AddRow(rowTime, rowTime, int64(4), []byte(`{"cursor":"abc"}`), rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 1))

	run := &script.Run{ID: "dpx_1", ScriptID: "script_1"}
	require.NoError(t, s.Enqueue(context.Background(), run))
	assert.Equal(t, int64(4), run.StateRevision)
	assert.Equal(t, "abc", run.StateRead["cursor"])
}

// TestContract_ReportsWhatTheSourceDoesWithState pins that the contract's
// reads/saves come from the source, not from the revision: a script that
// saves state says so before it has ever run.
func TestContract_ReportsWhatTheSourceDoesWithState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{
				id: "script_1", name: "sync", owner: "jane@example.com", paramsJSON: emptyParams(t),
				source: "since = run.state.get(\"synced_through\", \"\")\nplatform.save_state({\"synced_through\": run.fire_time})\n",
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).WillReturnRows(sqlmock.NewRows(scheduleSelectColumns))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnRows(sqlmock.NewRows(runSelectColumns))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_state")).WillReturnRows(sqlmock.NewRows(stateSelectColumns))

	got, err := s.Contract(context.Background(), "script_1")
	require.NoError(t, err)
	require.NotNil(t, got.State)
	assert.True(t, got.State.Reads)
	assert.True(t, got.State.Saves)
	assert.Zero(t, got.State.Revision)
	assert.Contains(t, got.Text(), "State: reads and saves state")
}

// A dry run's account keeps the state the draft would have saved (#1537), as
// NULL when it saved none and as the object when it did.
func TestRecordDryRun_KeepsTheStateTheDraftWouldHaveSaved(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_dry_runs")).
		WithArgs("dpx_draft_1", "script_1", script.SourceDigest("x = 1\n"), "jane@example.com",
			script.RunStatusSucceeded, "", "", false, sqlmock.AnyArg(), sqlmock.AnyArg(),
			[]byte(`{"synced_through":"2026-08-28"}`)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(rowTime))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_dry_runs")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	require.NoError(t, s.RecordDryRun(context.Background(), &script.DryRun{
		ID: "dpx_draft_1", ScriptID: "script_1", SourceSHA256: script.SourceDigest("x = 1\n"),
		RequestedBy: "jane@example.com", Status: script.RunStatusSucceeded,
		StateWritten: map[string]any{"synced_through": "2026-08-28"},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordDryRun_RefusesStateThatCannotBeJSON(t *testing.T) {
	s, mock := newMock(t)
	err := s.RecordDryRun(context.Background(), &script.DryRun{
		ID: "dpx_draft_1", ScriptID: "script_1", StateWritten: map[string]any{"bad": make(chan int)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal dry-run state")
	require.NoError(t, mock.ExpectationsWereMet(), "nothing reaches the database")
}

func TestLatestDryRun_ReadsTheStateWritten(t *testing.T) {
	s, mock := newMock(t)
	row := dryRunRow(script.RunStatusFailed)
	row[10] = []byte(`{}`)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WillReturnRows(sqlmock.NewRows(dryRunColumnNames).AddRow(row...))

	got, err := s.LatestDryRun(context.Background(), "script_1", script.SourceDigest("x = 1\n"))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, got.StateWritten, "{} is a write the draft would have made; NULL is none")

	s, mock = newMock(t)
	row = dryRunRow(script.RunStatusSucceeded)
	row[10] = []byte(`{not json`)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_dry_runs")).
		WillReturnRows(sqlmock.NewRows(dryRunColumnNames).AddRow(row...))
	_, err = s.LatestDryRun(context.Background(), "script_1", script.SourceDigest("x = 1\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode dry-run state")
}
