package scriptexec

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Failure-alert bounds.
const (
	// notifyWriteTimeout bounds the enqueue. It is one insert, and a run that
	// has already been recorded must not be held open behind a slow database
	// for the sake of telling somebody about it.
	notifyWriteTimeout = 5 * time.Second

	// maxAlertError and maxAlertLog bound what the alert carries. The whole
	// failure and the whole log live on the run row; an email exists to make
	// somebody go and read them, so it carries the head of the error and the
	// tail of the log — the two ends that say what happened last.
	maxAlertError = 2000
	maxAlertLog   = 2000
)

// notifyFailure alerts the people accountable for an automation that its
// scheduled run failed.
//
// Only scheduled runs. A run somebody asked for through run_script reports its
// own failure in the tool response they are already reading, and mailing that
// as well would be telling a person something they just read. A schedule is the
// case with nobody present, which is the whole reason this exists.
func (w *worker) notifyFailure(ctx context.Context, run *script.Run, sc *script.Script, v *script.Version, res script.RunResult) {
	if w.cfg.notifier == nil || sc == nil ||
		run.Trigger != script.TriggerSchedule || res.Status != script.RunStatusFailed {
		return
	}
	recipients := alertRecipients(sc, v)
	if len(recipients) == 0 {
		slog.Warn("scripts: a scheduled run failed and there is nobody to tell",
			logKeyRunID, run.ID, "script", logsan.SanitizeForLog(sc.Name))
		return
	}
	payload := notification.Payload{
		Kind:      notification.KindScriptRun,
		ItemID:    run.ID,
		ItemTitle: sc.Name,
		// The actor is the SCRIPT, which is both true and load-bearing: the
		// enqueuer rate-limits per actor and falls back to the recipient when
		// there is none, so an actorless alert would let one bad night —
		// forty schedules failing on the same unreachable warehouse — spend one
		// person's whole budget and drop the rest. Keyed on the script, a
		// repeatedly failing automation is throttled and its neighbors are not.
		Actor:   sc.Principal(),
		Message: alertDetail(res),
	}
	// The run is already recorded, so this write outlives the cancellation that
	// may have raced it, and is bounded so a wedged database cannot hold the
	// worker on an email.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyWriteTimeout)
	defer cancel()
	for _, recipient := range recipients {
		if _, err := w.cfg.notifier.Notify(writeCtx, recipient, notification.CategoryScriptRun, payload); err != nil {
			// A failed alert is not a failed run. It is logged and the next
			// recipient is still told.
			slog.Warn("scripts: queueing a run-failure alert failed", // #nosec G706 -- structured slog call; error sanitized
				logKeyRunID, run.ID, logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// alertRecipients is who hears about a failed automation: the script's owner,
// and the administrator who approved the version that failed.
//
// Both, because the two hold different halves of the answer. The owner wrote
// the script and can fix it; the approver decided this version and this
// capability set may run unattended, and a version failing every night is
// information that belongs to that decision. Duplicates collapse for the common
// case where they are the same person.
func alertRecipients(sc *script.Script, v *script.Version) []string {
	out := []string{}
	for _, addr := range []string{sc.OwnerEmail, approverOf(v)} {
		addr = strings.TrimSpace(addr)
		if addr != "" && !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}
	return out
}

// approverOf returns who approved a version, tolerating an unread one.
func approverOf(v *script.Version) string {
	if v == nil {
		return ""
	}
	return v.ApprovedBy
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
