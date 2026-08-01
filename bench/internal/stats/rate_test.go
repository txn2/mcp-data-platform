package stats

import (
	"strings"
	"testing"
)

func TestRateAdd(t *testing.T) {
	var r Rate
	r.Add(nil) // not applicable: excluded from the denominator
	if r.Den != 0 || r.Rate != 0 {
		t.Fatalf("nil outcome entered the rate: %+v", r)
	}
	r.Add(new(true))
	r.Add(new(false))
	r.Add(new(true))
	if r.Num != 2 || r.Den != 3 || r.Rate != 2.0/3.0 {
		t.Errorf("rate = %d/%d (%v), want 2/3", r.Num, r.Den, r.Rate)
	}
}

func TestRateAddConditional(t *testing.T) {
	var r Rate
	r.AddConditional(false, true) // outside the conditioning subset
	if r.Den != 0 {
		t.Fatalf("inapplicable outcome entered the denominator: %+v", r)
	}
	r.AddConditional(true, true)
	r.AddConditional(true, false)
	if r.Num != 1 || r.Den != 2 || r.Rate != 0.5 {
		t.Errorf("conditional rate = %d/%d (%v), want 1/2", r.Num, r.Den, r.Rate)
	}
}

func TestRateComplement(t *testing.T) {
	c := Rate{Num: 3, Den: 4, Rate: 0.75}.Complement()
	if c.Num != 1 || c.Den != 4 || c.Rate != 0.25 {
		t.Errorf("complement = %d/%d (%v), want 1/4 (0.25)", c.Num, c.Den, c.Rate)
	}
	// An empty denominator has no complement rate to report.
	if e := (Rate{}).Complement(); e.Den != 0 || e.Rate != 0 {
		t.Errorf("empty complement = %+v, want zero", e)
	}
}

func TestRateFillCI(t *testing.T) {
	mixed := Rate{Num: 6, Den: 10, Rate: 0.6}
	mixed.FillCI(NewRNG())
	if !(mixed.CILow < 0.6 && 0.6 < mixed.CIHigh) {
		t.Errorf("CI [%v, %v] does not bracket 0.6", mixed.CILow, mixed.CIHigh)
	}
	// An unexercised metric carries no interval.
	empty := Rate{}
	empty.FillCI(NewRNG())
	if empty.CILow != 0 || empty.CIHigh != 0 {
		t.Errorf("empty rate CI = [%v, %v], want [0, 0]", empty.CILow, empty.CIHigh)
	}
}

// TestFillCIsAppendPreservesEarlierIntervals pins the contract a scorecard
// relies on when it grows a new rate: the RNG advances per rate, so only a rate
// APPENDED after the existing ones is guaranteed to leave every earlier
// interval identical. (An inserted rate shifts the draws every later rate sees;
// their quantiles may or may not land on the same value, which is exactly why
// appending is the rule rather than a hope.)
func TestFillCIsAppendPreservesEarlierIntervals(t *testing.T) {
	a1, b1 := Rate{Num: 6, Den: 10}, Rate{Num: 3, Den: 10}
	FillCIs(NewRNG(), &a1, &b1)
	if a1.CILow == a1.CIHigh || b1.CILow == b1.CIHigh {
		t.Fatalf("degenerate intervals, the test cannot detect a shift: %+v %+v", a1, b1)
	}

	a2, b2, appended := Rate{Num: 6, Den: 10}, Rate{Num: 3, Den: 10}, Rate{Num: 1, Den: 10}
	FillCIs(NewRNG(), &a2, &b2, &appended)
	if a1 != a2 || b1 != b2 {
		t.Errorf("appending a rate shifted an earlier interval: %+v/%+v vs %+v/%+v", a1, b1, a2, b2)
	}
	if appended.CILow == appended.CIHigh && appended.Den > 0 {
		t.Errorf("appended rate got no interval: %+v", appended)
	}
}

func TestRateRow(t *testing.T) {
	withCI := Rate{Num: 2, Den: 4, Rate: 0.5, CILow: 0.1, CIHigh: 0.9}
	row := withCI.Row("capture rate")
	for _, want := range []string{"capture rate", "50.0%", "95% CI [10.0-90.0]", "(2/4)"} {
		if !strings.Contains(row, want) {
			t.Errorf("row %q missing %q", row, want)
		}
	}
	if !strings.HasSuffix(row, "\n") {
		t.Errorf("row %q is not newline-terminated", row)
	}
	// A zero-width interval prints no bracket: it carries no uncertainty the
	// counts do not already convey.
	if got := (Rate{Num: 4, Den: 4, Rate: 1, CILow: 1, CIHigh: 1}).Row("x"); strings.Contains(got, "CI") {
		t.Errorf("degenerate interval printed a bracket: %q", got)
	}
}
