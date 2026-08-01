package notifyrender

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// reviewQueueLinkText labels the review-queue alert's button. The generic
// label would read "Open "Knowledge review queue"", which quotes a place
// rather than naming the action.
const reviewQueueLinkText = "Open the review queue"

// reviewQueueSubject is the subject and heading of a knowledge review-queue
// staleness alert (#803): the one number an operator acts on, in the line an
// inbox shows without opening the message.
//
// A payload with no rollup still renders a meaningful line. That is not a
// theoretical case: a queue row outlives the check that wrote it, so an alert
// enqueued by an older build and delivered by this one arrives here with a nil
// Review.
func reviewQueueSubject(q *notification.ReviewQueue) string {
	if q == nil || q.Pending <= 0 {
		return "The knowledge review queue needs attention"
	}
	return fmt.Sprintf("%s awaiting review in the knowledge queue", countOf(q.Pending, "insight"))
}

// reviewQueueBody is the alert's prose body: the staleness the subject line
// has no room for, and what it costs. It renders unquoted (emailItem.Body),
// because the platform is speaking here, not a colleague.
func reviewQueueBody(q *notification.ReviewQueue) string {
	if q == nil {
		return ""
	}
	sentences := []string{}
	if q.OldestAgeDays > 0 {
		sentences = append(sentences,
			fmt.Sprintf("The oldest has been waiting %s.", countOf(q.OldestAgeDays, "day")))
	}
	if q.StaleCount > 0 && q.StaleAfterDays > 0 {
		sentences = append(sentences, fmt.Sprintf("%s been pending for %s or more.",
			countOf(q.StaleCount, "insight")+has(q.StaleCount), countOf(q.StaleAfterDays, "day")))
	}
	sentences = append(sentences,
		"Insights stay proposals until they are reviewed, so an unworked queue is knowledge your team has already captured but cannot use.")
	return strings.Join(sentences, " ")
}

// countOf renders a count with its noun, pluralized by adding "s" -- correct
// for every noun this package counts (insight, day).
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// has agrees the verb with a count rendered by countOf.
func has(n int) string {
	if n == 1 {
		return " has"
	}
	return " have"
}
