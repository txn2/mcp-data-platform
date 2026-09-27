package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// scriptRunNotification is the alert a failed nightly report raises.
func scriptRunNotification() notification.Notification {
	return notification.Notification{
		Recipient: "jane@b.io",
		Payload: notification.Payload{
			Kind: notification.KindScriptRun, ItemID: "dpx_1", ItemTitle: "daily-sales",
			Message: "Failure:\nError: division by zero\n\nLast output:\nquerying warehouse",
		},
	}
}

// TestScriptRunSubject pins the line an inbox shows: the script, named, because
// that is what the recipient owns and will go and look at.
func TestScriptRunSubject(t *testing.T) {
	got := scriptRunSubject(scriptRunNotification().Payload)
	if !strings.Contains(got, "daily-sales") || !strings.Contains(got, "failed") {
		t.Errorf("subject must name the script and say it failed, got %q", got)
	}
	if bare := scriptRunSubject(notification.Payload{Kind: notification.KindScriptRun}); bare == "" {
		t.Error("a payload from an older build must still render a meaningful line")
	}
}

// TestScriptRunBody pins that the failure detail is rendered as the platform's
// own prose rather than as a quotation: a backtrace is not something a
// colleague said.
func TestScriptRunBody(t *testing.T) {
	n := scriptRunNotification()
	item := buildItem(n)
	if item.Message != "" {
		t.Error("the failure detail must not render as a quotation")
	}
	for _, want := range []string{"dpx_1", "division by zero", "may succeed", "needs correcting"} {
		if !strings.Contains(item.Body, want) {
			t.Errorf("body must carry %q, got %q", want, item.Body)
		}
	}
	if got := Subject(n); !strings.Contains(got, "daily-sales") {
		t.Errorf("the shared summary line must name the script, got %q", got)
	}
}

// TestScriptRunBodyByCause holds #1859 and #1935: only a script failure that
// has repeated the same way tells the owner to correct the script. One script
// failure says the next run may succeed, since what the script reacted to may
// have been outside it; an upstream or a declared temporary failure says the
// next run should; the memory causes say how to hold less.
func TestScriptRunBodyByCause(t *testing.T) {
	cases := map[string]struct {
		cause       string
		repeats     int
		want, never string
	}{
		"upstream":                   {"upstream", 0, "unavailable or answered with an error", "needs correcting"},
		"upstream, repeated":         {"upstream", 4, "failed 4 runs in a row", "needs correcting"},
		"transient":                  {"transient", 1, "reported this failure as temporary", "needs correcting"},
		"memory":                     {"memory", 0, "append=True", "needs correcting"},
		"worker_lost":                {"worker_lost", 0, "stopped without reporting a result", "needs correcting"},
		"platform":                   {"platform", 0, "nothing in the script to fix", "needs correcting"},
		"state_conflict":             {"state_conflict", 0, "saved its state first", "needs correcting"},
		"script, once":               {"script", 1, "may succeed", "failed 1"},
		"script, twice":              {"script", 2, "may succeed", "runs in a row"},
		"script, repeated":           {"script", 3, "failed 3 runs in a row the same way, so the script needs correcting", "may succeed"},
		"no cause reads as a script": {"", 0, "may succeed", "the same way, so"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			n := scriptRunNotification()
			n.Payload.Cause = tc.cause
			n.Payload.Repeats = tc.repeats
			body := buildItem(n).Body
			if !strings.Contains(body, tc.want) {
				t.Errorf("body does not say %q: %s", tc.want, body)
			}
			if strings.Contains(body, tc.never) {
				t.Errorf("body says %q: %s", tc.never, body)
			}
			if strings.Contains(body, "fails the same way") {
				t.Errorf("body promises the same inputs fail the same way (#1935): %s", body)
			}
		})
	}
}

// TestScriptRunRendersThroughTheRealTemplates pins that the alert survives the
// renderer end to end, which is what a queue row written by this build and
// delivered by the send worker actually does.
func TestScriptRunRendersThroughTheRealTemplates(t *testing.T) {
	email, err := testRenderer(t).Render([]notification.Notification{scriptRunNotification()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(email.Subject, "daily-sales") {
		t.Errorf("subject = %q", email.Subject)
	}
	if !strings.Contains(email.HTML, "division by zero") || !strings.Contains(email.Text, "division by zero") {
		t.Error("both bodies must carry the failure")
	}
}

// TestScriptRunSubject_SpeaksOfAutomations pins the section's name in the
// inbox (#1912): the recipient owns an automation, whose kind is a script.
func TestScriptRunSubject_SpeaksOfAutomations(t *testing.T) {
	if got := scriptRunSubject(notification.Payload{Kind: notification.KindScriptRun}); got != "A scheduled automation failed" {
		t.Errorf("bare subject = %q", got)
	}
	if got := scriptRunSubject(scriptRunNotification().Payload); got != `The scheduled automation "daily-sales" failed` {
		t.Errorf("titled subject = %q", got)
	}
}
