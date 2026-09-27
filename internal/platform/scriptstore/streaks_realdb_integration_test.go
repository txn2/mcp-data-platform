//go:build integration

package scriptstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_FailureStreaks reads each script's recent finished runs, newest
// first (#1934, #1935): consecutive failures stop at a success, the same-error
// count stops at a failure that ended differently, the last success is found
// even outside the window, and a script with no finished run is absent.
func TestRealDB_FailureStreaks(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	failing, fv := savedScript(ctx, t, s, "failing")
	healthy, hv := savedScript(ctx, t, s, "healthy")
	idle, _ := savedScript(ctx, t, s, "idle")

	base := time.Now().UTC().Add(-time.Hour)
	run := func(sc *script.Script, v *script.Version, n int, status, errText string) {
		id := fmt.Sprintf("dpx_%s_%02d", sc.Name, n)
		require.NoError(t, s.Enqueue(ctx, &script.Run{ID: id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version, Trigger: script.TriggerSchedule}))
		at := base.Add(time.Duration(n) * time.Minute)
		_, err := db.ExecContext(ctx, `UPDATE script_runs SET status = $2, error = $3, created_at = $4, finished_at = $4 WHERE id = $1`,
			id, status, errText, at)
		require.NoError(t, err)
	}
	trace := func(last string) string {
		return "Traceback (most recent call last):\n  failing:3:5: in <toplevel>\n" + last + "\n"
	}

	// Oldest to newest: a success, a different failure, then three failures
	// ending the same way.
	run(failing, fv, 1, script.RunStatusSucceeded, "")
	run(failing, fv, 2, script.RunStatusFailed, trace("Error in fail: fail: bad row"))
	for n := 3; n <= 5; n++ {
		run(failing, fv, n, script.RunStatusFailed, trace("Error in fail: fail: NWS returned 500"))
	}
	run(healthy, hv, 1, script.RunStatusFailed, trace("Error: x"))
	run(healthy, hv, 2, script.RunStatusSucceeded, "")

	streaks, err := s.FailureStreaks(ctx, []string{failing.ID, healthy.ID, idle.ID})
	require.NoError(t, err)
	require.NotContains(t, streaks, idle.ID, "a script with no finished run contributes no row")

	f := streaks[failing.ID]
	assert.Equal(t, 4, f.Failed, "four failures since the success")
	assert.Equal(t, 3, f.SameError, "the newest three ended the same way")
	assert.Equal(t, "Error in fail: fail: NWS returned 500", f.LastError)
	assert.Equal(t, "dpx_failing_05", f.LastFailedRunID)
	require.NotNil(t, f.LastFailedAt)
	require.NotNil(t, f.LastSuccessAt)
	assert.WithinDuration(t, base.Add(time.Minute), *f.LastSuccessAt, time.Second)

	h := streaks[healthy.ID]
	assert.Zero(t, h.Failed, "the newest finished run succeeded")
	require.NotNil(t, h.LastSuccessAt)
}
