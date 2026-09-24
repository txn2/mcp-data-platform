// Package runcontrol is what a caller that does not execute a managed-script
// run does to it: wait for it to finish, and ask it to stop (#1845, #1847).
//
// Both run_script and the portal's run route hold a request open for a run a
// worker executes somewhere else, and both let the person who can read a run
// cancel it. The run store answers a cancel with the status the run had when
// the request arrived and the status it has now; this package is the one
// reading of what those mean, so the two surfaces cannot word it differently.
package runcontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MaxWaitSeconds caps how long a request holds open for a run to finish, on
// every surface that offers to wait. Past it the caller gets the run id and
// follows the run, rather than a request held open for the length of a run.
const MaxWaitSeconds = 300

// PollEvery is how often a wait re-reads the run row. The worker is woken the
// moment a run is enqueued, so this is how long a caller waits AFTER the run
// finishes, not before it starts; holding a LISTEN connection per waiting
// request would buy at most this interval and cost a connection each.
const PollEvery = 300 * time.Millisecond

// CancelOutcome is what a cancel request did, as the surfaces that take one
// report it (#1847).
type CancelOutcome string

// Cancel outcomes.
const (
	// CanceledQueued: the run was pending and is now canceled; no worker
	// will claim it.
	CanceledQueued CancelOutcome = "canceled"
	// CancelRequested: the run is executing, and its worker stops it at its
	// next report, within seconds.
	CancelRequested CancelOutcome = "requested"
	// CanceledOrphaned: the run was marked running but its worker had
	// stopped reporting, so the store canceled it directly (#1860).
	CanceledOrphaned CancelOutcome = "canceled_orphaned"
	// CancelAlreadyFinished: the run had already ended, and nothing changed.
	CancelAlreadyFinished CancelOutcome = "already_finished"
)

// OutcomeOf reads what a cancel did from the status the run had when it
// arrived and the status it has now, which is what the run store answers.
func OutcomeOf(prior, now string) CancelOutcome {
	switch {
	case prior == script.RunStatusPending:
		return CanceledQueued
	case prior == script.RunStatusRunning && now == script.RunStatusCanceled:
		return CanceledOrphaned
	case prior == script.RunStatusRunning:
		return CancelRequested
	default:
		return CancelAlreadyFinished
	}
}

// CancelMessage states what a cancel did, in the words a person reads.
func CancelMessage(prior, now string) string {
	switch OutcomeOf(prior, now) {
	case CanceledQueued:
		return "Canceled before it started; it will not run."
	case CancelRequested:
		return "Stopping. The run ends canceled within seconds, keeping the outputs it already wrote."
	case CanceledOrphaned:
		return "Canceled. The worker executing this run had stopped reporting, so no worker would have stopped it; " +
			"it was ended directly, keeping the outputs it already wrote."
	default:
		return "The run had already finished (" + prior + "); nothing was changed."
	}
}

// RunReader is the one store call waiting on a run needs.
type RunReader interface {
	GetRun(ctx context.Context, id string) (*script.Run, error)
}

// AwaitRun re-reads a run every pollEvery until it is terminal or budget runs
// out, and returns the latest read along with whether it finished. A read
// that fails, or a context that ends, stops the wait with the last run seen:
// the run is executing elsewhere, and neither says anything about it. A
// failed read is returned for the caller to log; it is not the run's failure.
// It is the one wait run_script and the portal's run route both use (#1845).
func AwaitRun(ctx context.Context, runs RunReader, run *script.Run, budget, pollEvery time.Duration) (*script.Run, bool, error) {
	deadline := time.Now().Add(budget)
	current := run
	for !current.Terminal() {
		if !time.Now().Before(deadline) {
			return current, false, nil
		}
		select {
		case <-ctx.Done():
			return current, false, nil
		case <-time.After(min(pollEvery, time.Until(deadline))):
		}
		latest, err := runs.GetRun(ctx, run.ID)
		if err != nil {
			return current, false, fmt.Errorf("reading run %s: %w", run.ID, err)
		}
		current = latest
	}
	return current, true, nil
}
