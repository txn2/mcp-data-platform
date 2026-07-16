package lifecycle

import (
	"strings"
	"testing"
)

func rate(num, den int) Rate {
	r := Rate{Num: num, Den: den}
	if den > 0 {
		r.Rate = float64(num) / float64(den)
	}
	return r
}

// baseMetrics is a healthy baseline scorecard: every headline rate high, no
// duplicates.
func baseMetrics() Metrics {
	return Metrics{
		Attempts:          10,
		CaptureRate:       rate(9, 10),
		PersonalRecall:    rate(9, 10),
		TransferRate:      rate(8, 10),
		UpdateCorrectness: rate(5, 5),
		// A current-harness run that scores duplicates always carries
		// update-capture data (the gate feeds the duplicate denominator).
		UpdateCaptureRate: rate(5, 5),
		AbstentionRate:    rate(9, 10),
		DuplicateRate:     rate(0, 5),
		PassK:             rate(7, 10),
	}
}

func lifecycleResults(arm, client string, m Metrics) *Results {
	return &Results{Manifest: Manifest{Arm: arm, ClientVersion: client}, Metrics: m}
}

func TestBaselineCompatible(t *testing.T) {
	base := lifecycleResults("a3", "", baseMetrics())
	if err := BaselineCompatible(lifecycleResults("a3", "", baseMetrics()), base); err != nil {
		t.Fatalf("same arm/client should be compatible: %v", err)
	}
	if err := BaselineCompatible(lifecycleResults("a2", "", baseMetrics()), base); err == nil {
		t.Fatal("arm mismatch should be refused")
	}
	if err := BaselineCompatible(lifecycleResults("a3", "claude 1.2", baseMetrics()), base); err == nil {
		t.Fatal("client-path mismatch (claude-cli candidate vs anthropic baseline) should be refused")
	}
	// Path parity, not exact version: two claude-cli runs on different CLI
	// versions are still comparable, so a benign version bump does not disable
	// the gate.
	claudeBase := lifecycleResults("a3", "claude 1.2", baseMetrics())
	if err := BaselineCompatible(lifecycleResults("a3", "claude 1.3", baseMetrics()), claudeBase); err != nil {
		t.Fatalf("two claude-cli versions should be comparable: %v", err)
	}
	if err := BaselineCompatible(lifecycleResults("a3", "", Metrics{}), lifecycleResults("a3", "", Metrics{})); err == nil {
		t.Fatal("an empty baseline should be refused")
	}
	// A baseline from before the update-capture gating of the duplicate rate
	// (duplicate denominator present, no update-capture data) counted capture
	// misses as duplicates; its duplicate numbers are definitionally inflated,
	// so gating against it must be refused, not silently absorbed.
	legacy := baseMetrics()
	legacy.UpdateCaptureRate = Rate{}
	if err := BaselineCompatible(lifecycleResults("a3", "", baseMetrics()), lifecycleResults("a3", "", legacy)); err == nil {
		t.Fatal("a pre-redefinition baseline (duplicates scored, no update-capture data) should be refused")
	}
	// A baseline with no supersede coverage at all (both denominators zero) is
	// not a legacy artifact — nothing about its duplicate numbers is stale.
	noSupersede := baseMetrics()
	noSupersede.DuplicateRate, noSupersede.UpdateCaptureRate = Rate{}, Rate{}
	if err := BaselineCompatible(lifecycleResults("a3", "", baseMetrics()), lifecycleResults("a3", "", noSupersede)); err != nil {
		t.Fatalf("a baseline without supersede coverage should be compatible: %v", err)
	}
}

func TestCheckRegressionClean(t *testing.T) {
	cand := lifecycleResults("a3", "", baseMetrics())
	if regs := CheckRegression(cand, lifecycleResults("a3", "", baseMetrics()), DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("identical candidate regressed: %+v", regs)
	}
	// A small drop within tolerance (5 pts) does not trip.
	m := baseMetrics()
	m.CaptureRate = rate(85, 100) // 90% -> 85%, exactly at the limit, not below
	if regs := CheckRegression(lifecycleResults("a3", "", m), lifecycleResults("a3", "", baseMetrics()), DefaultThresholds()); len(regs) != 0 {
		t.Fatalf("a drop to exactly the limit should not trip: %+v", regs)
	}
}

func TestCheckRegressionRateDrop(t *testing.T) {
	m := baseMetrics()
	m.TransferRate = rate(70, 100) // 80% -> 70%, a 10-pt drop past the 5-pt tolerance
	regs := CheckRegression(lifecycleResults("a3", "", m), lifecycleResults("a3", "", baseMetrics()), DefaultThresholds())
	if len(regs) != 1 || regs[0].Metric != "transfer_rate" || regs[0].LowerIsBetter {
		t.Fatalf("expected one transfer_rate drop, got %+v", regs)
	}
}

func TestCheckRegressionDuplicateIncrease(t *testing.T) {
	m := baseMetrics()
	m.DuplicateRate = rate(3, 5) // 0% -> 60%, a duplicate-rate rise (lower is better)
	regs := CheckRegression(lifecycleResults("a3", "", m), lifecycleResults("a3", "", baseMetrics()), DefaultThresholds())
	if len(regs) != 1 || regs[0].Metric != "duplicate_rate" || !regs[0].LowerIsBetter {
		t.Fatalf("expected one duplicate_rate increase marked lower-is-better, got %+v", regs)
	}
}

func TestCheckRegressionSkipsZeroDenominatorBaseline(t *testing.T) {
	base := baseMetrics()
	base.TransferRate = rate(0, 0) // baseline never ran transfer: no comparison point
	cand := baseMetrics()
	cand.TransferRate = rate(0, 3) // candidate ran it and failed, but it must be skipped
	regs := CheckRegression(lifecycleResults("a3", "", cand), lifecycleResults("a3", "", base), DefaultThresholds())
	for _, r := range regs {
		if r.Metric == "transfer_rate" {
			t.Fatalf("a zero-denominator baseline metric must be skipped, got %+v", regs)
		}
	}
}

func TestCheckRegressionSkipsZeroDenominatorCandidate(t *testing.T) {
	// The candidate ran a partial protocol set that never exercised transfer, so
	// its transfer denominator is 0 even though it graded other attempts. That is
	// a coverage gap, not a capability loss, and must not be scored as a 0% drop.
	cand := baseMetrics()
	cand.TransferRate = rate(0, 0)
	regs := CheckRegression(lifecycleResults("a3", "", cand), lifecycleResults("a3", "", baseMetrics()), DefaultThresholds())
	for _, r := range regs {
		if r.Metric == "transfer_rate" {
			t.Fatalf("an unexercised candidate metric must be skipped, got %+v", regs)
		}
	}
}

func TestCheckRegressionCoverage(t *testing.T) {
	cand := Metrics{Attempts: 0} // candidate graded nothing
	regs := CheckRegression(lifecycleResults("a3", "", cand), lifecycleResults("a3", "", baseMetrics()), DefaultThresholds())
	if len(regs) != 1 || regs[0].Metric != "coverage" {
		t.Fatalf("a zero-attempt candidate should be a single coverage regression, got %+v", regs)
	}
}

func TestRegressionReport(t *testing.T) {
	base := lifecycleResults("a3", "", baseMetrics())
	if out := RegressionReport(base, base, DefaultThresholds(), nil); !strings.Contains(out, "PASS") {
		t.Fatalf("clean report should say PASS:\n%s", out)
	}
	m := baseMetrics()
	m.DuplicateRate = rate(3, 5)
	cand := lifecycleResults("a3", "", m)
	regs := CheckRegression(cand, base, DefaultThresholds())
	out := RegressionReport(cand, base, DefaultThresholds(), regs)
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "duplicate_rate") || !strings.Contains(out, "lower is better") {
		t.Fatalf("regression report missing expected content:\n%s", out)
	}
}
