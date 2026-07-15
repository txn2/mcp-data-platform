package stats

import "testing"

func TestMeanBool(t *testing.T) {
	if got := MeanBool(nil); got != 0 {
		t.Errorf("MeanBool(nil) = %v, want 0", got)
	}
	if got := MeanBool([]bool{true, true, false, false}); got != 0.5 {
		t.Errorf("MeanBool = %v, want 0.5", got)
	}
	if got := MeanBool([]bool{true, true}); got != 1 {
		t.Errorf("MeanBool(all true) = %v, want 1", got)
	}
}

func TestQuantile(t *testing.T) {
	if got := Quantile(nil, 0.5); got != 0 {
		t.Errorf("Quantile(nil) = %v, want 0", got)
	}
	sorted := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if got := Quantile(sorted, 0); got != 0 {
		t.Errorf("Quantile p=0 = %v, want 0", got)
	}
	if got := Quantile(sorted, 1); got != 9 {
		t.Errorf("Quantile p=1 = %v, want 9", got)
	}
	// p=0.5 nearest-rank on 10 elements: idx = int(0.5*9) = 4.
	if got := Quantile(sorted, 0.5); got != 4 {
		t.Errorf("Quantile p=0.5 = %v, want 4", got)
	}
}

func TestBootstrapMeanCIBounds(t *testing.T) {
	// An all-true sample has a degenerate CI at 1; all-false at 0.
	if lo, hi := BootstrapMeanCI([]bool{true, true, true}, NewRNG()); lo != 1 || hi != 1 {
		t.Errorf("all-true CI = [%v, %v], want [1, 1]", lo, hi)
	}
	if lo, hi := BootstrapMeanCI([]bool{false, false}, NewRNG()); lo != 0 || hi != 0 {
		t.Errorf("all-false CI = [%v, %v], want [0, 0]", lo, hi)
	}
	if lo, hi := BootstrapMeanCI(nil, NewRNG()); lo != 0 || hi != 0 {
		t.Errorf("empty CI = [%v, %v], want [0, 0]", lo, hi)
	}
	// A mixed sample brackets its point estimate.
	bools := make([]bool, 100)
	for i := range 60 {
		bools[i] = true
	}
	lo, hi := BootstrapMeanCI(bools, NewRNG())
	if !(lo < 0.6 && 0.6 < hi) {
		t.Errorf("CI [%v, %v] does not bracket 0.6", lo, hi)
	}
	if lo < 0 || hi > 1 {
		t.Errorf("CI [%v, %v] escapes [0, 1]", lo, hi)
	}
}

func TestBootstrapReproducible(t *testing.T) {
	bools := []bool{true, false, true, false, true, true, false}
	lo1, hi1 := BootstrapMeanCI(bools, NewRNG())
	lo2, hi2 := BootstrapMeanCI(bools, NewRNG())
	if lo1 != lo2 || hi1 != hi2 {
		t.Errorf("CI not reproducible: [%v,%v] vs [%v,%v]", lo1, hi1, lo2, hi2)
	}
}

func TestProportionCIMatchesBoolSample(t *testing.T) {
	// ProportionCI(num, den) must equal BootstrapMeanCI over a bool slice whose
	// first num entries are true — same reconstruction, same seed, same draws.
	const num, den = 37, 90
	bools := make([]bool, den)
	for i := range num {
		bools[i] = true
	}
	wLo, wHi := BootstrapMeanCI(bools, NewRNG())
	gLo, gHi := ProportionCI(num, den, NewRNG())
	if gLo != wLo || gHi != wHi {
		t.Errorf("ProportionCI = [%v,%v], want [%v,%v]", gLo, gHi, wLo, wHi)
	}
	if lo, hi := ProportionCI(0, 0, NewRNG()); lo != 0 || hi != 0 {
		t.Errorf("ProportionCI(0,0) = [%v,%v], want [0,0]", lo, hi)
	}
}

func TestBootstrapDeltaCI(t *testing.T) {
	a := make([]bool, 100)
	for i := range 80 {
		a[i] = true
	}
	b := make([]bool, 100)
	for i := range 20 {
		b[i] = true
	}
	pts, lo, hi := BootstrapDeltaCI(a, b, NewRNG())
	if pts < 0.59 || pts > 0.61 {
		t.Errorf("delta points = %v, want ~0.6", pts)
	}
	if !(lo < pts && pts < hi) {
		t.Errorf("delta CI [%v,%v] does not bracket %v", lo, hi, pts)
	}
	// Empty side yields a zero-width interval around the point estimate.
	if p, l, h := BootstrapDeltaCI(nil, b, NewRNG()); l != 0 || h != 0 || p == 0 {
		t.Errorf("empty-side delta = (%v,%v,%v), want zero CI and nonzero point", p, l, h)
	}
}
