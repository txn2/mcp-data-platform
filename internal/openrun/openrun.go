// Package openrun is the refusal a script set to run one at a time answers
// with (#1986): the open run a new run of the script collides with, the error
// that names it, and the words a skipped fire's reason and a refused run both
// use. It is its own package so pkg/script's public surface does not carry it;
// the store raises it and the tool and the portal read it.
package openrun

import (
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ErrOpen marks a run refused because its script is exclusive and another of
// its runs is still open. The typed Error carrying the open run wraps it, so
// a caller matches on the sentinel and names the run from the rest.
var ErrOpen = errors.New("another run of this script is still open")

// BlockedMessage is the refusal of a save turning exclusive on while more
// than one run of the script is open: the index would hold neither of them to
// the setting, so the save waits until at most one is.
const BlockedMessage = "more than one run of this script is open, so it cannot be set to run one at a time yet; " +
	"wait for them to finish or cancel all but one, then save again"

// Run is the run an exclusive script is waiting on: enough for the caller to
// wait for it or cancel it.
type Run struct {
	ID        string
	Trigger   string
	Status    string
	StartedAt *time.Time
	CreatedAt time.Time
}

// Describe names the run, what started it and since when, in the words a
// refusal and a skipped fire's reason both use.
func (o Run) Describe() string {
	if o.StartedAt != nil {
		return fmt.Sprintf("run %s (started by %s, running since %s)",
			o.ID, triggerWords(o.Trigger), o.StartedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("run %s (started by %s, queued since %s)",
		o.ID, triggerWords(o.Trigger), o.CreatedAt.UTC().Format(time.RFC3339))
}

// triggerWords is a trigger as a person reads it.
func triggerWords(trigger string) string {
	switch trigger {
	case script.TriggerTool:
		return "run_script"
	case script.TriggerSchedule:
		return "its schedule"
	case script.TriggerPortal:
		return "the portal"
	default:
		return trigger
	}
}

// Error is one refused run of an exclusive script.
type Error struct {
	Open Run
}

// Error names the open run and what the caller can do about it.
func (e *Error) Error() string {
	return fmt.Sprintf("this script runs one at a time and %s is still open; "+
		"nothing was queued: wait for it to finish or cancel it, then run again", e.Open.Describe())
}

// Unwrap lets errors.Is match the sentinel.
func (*Error) Unwrap() error { return ErrOpen }
