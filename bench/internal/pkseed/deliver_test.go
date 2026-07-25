package pkseed

// The delivery arm's contract. The metadata block is the RQ3 treatment, so
// what it may and may not say is as load-bearing as the prose: it must
// estimate, never state the answer, and it must agree with the prose about
// when the belief was observed.

import (
	"strings"
	"testing"
	"time"
)

// enriched builds the enriched arm at a given age.
func enriched(days, calls int) Metadata {
	return Metadata{
		Enriched: true, AsOf: CaptureDate(),
		Now: CaptureDate().AddDate(0, 0, days), RecheckCalls: calls,
	}
}

// TestBareArmDeliversProseAlone checks the control arm adds nothing, so the
// RQ3 contrast is the block's presence and not some other difference.
func TestBareArmDeliversProseAlone(t *testing.T) {
	for _, s := range Seeds() {
		if got := Delivered(s, Metadata{}); got != s.Text {
			t.Errorf("seed %s: the bare arm changed the delivered text", s.ID)
		}
	}
}

// TestEnrichedBlockEstimatesWithoutStating checks the block carries the
// three estimators and none of the things that would make the agent's
// decision arithmetic rather than judgment.
func TestEnrichedBlockEstimatesWithoutStating(t *testing.T) {
	s := Compose(perishableAbsent(), Phrasing{})
	text := Delivered(s, enriched(24, 1))
	for _, want := range []string{"perishable", "hours to days", "24 days ago", "1 call", CaptureDate().Format(dateOnly)} {
		if !strings.Contains(text, want) {
			t.Errorf("the enriched block does not carry %q:\n%s", want, text)
		}
	}
	// No stated staleness, on any class or age. A probability, a
	// percentage, or an assertion that the belief is out of date would
	// reduce the threshold comparison to reading rather than estimating.
	banned := []string{"probability", "% likely", "percent", "is stale", "out of date", "no longer accurate", "likely wrong"}
	for _, class := range []string{ClassPerishable, ClassDurable, ClassEternal} {
		for _, days := range []int{0, 1, 24, 365} {
			block := strings.ToLower(enriched(days, 1).Block(class))
			for _, b := range banned {
				if strings.Contains(block, b) {
					t.Errorf("the %s block at %d days states %q rather than estimating", class, days, b)
				}
			}
		}
	}
	if found := AuditDelivered(text); len(found) > 0 {
		t.Errorf("the enriched delivery violates the invariants: %v", found)
	}
}

// TestBlockReportsTheClassItIsGiven checks each volatility class delivers
// its own gloss, since the discriminant control depends on an agent being
// able to tell a perishable belief from an invariant one.
func TestBlockReportsTheClassItIsGiven(t *testing.T) {
	seen := map[string]bool{}
	for _, class := range []string{ClassPerishable, ClassDurable, ClassEternal} {
		block := enriched(24, 1).Block(class)
		gloss := volatilityGloss[class]
		if !strings.Contains(block, gloss) {
			t.Errorf("class %s delivered %q, want the gloss %q", class, block, gloss)
		}
		if seen[gloss] {
			t.Errorf("class %s reuses another class's gloss", class)
		}
		seen[gloss] = true
	}
	// An unknown class degrades to its own name rather than silently
	// borrowing another class's shelf life.
	if got := enriched(24, 1).Block("mystery"); !strings.Contains(got, "mystery") {
		t.Errorf("an unknown class delivered %q", got)
	}
}

// TestAgeAndCostVaryAsDelivered checks the two estimators the study sweeps
// actually move the delivered string, since a cell that reads identically
// to another cell is not a cell.
func TestAgeAndCostVaryAsDelivered(t *testing.T) {
	s := Compose(perishableAbsent(), Phrasing{})
	fresh, stale := Delivered(s, enriched(1, 1)), Delivered(s, enriched(60, 1))
	if fresh == stale {
		t.Fatal("observation age does not change the delivered text")
	}
	if !strings.Contains(fresh, "1 day ago") || !strings.Contains(stale, "60 days ago") {
		t.Errorf("ages render wrong:\n fresh: %s\n stale: %s", fresh, stale)
	}
	cheap, dear := Delivered(s, enriched(24, 1)), Delivered(s, enriched(24, 3))
	if cheap == dear {
		t.Fatal("re-observation cost does not change the delivered text")
	}
	if !strings.Contains(cheap, "1 call") || !strings.Contains(dear, "3 calls") {
		t.Errorf("costs render wrong:\n cheap: %s\n dear: %s", cheap, dear)
	}
}

// TestMetadataMustAgreeWithTheProse checks the block's observation date is
// the same date the dated prose reports. A block claiming one date while
// the prose claims another is a confound wearing the costume of a
// treatment.
func TestMetadataMustAgreeWithTheProse(t *testing.T) {
	dated := Compose(perishableAbsent(), Phrasing{Dated: true})
	text := Delivered(dated, enriched(24, 1))
	stamp := CaptureDate().Format(dateOnly)
	if strings.Count(text, stamp) != 2 {
		t.Errorf("the prose and the block do not both report %s:\n%s", stamp, text)
	}
}

// TestValidateMetadataRefusals checks a malformed block is refused rather
// than delivered: an estimator that is wrong rather than imprecise would
// lead a correctly reasoning agent to the wrong answer.
func TestValidateMetadataRefusals(t *testing.T) {
	if err := ValidateMetadata(Metadata{}); err != nil {
		t.Errorf("the bare arm was refused: %v", err)
	}
	if err := ValidateMetadata(enriched(24, 1)); err != nil {
		t.Errorf("a well-formed block was refused: %v", err)
	}
	cases := map[string]Metadata{
		"no dates":      {Enriched: true, RecheckCalls: 1},
		"age backwards": {Enriched: true, AsOf: CaptureDate(), Now: CaptureDate().AddDate(0, 0, -5), RecheckCalls: 1},
		"free recheck":  {Enriched: true, AsOf: CaptureDate(), Now: CaptureDate(), RecheckCalls: 0},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMetadata(m); err == nil {
				t.Error("a malformed block was accepted")
			}
		})
	}
}

// TestCaptureDateParses guards the constant the prose and the block share.
func TestCaptureDateParses(t *testing.T) {
	if got := CaptureDate().Format(dateOnly); got != captureDate {
		t.Errorf("CaptureDate round-trips to %s, want %s", got, captureDate)
	}
	if CaptureDate().After(time.Now()) {
		t.Error("the capture date is in the future, so every delivered age would be negative")
	}
}
