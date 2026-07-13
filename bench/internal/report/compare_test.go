package report

import (
	"strings"
	"testing"
)

// armResultsFixture builds a one-arm Results where every s3 attempt tagged with
// the given trap class is correct iff correct is true, at k=2.
func armResultsFixture(arm string, s3Correct bool) *Results {
	r := &Results{Manifest: Manifest{Arm: arm, Model: "claude-x", LLMProvider: "anthropic", Seed: 930, TaskSetHash: "hash", K: 2}}
	// Two s1 tasks (always correct) and two s3 trap tasks (correct = s3Correct),
	// each at k=2.
	add := func(id, suite string, traps []string, correct bool) {
		for k := 1; k <= 2; k++ {
			r.Attempts = append(r.Attempts, Attempt{
				TaskID: id, Suite: suite, TrapClasses: traps, Attempt: k,
				Correct: correct, ToolCalls: 8, WallMS: 20000,
			})
		}
	}
	add("s1-a", "s1", nil, true)
	add("s1-b", "s1", nil, true)
	add("s3-units", "s3", []string{"units_cents"}, s3Correct)
	add("s3-net", "s3", []string{"net_revenue"}, s3Correct)
	r.Aggregate()
	return r
}

func TestNewComparisonAccuracyAndDelta(t *testing.T) {
	a0 := armResultsFixture("a0", false) // fails traps
	a2 := armResultsFixture("a2", true)  // passes traps
	c := NewComparison([]*Results{a2, a0})

	if got := c.Arms; len(got) != 2 || got[0] != "a0" || got[1] != "a2" {
		t.Fatalf("arms = %v, want [a0 a2]", got)
	}
	if c.Baseline != "a0" {
		t.Errorf("baseline = %q, want a0", c.Baseline)
	}
	// s3 accuracy: a0 = 0, a2 = 1.
	s3 := c.SuiteCells["s3"]
	if s3[0].Accuracy != 0 || s3[1].Accuracy != 1 {
		t.Errorf("s3 accuracy a0=%.2f a2=%.2f, want 0 and 1", s3[0].Accuracy, s3[1].Accuracy)
	}
	// pass^k: a0 fails traps so 0; a2 passes so 1.
	if s3[0].PassKRate != 0 || s3[1].PassKRate != 1 {
		t.Errorf("s3 passK a0=%.2f a2=%.2f, want 0 and 1", s3[0].PassKRate, s3[1].PassKRate)
	}
	// Delta a2 vs a0 on s3 is +1.00 (100 points).
	var s3delta *Delta
	for i := range c.Deltas {
		if c.Deltas[i].Suite == "s3" && c.Deltas[i].Arm == "a2" {
			s3delta = &c.Deltas[i]
		}
	}
	if s3delta == nil || s3delta.Points < 0.99 {
		t.Fatalf("s3 delta = %+v, want ~+1.0", s3delta)
	}
}

func TestTrapBreakdown(t *testing.T) {
	a0 := armResultsFixture("a0", false)
	a2 := armResultsFixture("a2", true)
	c := NewComparison([]*Results{a0, a2})
	if len(c.TrapClasses) != 2 {
		t.Fatalf("trap classes = %v, want 2", c.TrapClasses)
	}
	for _, class := range c.TrapClasses {
		cells := c.TrapCells[class]
		if cells[0].Accuracy != 0 || cells[1].Accuracy != 1 {
			t.Errorf("trap %s: a0=%.2f a2=%.2f, want 0 and 1", class, cells[0].Accuracy, cells[1].Accuracy)
		}
	}
}

func TestBootstrapReproducible(t *testing.T) {
	a0 := armResultsFixture("a0", false)
	a2 := armResultsFixture("a2", true)
	c1 := NewComparison([]*Results{a0, a2})
	c2 := NewComparison([]*Results{a0, a2})
	// The fixed seed must produce identical CIs on identical inputs.
	for suite, cells1 := range c1.SuiteCells {
		cells2 := c2.SuiteCells[suite]
		for i := range cells1 {
			if cells1[i].CILow != cells2[i].CILow || cells1[i].CIHigh != cells2[i].CIHigh {
				t.Errorf("suite %s arm %s CI not reproducible: %v vs %v", suite, cells1[i].Arm, cells1[i], cells2[i])
			}
		}
	}
}

func TestComparisonMarkdown(t *testing.T) {
	a0 := armResultsFixture("a0", false)
	a2 := armResultsFixture("a2", true)
	md := NewComparison([]*Results{a0, a2}).Markdown()
	for _, want := range []string{
		"# Agent-effectiveness benchmark",
		"## Manifest",
		"## Overall accuracy",
		"## Accuracy by suite",
		"## S3 knowledge-trap accuracy by class",
		"## Accuracy delta vs a0",
		"## Caveats",
		"units_cents",
		"net_revenue",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestHumanTable(t *testing.T) {
	a0 := armResultsFixture("a0", false)
	a2 := armResultsFixture("a2", true)
	tbl := NewComparison([]*Results{a0, a2}).HumanTable()
	for _, want := range []string{"baseline a0", "s1", "s3", "a0", "a2"} {
		if !strings.Contains(tbl, want) {
			t.Errorf("human table missing %q:\n%s", want, tbl)
		}
	}
}

func TestPickBaselineNoA0(t *testing.T) {
	// With no a0 arm, the first arm by name is the baseline.
	a1 := armResultsFixture("a1", false)
	a2 := armResultsFixture("a2", true)
	c := NewComparison([]*Results{a2, a1})
	if c.Baseline != "a1" {
		t.Errorf("baseline = %q, want a1 (first arm when a0 absent)", c.Baseline)
	}
}

func TestAccWithCIEmpty(t *testing.T) {
	if got := accWithCI(Cell{Graded: 0}); got != "—" {
		t.Errorf("accWithCI(empty) = %q, want em dash", got)
	}
}

func TestTaskSetDriftWarning(t *testing.T) {
	a0 := armResultsFixture("a0", false)
	a2 := armResultsFixture("a2", true)
	a2.Manifest.TaskSetHash = "different" // simulate a drifted run
	md := NewComparison([]*Results{a0, a2}).Markdown()
	if !strings.Contains(md, "WARNING") || !strings.Contains(md, "task-set hash differs") {
		t.Errorf("expected a task-set drift warning in:\n%s", md)
	}
}
