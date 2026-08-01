package stats

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- bootstrap-CI resampling for benchmark statistics; not security-sensitive, and a seedable PRNG is required for reproducible intervals.
	"math/rand"
)

// Rate is one metric's numerator, denominator, and ratio. Denominator counts
// only the outcomes where the metric was applicable and reached. It lives here
// rather than in a suite package because the S5 lifecycle and the cold-start
// suite both report num/den rates with the same bootstrap interval: one shared
// rate type, not a per-suite fork that could drift on how an inapplicable
// outcome or an empty denominator is handled.
type Rate struct {
	Num  int     `json:"num"`
	Den  int     `json:"den"`
	Rate float64 `json:"rate"`
	// CILow/CIHigh are the 95% percentile-bootstrap confidence interval on the
	// rate (issue #965), resampled from num/den with a fixed seed so the interval
	// is reproducible. Both zero when the denominator is empty — the metric was
	// not exercised, so it carries no interval. The bootstrap treats each
	// applicable outcome as an independent draw (like the S1-S3 report); it does
	// not model protocol-level correlation across the k replicates, so a narrow
	// interval on a small, few-protocol denominator still warrants caution.
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

// FillCI attaches a bootstrap confidence interval to the rate from its num/den.
// The caller threads one seeded RNG across a scorecard's rates so the whole
// report is reproducible from a single seed (issue #965).
func (r *Rate) FillCI(rng *rand.Rand) {
	r.CILow, r.CIHigh = ProportionCI(r.Num, r.Den, rng)
}

// FillCIs attaches an interval to each rate in order, threading one RNG so a
// scorecard is reproducible from a single seed. Order is part of the contract:
// the RNG advances per rate, so a rate inserted ahead of an existing one changes
// the draws every later rate sees and can move their intervals. Append new rates
// at the end — that is the only placement guaranteed to leave every previously
// reported interval identical.
func FillCIs(rng *rand.Rand, rates ...*Rate) {
	for _, r := range rates {
		r.FillCI(rng)
	}
}

// Add folds one applicable outcome into the rate. A nil outcome (the metric was
// not applicable, or was never reached) is excluded from the denominator.
func (r *Rate) Add(v *bool) {
	if v == nil {
		return
	}
	r.AddConditional(true, *v)
}

// AddConditional folds one outcome into a conditional rate: the outcome counts
// toward the denominator only when it belongs to the conditioning subset (e.g.
// "among transfer attempts where the fact surfaced"). It backs the decompositions
// whose denominators are subsets of the attempts rather than a single nil-able
// outcome.
func (r *Rate) AddConditional(applicable, value bool) {
	if !applicable {
		return
	}
	r.Den++
	if value {
		r.Num++
	}
	r.Rate = float64(r.Num) / float64(r.Den)
}

// Complement returns the rate of the opposite outcome over the same denominator,
// so a pair like supersede/duplicate is stored once and derived once rather than
// accumulated twice (which could silently drift apart).
func (r Rate) Complement() Rate {
	c := Rate{Num: r.Den - r.Num, Den: r.Den}
	if c.Den > 0 {
		c.Rate = float64(c.Num) / float64(c.Den)
	}
	return c
}

// Row renders one metric line for a terminal scorecard, newline-terminated. The
// 95% CI bracket is shown only when the interval has width (CILow != CIHigh). A
// zero-width interval carries no uncertainty and is omitted: it arises for an
// unexercised metric (empty denominator), for an all-failure rate whose
// bootstrap collapses to a point at zero AND for an all-success rate that
// collapses to a point at one (both ends, not just zero), and for a pre-#965
// results file whose stored metrics carry no interval (the fields default to
// zero) — all of which would otherwise print a meaningless [x.x-x.x] bracket the
// count already conveys.
func (r Rate) Row(label string) string {
	if r.CILow == r.CIHigh {
		return fmt.Sprintf("  %-22s %5.1f%%  (%d/%d)\n", label, r.Rate*100, r.Num, r.Den)
	}
	return fmt.Sprintf("  %-22s %5.1f%%  95%% CI [%.1f-%.1f]  (%d/%d)\n",
		label, r.Rate*100, r.CILow*100, r.CIHigh*100, r.Num, r.Den)
}
