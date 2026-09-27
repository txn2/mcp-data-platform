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
		return "A scheduled automation failed"
	}
	return fmt.Sprintf("The scheduled automation %q failed", p.ItemTitle)
}

// scriptRunBody is the alert's prose body: what failed, and what the script had
// printed by then. It renders unquoted (emailItem.Body) because the platform is
// speaking here — the text it carries is a stack trace and a program's own
// output, not something a person wrote.
func scriptRunBody(p notification.Payload) string {
	sentences := []string{
		"The platform ran this automation on its schedule and the run did not finish.",
	}
	if p.ItemID != "" {
		sentences = append(sentences, fmt.Sprintf("Its run is %s.", p.ItemID))
	}
	sentences = append(sentences, scriptRunCauseSentence(p.Cause, p.Repeats))
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
	causeTransient  = "transient"
	causeMemory     = "memory"
	causeWorkerLost = "worker_lost"
	causePlatform   = "platform"
	causeState      = "state_conflict"
)

// repeatedFailure is how many runs in a row must fail the same way
// before the alert says the script needs correcting (#1935). A script that
// reads the outside world can fail once and succeed on its next run, so one
// failure is not taken as a defect in the script.
const repeatedFailure = 3

// scriptRunCauseSentence says what the failure means for the owner: whether
// there is something in the script to fix, and whether the next scheduled run
// is expected to go through. An upstream that was unavailable for a moment is
// not a bug (#1859), and neither is one script failure: the script raised it,
// but what it reacted to may have been outside it, so only a failure repeated
// word for word sends the owner to fix the script (#1935). A row with no cause
// predates the field and reads as a script failure.
func scriptRunCauseSentence(cause string, repeats int) string {
	switch cause {
	case causeUpstream:
		return "It failed because a service it called was unavailable or answered with an error: it timed out, " +
			"dropped the connection, kept refusing after the platform waited and retried, or answered the " +
			"script's last call with a server error just before the script stopped. This is usually temporary " +
			"and there is nothing in the script to fix; the next scheduled run will try again." + repeatedNote(repeats)
	case causeTransient:
		return "The script reported this failure as temporary, so there is nothing in it to fix yet; the next " +
			"scheduled run will try again." + repeatedNote(repeats)
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
		if repeats >= repeatedFailure {
			return fmt.Sprintf("It has now failed %d runs in a row the same way, so the script needs "+
				"correcting: find the cause in the failure below, fix it, dry-run it and save it.", repeats)
		}
		return "The script raised this failure. If it depends on something outside the script, such as a " +
			"service that answered with an error or data that changed, the next scheduled run may succeed; " +
			"if the same failure repeats, the script needs correcting."
	}
}

// repeatedNote adds, to a failure that is usually temporary, that this one has
// not been: the owner is the one who can find out why it keeps happening.
func repeatedNote(repeats int) string {
	if repeats < repeatedFailure {
		return ""
	}
	return fmt.Sprintf(" It has now failed %d runs in a row the same way, which is worth looking into.", repeats)
}
