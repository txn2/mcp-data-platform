package scriptincident

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Alert bounds.
const (
	// WriteTimeout bounds each alert write. It is one insert, and a run that
	// has already been recorded must not be held open behind a slow database
	// for the sake of telling somebody about it.
	WriteTimeout = 5 * time.Second

	// maxAlertError and maxAlertLog bound what the alert carries. The whole
	// failure and the whole log live on the run row; an email exists to make
	// somebody go and read them, so it carries the head of the error and the
	// tail of the log — the two ends that say what happened last.
	maxAlertError = 2000
	maxAlertLog   = 2000

	logKeyRunID = "run_id"
	logKeyError = "error"
)

// Notifier queues one notification for a person.
type Notifier interface {
	Notify(ctx context.Context, recipient, category string, p notification.Payload) (bool, error)
}

// Poster posts an alert to the operator's notification channels under a key.
type Poster interface {
	Post(ctx context.Context, key string, p notification.Payload) (int, error)
}

// Alerter tells a script's owner and the operator's channels what a finished
// run means for the script's incident. Store nil tells every failure and no
// recovery; Notifier or Channels nil skips that half.
type Alerter struct {
	Store     Store
	Notifier  Notifier
	Channels  Poster
	PortalURL string
}

// Failed tells of a failed scheduled run when it opens the incident; one
// counted on an incident already open sends nothing. repeats is read only when
// the run is told. When the incident cannot be recorded the run is told
// anyway, since a repeated email is better than none.
func (a Alerter) Failed(ctx context.Context, run *script.Run, sc *script.Script, res script.RunResult, repeats func() int) {
	// The run is already recorded, so these writes outlive the cancellation
	// that may have raced it, and are bounded so a wedged database cannot hold
	// the worker on an alert.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), WriteTimeout)
	defer cancel()
	if !a.opens(writeCtx, ctx, run, sc, res) {
		return
	}
	payload := notification.Payload{
		Kind:      notification.KindScriptRun,
		ItemID:    run.ID,
		ItemTitle: sc.Name,
		// The run's own page, where the whole failure and log are.
		Link: RunPage(a.PortalURL, sc.ID, run.ID),
		// The actor is the SCRIPT, which is both true and load-bearing: the
		// enqueuer rate-limits per actor and falls back to the recipient when
		// there is none, so an actorless alert would let one bad night —
		// forty schedules failing on the same unreachable warehouse — spend one
		// person's whole budget and drop the rest. Keyed on the script, a
		// repeatedly failing automation is throttled and its neighbors are not.
		Actor:   sc.Principal(),
		Message: alertDetail(res),
		// Why it failed decides what the alert tells the owner to do (#1859),
		// and whether it keeps failing the same way decides how firmly (#1935).
		Cause:   res.Cause,
		Repeats: repeats(),
	}
	a.mailOwner(writeCtx, ctx, run, sc, payload)
	// The script is the key: the channel's repeat window holds back a repeat
	// of the same failure post.
	a.post(writeCtx, ctx, run, "script:"+sc.ID, payload)
}

// opens records the failed run on its script's incident and reports whether
// it opened it, which is whether it is told.
func (a Alerter) opens(writeCtx, ctx context.Context, run *script.Run, sc *script.Script, res script.RunResult) bool {
	if a.Store == nil {
		return true
	}
	opened, err := a.Store.Open(writeCtx, Opening{
		ScriptID: sc.ID, RunID: run.ID, Cause: res.Cause, Error: failureLine(res.Error), FailedAt: time.Now(),
	})
	if err != nil {
		slog.WarnContext(ctx, "scripts: recording a failed run on its automation's incident failed; alerting anyway", // #nosec G706 -- structured slog call; error sanitized
			logKeyRunID, run.ID, logKeyError, logsan.SanitizeForLog(err.Error()))
		return true
	}
	return opened
}

// Succeeded closes the script's incident and tells of the recovery, when one
// was open. Any succeeded run closes it, as any succeeded run ends the failure
// the session-start notice reports (#1934). It says the latest run succeeded;
// it does not say that what the failed runs left undone was done.
func (a Alerter) Succeeded(ctx context.Context, run *script.Run, sc *script.Script) {
	if a.Store == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), WriteTimeout)
	defer cancel()
	in, err := a.Store.Close(writeCtx, sc.ID)
	if err != nil {
		slog.WarnContext(ctx, "scripts: closing an automation's failure incident failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyRunID, run.ID, logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	if in == nil {
		return
	}
	payload := notification.Payload{
		Kind:      notification.KindScriptRun,
		ItemID:    run.ID,
		ItemTitle: sc.Name,
		Link:      RunPage(a.PortalURL, sc.ID, run.ID),
		Actor:     sc.Principal(),
		Recovery: &notification.ScriptRecovery{
			FailedRuns:   in.FailedRuns,
			FailingSince: in.OpenedAt,
			RecoveredAt:  time.Now(),
			OpeningRunID: in.OpeningRunID,
			OpeningCause: in.OpeningCause,
			OpeningError: in.OpeningError,
			OpeningLink:  RunPage(a.PortalURL, sc.ID, in.OpeningRunID),
		},
	}
	a.mailOwner(writeCtx, ctx, run, sc, payload)
	// Its own key: the failure's key is inside its repeat window for any
	// outage shorter than the window, and the recovery must not be held back
	// by the failure it ends.
	a.post(writeCtx, ctx, run, "script:"+sc.ID+":recovered", payload)
}

// mailOwner queues the alert for the script's owner.
func (a Alerter) mailOwner(writeCtx, ctx context.Context, run *script.Run, sc *script.Script, payload notification.Payload) {
	if a.Notifier == nil {
		return
	}
	recipients := Recipients(sc)
	if len(recipients) == 0 {
		slog.WarnContext(ctx, "scripts: an automation's run alert has no owner to tell",
			logKeyRunID, run.ID, "script", logsan.SanitizeForLog(sc.Name))
		return
	}
	for _, recipient := range recipients {
		if _, err := a.Notifier.Notify(writeCtx, recipient, notification.CategoryScriptRun, payload); err != nil {
			// A failed alert is not a failed run. It is logged and the next
			// recipient is still told.
			slog.WarnContext(ctx, "scripts: queueing a run alert failed", // #nosec G706 -- structured slog call; error sanitized
				logKeyRunID, run.ID, logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// post posts the alert to the operator's channels under key.
func (a Alerter) post(writeCtx, ctx context.Context, run *script.Run, key string, payload notification.Payload) {
	if a.Channels == nil {
		return
	}
	if _, err := a.Channels.Post(writeCtx, key, payload); err != nil {
		slog.WarnContext(ctx, "scripts: posting a run alert to channels failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyRunID, run.ID, logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// RunPage is a run's page, or nothing when the deployment does not know its
// own public address.
func RunPage(portalURL, scriptID, runID string) string {
	if portalURL == "" {
		return ""
	}
	return strings.TrimRight(portalURL, "/") + "/portal/automations/" + scriptID + "/runs/" + runID
}

// Recipients is who hears about a failing automation: the script's owner,
// who is accountable for it and can fix it.
func Recipients(sc *script.Script) []string {
	addr := strings.TrimSpace(sc.OwnerEmail)
	if addr == "" {
		return []string{}
	}
	return []string{addr}
}

// alertDetail composes what the alert carries: the failure, and the tail of
// what the script printed before it.
func alertDetail(res script.RunResult) string {
	parts := []string{}
	if failure := strings.TrimSpace(res.Error); failure != "" {
		parts = append(parts, "Failure:\n"+headOf(failure, maxAlertError))
	}
	if log := strings.TrimSpace(res.Log); log != "" {
		parts = append(parts, "Last output:\n"+tailOf(log, maxAlertLog))
	}
	return strings.Join(parts, "\n\n")
}

// failureLine is the line of an error that names the failure: its last
// non-empty one. A script's error is a traceback, which opens with
// "Traceback (most recent call last):" and ends with what was raised; the
// failure streak compares the same line (#1935).
func failureLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return headOf(strings.TrimSpace(lines[len(lines)-1]), maxAlertError)
}

// headOf truncates from the end, marking that it did. An error reads from the
// top: the first line names what went wrong and the rest is the path to it.
// The cut is on a rune boundary, because a mail body is text and half a
// character is not.
func headOf(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "\n[truncated]"
}

// tailOf truncates from the start, marking that it did. A log reads from the
// bottom: what a script printed just before it failed is what explains the
// failure.
func tailOf(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return "[truncated]\n" + string(runes[len(runes)-limit:])
}
