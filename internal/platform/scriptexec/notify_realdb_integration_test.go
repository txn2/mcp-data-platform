//go:build integration

package scriptexec

// The real-substrate proof that a failed scheduled run actually reaches
// somebody (#1286). A fake notifier proves the worker CALLS the enqueue path;
// it cannot prove that the call produces a queue row, and every step between
// the two is a place the alert can vanish silently: the category has to be one
// the preference check admits, the recipient's default preferences have to
// allow it, and the payload has to survive the JSONB round trip. This test
// assembles the enqueue side the composition root builds — over a real
// Postgres, through the same newNotifier the production path calls — and looks
// for the row.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestRealDB_AFailedScheduledRunLandsANotificationRow(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	notifier := newNotifier(Config{DB: db})
	require.NotNil(t, notifier, "a deployment with a database builds the enqueue side")

	sc, v, run := executableState()
	run.Trigger = script.TriggerSchedule
	run.ScheduleID = "sched_1"
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(ctx, run))
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: &fakeExecutor{out: attempt{result: script.RunResult{
			Status: script.RunStatusFailed,
			Error:  "Error: division by zero",
			Log:    "querying warehouse\n",
		}}},
		notifier: notifier,
	})

	drainAll(w)

	rows, err := db.QueryContext(ctx, `
		SELECT recipient, category, payload->>'kind', payload->>'item_id', payload->>'message'
		  FROM notifications ORDER BY recipient`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	type queued struct{ recipient, category, kind, itemID, message string }
	var got []queued
	for rows.Next() {
		var q queued
		require.NoError(t, rows.Scan(&q.recipient, &q.category, &q.kind, &q.itemID, &q.message))
		got = append(got, q)
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, 1, "the owner gets the one row")
	q := got[0]
	assert.Equal(t, "jane@example.com", q.recipient, "the owner who can fix it")
	assert.Equal(t, notification.CategoryScriptRun, q.category)
	assert.Equal(t, notification.KindScriptRun, q.kind)
	assert.Equal(t, run.ID, q.itemID, "the row names the run to go and read")
	assert.Contains(t, q.message, "division by zero")
	assert.Contains(t, q.message, "querying warehouse")
}

// TestRealDB_ASuccessfulScheduledRunMailsNobody is the other half of the
// boundary, against the same substrate: the alert exists for the case nobody is
// watching a failure, not for every scheduled run.
func TestRealDB_ASuccessfulScheduledRunMailsNobody(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	sc, v, run := executableState()
	run.Trigger = script.TriggerSchedule
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(ctx, run))
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner:   &fakeExecutor{out: attempt{result: script.RunResult{Status: script.RunStatusSucceeded}}},
		notifier: newNotifier(Config{DB: db}),
	})

	drainAll(w)

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&count))
	assert.Zero(t, count)
}

// TestRealDB_AFailingScheduleMailsOnceAndOnceMoreWhenItRecovers is #2041
// over the real queue and incident tables: two failed scheduled runs land one
// failure row, and the run that succeeds lands one recovered row naming the
// first failed run and the count.
func TestRealDB_AFailingScheduleMailsOnceAndOnceMoreWhenItRecovers(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	sc, v, _ := executableState()
	require.NoError(t, scriptstore.New(db).Create(ctx, sc, script.Author{Email: sc.OwnerEmail}))
	// The run executes the script's current version, which the store numbered.
	v.ScriptID, v.Version = sc.ID, sc.Version
	// The fake script store serves it as runnable; its row only anchors the
	// incident's foreign key.
	sc.Status, sc.Enabled, sc.Library = script.StatusActive, true, false

	runs := &fakeRuns{}
	exec := &fakeExecutor{}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: exec, notifier: newNotifier(Config{DB: db}), incidents: failureIncidents(Config{DB: db}),
	})
	for i, status := range []string{script.RunStatusFailed, script.RunStatusFailed, script.RunStatusSucceeded} {
		exec.out = attempt{result: script.RunResult{Status: status, Error: "Error: upstream timed out"}}
		if status == script.RunStatusSucceeded {
			exec.out.result.Error = ""
		}
		run := &script.Run{ID: "dpx_" + string(rune('1'+i)), ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
			Trigger: script.TriggerSchedule, ScheduleID: "sched_1"}
		require.NoError(t, runs.Enqueue(ctx, run))
		drainAll(w)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT payload->>'item_id', COALESCE(payload->'recovery'->>'failed_runs', ''), COALESCE(payload->'recovery'->>'opening_run_id', '')
		  FROM notifications ORDER BY id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var got [][3]string
	for rows.Next() {
		var r [3]string
		require.NoError(t, rows.Scan(&r[0], &r[1], &r[2]))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, [][3]string{{"dpx_1", "", ""}, {"dpx_3", "2", "dpx_1"}}, got)

	var open int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM script_failure_incidents`).Scan(&open))
	assert.Zero(t, open, "the recovery closed the incident")
}
