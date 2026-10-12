package scriptexec

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptincident"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memIncidents is script_failure_incidents in memory, with the store's
// contract: Open reports whether it opened, Close returns and forgets.
type memIncidents struct {
	mu       sync.Mutex
	open     map[string]*scriptincident.Incident
	openErr  error
	closeErr error
}

func (m *memIncidents) Open(_ context.Context, o scriptincident.Opening) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openErr != nil {
		return false, m.openErr
	}
	if in, ok := m.open[o.ScriptID]; ok {
		in.FailedRuns++
		in.LastFailedAt = o.FailedAt
		return false, nil
	}
	if m.open == nil {
		m.open = map[string]*scriptincident.Incident{}
	}
	m.open[o.ScriptID] = &scriptincident.Incident{
		OpenedAt: o.FailedAt, OpeningRunID: o.RunID, OpeningCause: o.Cause, OpeningError: o.Error,
		FailedRuns: 1, LastFailedAt: o.FailedAt,
	}
	return true, nil
}

func (m *memIncidents) Close(_ context.Context, scriptID string) (*scriptincident.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	in := m.open[scriptID]
	delete(m.open, scriptID)
	return in, nil
}

// incidentWorker raises alerts through an incident store.
func incidentWorker(runs *fakeRuns, inc scriptincident.Store) (*worker, *fakeNotifier, *fakePoster) {
	n, p := &fakeNotifier{}, &fakePoster{}
	return newWorker(workerConfig{runs: runs, notifier: n, channels: p, incidents: inc, portalURL: "https://portal.example.com"}), n, p
}

// finish is the run f's script finishing with status, as run id.
func (f failedRun) finish(id, trigger, status string) (*script.Run, script.RunResult) {
	run := *f.run
	run.ID, run.Trigger = id, trigger
	res := f.result
	res.Status = status
	if status != script.RunStatusFailed {
		res.Error, res.Cause = "", ""
	}
	return &run, res
}

// TestIncident_OneFailureEmailOneRecoveryEmail is #2041: a script that keeps
// failing tells its owner once, and the run that succeeds tells them once
// more, naming the failed run that began it, how many failed and since when.
func TestIncident_OneFailureEmailOneRecoveryEmail(t *testing.T) {
	f := failedScheduledRun()
	f.result.Cause = runstate.CauseUpstream
	w, n, p := incidentWorker(&fakeRuns{}, &memIncidents{})
	ctx := context.Background()

	for _, id := range []string{"run-1", "run-2", "run-3"} {
		run, res := f.finish(id, script.TriggerSchedule, script.RunStatusFailed)
		w.notifyOutcome(ctx, run, f.script, res)
	}
	require.Len(t, n.payloads, 1, "three failed runs, one email")
	assert.Equal(t, "run-1", n.payloads[0].ItemID)
	assert.Nil(t, n.payloads[0].Recovery)
	assert.Equal(t, []string{"script:" + f.script.ID}, p.keys)

	run, res := f.finish("run-4", script.TriggerSchedule, script.RunStatusSucceeded)
	w.notifyOutcome(ctx, run, f.script, res)
	require.Len(t, n.payloads, 2, "the recovery is the second email")
	rec := n.payloads[1]
	assert.Equal(t, "run-4", rec.ItemID)
	assert.Equal(t, notification.KindScriptRun, rec.Kind)
	require.NotNil(t, rec.Recovery)
	assert.Equal(t, 3, rec.Recovery.FailedRuns)
	assert.Equal(t, "run-1", rec.Recovery.OpeningRunID)
	assert.Equal(t, runstate.CauseUpstream, rec.Recovery.OpeningCause)
	assert.Equal(t, "Error: division by zero", rec.Recovery.OpeningError, "the line of the opening run's error that names the failure")
	assert.Equal(t, "https://portal.example.com/portal/automations/"+f.script.ID+"/runs/run-1", rec.Recovery.OpeningLink)
	assert.Equal(t, "https://portal.example.com/portal/automations/"+f.script.ID+"/runs/run-4", rec.Link)
	assert.False(t, rec.Recovery.RecoveredAt.Before(rec.Recovery.FailingSince))
	assert.Equal(t, []string{"script:" + f.script.ID, "script:" + f.script.ID + ":recovered"}, p.keys,
		"the recovery posts under its own key, so the failure's repeat window cannot hold it back")

	run, res = f.finish("run-5", script.TriggerSchedule, script.RunStatusSucceeded)
	w.notifyOutcome(ctx, run, f.script, res)
	assert.Len(t, n.payloads, 2, "a success with no incident open tells nobody")

	run, res = f.finish("run-6", script.TriggerSchedule, script.RunStatusFailed)
	w.notifyOutcome(ctx, run, f.script, res)
	require.Len(t, n.payloads, 3, "the next failure opens a new incident and is told")
	assert.Equal(t, "run-6", n.payloads[2].ItemID)
}

// TestIncident_WhatNeitherOpensNorCloses: a canceled run and a failed run
// somebody asked for leave the incident as it was; a run somebody asked for
// that succeeds closes it, as it ends the failure the session notice reports.
func TestIncident_WhatNeitherOpensNorCloses(t *testing.T) {
	f := failedScheduledRun()
	inc := &memIncidents{}
	w, n, _ := incidentWorker(&fakeRuns{}, inc)
	ctx := context.Background()

	run, res := f.finish("tool-fail", script.TriggerTool, script.RunStatusFailed)
	w.notifyOutcome(ctx, run, f.script, res)
	assert.Empty(t, n.payloads)
	assert.Empty(t, inc.open, "a run somebody asked for opens no incident")

	run, res = f.finish("run-1", script.TriggerSchedule, script.RunStatusFailed)
	w.notifyOutcome(ctx, run, f.script, res)
	run, res = f.finish("canceled", script.TriggerSchedule, script.RunStatusCanceled)
	w.notifyOutcome(ctx, run, f.script, res)
	require.Len(t, n.payloads, 1)
	require.Contains(t, inc.open, f.script.ID, "a canceled run does not close it")
	assert.Equal(t, 1, inc.open[f.script.ID].FailedRuns, "nor is it counted")

	run, res = f.finish("tool-ok", script.TriggerTool, script.RunStatusSucceeded)
	w.notifyOutcome(ctx, run, f.script, res)
	require.Len(t, n.payloads, 2)
	assert.Equal(t, "tool-ok", n.payloads[1].ItemID)
	assert.NotNil(t, n.payloads[1].Recovery)
}

// TestIncident_ReclaimedRunsCount: a run failed for its spent reclaims
// (worker_lost) counts as a failure.
func TestIncident_ReclaimedRunsCount(t *testing.T) {
	f := failedScheduledRun()
	inc := &memIncidents{}
	w, n, _ := incidentWorker(&fakeRuns{}, inc)
	run, res := f.finish("lost", script.TriggerSchedule, script.RunStatusFailed)
	res.Cause = runstate.CauseWorkerLost
	w.notifyOutcome(context.Background(), run, f.script, res)
	require.Len(t, n.payloads, 1)
	assert.Equal(t, runstate.CauseWorkerLost, inc.open[f.script.ID].OpeningCause)
}

// TestIncident_DegradesToTelling: an incident that cannot be recorded sends
// the failure alert (a repeat beats silence), and one that cannot be closed
// sends no recovery.
func TestIncident_DegradesToTelling(t *testing.T) {
	f := failedScheduledRun()
	w, n, _ := incidentWorker(&fakeRuns{}, &memIncidents{openErr: errors.New("down"), closeErr: errors.New("down")})
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		run, res := f.finish(id, script.TriggerSchedule, script.RunStatusFailed)
		w.notifyOutcome(ctx, run, f.script, res)
	}
	assert.Len(t, n.payloads, 2)
	run, res := f.finish("c", script.TriggerSchedule, script.RunStatusSucceeded)
	w.notifyOutcome(ctx, run, f.script, res)
	assert.Len(t, n.payloads, 2)
}

// TestIncident_FollowsTheRecordedOutcome: a run that finished successfully but
// was recorded as failed, because another run saved the script's state first,
// is a failure for the incident, not a recovery.
func TestIncident_FollowsTheRecordedOutcome(t *testing.T) {
	f := failedScheduledRun()
	inc := &memIncidents{}
	runs := &fakeRuns{}
	w, n, _ := incidentWorker(runs, inc)
	ctx := context.Background()

	run, res := f.finish("run-1", script.TriggerSchedule, script.RunStatusFailed)
	w.notifyOutcome(ctx, run, f.script, res)

	run, res = f.finish("run-2", script.TriggerSchedule, script.RunStatusSucceeded)
	res.State = &script.StateWrite{Value: map[string]any{"n": 1}}
	recorded := *run
	recorded.Status, recorded.Error, recorded.Cause = script.RunStatusFailed, "state moved", runstate.CauseStateConflict
	runs.queue = append(runs.queue, &recorded)
	w.notifyOutcome(ctx, run, f.script, res)
	assert.Len(t, n.payloads, 1, "no recovery for a run recorded as failed")
	assert.Equal(t, 2, inc.open[f.script.ID].FailedRuns)

	run, res = f.finish("run-3", script.TriggerSchedule, script.RunStatusSucceeded)
	res.State = &script.StateWrite{Value: map[string]any{"n": 2}}
	w.notifyOutcome(ctx, run, f.script, res)
	require.Len(t, n.payloads, 2, "a run whose row cannot be read is taken as it finished")
	assert.NotNil(t, n.payloads[1].Recovery)
}

func TestFailureIncidents(t *testing.T) {
	supplied := &memIncidents{}
	assert.Same(t, supplied, failureIncidents(Config{Incidents: supplied}))
	assert.Nil(t, failureIncidents(Config{}))
}
