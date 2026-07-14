package report

import (
	"strings"
	"testing"
)

// baselineResults builds a two-suite baseline standing in for a committed run.
func baselineResults() *Results {
	return &Results{
		Manifest: Manifest{Arm: "a2", PlatformVersion: "v1.102.0-baseline"},
		Suites: []SuiteSummary{
			{Suite: "s1", Graded: 51, Accuracy: 1.00, PassKRate: 1.00, MedianToolCalls: 8},
			{Suite: "s3", Graded: 75, Accuracy: 0.99, PassKRate: 0.98, MedianToolCalls: 10},
		},
	}
}

func TestCheckRegressionNoRegressionWhenEqual(t *testing.T) {
	base := baselineResults()
	// A candidate identical to the baseline never regresses.
	cand := baselineResults()
	cand.Manifest.PlatformVersion = "v1.102.0-candidate"
	if regs := CheckRegression(cand, base, DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("equal candidate regressed: %+v", regs)
	}
}

func TestCheckRegressionToleratesSmallNoise(t *testing.T) {
	base := baselineResults()
	cand := baselineResults()
	// One-point accuracy wobble and a couple extra calls are within thresholds.
	cand.Suites[1].Accuracy = 0.98
	cand.Suites[0].MedianToolCalls = 9
	if regs := CheckRegression(cand, base, DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("in-threshold noise flagged: %+v", regs)
	}
}

// TestCheckRegressionFailsOnDegradedConfig is the committed, no-API demonstration
// that the gate trips on a real capability loss: a candidate where the knowledge
// suite collapses (as it would if enrichment/search broke) and the discovery
// suite needs far more tool calls must produce regressions.
func TestCheckRegressionFailsOnDegradedConfig(t *testing.T) {
	base := baselineResults()
	cand := baselineResults()
	cand.Manifest.PlatformVersion = "v1.102.0-degraded"
	cand.Suites[1].Accuracy = 0.43      // s3 collapses back to the bare-tools level
	cand.Suites[1].PassKRate = 0.40     // pass^k collapses with it
	cand.Suites[0].MedianToolCalls = 16 // s1 flails: 8 -> 16 (2x, over the 1.25 ceiling)

	regs := CheckRegression(cand, base, DefaultThresholds())
	got := map[string]bool{}
	for _, r := range regs {
		got[r.Suite+"/"+r.Metric] = true
	}
	for _, want := range []string{"s3/accuracy", "s3/pass_k", "s1/median_tool_calls"} {
		if !got[want] {
			t.Errorf("degraded config did not trip %s; got %+v", want, regs)
		}
	}
	report := RegressionReport(cand, base, DefaultThresholds(), regs)
	if !strings.Contains(report, "FAIL") {
		t.Errorf("report missing FAIL header:\n%s", report)
	}
}

func TestCheckRegressionFlagsDroppedSuite(t *testing.T) {
	base := baselineResults()
	cand := &Results{
		Manifest: Manifest{Arm: "a2"},
		Suites:   []SuiteSummary{{Suite: "s1", Accuracy: 1.00, PassKRate: 1.00, MedianToolCalls: 8}},
	}
	regs := CheckRegression(cand, base, DefaultThresholds())
	var found bool
	for _, r := range regs {
		if r.Suite == "s3" && r.Metric == "coverage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dropped suite not flagged as coverage regression: %+v", regs)
	}
}

func TestBaselineCompatible(t *testing.T) {
	base := baselineResults()
	if err := BaselineCompatible(baselineResults(), base); err != nil {
		t.Fatalf("same-arm same-client compatible baseline rejected: %v", err)
	}
	// Different arm: refuse.
	cand := baselineResults()
	cand.Manifest.Arm = "a0"
	if err := BaselineCompatible(cand, base); err == nil {
		t.Error("cross-arm baseline accepted")
	}
	// Different client path (claude-cli vs anthropic): refuse.
	cand = baselineResults()
	cand.Manifest.ClientVersion = "claude 2.1.0"
	if err := BaselineCompatible(cand, base); err == nil {
		t.Error("cross-client baseline accepted")
	}
	// Baseline with nothing graded: refuse.
	empty := &Results{Manifest: Manifest{Arm: "a2"}, Suites: []SuiteSummary{{Suite: "s1", Graded: 0}}}
	if err := BaselineCompatible(baselineResults(), empty); err == nil {
		t.Error("baseline with no graded suites accepted")
	}
}

func TestCheckRegressionSuiteFilterSkipsCoverage(t *testing.T) {
	base := baselineResults() // s1 + s3
	// A candidate run with -suite s1 legitimately omits s3; it must not be
	// reported as a coverage regression.
	cand := &Results{
		Manifest: Manifest{Arm: "a2", Suite: "s1"},
		Suites:   []SuiteSummary{{Suite: "s1", Accuracy: 1.00, PassKRate: 1.00, MedianToolCalls: 8, Graded: 10}},
	}
	if regs := CheckRegression(cand, base, DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("suite-filtered candidate flagged: %+v", regs)
	}
}

func TestCheckRegressionSkipsUngradedBaselineSuite(t *testing.T) {
	base := &Results{Manifest: Manifest{Arm: "a2"}, Suites: []SuiteSummary{
		{Suite: "s3", Graded: 0, Accuracy: 0}, // degenerate: nothing graded
	}}
	// Candidate accuracy of 0 on s3 must NOT trip a regression against a baseline
	// suite that has nothing graded (no valid comparison point).
	cand := &Results{Manifest: Manifest{Arm: "a2"}, Suites: []SuiteSummary{
		{Suite: "s3", Graded: 10, Accuracy: 0},
	}}
	if regs := CheckRegression(cand, base, DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("ungraded baseline suite produced a regression: %+v", regs)
	}
}

func TestRegressionReportPassMessage(t *testing.T) {
	base := baselineResults()
	out := RegressionReport(base, base, DefaultThresholds(), nil)
	if !strings.Contains(out, "PASS") {
		t.Errorf("clean report missing PASS:\n%s", out)
	}
}
