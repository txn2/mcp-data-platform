package main

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/graphprobe"
)

// studyCell builds a two-constraint cell: one ordinary off-entry constraint
// and one discontinuity constraint, so the tally's split is observable.
func studyCell() graphfix.CompletionCell {
	return graphfix.CompletionCell{
		ID:       "cell",
		EntryKey: "entry",
		Constraints: []graphfix.Constraint{
			{ID: "plain", Pages: []string{"a"}},
			{ID: "disc", Pages: []string{"b"}, Discontinuity: true},
		},
	}
}

// TestStudyTallySplitsDiscontinuityAndClaims: the summary's discontinuity
// column counts only discontinuity constraints, grounded only; the claim
// columns read the graded claim, and a failed attempt counts nowhere else.
func TestStudyTallySplitsDiscontinuityAndClaims(t *testing.T) {
	t.Parallel()
	cell := studyCell()
	var tl studyTally
	tl.add(graphprobe.CompletionAttempt{
		Coverage: graphprobe.Coverage{
			Constraints: []graphprobe.ConstraintResult{
				{ID: "plain", Covered: true, Grounded: true},
				{ID: "disc", Covered: true, Grounded: true},
			},
			OffEntryTotal: 2, OffEntryCovered: 2, OffEntryGrounded: 2,
		},
		Claim:     &graphprobe.CompletenessClaim{Stated: true, Complete: true},
		Overclaim: false,
	}, cell)
	tl.add(graphprobe.CompletionAttempt{
		Coverage: graphprobe.Coverage{
			Constraints: []graphprobe.ConstraintResult{
				{ID: "plain", Covered: true, Grounded: false},
				{ID: "disc"},
			},
			OffEntryTotal: 2, OffEntryCovered: 1, UnreadCovered: 1,
		},
		Claim:     &graphprobe.CompletenessClaim{Stated: true, Complete: true},
		Overclaim: true,
	}, cell)
	tl.add(graphprobe.CompletionAttempt{
		Coverage: graphprobe.Coverage{
			Constraints:   []graphprobe.ConstraintResult{{ID: "plain"}, {ID: "disc"}},
			OffEntryTotal: 2,
		},
		Claim: &graphprobe.CompletenessClaim{},
	}, cell)
	tl.add(graphprobe.CompletionAttempt{Error: "episode: lost"}, cell)
	switch {
	case tl.n != 3 || tl.failed != 1:
		t.Errorf("n=%d failed=%d, want 3 and 1", tl.n, tl.failed)
	case tl.discTotal != 3 || tl.discGrounded != 1:
		t.Errorf("disc %d/%d, want 1/3", tl.discGrounded, tl.discTotal)
	case tl.complete != 2 || tl.overclaim != 1 || tl.noStatement != 1:
		t.Errorf("complete=%d overclaim=%d nostmt=%d, want 2, 1, 1", tl.complete, tl.overclaim, tl.noStatement)
	case tl.unread != 1:
		t.Errorf("unread=%d, want 1", tl.unread)
	}
	if got := ratio(tl.discGrounded, tl.discTotal); got != "0.33" {
		t.Errorf("ratio = %s, want 0.33", got)
	}
	if got := ratio(0, 0); got != "-" {
		t.Errorf("empty-denominator ratio = %s, want -", got)
	}
}

// TestResultErrorFailsARunWithNoResults: a run whose every attempt failed
// must exit non-zero, or a driver would reset the corpus and report a
// matrix cell that does not exist.
func TestResultErrorFailsARunWithNoResults(t *testing.T) {
	t.Parallel()
	if err := resultError(nil); err == nil {
		t.Error("nil results accepted")
	}
	if err := resultError(&graphprobe.CompletionResults{}); err == nil {
		t.Error("empty attempt list accepted")
	}
	allFailed := &graphprobe.CompletionResults{Attempts: []graphprobe.CompletionAttempt{
		{CellID: "a", Error: "episode: lost"}, {CellID: "b", Error: "episode: lost"},
	}}
	if err := resultError(allFailed); err == nil {
		t.Error("all-failed run accepted")
	}
	oneResult := &graphprobe.CompletionResults{Attempts: []graphprobe.CompletionAttempt{
		{CellID: "a", Error: "episode: lost"}, {CellID: "b"},
	}}
	if err := resultError(oneResult); err != nil {
		t.Errorf("run with a surviving result refused: %v", err)
	}
}
