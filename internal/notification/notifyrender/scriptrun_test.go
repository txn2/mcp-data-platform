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
	for _, want := range []string{"dpx_1", "division by zero", "not retried", "corrected"} {
		if !strings.Contains(item.Body, want) {
			t.Errorf("body must carry %q, got %q", want, item.Body)
		}
	}
	if got := Subject(n); !strings.Contains(got, "daily-sales") {
		t.Errorf("the shared summary line must name the script, got %q", got)
	}
}

// TestScriptRunBodyByCause holds #1859: only a script error tells the owner to
// fix the script. An upstream that was briefly unavailable says the next run
// should succeed, and the memory causes say how to hold less.
func TestScriptRunBodyByCause(t *testing.T) {
	cases := map[string]struct {
		want, never string
	}{
		"upstream":       {want: "temporarily unavailable", never: "corrected"},
		"memory":         {want: "append=True", never: "corrected"},
		"worker_lost":    {want: "stopped without reporting a result", never: "corrected"},
		"platform":       {want: "nothing in the script to fix", never: "corrected"},
		"state_conflict": {want: "saved its state first", never: "corrected"},
		"script":         {want: "corrected", never: "temporarily"},
		"":               {want: "corrected", never: "temporarily"},
	}
	for cause, tc := range cases {
		t.Run("cause "+cause, func(t *testing.T) {
			n := scriptRunNotification()
			n.Payload.Cause = cause
			body := buildItem(n).Body
			if !strings.Contains(body, tc.want) {
				t.Errorf("body for cause %q does not say %q: %s", cause, tc.want, body)
			}
			if strings.Contains(body, tc.never) {
				t.Errorf("body for cause %q says %q: %s", cause, tc.never, body)
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
