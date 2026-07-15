package lifecycle

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Thresholds bound how far a candidate lifecycle run may fall below a committed
// baseline before it counts as a regression (issue #966). RateDrop is the
// tolerated absolute drop for the higher-is-better rates (0.05 = five percentage
// points); DuplicateIncrease is the tolerated absolute increase in the
// duplicate rate (lower is better); PassKDrop is the tolerated pass^k drop.
//
// The defaults match the S1-S3 gate's loose bounds: lifecycle runs are
// stochastic and the per-metric denominators are small, so a gate that trips on
// one-point noise would fire on every rerun. The gate exists to catch a real
// lifecycle capability loss (capture stops landing, transfer stops surfacing,
// the supersede gate starts leaving duplicates), not run-to-run variance.
type Thresholds struct {
	RateDrop          float64
	DuplicateIncrease float64
	PassKDrop         float64
}

// DefaultThresholds returns the standard lifecycle regression bounds.
func DefaultThresholds() Thresholds {
	return Thresholds{RateDrop: 0.05, DuplicateIncrease: 0.05, PassKDrop: 0.05}
}

// MetricRegression is one breached threshold for one lifecycle metric.
type MetricRegression struct {
	Metric    string  `json:"metric"`
	Baseline  float64 `json:"baseline"`
	Candidate float64 `json:"candidate"`
	Limit     float64 `json:"limit"` // the value at or beyond which it counts as a regression
	// LowerIsBetter marks the duplicate-rate direction, where a regression is an
	// increase past the limit rather than a drop below it.
	LowerIsBetter bool `json:"lower_is_better,omitempty"`
}

// gatedMetric names one lifecycle metric the gate scores. Only the headline
// capability rates are gated; the #964 diagnostic decompositions (transfer
// surfaced / used-given-surfaced / capture budget-starved) are deliberately not
// gated — their denominators are small enough that gating them would trip on
// noise, and they exist to explain a regression, not to define one.
type gatedMetric struct {
	name          string
	get           func(Metrics) Rate
	lowerIsBetter bool
}

var gatedMetrics = []gatedMetric{
	{"capture_rate", func(m Metrics) Rate { return m.CaptureRate }, false},
	{"personal_recall", func(m Metrics) Rate { return m.PersonalRecall }, false},
	{"transfer_rate", func(m Metrics) Rate { return m.TransferRate }, false},
	{"update_correctness", func(m Metrics) Rate { return m.UpdateCorrectness }, false},
	{"abstention_rate", func(m Metrics) Rate { return m.AbstentionRate }, false},
	{"duplicate_rate", func(m Metrics) Rate { return m.DuplicateRate }, true},
}

// BaselineCompatible reports whether a candidate lifecycle run may be gated
// against a baseline at all. Comparing across arms or across the anthropic vs
// claude-cli client path, or against a baseline that graded nothing, produces
// meaningless verdicts, so the gate refuses rather than silently mis-report.
func BaselineCompatible(candidate, baseline *Results) error {
	if candidate.Manifest.Arm != baseline.Manifest.Arm {
		return fmt.Errorf("arm mismatch: candidate %q vs baseline %q — a baseline is only comparable within the same arm",
			candidate.Manifest.Arm, baseline.Manifest.Arm)
	}
	// Gate on the client PATH (anthropic vs claude-cli), not the exact CLI
	// version: a benign `claude` patch bump must not disable the regression gate.
	// Anthropic runs record an empty ClientVersion; claude-cli runs record a
	// non-empty one, so parity is an emptiness comparison.
	if (candidate.Manifest.ClientVersion == "") != (baseline.Manifest.ClientVersion == "") {
		return fmt.Errorf("client path mismatch: candidate %q vs baseline %q — anthropic and claude-cli numbers are not comparable",
			clientLabel(candidate.Manifest.ClientVersion), clientLabel(baseline.Manifest.ClientVersion))
	}
	if baseline.Metrics.Attempts == 0 {
		return errors.New("baseline graded no attempts — nothing to gate against")
	}
	return nil
}

// clientLabel names the client path for an error message.
func clientLabel(v string) string {
	if v == "" {
		return "in-process (anthropic)"
	}
	return "claude-cli " + v
}

// CheckRegression compares a candidate lifecycle run against a baseline metric by
// metric and returns every breached threshold. A metric that either run did not
// exercise (zero denominator on either side) is skipped — a coverage gap is not
// a capability loss. A candidate that graded no attempts at all is a single
// coverage regression, not one per metric. An empty result means the candidate
// held the line. Call BaselineCompatible first — this assumes same-arm,
// same-client-path.
func CheckRegression(candidate, baseline *Results, t Thresholds) []MetricRegression {
	if candidate.Metrics.Attempts == 0 {
		return []MetricRegression{{Metric: "coverage", Baseline: 1, Candidate: 0, Limit: 1}}
	}
	var regs []MetricRegression
	for _, gm := range gatedMetrics {
		b := gm.get(baseline.Metrics)
		c := gm.get(candidate.Metrics)
		// A metric is comparable only when BOTH runs exercised it. A zero
		// denominator on either side is a coverage gap (a partial protocol set),
		// not a capability loss, so scoring it would flag a false regression.
		if b.Den == 0 || c.Den == 0 {
			continue
		}
		if reg, ok := rateRegression(gm.name, b, c, t.rateTolerance(gm.lowerIsBetter), gm.lowerIsBetter); ok {
			regs = append(regs, reg)
		}
	}
	if bp, cp := baseline.Metrics.PassK, candidate.Metrics.PassK; bp.Den > 0 && cp.Den > 0 {
		if reg, ok := rateRegression("pass_k", bp, cp, t.PassKDrop, false); ok {
			regs = append(regs, reg)
		}
	}
	sort.Slice(regs, func(i, j int) bool { return regs[i].Metric < regs[j].Metric })
	return regs
}

// rateTolerance returns the tolerance for a metric given its direction.
func (t Thresholds) rateTolerance(lowerIsBetter bool) float64 {
	if lowerIsBetter {
		return t.DuplicateIncrease
	}
	return t.RateDrop
}

// rateRegression returns the regression a candidate rate breached against its
// baseline, if any. For a higher-is-better metric the breach is a drop below
// baseline-tolerance; for a lower-is-better metric it is a rise above
// baseline+tolerance.
func rateRegression(name string, b, c Rate, tol float64, lowerIsBetter bool) (MetricRegression, bool) {
	if lowerIsBetter {
		limit := b.Rate + tol
		if c.Rate > limit {
			return MetricRegression{Metric: name, Baseline: b.Rate, Candidate: c.Rate, Limit: limit, LowerIsBetter: true}, true
		}
		return MetricRegression{}, false
	}
	limit := b.Rate - tol
	if c.Rate < limit {
		return MetricRegression{Metric: name, Baseline: b.Rate, Candidate: c.Rate, Limit: limit}, true
	}
	return MetricRegression{}, false
}

// RegressionReport renders a lifecycle regression check for a terminal, naming
// the baseline and candidate so a CI log records exactly what was compared.
func RegressionReport(candidate, baseline *Results, t Thresholds, regs []MetricRegression) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lifecycle regression check: candidate %s @ %s vs baseline %s @ %s\n",
		candidate.Manifest.Arm, short(candidate.Manifest.PlatformVersion),
		baseline.Manifest.Arm, short(baseline.Manifest.PlatformVersion))
	fmt.Fprintf(&b, "  thresholds: rate -%.0f pts, duplicate +%.0f pts, pass^k -%.0f pts\n",
		t.RateDrop*100, t.DuplicateIncrease*100, t.PassKDrop*100)
	if len(regs) == 0 {
		b.WriteString("  PASS: no lifecycle metric regressed beyond thresholds\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  FAIL: %d regression(s)\n", len(regs))
	for _, r := range regs {
		switch {
		case r.Metric == "coverage":
			b.WriteString("    coverage: candidate graded no attempts (baseline did)\n")
		case r.LowerIsBetter:
			fmt.Fprintf(&b, "    %s: %.1f%% -> %.1f%% (limit %.1f%%, lower is better)\n", r.Metric, r.Baseline*100, r.Candidate*100, r.Limit*100)
		default:
			fmt.Fprintf(&b, "    %s: %.1f%% -> %.1f%% (limit %.1f%%)\n", r.Metric, r.Baseline*100, r.Candidate*100, r.Limit*100)
		}
	}
	return b.String()
}
