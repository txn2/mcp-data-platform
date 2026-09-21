package scriptdraft

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// Persisted states what one draft persisted, for the surface showing its
// outcome to put beside the lists that record it. noun is what that surface
// calls a draft ("draft", "dry run").
//
// It is one sentence for both surfaces because the reader acts on it: "nothing
// was persisted" on a draft that created a resource is the sentence a reader
// would act on wrongly, and two surfaces each writing their own had already
// disagreed with the run about platform.export (#1822).
func (o *Outcome) Persisted(noun string) string {
	var result *scriptrun.Result
	if o != nil {
		result = o.Result
	}
	msg := "Nothing was persisted. platform.export reported the shape of each output rather than writing it, " +
		"and a write-class platform.call would have been refused rather than made."
	if o != nil && o.AllowWrites {
		msg = allowedMessage(noun, result)
	}
	if result != nil && result.State != nil {
		msg += " platform.save_state reported the state a platform run would have saved and did not save it."
	}
	return msg
}

// allowedMessage states what a draft run with allow_writes wrote.
func allowedMessage(noun string, result *scriptrun.Result) string {
	var calls, written, previewed int
	if result != nil {
		calls = len(result.Writes)
		for _, e := range result.Exports {
			if e.Preview {
				previewed++
			} else {
				written++
			}
		}
	}
	msg := fmt.Sprintf("This %s was run with allow_writes.", noun)
	switch {
	case calls > 0 && written > 0:
		msg += fmt.Sprintf(" %s listed under writes, and %s listed under exports, persisted for real.",
			count(calls, "call"), count(written, "output"))
	case calls > 0:
		msg += fmt.Sprintf(" %s listed under writes persisted for real.", count(calls, "call"))
	case written > 0:
		msg += fmt.Sprintf(" %s listed under exports persisted for real.", count(written, "output"))
	default:
		msg += " It made no write."
	}
	if previewed > 0 {
		msg += " This deployment has nowhere to write platform.export outputs from a draft, so each one " +
			"marked preview reported its shape instead."
	}
	return msg
}

// count renders a count with its noun: "The 1 call", "The 2 outputs".
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("The 1 %s", noun)
	}
	return fmt.Sprintf("The %d %ss", n, noun)
}
