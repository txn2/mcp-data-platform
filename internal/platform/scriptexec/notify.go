package scriptexec

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptincident"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// notifyOutcome tells the owner and the operator's channels what a finished
// run means for its automation's failure incident (#2041): a failed scheduled
// run opens one, or is counted on the one already open; a succeeded run closes
// it. A canceled run does neither.
//
// Only a scheduled failure is told. A run somebody asked for through
// run_script reports its own failure in the tool response they are already
// reading, and mailing that as well would be telling a person something they
// just read. A schedule is the case with nobody present, which is the whole
// reason this exists.
func (w *worker) notifyOutcome(ctx context.Context, run *script.Run, sc *script.Script, res script.RunResult) {
	if sc == nil || (w.cfg.notifier == nil && w.cfg.channels == nil) {
		return
	}
	alerts := scriptincident.Alerter{Store: w.cfg.incidents, PortalURL: w.cfg.portalURL}
	if w.cfg.notifier != nil {
		alerts.Notifier = w.cfg.notifier
	}
	if w.cfg.channels != nil {
		alerts.Channels = w.cfg.channels
	}
	res = w.recordedResult(ctx, run, res)
	switch {
	// An event run (#2332) has nobody present either: the sender was
	// answered when the event was stored.
	case res.Status == script.RunStatusFailed && (run.Trigger == script.TriggerSchedule || run.Trigger == runstate.TriggerEvent):
		alerts.Failed(ctx, run, sc, res, func() int { return w.repeats(ctx, sc.ID) })
	case res.Status == script.RunStatusSucceeded:
		alerts.Succeeded(ctx, run, sc)
	}
}

// recordedResult is the result the run was recorded with. A run that saved
// state can be recorded as failed although it finished successfully, when
// another run moved the state first (runstate.CauseStateConflict); the run row
// says so, and what it says is what the incident follows.
func (w *worker) recordedResult(ctx context.Context, run *script.Run, res script.RunResult) script.RunResult {
	if res.Status != script.RunStatusSucceeded || res.State == nil {
		return res
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scriptincident.WriteTimeout)
	defer cancel()
	recorded, err := w.cfg.runs.GetRun(readCtx, run.ID)
	if err != nil || recorded.Status == res.Status {
		return res
	}
	res.Status, res.Error, res.Cause = recorded.Status, recorded.Error, recorded.Cause
	return res
}

// repeats is how many runs of the script have failed in a row ending on the
// same error, the one just recorded included, or zero where the run store
// cannot say. The store that keeps runs in PostgreSQL can; a store assembled
// for a test need not, and its alerts read as a first failure.
func (w *worker) repeats(ctx context.Context, scriptID string) int {
	reader, ok := w.cfg.runs.(runstate.FailureStreakReader)
	if !ok {
		return 0
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scriptincident.WriteTimeout)
	defer cancel()
	streaks, err := reader.FailureStreaks(readCtx, []string{scriptID})
	if err != nil {
		slog.WarnContext(ctx, "scripts: reading a failing script's recent runs failed; the alert reads as a first failure",
			"script_id", scriptID, logKeyError, logsan.SanitizeForLog(err.Error()))
		return 0
	}
	return streaks[scriptID].SameError
}
