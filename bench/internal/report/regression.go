package report

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Thresholds bound how far a candidate run may fall below a committed baseline
// before it counts as a regression. Accuracy and pass^k are absolute drops in
// rate (0.05 = five percentage points); ToolCallRatio is a multiplicative
// ceiling on median tool calls (1.25 = up to 25% more calls is tolerated).
//
// The defaults are deliberately loose: agent runs are stochastic, and k-repeat
// bootstrap CIs on the phase-2 suites already span several points, so a gate
// that trips on one-point noise would fire on every rerun. The gate exists to
// catch a real capability loss (a broken enrichment path, a persona that stops
// granting search), not run-to-run variance.
type Thresholds struct {
	AccuracyDrop  float64 // max tolerated accuracy drop, in rate points
	PassKDrop     float64 // max tolerated pass^k drop, in rate points
	ToolCallRatio float64 // max tolerated median-tool-call multiple (candidate/baseline)
}

// DefaultThresholds returns the standard regression bounds.
func DefaultThresholds() Thresholds {
	return Thresholds{AccuracyDrop: 0.05, PassKDrop: 0.05, ToolCallRatio: 1.25}
}

// SuiteRegression is one breached threshold for one suite.
type SuiteRegression struct {
	Suite     string  `json:"suite"`
	Metric    string  `json:"metric"` // "accuracy" | "pass_k" | "median_tool_calls"
	Baseline  float64 `json:"baseline"`
	Candidate float64 `json:"candidate"`
	Limit     float64 `json:"limit"` // the value at or beyond which it counts as a regression
}

// BaselineCompatible reports whether a candidate may be gated against a baseline
// at all. Comparing across arms, or across the anthropic vs claude-cli client
// path, or against a baseline with nothing graded, produces meaningless pass/fail
// verdicts, so the gate must refuse rather than silently mis-report. The suite
// filter is deliberately NOT checked here: a candidate run with -suite is a valid
// gate target (CheckRegression compares only the suites it actually ran).
func BaselineCompatible(candidate, baseline *Results) error {
	if candidate.Manifest.Arm != baseline.Manifest.Arm {
		return fmt.Errorf("arm mismatch: candidate %q vs baseline %q — a baseline is only comparable within the same arm",
			candidate.Manifest.Arm, baseline.Manifest.Arm)
	}
	if candidate.Manifest.ClientVersion != baseline.Manifest.ClientVersion {
		return fmt.Errorf("client path mismatch: candidate %q vs baseline %q — anthropic and claude-cli numbers are not comparable",
			clientLabel(candidate.Manifest.ClientVersion), clientLabel(baseline.Manifest.ClientVersion))
	}
	for _, b := range baseline.Suites {
		if b.Graded > 0 {
			return nil
		}
	}
	return errors.New("baseline has no graded suites — nothing to gate against")
}

// clientLabel names the client path for an error message.
func clientLabel(v string) string {
	if v == "" {
		return "in-process (anthropic)"
	}
	return "claude-cli " + v
}

// CheckRegression compares a candidate run against a baseline suite-by-suite and
// returns every breached threshold. Baseline suites with nothing graded are
// skipped (no valid comparison point). A baseline suite missing from the
// candidate is a coverage regression only when the candidate was a full run; a
// candidate run with an explicit -suite filter legitimately omits the others, so
// those are not flagged. An empty result means the candidate held the line on
// every comparable baseline suite. Call BaselineCompatible first — this function
// assumes the two runs are same-arm, same-client.
func CheckRegression(candidate, baseline *Results, t Thresholds) []SuiteRegression {
	cand := map[string]SuiteSummary{}
	for _, s := range candidate.Suites {
		cand[s.Suite] = s
	}
	filtered := candidate.Manifest.Suite != ""
	var regs []SuiteRegression
	for _, b := range baseline.Suites {
		if b.Graded == 0 {
			continue // degenerate baseline suite: no attempts graded, nothing to compare
		}
		c, ok := cand[b.Suite]
		if !ok {
			if filtered {
				continue // candidate intentionally ran a suite subset that omits this one
			}
			regs = append(regs, SuiteRegression{Suite: b.Suite, Metric: "coverage", Baseline: 1, Candidate: 0, Limit: 1})
			continue
		}
		regs = append(regs, suiteRegressions(b, c, t)...)
	}
	sort.Slice(regs, func(i, j int) bool {
		if regs[i].Suite != regs[j].Suite {
			return regs[i].Suite < regs[j].Suite
		}
		return regs[i].Metric < regs[j].Metric
	})
	return regs
}

// suiteRegressions returns the thresholds one candidate suite breached against
// its baseline counterpart: an accuracy drop, a pass^k drop, or a median
// tool-call increase (efficiency). The tool-call check is guarded against a zero
// baseline so the ratio stays meaningful.
func suiteRegressions(b, c SuiteSummary, t Thresholds) []SuiteRegression {
	var regs []SuiteRegression
	if limit := b.Accuracy - t.AccuracyDrop; c.Accuracy < limit {
		regs = append(regs, SuiteRegression{Suite: b.Suite, Metric: "accuracy", Baseline: b.Accuracy, Candidate: c.Accuracy, Limit: limit})
	}
	if limit := b.PassKRate - t.PassKDrop; c.PassKRate < limit {
		regs = append(regs, SuiteRegression{Suite: b.Suite, Metric: "pass_k", Baseline: b.PassKRate, Candidate: c.PassKRate, Limit: limit})
	}
	if b.MedianToolCalls > 0 {
		if limit := b.MedianToolCalls * t.ToolCallRatio; c.MedianToolCalls > limit {
			regs = append(regs, SuiteRegression{Suite: b.Suite, Metric: "median_tool_calls", Baseline: b.MedianToolCalls, Candidate: c.MedianToolCalls, Limit: limit})
		}
	}
	return regs
}

// RegressionReport renders a regression check for a terminal. It names the
// baseline and candidate runs so a CI log records exactly what was compared.
func RegressionReport(candidate, baseline *Results, t Thresholds, regs []SuiteRegression) string {
	var b strings.Builder
	fmt.Fprintf(&b, "regression check: candidate %s @ %s vs baseline %s @ %s\n",
		candidate.Manifest.Arm, short(candidate.Manifest.PlatformVersion),
		baseline.Manifest.Arm, short(baseline.Manifest.PlatformVersion))
	fmt.Fprintf(&b, "  thresholds: accuracy -%.0f pts, pass^k -%.0f pts, tool calls x%.2f\n",
		t.AccuracyDrop*100, t.PassKDrop*100, t.ToolCallRatio)
	if len(regs) == 0 {
		b.WriteString("  PASS: no suite regressed beyond thresholds\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  FAIL: %d regression(s)\n", len(regs))
	for _, r := range regs {
		switch r.Metric {
		case "coverage":
			fmt.Fprintf(&b, "    %s: suite missing from candidate (baseline had it)\n", r.Suite)
		case "median_tool_calls":
			fmt.Fprintf(&b, "    %s %s: %.1f -> %.1f (limit %.1f)\n", r.Suite, r.Metric, r.Baseline, r.Candidate, r.Limit)
		default:
			fmt.Fprintf(&b, "    %s %s: %.1f%% -> %.1f%% (limit %.1f%%)\n", r.Suite, r.Metric, r.Baseline*100, r.Candidate*100, r.Limit*100)
		}
	}
	return b.String()
}
