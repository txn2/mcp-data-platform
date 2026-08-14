package notifyrender

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// scriptRunSubject is the subject and heading of a failed scheduled run
// (#1286). It names the script, because that is what the recipient owns and
// what they will go and look at; the run id is in the body.
//
// A payload with no title still renders a meaningful line: a queue row outlives
// the check that wrote it, so a row enqueued by one build may be delivered by
// another.
func scriptRunSubject(p notification.Payload) string {
	if p.ItemTitle == "" {
		return "A scheduled script failed"
	}
	return fmt.Sprintf("The scheduled script %q failed", p.ItemTitle)
}

// scriptRunBody is the alert's prose body: what failed, and what the script had
// printed by then. It renders unquoted (emailItem.Body) because the platform is
// speaking here — the text it carries is a stack trace and a program's own
// output, not something a person wrote.
func scriptRunBody(p notification.Payload) string {
	sentences := []string{
		"The platform ran this script on its schedule and the run did not finish.",
	}
	if p.ItemID != "" {
		sentences = append(sentences, fmt.Sprintf("Its run is %s.", p.ItemID))
	}
	sentences = append(sentences,
		"A script failure is never retried: the same version on the same inputs fails the same way, so the schedule will try again at its next fire and fail again until the script is corrected and the correction approved.")
	body := strings.Join(sentences, " ")
	if detail := strings.TrimSpace(p.Message); detail != "" {
		body += "\n\n" + detail
	}
	return body
}
