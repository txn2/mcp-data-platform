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

	w.drain()

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

	w.drain()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&count))
	assert.Zero(t, count)
}
