package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// scriptReviewNotification is a queued script review-queue alert with the given
// rollup.
func scriptReviewNotification(q *notification.ReviewQueue) notification.Notification {
	return notification.Notification{
		Recipient: "reviewer@example.com",
		Category:  notification.CategoryScriptReview,
		Payload: notification.Payload{
			Kind:      notification.KindScriptReview,
			ItemTitle: "Script review queue",
			Link:      "https://data.example.com/portal/admin/scripts",
			Review:    q,
		},
	}
}

func TestRender_ScriptReviewAlert(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{scriptReviewNotification(
		&notification.ReviewQueue{Pending: 3, OldestAgeDays: 9},
	)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "3 script versions awaiting approval" {
		t.Errorf("Subject = %q", email.Subject)
	}
	for _, want := range []string{
		"3 script versions awaiting approval",
		"The oldest has been waiting 9 days.",
		"Nothing runs unattended until somebody approves it",
		"Approving binds the capabilities that version may use",
		"https://data.example.com/portal/admin/scripts",
		"Open the script review queue",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if !strings.Contains(email.Text, "https://data.example.com/portal/admin/scripts") {
		t.Error("Text missing the deep link")
	}
	// The body is the platform speaking; only text a person wrote is quoted.
	if strings.Contains(email.HTML, "&ldquo;The oldest") {
		t.Error("alert body rendered as a quotation")
	}
}

// TestRender_ScriptReviewAlertSingular checks the one-version wording, which is
// the common case for a small deployment.
func TestRender_ScriptReviewAlertSingular(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{scriptReviewNotification(
		&notification.ReviewQueue{Pending: 1, OldestAgeDays: 1},
	)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "1 script version awaiting approval" {
		t.Errorf("Subject = %q", email.Subject)
	}
	if !strings.Contains(email.HTML, "The oldest has been waiting 1 day.") {
		t.Error("HTML missing the singular age")
	}
}

// TestRender_ScriptReviewAlertWithoutRollup covers the row that outlives the
// build that wrote it: an alert enqueued before this payload carried a rollup
// still renders a meaningful subject rather than a broken sentence.
func TestRender_ScriptReviewAlertWithoutRollup(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{scriptReviewNotification(nil)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "The script review queue needs attention" {
		t.Errorf("Subject = %q", email.Subject)
	}
	if strings.Contains(email.HTML, "The oldest has been waiting") {
		t.Error("a nil rollup must not render an age")
	}
}
