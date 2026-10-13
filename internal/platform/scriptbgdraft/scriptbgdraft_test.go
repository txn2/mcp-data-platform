package scriptbgdraft

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
)

func TestConfigBounds(t *testing.T) {
	assert.NoError(t, Config{}.Validate())
	assert.NoError(t, Config{MaxSeconds: 900}.Validate())
	assert.Error(t, Config{MaxSeconds: 30}.Validate(), "under the default")
	assert.Error(t, Config{MaxSeconds: 3601}.Validate())
	assert.Equal(t, 60*time.Second, Config{}.Max())
	assert.Equal(t, 15*time.Minute, Config{MaxSeconds: 900}.Max())
}

// TestDecide is #2117's rule: anyone runs a draft of up to 60 seconds inline,
// and an administrator may ask for up to the configured cap, past 60 in the
// background.
func TestDecide(t *testing.T) {
	cfg := Config{MaxSeconds: 900}
	cases := []struct {
		name       string
		seconds    int
		admin      bool
		limit      time.Duration
		background bool
		refused    string
	}{
		{"the default", 0, false, 60 * time.Second, false, ""},
		{"shorter, inline", 30, false, 30 * time.Second, false, ""},
		{"ten minutes for an administrator", 600, true, 10 * time.Minute, true, ""},
		{"past 60 for someone else", 61, false, 0, false, "administrator's to run"},
		{"past the cap", 901, true, 0, false, "at most 900"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limit, background, err := cfg.Decide(c.seconds, c.admin)
			if c.refused != "" {
				require.ErrorIs(t, err, ErrTooLong)
				assert.Contains(t, err.Error(), c.refused)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.limit, limit)
			assert.Equal(t, c.background, background)
		})
	}
	_, _, err := Config{}.Decide(120, true)
	assert.ErrorContains(t, err, MaxSecondsKey, "a deployment that set no cap allows no long draft")
}

var draftCols = []string{
	"id", "owner_email", "script_name", "status", "log", "progress", "error", "draft",
	"started_at", "deadline", "finished_at", "reported_at",
}

// TestABackgroundDraftRunsUnderItsID is #2117's criterion: an
// administrator's long draft answers its run id at once, runs under that id
// for its whole limit, and once it ends its log and what run_draft would have
// answered are recorded for get_run.
func TestABackgroundDraftRunsUnderItsID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launcher := NewLauncher(NewStore(db), Config{})
	launcher.every = time.Hour

	mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO script_draft_runs").
		WithArgs(sqlmock.AnyArg(), "admin@example.com", "flow", StatusRunning, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE script_draft_runs\\s+SET status").
		WithArgs(sqlmock.AnyArg(), StatusSucceeded, "logged in\n", nil, "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	release := make(chan struct{})
	ran := make(chan scriptdraft.Long, 1)
	out, err := launcher.Begin(context.Background(), "admin@example.com", "flow", 10*time.Minute,
		func(_ context.Context, long scriptdraft.Long) (map[string]any, bool, error) {
			long.Live.Print("logged in")
			<-release
			ran <- long
			return map[string]any{"status": "succeeded", "recording": "dpx_rec"}, false, nil
		})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, out["status"], "answered while the draft still runs")
	assert.Equal(t, 600, out["timeout_seconds"])
	close(release)
	got := <-ran
	assert.Equal(t, out["run_id"], got.RunID, "the draft runs under the id run_draft answered")
	assert.Equal(t, 10*time.Minute, got.Timeout)
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 5*time.Second, 5*time.Millisecond)
}

// TestAReportWritesTheLogAndProgress: what a running draft has printed and
// reported is what get_run reads before it ends.
func TestAReportWritesTheLogAndProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launcher := NewLauncher(NewStore(db), Config{})
	live := scriptlive.New(1024, 1024)
	live.Print("page 1")
	mock.ExpectExec("UPDATE script_draft_runs SET log").WithArgs("dpx_1", "page 1\n", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, launcher.report(context.Background(), "dpx_1", live))
	mock.ExpectExec("UPDATE script_draft_runs SET log").WillReturnError(errors.New("down"))
	assert.Error(t, launcher.report(context.Background(), "dpx_1", live))
}

func rows(status string, deadline time.Time, finished any) *sqlmock.Rows {
	return sqlmock.NewRows(draftCols).AddRow("dpx_1", "jane@example.com", "flow", status, "step 3", []byte(`{"label":"page 3"}`), "",
		[]byte(`{"status":"succeeded"}`), time.Now(), deadline, finished, time.Now())
}

func TestAnswer(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launcher := NewLauncher(NewStore(db), Config{})
	future := time.Now().Add(time.Hour)

	mock.ExpectQuery("FROM script_draft_runs WHERE id").WithArgs("dpx_1").
		WillReturnRows(rows(StatusSucceeded, future, time.Now()))
	out, ok, err := launcher.Answer(context.Background(), "dpx_1", "jane@example.com", false)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "draft", out["kind"])
	assert.Equal(t, StatusSucceeded, out["status"])
	assert.Equal(t, "step 3", out["log"])
	assert.Equal(t, map[string]any{"status": "succeeded"}, out["draft"])

	mock.ExpectQuery("FROM script_draft_runs").WillReturnRows(rows(StatusRunning, future, nil))
	_, ok, err = launcher.Answer(context.Background(), "dpx_1", "bob@example.com", false)
	assert.True(t, ok)
	assert.EqualError(t, err, "run not found", "another person's draft is not there")

	mock.ExpectQuery("FROM script_draft_runs").WillReturnRows(rows(StatusRunning, future, nil))
	out, _, err = launcher.Answer(context.Background(), "dpx_1", "admin@example.com", true)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, out["status"], "an administrator reads any draft")

	mock.ExpectQuery("FROM script_draft_runs").WillReturnRows(rows(StatusRunning,
		time.Now().Add(-2*time.Hour), nil))
	out, _, err = launcher.Answer(context.Background(), "dpx_1", "jane@example.com", false)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, out["status"], "a draft past its deadline whose replica never wrote its end")

	mock.ExpectQuery("FROM script_draft_runs").WillReturnRows(sqlmock.NewRows(draftCols))
	_, ok, err = launcher.Answer(context.Background(), "run_123", "jane@example.com", false)
	assert.False(t, ok, "not a draft: get_run reads its runs")
	assert.NoError(t, err)

	mock.ExpectQuery("FROM script_draft_runs").WillReturnError(errors.New("down"))
	_, ok, err = launcher.Answer(context.Background(), "dpx_1", "jane@example.com", false)
	assert.True(t, ok)
	assert.EqualError(t, err, "failed to read the run")

	var none *Launcher
	_, ok, _ = none.Answer(context.Background(), "dpx_1", "jane@example.com", false)
	assert.False(t, ok)
	_, err = none.Begin(context.Background(), "o", "s", time.Minute, nil)
	assert.Error(t, err)
	assert.Nil(t, NewLauncher(nil, Config{}))
	assert.Nil(t, NewStore(nil))
}

func TestAFailedDraftIsRecordedAsFailed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	launcher := NewLauncher(NewStore(db), Config{})
	launcher.every = time.Hour
	done := make(chan struct{})
	mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO script_draft_runs").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE script_draft_runs\\s+SET status").
		WithArgs(sqlmock.AnyArg(), StatusFailed, "", nil, "no session", nil).
		WillReturnResult(driver.RowsAffected(1))
	_, err = launcher.Begin(context.Background(), "o", "s", time.Minute,
		func(context.Context, scriptdraft.Long) (map[string]any, bool, error) {
			defer close(done)
			return nil, false, errors.New("no session")
		})
	require.NoError(t, err)
	<-done
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, time.Second, 5*time.Millisecond)

	mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnError(errors.New("down"))
	_, err = launcher.Begin(context.Background(), "o", "s", time.Minute, nil)
	assert.Error(t, err, "a draft that could not be recorded is not started")
}

func TestANilLauncherPlansTheDefault(t *testing.T) {
	var none *Launcher
	limit, background, err := none.Plan(0, true)
	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, limit)
	assert.False(t, background)
	assert.Equal(t, 60*time.Second, none.Max())
	l := NewLauncher(&Store{}, Config{MaxSeconds: 600})
	assert.Equal(t, 10*time.Minute, l.Max())
}

// TestProgressJSONIsWhatTheDraftLastReported: nothing reported writes no
// progress, and a report is written as get_run reads it.
func TestProgressJSONIsWhatTheDraftLastReported(t *testing.T) {
	live := scriptlive.New(1024, 1024)
	assert.Nil(t, progressJSON(live))
	_, err := starlark.Call(&starlark.Thread{}, live.Bindings()["progress"], starlark.Tuple{starlark.String("page 3")}, nil)
	require.NoError(t, err)
	assert.Contains(t, string(progressJSON(live)), `"message":"page 3"`)
}

// TestAnEndThatCannotBeRecordedIsLogged: a store failure writing how the draft
// ended is logged rather than lost silently, and the draft still ends.
func TestAnEndThatCannotBeRecordedIsLogged(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launcher := NewLauncher(NewStore(db), Config{})
	launcher.every = time.Hour
	done := make(chan struct{})
	mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO script_draft_runs").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE script_draft_runs\\s+SET status").WillReturnError(errors.New("down"))
	_, err = launcher.Begin(context.Background(), "o", "s", time.Minute,
		func(context.Context, scriptdraft.Long) (map[string]any, bool, error) {
			defer close(done)
			return map[string]any{"status": "succeeded"}, false, nil
		})
	require.NoError(t, err)
	<-done
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, time.Second, 5*time.Millisecond)
}

// TestADraftWhoseReplicaStoppedReadsAsFailed: a running draft its replica has
// not written for a minute reads as failed, long before its deadline.
func TestADraftWhoseReplicaStoppedReadsAsFailed(t *testing.T) {
	now := time.Now()
	run := &Run{Deadline: now.Add(time.Hour), ReportedAt: now.Add(-2 * time.Minute)}
	assert.True(t, stopped(run, now))
	run.ReportedAt = now.Add(-5 * time.Second)
	assert.False(t, stopped(run, now), "written seconds ago, it is running")
	run.Deadline = now.Add(-2 * time.Minute)
	assert.True(t, stopped(run, now), "past its deadline")
}

// TestAReplicaRunsAtMostMaxBackgroundDrafts: the draft past the cap is
// refused before anything is recorded, and a slot frees when a draft ends.
func TestAReplicaRunsAtMostMaxBackgroundDrafts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	launcher := NewLauncher(NewStore(db), Config{})
	launcher.every = time.Hour
	release := make(chan struct{})
	for range MaxBackground {
		mock.ExpectExec("DELETE FROM script_draft_runs").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO script_draft_runs").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE script_draft_runs\\s+SET status").WillReturnResult(sqlmock.NewResult(0, 1))
		_, err := launcher.Start(context.Background(), "o", "s", time.Minute,
			func(context.Context, scriptdraft.Long) (map[string]any, bool, error) {
				<-release
				return nil, false, nil
			})
		require.NoError(t, err)
	}
	_, err = launcher.Start(context.Background(), "o", "s", time.Minute, nil)
	require.ErrorIs(t, err, ErrBusy)
	close(release)
	require.Eventually(t, func() bool { return launcher.active.Load() == 0 }, 5*time.Second, 5*time.Millisecond)
}
