package notifyrender

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// approvalQueueLinkText labels the approvals queue alert's link.
const approvalQueueLinkText = "Open Approvals"

// approvalQueueSubject is the subject and heading of an approvals queue
// staleness alert (#2437): how many requests wait for a decision.
func approvalQueueSubject(q *notification.ReviewQueue) string {
	if q == nil || q.Pending <= 0 {
		return "The approvals queue needs attention"
	}
	return fmt.Sprintf("%s waiting for a decision in Approvals", countOf(q.Pending, "request"))
}

// approvalQueueBody is what the subject has no room for: how long the oldest
// has waited, how many are past the stale age, and what waiting costs.
func approvalQueueBody(q *notification.ReviewQueue) string {
	if q == nil {
		return ""
	}
	sentences := []string{}
	if q.OldestAgeDays > 0 {
		sentences = append(sentences, fmt.Sprintf("The oldest has been waiting %s.", countOf(q.OldestAgeDays, "day")))
	}
	if q.StaleCount > 0 && q.StaleAfterDays > 0 {
		sentences = append(sentences, fmt.Sprintf("%s been waiting %s or more.",
			countOf(q.StaleCount, "request")+has(q.StaleCount), countOf(q.StaleAfterDays, "day")))
	}
	sentences = append(sentences,
		"Each one is a call a policy held, or a version of a script that waits to run, until someone it names or an administrator decides it.")
	return strings.Join(sentences, " ")
}
