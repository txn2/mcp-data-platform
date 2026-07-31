package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// reviewQueueNotification is a queued review-queue alert with the given
// rollup.
func reviewQueueNotification(q *notification.ReviewQueue) notification.Notification {
	return notification.Notification{
		Recipient: "ops@example.com",
		Category:  notification.CategoryReviewQueue,
		Payload: notification.Payload{
			Kind:      notification.KindReviewQueue,
			ItemTitle: "Knowledge review queue",
			Link:      "https://data.example.com/portal/knowledge#review",
			Review:    q,
		},
	}
}

func TestRender_ReviewQueueAlert(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{reviewQueueNotification(
		&notification.ReviewQueue{Pending: 12, OldestAgeDays: 94, StaleCount: 5, StaleAfterDays: 30},
	)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "12 insights awaiting review in the knowledge queue" {
		t.Errorf("Subject = %q", email.Subject)
	}
	// Every number the operator needs to decide whether to act, plus the way in.
	for _, want := range []string{
		"12 insights awaiting review",
		"The oldest has been waiting 94 days.",
		"5 insights have been pending for 30 days or more.",
		"https://data.example.com/portal/knowledge#review",
		"Open the review queue",
		"ACME Data Platform",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, want := range []string{
		"The oldest has been waiting 94 days.",
		"https://data.example.com/portal/knowledge#review",
	} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("Text missing %q", want)
		}
	}
	// The body is the platform speaking; only text a person wrote is quoted.
	if strings.Contains(email.HTML, "&ldquo;The oldest") {
		t.Error("alert body rendered as a quotation")
	}
}

func TestRender_ReviewQueueSingularCounts(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{reviewQueueNotification(
		&notification.ReviewQueue{Pending: 1, OldestAgeDays: 1, StaleCount: 1, StaleAfterDays: 1},
	)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "1 insight awaiting review in the knowledge queue" {
		t.Errorf("Subject = %q", email.Subject)
	}
	for _, want := range []string{
		"The oldest has been waiting 1 day.",
		"1 insight has been pending for 1 day or more.",
	} {
		if !strings.Contains(email.Text, want) {
			t.Errorf("Text missing %q", want)
		}
	}
}

func TestRender_ReviewQueueWithoutRollup(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{reviewQueueNotification(nil)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "The knowledge review queue needs attention" {
		t.Errorf("Subject = %q", email.Subject)
	}
	if !strings.Contains(email.HTML, "Open the review queue") {
		t.Error("HTML missing the review queue link")
	}
}

func TestReviewQueueBody_OmitsAbsentFigures(t *testing.T) {
	body := reviewQueueBody(&notification.ReviewQueue{Pending: 3})
	for _, unwanted := range []string{"waiting", "or more"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body %q states a figure it does not have", body)
		}
	}
	if !strings.Contains(body, "Insights stay proposals") {
		t.Errorf("body = %q; want the cost of an unworked queue stated", body)
	}
	if reviewQueueBody(nil) != "" {
		t.Error("a nil rollup renders no body")
	}
}

func TestRender_ReviewQueueInDigest(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{
		reviewQueueNotification(&notification.ReviewQueue{Pending: 7, OldestAgeDays: 40}),
		{
			Recipient: "ops@example.com",
			Category:  notification.CategoryShare,
			Payload: notification.Payload{
				Kind: notification.KindAsset, ItemTitle: "Quarterly Revenue",
				Actor: "owner@example.com",
			},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A daily-mode recipient reads the alert as one line of their digest.
	for _, want := range []string{
		"7 insights awaiting review in the knowledge queue",
		"The oldest has been waiting 40 days.",
		"Quarterly Revenue",
	} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("digest HTML missing %q", want)
		}
	}
}

func TestSubject_ReviewQueue(t *testing.T) {
	got := Subject(reviewQueueNotification(&notification.ReviewQueue{Pending: 4}))
	if got != "4 insights awaiting review in the knowledge queue" {
		t.Errorf("Subject = %q", got)
	}
	// The admin monitoring tab reads the same line for a rollup-less row.
	if Subject(reviewQueueNotification(nil)) != "The knowledge review queue needs attention" {
		t.Error("a rollup-less row must still summarize as something readable")
	}
}
