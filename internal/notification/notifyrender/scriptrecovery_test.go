package notifyrender

import (
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// recoveredNotification is the alert a nightly report that failed three runs
// raises when the fourth succeeds.
func recoveredNotification() notification.Notification {
	since := time.Date(2026, 10, 11, 1, 5, 0, 0, time.UTC)
	return notification.Notification{
		Recipient: "jane@b.io",
		Payload: notification.Payload{
			Kind: notification.KindScriptRun, ItemID: "dpx_4", ItemTitle: "daily-sales",
			Link: "https://p.example.com/portal/automations/s/runs/dpx_4",
			Recovery: &notification.ScriptRecovery{
				FailedRuns: 3, FailingSince: since, RecoveredAt: since.Add(2*time.Hour + 15*time.Minute),
				OpeningRunID: "dpx_1", OpeningCause: "upstream", OpeningError: "Error: timeout",
				OpeningLink: "https://p.example.com/portal/automations/s/runs/dpx_1",
			},
		},
	}
}

// TestScriptRecovery is #2041's recovered email: it says the automation
// recovered, names the run that succeeded and the failed run that began it,
// how many failed and for how long, and claims nothing about the work.
func TestScriptRecovery(t *testing.T) {
	n := recoveredNotification()
	if got := scriptRunSubject(n.Payload); got != `The scheduled automation "daily-sales" recovered` {
		t.Errorf("subject = %q", got)
	}
	body := scriptRunBody(n.Payload)
	for _, want := range []string{
		"Run dpx_4 of this automation succeeded.",
		"It had been failing since 2026-10-11 01:05 UTC, for 2 hours 15 minutes, and 3 runs failed in that time.",
		"The first failed run was dpx_1.",
		"does not redo what the failed runs left undone",
		"Cause: upstream\nFailure: Error: timeout\nFirst failed run: https://p.example.com/portal/automations/s/runs/dpx_1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "did not finish") {
		t.Error("a recovery is not worded as a failure")
	}

	bare := notification.Payload{Kind: notification.KindScriptRun, Recovery: &notification.ScriptRecovery{FailedRuns: 1, OpeningRunID: "r"}}
	if got := scriptRunSubject(bare); got != "A scheduled automation recovered" {
		t.Errorf("bare subject = %q", got)
	}
	if got := scriptRunBody(bare); !strings.HasPrefix(got, "A run of this automation succeeded.") || !strings.Contains(got, "and 1 run failed") || strings.Contains(got, "Cause:") {
		t.Errorf("bare body = %q", got)
	}

	email, err := testRenderer(t).Render([]notification.Notification{n})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(email.Subject, "recovered") || !strings.Contains(email.Text, "dpx_1") || !strings.Contains(email.HTML, "dpx_1") {
		t.Errorf("rendered email: subject %q", email.Subject)
	}
}

// TestScriptRunBody_SaysFurtherFailuresAreNotMailed: the failure email tells
// its owner they will hear once more, when it recovers.
func TestScriptRunBody_SaysFurtherFailuresAreNotMailed(t *testing.T) {
	if body := scriptRunBody(scriptRunNotification().Payload); !strings.Contains(body, "You will not be emailed about further failed runs while it keeps failing") {
		t.Errorf("body = %s", body)
	}
}

func TestFailingFor(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:              "less than a minute",
		time.Minute:                   "1 minute",
		5 * time.Minute:               "5 minutes",
		time.Hour:                     "1 hour",
		3 * time.Hour:                 "3 hours",
		time.Hour + time.Minute:       "1 hour 1 minute",
		26*time.Hour + 59*time.Second: "26 hours",
	} {
		if got := failingFor(d); got != want {
			t.Errorf("failingFor(%v) = %q; want %q", d, got, want)
		}
	}
}
