package script

import "strings"

// The sentences a completed act on a script is reported with.
//
// Every one of these acts has two surfaces: the manage_script tool an agent
// calls and the portal route a person's browser calls. They perform the same
// act through the same store, so they owe the caller the same account of what
// happened, and an agent relaying the act to a person needs exactly what the
// person would have read (#1593). Composing each sentence here rather than
// beside each handler is what keeps the two from drifting: before this, the
// tool answered a delete with a bare status while the route explained the
// cascade, and the two save messages had grown apart word by word.

// Removed is what one delete took with the script, as the store found it at
// the moment of the removal. Versions are not a field: a script always has at
// least the version its creation wrote, so a delete always takes saved
// versions with it.
//
// The zero value is a script that carried nothing but its own history, which
// is what a delete of a script that was never scheduled, never ran and never
// saved state reports.
type Removed struct {
	// Schedule is true when a cadence row went with the script.
	Schedule bool
	// Runs is true when the script had run history.
	Runs bool
	// State is true when the script carried a state object between runs.
	State bool
}

// DeleteMessage states the consequence of removing a script, both halves of
// it. What went is the part a person is warned about; what stayed is the part
// they are most likely to be wrong about, because "delete the script" reads to
// many people as "delete the reports it wrote".
//
// It names only what the script actually had: telling somebody their schedule
// and their carried state were destroyed when the script had neither is the
// same defect as saying nothing, one direction over.
//
// What stayed is stated unconditionally. Whether a script produced any assets
// is a portal-store question, and the tool surface cannot ask it; making the
// clause conditional on one surface would break the one thing this function
// exists to hold, which is that both surfaces say the same words.
func DeleteMessage(name string, rm Removed) string {
	went := []string{"its saved versions"}
	if rm.Schedule {
		went = append(went, "its schedule")
	}
	if rm.Runs {
		went = append(went, "its run history")
	}
	if rm.State {
		went = append(went, "the state it carried")
	}
	return name + " is gone, with " + joinAnd(went) + ". " +
		"The assets and resources it wrote remain, and they still record that it wrote them."
}

// joinAnd renders a list as prose: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// SavedMessage states what a saved version means for whether anything will run
// it. The run gate is re-read at execution, so a save onto a disabled or
// retired script produces a version that nothing executes, and that is the one
// thing an author needs told back.
func SavedMessage(sc *Script) string {
	if err := RefuseRun(sc); err != nil {
		return "Saved. Nothing executes this script: " + err.Error() + "."
	}
	return "Saved, and this version is what runs now: it presents the roles you held when you saved it, " +
		"and any schedule fires it."
}

// StateResetMessage states what a person's reset of the carried state means
// for the next run and for a run already in flight. Both halves matter: the
// reset is the recovery from a wrong watermark, and the in-flight failure is
// correct rather than a fault, because the reset was after that run's premise.
func StateResetMessage(cleared bool) string {
	if cleared {
		return "State cleared. The next run starts from {}; " +
			"a run already in flight that read the previous revision fails at its write."
	}
	return "State replaced. The next run reads this object; " +
		"a run already in flight that read the previous revision fails at its write."
}
