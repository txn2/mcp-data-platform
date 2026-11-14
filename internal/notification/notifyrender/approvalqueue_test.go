package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func approvalQueueNotification(q *notification.ReviewQueue) notification.Notification {
	return notification.Notification{
		Recipient: "ops@example.com",
		Category:  notification.CategoryReviewQueue,
		Payload: notification.Payload{
			Kind: notification.KindApprovalQueue, ItemTitle: "Approvals queue",
			Link: "https://data.example.com/portal/approvals", Review: q,
		},
	}
}

// TestRender_ApprovalQueueAlert says how many requests wait, for how long,
// and links to Approvals.
func TestRender_ApprovalQueueAlert(t *testing.T) {
	r := testRenderer(t)
	email, err := r.Render([]notification.Notification{approvalQueueNotification(
		&notification.ReviewQueue{Pending: 3, OldestAgeDays: 4, StaleCount: 1, StaleAfterDays: 2},
	)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "3 requests waiting for a decision in Approvals" {
		t.Errorf("Subject = %q", email.Subject)
	}
	for _, want := range []string{"The oldest has been waiting 4 days.", "1 request has been waiting 2 days or more.", "Open Approvals", "/portal/approvals"} {
		if !strings.Contains(email.HTML, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	email, err = r.Render([]notification.Notification{approvalQueueNotification(nil)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "The approvals queue needs attention" {
		t.Errorf("Subject without a rollup = %q", email.Subject)
	}
}
