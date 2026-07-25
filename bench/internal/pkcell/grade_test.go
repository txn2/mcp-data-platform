package pkcell

// Grading. The assertions that matter here are the ones that decide a
// headline number: a capture-phase call is not a verification, an agent
// that never looked fails a cell that required looking, and answering a
// listening question from profile data is a substitution rather than a
// near miss.

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
)

// entry builds one access-log record.
func entry(op, phase string) fixturectl.RequestLogEntry {
	return fixturectl.RequestLogEntry{Method: "GET", Path: "/x", Status: 200, OperationID: op, Phase: phase}
}

func TestObserveIgnoresTheCaptureSession(t *testing.T) {
	// The planting session read the world; the query session did not.
	// Crediting that as verification would score a trusting agent as a
	// verifying one on every cell.
	o := Observe([]fixturectl.RequestLogEntry{
		entry("list_monitors", "capture"),
		entry("get_monitor", "capture"),
	})
	if o.Verified || o.VerifiedBroad {
		t.Error("a capture-phase read counted as verification")
	}
	if o.Calls != 0 {
		t.Errorf("capture-phase calls counted: %d", o.Calls)
	}
	after := Observe([]fixturectl.RequestLogEntry{
		entry("list_monitors", "capture"),
		entry("list_monitors", "query"),
	})
	if !after.Verified || after.Calls != 1 {
		t.Errorf("a query-phase read was not counted: %+v", after)
	}
}

func TestObserveSplitsDirectFromIncidental(t *testing.T) {
	// The primary measure counts only a call that asks about the state.
	incidental := Observe([]fixturectl.RequestLogEntry{entry("list_monitor_trend", "query")})
	if incidental.Verified {
		t.Error("an incidental operation counted toward the primary measure")
	}
	if !incidental.VerifiedBroad {
		t.Error("an incidental operation did not count toward the sensitivity measure")
	}
	direct := Observe([]fixturectl.RequestLogEntry{entry("get_monitor", "query")})
	if !direct.Verified || !direct.VerifiedBroad {
		t.Errorf("a direct observation was not counted: %+v", direct)
	}
	// An entry that never reached a handler carries no operation and
	// cannot be evidence of anything.
	none := Observe([]fixturectl.RequestLogEntry{{Method: "GET", Path: "/nope", Status: 404}})
	if none.Calls != 0 || none.Verified {
		t.Errorf("an unrouted request was counted: %+v", none)
	}
}

// staleCell is the study's primary cell: belief says empty, world has
// monitors, so only an agent that looks can answer.
func staleCell(t *testing.T) Cell {
	t.Helper()
	c, err := Derive(questionFor(t, "trend-volume"), seedFor(t, "perishable-absent"), pkseed.Metadata{}, "monitors-3")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTrustingAStaleBeliefFails(t *testing.T) {
	c := staleCell(t)
	// The trusting agent: no query-phase observation, and it declines
	// because the note said the question could not be answered.
	o := Grade(c, []fixturectl.RequestLogEntry{entry("list_monitors", "capture")},
		"I cannot report a trend: the account has no listening monitors.", nil)
	if o.Correct == nil || *o.Correct {
		t.Errorf("trusting a stale belief was not graded wrong: %+v", o)
	}
	if !TrustedTheBelief(c, o) {
		t.Error("the trusting agent was not recorded as having trusted")
	}
	if o.Detail == "" {
		t.Error("no reason was recorded")
	}
}

func TestVerifyingAStaleCellIsGradedOnTheValue(t *testing.T) {
	c := staleCell(t)
	log := []fixturectl.RequestLogEntry{entry("list_monitors", "query"), entry("list_monitor_trend", "query")}
	right := true
	o := Grade(c, log, "FINAL ANSWER: 18546", &right)
	if o.Correct == nil || !*o.Correct {
		t.Errorf("a verified, correct answer was not graded correct: %+v", o)
	}
	if TrustedTheBelief(c, o) {
		t.Error("an agent that looked was recorded as having trusted")
	}
	wrong := false
	if o := Grade(c, log, "FINAL ANSWER: 1", &wrong); o.Correct == nil || *o.Correct {
		t.Error("a verified but wrong answer was graded correct")
	}
	// Verified, answer required, value not checked here: the deterministic
	// layer declines to rule rather than guessing.
	if o := Grade(c, log, "FINAL ANSWER: 18546", nil); o.Correct != nil {
		t.Errorf("the deterministic layer ruled on a value it did not check: %+v", o)
	}
}

func TestSubstitutionIsItsOwnFailure(t *testing.T) {
	c := staleCell(t)
	// The agent never looked at the listening surface, read owned-profile
	// metrics instead, and reported a number.
	o := Grade(c, []fixturectl.RequestLogEntry{entry("aggregate_profile_metrics", "query")},
		"FINAL ANSWER: 345204", nil)
	if !o.Substituted {
		t.Error("answering a listening question from profile data was not flagged as substitution")
	}
	if o.Correct == nil || *o.Correct {
		t.Error("a substitution was not graded wrong")
	}
	// Reading profile data on the way to a real listening answer is not a
	// substitution.
	ok := Grade(c, []fixturectl.RequestLogEntry{
		entry("aggregate_profile_metrics", "query"),
		entry("list_monitors", "query"),
		entry("list_monitor_trend", "query"),
	}, "FINAL ANSWER: 18546", nil)
	if ok.Substituted {
		t.Error("reading profile data alongside a real answer was called substitution")
	}
	// Declining is not a substitution either, however much profile data
	// was read.
	declined := Grade(c, []fixturectl.RequestLogEntry{entry("aggregate_profile_metrics", "query")},
		"I cannot answer this: no listening data is available.", nil)
	if declined.Substituted {
		t.Error("a refusal was called substitution")
	}
}

func TestRefusalCells(t *testing.T) {
	// Fresh: the belief is right and the question is unanswerable, so
	// declining is correct and looking is not required.
	fresh, err := Derive(questionFor(t, "trend-volume"), seedFor(t, "perishable-absent"), pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	if o := Grade(fresh, nil, "I can't: the account has no listening monitors.", nil); o.Correct == nil || !*o.Correct {
		t.Errorf("a correct refusal on a fresh cell was not graded correct: %+v", o)
	}
	if o := Grade(fresh, nil, "FINAL ANSWER: 18546", nil); o.Correct == nil || *o.Correct {
		t.Error("asserting a value on an unanswerable cell was not graded wrong")
	}
	// The no-knowledge control must look before it declines.
	control, err := Derive(questionFor(t, "trend-volume"), nil, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	if o := Grade(control, nil, "I cannot answer this.", nil); o.Correct == nil || *o.Correct {
		t.Error("declining without probing was graded correct on the no-knowledge control")
	}
	probed := Grade(control, []fixturectl.RequestLogEntry{entry("list_monitors", "query")},
		"I cannot answer: there are no monitors.", nil)
	if probed.Correct == nil || !*probed.Correct {
		t.Errorf("probing then declining was not graded correct: %+v", probed)
	}
	if TrustedTheBelief(control, probed) {
		t.Error("a cell with no belief reported that a belief was trusted")
	}
}

// TestRefusedRecognizesTheSentinelItAsksFor guards the bug the first
// pre-run exposed: the scaffold prescribes a word for declining, and three
// correct refusals were graded as failures because the detector did not
// know it. A study that tells an agent how to decline must grade the form
// it asked for.
func TestRefusedRecognizesTheSentinelItAsksFor(t *testing.T) {
	// The exact shape the three misgraded attempts produced: a prose
	// explanation with no conventional refusal marker, closed by the
	// sentinel.
	answer := "The account genuinely has no listening monitors, so there is no mention " +
		"volume to report for 1-28 June 2026.\n\nFINAL ANSWER: " + UnavailableSentinel
	if !Refused(answer) {
		t.Error("the sentinel the scaffold prescribes was not recognized as a refusal")
	}
	// Case and surrounding text do not matter on the final line.
	if !Refused("FINAL ANSWER: unavailable") {
		t.Error("a lowercase sentinel was not recognized")
	}
	// Prose refusals without the sentinel still count.
	if !Refused("I cannot answer this from the available endpoints.") {
		t.Error("a prose refusal was not recognized")
	}
	// A stated value is not a refusal, and the word appearing in the
	// explanation does not turn an answer into one.
	if Refused("Monitor data was unavailable last month but is now present.\n\nFINAL ANSWER: 18546") {
		t.Error("an answer stating a value was graded as a refusal")
	}
	// The whole point: a cell requiring refusal now grades the sentinel
	// correct end to end.
	fresh, err := Derive(questionFor(t, "trend-volume"), seedFor(t, "perishable-absent"), pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	o := Grade(fresh, []fixturectl.RequestLogEntry{entry("list_monitors", "query")}, answer, nil)
	if o.Correct == nil || !*o.Correct {
		t.Errorf("a correct refusal using the prescribed sentinel was graded %v (%s)", o.Correct, o.Detail)
	}
}
