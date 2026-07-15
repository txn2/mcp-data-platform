// Package stats holds the benchmark's shared resampling primitives: the
// percentile bootstrap that puts a confidence interval on every reported rate.
// Both the S1-S3 cross-arm report (internal/report) and the S5 lifecycle report
// (internal/lifecycle) draw their CIs from here, so the two suites cannot drift
// into two divergent bootstrap implementations (one shared abstraction, not a
// per-suite fork). The RNG is seeded from a fixed constant so two runs over
// identical inputs produce identical intervals (the #930 reproducibility
// criterion).
package stats

import (
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- bootstrap CIs use a FIXED seed so the report is reproducible; crypto/rand would break that
	"math/rand"
	"sort"
)

// Seed pins the resampling RNG so CIs are reproducible across runs with
// identical inputs.
const Seed = 930

// Iters is the number of bootstrap resamples per interval.
const Iters = 5000

// Alpha is the two-sided confidence-level complement: 0.05 yields a 95% CI.
const Alpha = 0.05

// NewRNG returns a bootstrap RNG seeded with Seed. Callers thread one RNG
// through a report's cells so the whole report is reproducible from one seed.
func NewRNG() *rand.Rand {
	return rand.New(rand.NewSource(Seed)) // #nosec G404 -- reproducible bootstrap, not crypto
}

// MeanBool returns the fraction of true values, or 0 for an empty slice.
func MeanBool(bools []bool) float64 {
	if len(bools) == 0 {
		return 0
	}
	n := 0
	for _, b := range bools {
		if b {
			n++
		}
	}
	return float64(n) / float64(len(bools))
}

// Quantile returns the p-quantile of a sorted slice (nearest-rank).
func Quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := max(0, min(int(p*float64(len(sorted)-1)), len(sorted)-1))
	return sorted[idx]
}

// BootstrapMeanCI returns the Alpha-level percentile bootstrap CI for the mean
// of a set of Bernoulli outcomes. It resamples with replacement Iters times.
func BootstrapMeanCI(bools []bool, rng *rand.Rand) (lo, hi float64) {
	if len(bools) == 0 {
		return 0, 0
	}
	means := make([]float64, Iters)
	for i := range means {
		means[i] = resampleMean(bools, rng)
	}
	sort.Float64s(means)
	return Quantile(means, Alpha/2), Quantile(means, 1-Alpha/2)
}

// BootstrapDeltaCI returns the mean difference (a - b) and its Alpha-level CI,
// resampling both samples independently.
func BootstrapDeltaCI(a, b []bool, rng *rand.Rand) (points, lo, hi float64) {
	points = MeanBool(a) - MeanBool(b)
	if len(a) == 0 || len(b) == 0 {
		return points, 0, 0
	}
	diffs := make([]float64, Iters)
	for i := range diffs {
		diffs[i] = resampleMean(a, rng) - resampleMean(b, rng)
	}
	sort.Float64s(diffs)
	return points, Quantile(diffs, Alpha/2), Quantile(diffs, 1-Alpha/2)
}

// ProportionCI returns the Alpha-level percentile bootstrap CI for a proportion
// given only its numerator and denominator. The nonparametric bootstrap of a
// Bernoulli sample depends only on the success count and the sample size, so a
// rate stored as num/den (the S5 lifecycle rates, which retain counts rather
// than per-outcome booleans) reconstructs the same interval it would produce
// from the individual outcomes.
func ProportionCI(num, den int, rng *rand.Rand) (lo, hi float64) {
	if den <= 0 {
		return 0, 0
	}
	bools := make([]bool, den)
	for i := range min(num, den) {
		bools[i] = true
	}
	return BootstrapMeanCI(bools, rng)
}

// resampleMean draws len(bools) samples with replacement and returns the mean.
func resampleMean(bools []bool, rng *rand.Rand) float64 {
	n := 0
	for range bools {
		if bools[rng.Intn(len(bools))] {
			n++
		}
	}
	return float64(n) / float64(len(bools))
}
