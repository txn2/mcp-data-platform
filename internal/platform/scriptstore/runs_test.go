package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// runSelectColumns is the result-set shape a run SELECT mock must return, in
// runColumns order.
var runSelectColumns = []string{
	"id", "script_id", "script_version_id", "version", "trigger_kind", "status",
	"params", "fire_time", "requested_by", "scheduled_for", "started_at", "finished_at", "attempt",
	"locked_until", "locked_by", "error", "log_text", "log_truncated", "metrics", "outputs",
	"schedule_id", "state_revision", "state_read", "state_written", "state_revision_written",
	"created_at", "updated_at",
}

// runRow returns one full run row in runColumns order.
func runRow(status string, attempt int, outputs []byte) []driver.Value { //nolint:unparam // attempt is part of the row shape these cases assert against
	if outputs == nil {
		outputs = []byte("[]")
	}
	return []driver.Value{
		"dpx_1", "script_1", "sver_1", 3, script.TriggerTool, status,
		[]byte(`{"day":"2026-08-12"}`), rowTime, "jane@example.com", rowTime, nil, nil, attempt,
		nil, "worker-a", "", "", false, []byte(`{"steps":10}`), outputs,
		"", int64(0), []byte("{}"), nil, nil, rowTime, rowTime,
	}
}

// enqueueReturning is the column set an enqueue insert hands back.
var enqueueReturning = []string{"fire_time", "scheduled_for", "state_revision", "state_read", "created_at", "updated_at"}

// testLease is the fencing token matching runRow's worker and attempt.
var testLease = script.RunLease{RunID: "dpx_1", Worker: "worker-a", Attempt: 1}

func TestEnqueue_RequiresACallerMintedID(t *testing.T) {
	s, _ := newMock(t)
	err := s.Enqueue(context.Background(), &script.Run{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id minted by its caller",
		"the run id is also the run's session id, so the caller mints it before the row exists")
}

// TestEnqueue_WritesAPendingRowAndWakesAWorker pins both halves of enqueueing:
// the row, and the best-effort notification that saves a caller the poll
// interval.
func TestEnqueue_WritesAPendingRowAndWakesAWorker(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
		WithArgs("dpx_1", "script_1", "sver_1", 3, script.TriggerTool,
			script.RunStatusPending, sqlmock.AnyArg(), "jane@example.com", nil, nil).
		WillReturnRows(sqlmock.NewRows(enqueueReturning).
			AddRow(rowTime, rowTime, int64(0), []byte("{}"), rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).
		WithArgs(NotifyChannel, "dpx_1").WillReturnResult(sqlmock.NewResult(0, 1))

	run := &script.Run{
		ID: "dpx_1", ScriptID: "script_1", VersionID: "sver_1", Version: 3,
		Trigger: script.TriggerTool, RequestedBy: "jane@example.com",
		Params: map[string]any{"day": "2026-08-12"},
	}
	require.NoError(t, s.Enqueue(context.Background(), run))
	assert.Equal(t, script.RunStatusPending, run.Status)
	assert.Equal(t, rowTime, run.ScheduledFor)
	assert.Equal(t, rowTime, run.FireTime)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEnqueue_SurvivesAFailedWakeup pins that the notification is best-effort:
// the worker polls regardless, so a NOTIFY failure must not fail the enqueue.
func TestEnqueue_SurvivesAFailedWakeup(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).
		WillReturnRows(sqlmock.NewRows(enqueueReturning).
			AddRow(rowTime, rowTime, int64(0), []byte("{}"), rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnError(errors.New("no listen privilege"))

	require.NoError(t, s.Enqueue(context.Background(), &script.Run{ID: "dpx_1"}))
}

func TestEnqueue_InsertFailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_runs")).WillReturnError(errors.New("boom"))

	err := s.Enqueue(context.Background(), &script.Run{ID: "dpx_1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enqueue script run")
}

func TestGetRun(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs WHERE id = $1")).
			WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusSucceeded, 1, nil)...))

		run, err := s.GetRun(context.Background(), "dpx_1")
		require.NoError(t, err)
		assert.True(t, run.Terminal())
		assert.Equal(t, map[string]any{"day": "2026-08-12"}, run.Params)
		assert.Equal(t, uint64(10), run.Metrics.Steps)
	})

	t.Run("missing is a typed error", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnRows(sqlmock.NewRows(runSelectColumns))

		_, err := s.GetRun(context.Background(), "nope")
		assert.ErrorIs(t, err, script.ErrRunNotFound)
	})

	t.Run("query failure is wrapped", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnError(errors.New("boom"))

		_, err := s.GetRun(context.Background(), "dpx_1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get script run")
	})
}

func TestListRuns_FiltersAndCaps(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3")).
		WithArgs("script_1", script.RunStatusFailed, 5).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusFailed, 1, nil)...))

	runs, err := s.ListRuns(context.Background(), script.RunFilter{
		ScriptID: "script_1", Status: script.RunStatusFailed, Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A listing across a set of scripts is one query, which is how a caller reads
// the runs of everything they own (#1405).
func TestListRuns_ScopesToASetOfScripts(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = ANY($1) ORDER BY created_at DESC LIMIT $2")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusSucceeded, 1, nil)...))

	runs, err := s.ListRuns(context.Background(), script.RunFilter{
		ScriptIDs: []string{"script_1", "script_2"},
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An empty, non-nil set matches nothing. A caller who owns no script must not
// fall through to a listing across every run on the platform.
func TestListRuns_AnEmptySetIsStillAPredicate(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = ANY($1)")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns))

	runs, err := s.ListRuns(context.Background(), script.RunFilter{ScriptIDs: []string{}})
	require.NoError(t, err)
	assert.Empty(t, runs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListRuns_UnboundedListingTakesTheStoreCap(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("LIMIT $1")).WithArgs(defaultRunListLimit).
		WillReturnRows(sqlmock.NewRows(runSelectColumns))

	runs, err := s.ListRuns(context.Background(), script.RunFilter{Limit: 10_000})
	require.NoError(t, err)
	assert.Empty(t, runs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestLatestRuns_OnePerScriptInOneQuery pins the listing read: a row per
// script, keyed by script, taken from one statement rather than one per row.
func TestLatestRuns_OnePerScriptInOneQuery(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (script_id)")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusFailed, 1, nil)...))

	latest, err := s.LatestRuns(context.Background(), []string{"script_1", "script_2"})
	require.NoError(t, err)
	require.Len(t, latest, 1)
	assert.Equal(t, script.RunStatusFailed, latest["script_1"].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Asking about no scripts asks the database nothing. A caller who owns nothing
// must not fall through to an unfiltered read.
func TestLatestRuns_NoScriptsIsNoQuery(t *testing.T) {
	s, mock := newMock(t)
	latest, err := s.LatestRuns(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, latest)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLatestRuns_QueryFailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (script_id)")).WillReturnError(errors.New("boom"))

	_, err := s.LatestRuns(context.Background(), []string{"script_1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list latest script runs")
}

func TestLatestRuns_ScanFailureIsReported(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT ON (script_id)")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusSucceeded, 1, []byte("not json"))...))

	_, err := s.LatestRuns(context.Background(), []string{"script_1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal run outputs")
}

func TestListRuns_QueryFailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).WillReturnError(errors.New("boom"))

	_, err := s.ListRuns(context.Background(), script.RunFilter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list script runs")
}

// TestClaim_TakesTheRunAndStampsTheLease pins the claim: one statement that
// marks the row running, counts the attempt, and takes the lease the later
// writes are fenced on.
func TestClaim_TakesTheRunAndStampsTheLease(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE script_runs")).
		WithArgs("worker-a", 900).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(runRow(script.RunStatusRunning, 1, nil)...))

	run, err := s.Claim(context.Background(), "worker-a", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusRunning, run.Status)
	assert.Equal(t, script.RunLease{RunID: "dpx_1", Worker: "worker-a", Attempt: 1}, run.Lease())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaim_NoDueRunIsNotAFailure(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE script_runs")).WillReturnRows(sqlmock.NewRows(runSelectColumns))

	_, err := s.Claim(context.Background(), "worker-a", time.Minute)
	assert.ErrorIs(t, err, script.ErrNoWork)
}

func TestClaim_FailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE script_runs")).WillReturnError(errors.New("boom"))

	_, err := s.Claim(context.Background(), "worker-a", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claim script run")
}

// TestLeaseFencing is the property that makes reclaim safe: every write carries
// the lease it was taken under, and a worker whose run was reclaimed matches no
// row and is told so rather than overwriting the new holder's work.
func TestLeaseFencing(t *testing.T) {
	writes := []struct {
		name    string
		query   string
		call    func(*Store) error
		wantErr string
	}{
		{"record output", "UPDATE script_runs SET outputs", func(s *Store) error {
			return s.RecordOutput(context.Background(), testLease, script.RunOutput{Name: "daily"})
		}, "record script run output"},
		{"finish", "UPDATE script_runs", func(s *Store) error {
			return s.Finish(context.Background(), testLease, script.RunResult{Status: script.RunStatusSucceeded})
		}, "finish script run"},
		{"retry", "UPDATE script_runs", func(s *Store) error {
			return s.Retry(context.Background(), testLease, "trino unreachable", time.Minute)
		}, "retry script run"},
	}
	for _, w := range writes {
		t.Run(w.name+" refuses a lost lease", func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectExec(regexp.QuoteMeta(w.query)).WillReturnResult(sqlmock.NewResult(0, 0))

			err := w.call(s)
			require.Error(t, err)
			assert.ErrorIs(t, err, script.ErrLeaseLost)
		})

		t.Run(w.name+" wraps a store failure", func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectExec(regexp.QuoteMeta(w.query)).WillReturnError(errors.New("boom"))

			err := w.call(s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), w.wantErr)
		})
	}
}

// TestRecordOutput_AppendsInSQL pins that the output list grows with a JSONB
// append rather than a read-modify-write, so two writes cannot lose one another.
func TestRecordOutput_AppendsInSQL(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("SET outputs = outputs || $4::jsonb")).
		WithArgs("dpx_1", "worker-a", 1, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, s.RecordOutput(context.Background(), testLease,
		script.RunOutput{Name: "daily", AssetID: "asset_1", AssetVersion: 2}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFinish_WritesTheResultAndWakesWaiters pins that finishing a run notifies,
// which is what lets anything watching the run see it end promptly.
func TestFinish_WritesTheResultAndWakesWaiters(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_runs")).
		WithArgs("dpx_1", "worker-a", 1, script.RunStatusFailed, "boom", "log line", false, sqlmock.AnyArg(), nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).
		WithArgs(NotifyChannel, "dpx_1").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, s.Finish(context.Background(), testLease, script.RunResult{
		Status: script.RunStatusFailed, Error: "boom", Log: "log line",
		Metrics: script.RunMetrics{Steps: 12},
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPurgeRuns_OnlySweepsTerminalRows pins the retention predicate: live work
// is never swept, however old the row is, and a schedule's skipped fires age
// out on the same clock as the runs that did execute.
func TestPurgeRuns_OnlySweepsTerminalRows(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("WHERE status IN ('succeeded', 'failed', 'skipped_overlap')")).
		WithArgs(86400).WillReturnResult(sqlmock.NewResult(0, 7))

	n, err := s.PurgeRuns(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeRuns_FailureIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_runs")).WillReturnError(errors.New("boom"))

	_, err := s.PurgeRuns(context.Background(), time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "purge script runs")
}

// TestScanRun_MalformedJSONIsReported covers each JSONB column's decode path,
// so a corrupt row names the column rather than failing opaquely.
func TestScanRun_MalformedJSONIsReported(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]driver.Value)
		wantErr string
	}{
		{"params", func(row []driver.Value) { row[6] = []byte("{not json") }, "unmarshal run params"},
		{"metrics", func(row []driver.Value) { row[18] = []byte("{not json") }, "unmarshal run metrics"},
		{"outputs", func(row []driver.Value) { row[19] = []byte("{not json") }, "unmarshal run outputs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, mock := newMock(t)
			row := runRow(script.RunStatusSucceeded, 1, nil)
			tt.mutate(row)
			mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
				WillReturnRows(sqlmock.NewRows(runSelectColumns).AddRow(row...))

			_, err := s.GetRun(context.Background(), "dpx_1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestRunColumnsMatchTheScanOrder pins the one drift this store cannot survive:
// a column added to the SELECT list but not to the scan, which would read every
// later field one position off.
func TestRunColumnsMatchTheScanOrder(t *testing.T) {
	assert.Len(t, splitTopLevel(runColumns), len(runSelectColumns),
		"runColumns and the scan order in scanRun must list the same columns")
}
