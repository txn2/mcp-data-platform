package scriptexec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeNotifier records what was queued and to whom.
type fakeNotifier struct {
	mu         sync.Mutex
	recipients []string
	payloads   []notification.Payload
	categories []string
	err        error
}

func (f *fakeNotifier) Notify(_ context.Context, recipient, category string, p notification.Payload) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recipients = append(f.recipients, recipient)
	f.categories = append(f.categories, category)
	f.payloads = append(f.payloads, p)
	return f.err == nil, f.err
}

func (f *fakeNotifier) queued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.recipients...)
}

// failedRun is the state a failed nightly report leaves behind.
type failedRun struct {
	run    *script.Run
	script *script.Script
	result script.RunResult
}

// failedScheduledRun builds that state.
func failedScheduledRun() failedRun {
	sc, _, run := executableState()
	run.Trigger = script.TriggerSchedule
	run.ScheduleID = "sched_1"
	return failedRun{run: run, script: sc, result: script.RunResult{
		Status: script.RunStatusFailed,
		Error:  "Traceback:\n  in main\nError: division by zero",
		Log:    "starting\nquerying warehouse\n",
	}}
}

// notifierWorker builds a worker whose only job in these tests is to raise the
// alert.
func notifierWorker(n Notifier) *worker {
	return newWorker(workerConfig{runs: &fakeRuns{}, notifier: n})
}

// TestNotifyFailure_TellsTheOwner pins who hears about an automation nobody is
// watching — the owner, who is accountable for it and can fix it — and what
// they are told.
func TestNotifyFailure_TellsTheOwner(t *testing.T) {
	f := failedScheduledRun()
	n := &fakeNotifier{}

	notifierWorker(n).notifyFailure(context.Background(), f.run, f.script, f.result)

	assert.Equal(t, []string{"jane@example.com"}, n.queued(),
		"the owner, and only the owner, is told")
	require.Len(t, n.payloads, 1)
	assert.Equal(t, notification.KindScriptRun, n.payloads[0].Kind)
	assert.Equal(t, notification.CategoryScriptRun, n.categories[0])
	assert.Equal(t, f.run.ID, n.payloads[0].ItemID)
	assert.Equal(t, f.script.Name, n.payloads[0].ItemTitle)
	assert.Contains(t, n.payloads[0].Message, "division by zero")
	assert.Contains(t, n.payloads[0].Message, "querying warehouse")
	// The actor is the script, which is what the enqueuer rate-limits on. An
	// actorless alert falls back to the RECIPIENT as the key, so one bad night
	// across many schedules would spend one person's budget and drop the rest.
	assert.Equal(t, f.script.Principal(), n.payloads[0].Actor)
}

// TestNotifyFailure_OnlyForAScheduledFailure pins the boundary. Every other
// case is either already reported to somebody who is reading it, or is not a
// failure at all.
func TestNotifyFailure_OnlyForAScheduledFailure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*script.Run, *script.RunResult)
	}{
		{"a tool run reports itself to its caller", func(r *script.Run, _ *script.RunResult) {
			r.Trigger = script.TriggerTool
		}},
		{"a scheduled run that succeeded", func(_ *script.Run, res *script.RunResult) {
			res.Status = script.RunStatusSucceeded
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := failedScheduledRun()
			tt.mutate(f.run, &f.result)
			n := &fakeNotifier{}

			notifierWorker(n).notifyFailure(context.Background(), f.run, f.script, f.result)
			assert.Empty(t, n.queued())
		})
	}
}

// TestNotifyFailure_DegradesQuietly pins that nothing here can turn a recorded
// run into a worker fault: no notifier, no script, no recipients, or a queue
// that refuses the write.
func TestNotifyFailure_DegradesQuietly(t *testing.T) {
	t.Run("no notifier wired", func(*testing.T) {
		f := failedScheduledRun()
		notifierWorker(nil).notifyFailure(context.Background(), f.run, f.script, f.result)
	})

	t.Run("the script is gone, so there is nobody to tell", func(t *testing.T) {
		f := failedScheduledRun()
		n := &fakeNotifier{}
		notifierWorker(n).notifyFailure(context.Background(), f.run, nil, f.result)
		assert.Empty(t, n.queued())
	})

	t.Run("an ownerless script", func(t *testing.T) {
		f := failedScheduledRun()
		f.script.OwnerEmail = ""
		n := &fakeNotifier{}
		notifierWorker(n).notifyFailure(context.Background(), f.run, f.script, f.result)
		assert.Empty(t, n.queued())
	})

	t.Run("the enqueue failed", func(t *testing.T) {
		f := failedScheduledRun()
		n := &fakeNotifier{err: errors.New("boom")}
		notifierWorker(n).notifyFailure(context.Background(), f.run, f.script, f.result)
		assert.Len(t, n.queued(), 1, "the write was attempted; its failure is logged, not raised")
	})

	t.Run("a canceled context still queues the alert", func(t *testing.T) {
		f := failedScheduledRun()
		n := &fakeNotifier{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		notifierWorker(n).notifyFailure(ctx, f.run, f.script, f.result)
		assert.Len(t, n.queued(), 1, "the run is already recorded; the alert about it survives the cancel")
	})
}

// TestAlertRecipients_IsTheOwnerOnly pins who the alert set is: the script's
// owner, trimmed, and nobody when the script has no owner address.
func TestAlertRecipients_IsTheOwnerOnly(t *testing.T) {
	assert.Equal(t, []string{"jane@example.com"},
		alertRecipients(&script.Script{OwnerEmail: " jane@example.com "}))
	assert.Empty(t, alertRecipients(&script.Script{OwnerEmail: "  "}))
}

// TestAlertDetail_TruncatesFromTheRightEnd pins the two directions: an error
// reads from the top, a log reads from the bottom.
func TestAlertDetail_TruncatesFromTheRightEnd(t *testing.T) {
	detail := alertDetail(script.RunResult{
		Error: "E" + strings.Repeat("x", maxAlertError+50),
		Log:   strings.Repeat("y", maxAlertLog+50) + "LAST",
	})
	assert.Contains(t, detail, "Ex", "the error's first line survives")
	assert.Contains(t, detail, "LAST", "the log's last line survives")
	assert.Contains(t, detail, "[truncated]")

	assert.Empty(t, alertDetail(script.RunResult{}), "nothing to say is said with nothing")
}

// TestAlertDetail_CutsOnARuneBoundary pins that a truncated body is still text.
func TestAlertDetail_CutsOnARuneBoundary(t *testing.T) {
	detail := alertDetail(script.RunResult{Error: strings.Repeat("é", maxAlertError+10)})
	assert.True(t, isValidUTF8(detail), "half a character is not a character")
}

// isValidUTF8 reports whether s decodes cleanly.
func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestWorker_AFailedScheduledRunNotifies is the wiring proof: the alert is
// raised by the worker's own resolve path, for a run it actually recorded as
// failed, rather than by a caller remembering to ask for it.
func TestWorker_AFailedScheduledRunNotifies(t *testing.T) {
	sc, v, run := executableState()
	run.Trigger = script.TriggerSchedule
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	n := &fakeNotifier{}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner:   &fakeExecutor{out: attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: "boom"}}},
		notifier: n,
	})

	w.drain()

	assert.Equal(t, []string{"jane@example.com"}, n.queued())
}

// TestWorker_ARetriedRunDoesNotNotify pins that a platform fault the worker is
// about to retry is not reported as a failed automation. The run has not
// failed; it has not finished.
func TestWorker_ARetriedRunDoesNotNotify(t *testing.T) {
	sc, v, run := executableState()
	run.Trigger = script.TriggerSchedule
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	n := &fakeNotifier{}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: &fakeExecutor{out: attempt{
			result:    script.RunResult{Status: script.RunStatusFailed, Error: "the warehouse was unreachable"},
			retryable: true,
		}},
		notifier: n,
	})

	w.drain()

	assert.Empty(t, n.queued())
}

// TestWorker_AGateRefusalStillReachesTheOwner pins why load returns what it
// read alongside a refusal: a schedule attached to a script that was disabled
// after queueing must reach the person who owns it.
func TestWorker_AGateRefusalStillReachesTheOwner(t *testing.T) {
	sc, v, run := executableState()
	run.Trigger = script.TriggerSchedule
	sc.Enabled = false
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	n := &fakeNotifier{}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: &fakeExecutor{}, notifier: n,
	})

	w.drain()

	assert.Equal(t, []string{"jane@example.com"}, n.queued())
	require.Len(t, n.payloads, 1)
	assert.Contains(t, n.payloads[0].Message, "disabled")
}

// TestNewNotifier covers the three ways the enqueue side is resolved.
func TestNewNotifier(t *testing.T) {
	supplied := &fakeNotifier{}
	assert.Equal(t, Notifier(supplied), newNotifier(Config{Notifier: supplied}),
		"a supplied notifier is used as-is")
	assert.Nil(t, newNotifier(Config{}), "no database, no notifications")
	assert.Nil(t, newNotifier(Config{NotificationsDisabled: true}),
		"a deployment that turned notifications off queues nothing")
}

// TestNotifyWriteIsBounded pins that the alert cannot hold a worker open on a
// wedged queue for longer than its own timeout.
func TestNotifyWriteIsBounded(t *testing.T) {
	assert.LessOrEqual(t, notifyWriteTimeout, 10*time.Second)
}
