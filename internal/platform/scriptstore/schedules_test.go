package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// scheduleSelectColumns is the result-set shape a schedule SELECT mock must
// return, in scheduleColumns order.
var scheduleSelectColumns = []string{
	"id", "script_id", "cron_spec", "timezone", "params", "enabled",
	"next_run_at", "last_fire_at", "missed_fires", "created_by", "updated_by",
	"created_at", "updated_at",
}

// scheduleRow returns one full schedule row in scheduleColumns order.
func scheduleRow(nextRunAt any) []driver.Value {
	return []driver.Value{
		"sched_1", "script_1", "0 7 * * 1-5", "America/Los_Angeles",
		[]byte(`{"report_date":"${fire_date}"}`), true,
		nextRunAt, nil, 0, "jane@example.com", "jane@example.com",
		rowTime, rowTime,
	}
}

// TestScheduleColumnsMatchTheScanOrder pins the drift this store cannot
// survive: a column in the SELECT list that the scan does not read, which
// silently shifts every later field.
func TestScheduleColumnsMatchTheScanOrder(t *testing.T) {
	assert.Len(t, splitTopLevel(scheduleColumns), len(scheduleSelectColumns),
		"scheduleColumns and the scan order in scanSchedule must list the same columns")
}

func TestSetSchedule_UpsertsOnTheScript(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (script_id) DO UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("sched_1", rowTime, rowTime))

	sched := &script.Schedule{
		ScriptID: "script_1", CronSpec: "@daily", Timezone: "UTC",
		Enabled: true, NextRunAt: rowTime, UpdatedBy: "jane@example.com",
	}
	require.NoError(t, s.SetSchedule(context.Background(), sched))
	assert.Equal(t, "sched_1", sched.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSetSchedule_AZeroNextFireBindsNull pins the parked case: an expression
// with no further fire stores NULL rather than the zero time, which the due
// predicate would otherwise read as perpetually overdue.
func TestSetSchedule_AZeroNextFireBindsNull(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_schedules")).
		WithArgs("script_1", "@daily", "UTC", []byte(`{}`), true, nil, "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("sched_1", rowTime, rowTime))

	require.NoError(t, s.SetSchedule(context.Background(), &script.Schedule{
		ScriptID: "script_1", CronSpec: "@daily", Timezone: "UTC", Enabled: true,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSchedule(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules WHERE script_id = $1")).
			WithArgs("script_1").
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))

		sched, err := s.GetSchedule(context.Background(), "script_1")
		require.NoError(t, err)
		assert.Equal(t, "0 7 * * 1-5", sched.CronSpec)
		assert.Equal(t, map[string]any{"report_date": script.FireDateToken}, sched.Params)
		assert.Equal(t, rowTime, sched.NextRunAt)
	})

	t.Run("a null next fire reads as parked, not as the zero time", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(nil)...))

		sched, err := s.GetSchedule(context.Background(), "script_1")
		require.NoError(t, err)
		assert.True(t, sched.NextRunAt.IsZero())
	})

	t.Run("missing is a typed error", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns))

		_, err := s.GetSchedule(context.Background(), "nope")
		assert.ErrorIs(t, err, script.ErrScheduleNotFound)
	})

	t.Run("query failure is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).WillReturnError(errors.New("boom"))

		_, err := s.GetSchedule(context.Background(), "script_1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get script schedule")
	})

	t.Run("malformed params are reported", func(t *testing.T) {
		s, mock := newMock(t)
		row := scheduleRow(rowTime)
		row[4] = []byte("{not json")
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(row...))

		_, err := s.GetSchedule(context.Background(), "script_1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal schedule params")
	})
}

func TestListSchedules_FiltersAndCaps(t *testing.T) {
	s, mock := newMock(t)
	enabled := true
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 AND enabled = $2")).
		WithArgs("script_1", true, defaultScheduleListLimit).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))

	out, err := s.ListSchedules(context.Background(), script.ScheduleFilter{
		ScriptID: "script_1", Enabled: &enabled, Limit: 10000,
	})
	require.NoError(t, err)
	assert.Len(t, out, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetScheduleEnabled(t *testing.T) {
	t.Run("applies", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).
			WithArgs("script_1", false, "jane@example.com").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, s.SetScheduleEnabled(context.Background(), "script_1", false, "jane@example.com"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no schedule is a typed error, not a silent success", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := s.SetScheduleEnabled(context.Background(), "nope", true, "jane@example.com")
		assert.ErrorIs(t, err, script.ErrScheduleNotFound)
	})
}

func TestDueSchedules_ExcludesDisabledAndParked(t *testing.T) {
	s, mock := newMock(t)
	now := rowTime
	mock.ExpectQuery(regexp.QuoteMeta("WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1")).
		WithArgs(now.UTC(), defaultDueLimit).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))

	out, err := s.DueSchedules(context.Background(), now, 0)
	require.NoError(t, err)
	assert.Len(t, out, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// materializing is the run one fire produces.
func materializing() *script.Run {
	return &script.Run{
		ID: "dpx_1", ScriptID: "script_1", VersionID: "sver_1", Version: 3,
		ScheduleID: "sched_1", Trigger: script.TriggerSchedule,
		FireTime: rowTime, ScheduledFor: rowTime,
	}
}

func TestMaterializeRun(t *testing.T) {
	t.Run("the insert lands and wakes a worker", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(rowTime, rowTime))
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).
			WithArgs(NotifyChannel, "dpx_1").WillReturnResult(sqlmock.NewResult(0, 1))

		run := materializing()
		outcome, err := s.MaterializeRun(context.Background(), run)
		require.NoError(t, err)
		assert.Equal(t, script.MaterializedRun, outcome)
		assert.Equal(t, script.RunStatusPending, run.Status)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a conflict on an existing fire is another replica, not a fault", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		outcome, err := s.MaterializeRun(context.Background(), materializing())
		require.NoError(t, err)
		assert.Equal(t, script.MaterializedDuplicate, outcome)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a conflict with no row for the fire is an overlap, recorded as a run", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(rowTime, rowTime))

		run := materializing()
		outcome, err := s.MaterializeRun(context.Background(), run)
		require.NoError(t, err)
		assert.Equal(t, script.MaterializedSkippedOverlap, outcome)
		assert.Equal(t, script.RunStatusSkippedOverlap, run.Status)
		assert.True(t, run.Terminal(), "a skip is finished on arrival; nothing will ever claim it")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("losing the race to record the skip reports the duplicate", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}))

		outcome, err := s.MaterializeRun(context.Background(), materializing())
		require.NoError(t, err)
		assert.Equal(t, script.MaterializedDuplicate, outcome,
			"a caller must not claim a skip it did not record")
	})

	t.Run("an insert failure is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errors.New("boom"))

		_, err := s.MaterializeRun(context.Background(), materializing())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "materialize scheduled run")
	})

	t.Run("a failed fire lookup is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).WillReturnError(errors.New("boom"))

		_, err := s.MaterializeRun(context.Background(), materializing())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checking a materialized fire")
	})
}

func TestAdvanceSchedule(t *testing.T) {
	next := rowTime.Add(time.Hour)

	t.Run("moves the row it found", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).
			WithArgs("sched_1", rowTime, next, rowTime, 2).
			WillReturnResult(sqlmock.NewResult(0, 1))

		moved, err := s.AdvanceSchedule(context.Background(), script.ScheduleAdvance{
			ID: "sched_1", From: rowTime, Next: next, Fired: rowTime, Missed: 2,
		})
		require.NoError(t, err)
		assert.True(t, moved)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("losing the race moves nothing and is not an error", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		moved, err := s.AdvanceSchedule(context.Background(), script.ScheduleAdvance{ID: "sched_1", From: rowTime})
		require.NoError(t, err, "the unique index is the guarantee; this is bookkeeping")
		assert.False(t, moved)
	})

	t.Run("a parked schedule stores a null next fire", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).
			WithArgs("sched_1", rowTime, nil, nil, 0).
			WillReturnResult(sqlmock.NewResult(0, 1))

		_, err := s.AdvanceSchedule(context.Background(), script.ScheduleAdvance{ID: "sched_1", From: rowTime})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("failure is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).WillReturnError(errors.New("boom"))

		_, err := s.AdvanceSchedule(context.Background(), script.ScheduleAdvance{ID: "sched_1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "advance script schedule")
	})
}

// TestSetSchedule_Failures covers the two ways the write can refuse: a binding
// that cannot be encoded, and a database that says no.
func TestSetSchedule_Failures(t *testing.T) {
	t.Run("unencodable params", func(t *testing.T) {
		s, _ := newMock(t)
		err := s.SetSchedule(context.Background(), &script.Schedule{
			ScriptID: "script_1", Params: map[string]any{"bad": make(chan int)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal schedule params")
	})

	t.Run("the write failed", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_schedules")).WillReturnError(errors.New("boom"))
		err := s.SetSchedule(context.Background(), &script.Schedule{ScriptID: "script_1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "set script schedule")
	})
}

// TestSetScheduleEnabled_FailureIsWrapped pins that a store fault is not
// mistaken for "there is no schedule".
func TestSetScheduleEnabled_FailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_schedules")).WillReturnError(errors.New("boom"))
	err := s.SetScheduleEnabled(context.Background(), "script_1", true, "jane@example.com")
	require.Error(t, err)
	assert.NotErrorIs(t, err, script.ErrScheduleNotFound)
	assert.Contains(t, err.Error(), "set script schedule enabled")
}

// TestListSchedules_Failures covers the three ways a listing can go wrong: the
// query, a row that will not scan, and an iteration that ends badly.
func TestListSchedules_Failures(t *testing.T) {
	t.Run("the query failed", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).WillReturnError(errors.New("boom"))
		_, err := s.ListSchedules(context.Background(), script.ScheduleFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list script schedules")
	})

	t.Run("a row will not scan", func(t *testing.T) {
		s, mock := newMock(t)
		row := scheduleRow(rowTime)
		row[4] = []byte("{not json")
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(row...))
		_, err := s.ListSchedules(context.Background(), script.ScheduleFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal schedule params")
	})

	t.Run("the iteration failed", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).
				AddRow(scheduleRow(rowTime)...).RowError(0, errors.New("boom")))
		_, err := s.ListSchedules(context.Background(), script.ScheduleFilter{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iterate script schedules")
	})
}

// TestMaterializeRun_UnencodableParams pins that a binding that cannot be
// encoded fails the fire rather than writing a row without its parameters.
func TestMaterializeRun_UnencodableParams(t *testing.T) {
	s, _ := newMock(t)
	run := materializing()
	run.Params = map[string]any{"bad": make(chan int)}
	_, err := s.MaterializeRun(context.Background(), run)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal run params")
}

// TestListSchedules_ScopesToASetOfScripts pins the visibility scope, including
// the distinction that makes it safe: a nil slice is "no scope", while an empty
// one matches nothing — so a caller who may see no scripts sees no schedules
// rather than all of them.
func TestListSchedules_ScopesToASetOfScripts(t *testing.T) {
	t.Run("a set of ids", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = ANY($1)")).
			WithArgs(pq.Array([]string{"script_1", "script_2"}), defaultScheduleListLimit).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))

		out, err := s.ListSchedules(context.Background(), script.ScheduleFilter{
			ScriptIDs: []string{"script_1", "script_2"},
		})
		require.NoError(t, err)
		assert.Len(t, out, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("an empty set still filters", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = ANY($1)")).
			WithArgs(pq.Array([]string{}), defaultScheduleListLimit).
			WillReturnRows(sqlmock.NewRows(scheduleSelectColumns))

		out, err := s.ListSchedules(context.Background(), script.ScheduleFilter{ScriptIDs: []string{}})
		require.NoError(t, err)
		assert.Empty(t, out)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
