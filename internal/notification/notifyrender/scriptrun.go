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
	sentences = append(sentences, scriptRunCauseSentence(p.Cause))
	body := strings.Join(sentences, " ")
	if detail := strings.TrimSpace(p.Message); detail != "" {
		body += "\n\n" + detail
	}
	return body
}

// Causes a script run failure is recorded with (script.Cause*, #1859). They are
// spelled here rather than imported: this package renders what a queue row
// says, and a row names its cause as a string.
const (
	causeUpstream   = "upstream"
	causeMemory     = "memory"
	causeWorkerLost = "worker_lost"
	causePlatform   = "platform"
	causeState      = "state_conflict"
)

// scriptRunCauseSentence says what the failure means for the owner: whether
// there is something in the script to fix, and whether the next scheduled run
// is expected to go through. Only a script error sends them looking for a bug;
// an upstream that was unavailable for a moment is not one (#1859). A row with
// no cause predates the field and reads as a script error, which is what every
// failure was recorded as then.
func scriptRunCauseSentence(cause string) string {
	switch cause {
	case causeUpstream:
		return "It failed because a service it called was temporarily unavailable: it timed out, dropped the " +
			"connection, or kept refusing the request after the platform waited and retried. This is usually " +
			"temporary and there is nothing in the script to fix; the next scheduled run will try again."
	case causeMemory:
		return "It was stopped for holding more memory than a run is allowed. The next scheduled run will stop " +
			"the same way: page the work and export each page (platform.export with append=True), or hold less " +
			"of each result at once."
	case causeWorkerLost:
		return "The worker executing it stopped without reporting a result several times, most often because " +
			"the run used more memory than its replica has, so it is not run again. Page the work and export " +
			"each page, or ask an administrator for more memory."
	case causeState:
		return "Another run of the script saved its state first, so this run's save was refused; the outputs it " +
			"wrote stand, and the next scheduled run reads the state the other one saved."
	case causePlatform:
		return "The platform could not execute it: the run's session or the script itself could not be read. " +
			"There is nothing in the script to fix; the next scheduled run will try again."
	default:
		return "A script failure is not retried: the same version on the same inputs fails the same way, so the " +
			"next scheduled run will fail again until the script is corrected and the correction approved."
	}
}
