package notifyrender

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// scriptReviewLinkText labels the script review-queue alert's button.
const scriptReviewLinkText = "Open the script review queue"

// scriptReviewSubject is the subject and heading of a script review-queue
// alert (#1287): the one number an operator acts on, in the line an inbox
// shows without opening the message.
//
// A payload with no rollup still renders a meaningful line. A queue row
// outlives the check that wrote it, so an alert enqueued by an older build and
// delivered by this one arrives here with a nil Review.
func scriptReviewSubject(q *notification.ReviewQueue) string {
	if q == nil || q.Pending <= 0 {
		return "The script review queue needs attention"
	}
	return fmt.Sprintf("%s awaiting approval", countOf(q.Pending, "script version"))
}

// scriptReviewBody is the alert's prose body: what an unworked script queue
// actually costs, which is not the same cost an unworked insight queue carries.
// It renders unquoted (emailItem.Body), because the platform is speaking here,
// not a colleague.
func scriptReviewBody(q *notification.ReviewQueue) string {
	if q == nil {
		return ""
	}
	sentences := []string{}
	if q.OldestAgeDays > 0 {
		sentences = append(sentences,
			fmt.Sprintf("The oldest has been waiting %s.", countOf(q.OldestAgeDays, "day")))
	}
	sentences = append(sentences,
		"Nothing runs unattended until somebody approves it, so a version waiting here is either automation that has never started or a correction to automation that is still running the code it was meant to replace.",
		"Approving binds the capabilities that version may use, so the review is where those are decided.")
	return strings.Join(sentences, " ")
}
