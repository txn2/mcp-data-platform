package graphgen

import (
	"context"
	"strings"
	"testing"
)

// fakeEmbedder maps texts onto crafted vectors so ranking is controlled:
// task phrasings and entry pages share a direction, discontinuity pages sit
// orthogonal to it, and everything else lands in between.
type fakeEmbedder struct {
	discMarkers []string
}

func (f fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		out[i] = f.vec(text)
	}
	return out, nil
}

func (f fakeEmbedder) vec(text string) []float64 {
	lower := strings.ToLower(text)
	for _, marker := range f.discMarkers {
		if strings.Contains(lower, marker) {
			return []float64{0, 1, 0}
		}
	}
	if strings.Contains(lower, "runbook") {
		return []float64{1, 0, 0}
	}
	if strings.Contains(lower, "write the complete") || strings.Contains(lower, "what happens") ||
		strings.Contains(lower, "how are") || strings.Contains(lower, "change plan") ||
		strings.Contains(lower, "escalation") || strings.Contains(lower, "delivery") {
		return []float64{1, 0, 0.1}
	}
	return []float64{0.4, 0.4, 0.2}
}

// discMarkers are phrases unique to the six discontinuity pages' texts.
var discMarkers = []string{
	"close calendar", "records retention", "worked-hours ledger",
	"regulatory filing", "procurement commitment", "sharing agreement",
}

// TestCertifyDiscontinuityPassesWhenPagesAreDistant: with discontinuity
// pages orthogonal to every phrasing and entries aligned, the certification
// passes and records the enumeration profile.
func TestCertifyDiscontinuityPassesWhenPagesAreDistant(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	report, err := CertifyDiscontinuity(context.Background(), fakeEmbedder{discMarkers: discMarkers}, res, 5, 25)
	if err != nil {
		t.Fatalf("CertifyDiscontinuity: %v", err)
	}
	if !report.Pass {
		for _, p := range report.Phrasings {
			if !p.Pass {
				t.Errorf("phrasing %q of %s failed: entry rank %d, violations %v", p.Phrasing, p.CellID, p.EntryRank, p.DiscontinuityViolations)
			}
		}
		t.Fatal("certification failed under a distant-discontinuity embedding")
	}
	if len(report.Phrasings) != 12 {
		t.Errorf("phrasings = %d, want 12 (3 cells x prompt + 3 queries)", len(report.Phrasings))
	}
	for _, p := range report.Phrasings {
		if len(p.ConstraintRanks) == 0 {
			t.Errorf("phrasing %q of %s recorded no enumeration profile", p.Phrasing, p.CellID)
		}
	}
}

// TestEffectiveTopKScalesWithTheCorpus pins the horizon rule: two percent
// of the corpus, floored at the modal episode limit and capped at the
// widest swept one, so the bound's strictness does not invert the scale
// axis.
func TestEffectiveTopKScalesWithTheCorpus(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]int{50: 25, 500: 25, 2500: 50, 5000: 100, 50000: 100} {
		if got := EffectiveTopK(n); got != want {
			t.Errorf("EffectiveTopK(%d) = %d, want %d", n, got, want)
		}
	}
}

// TestWithinCeilingMarksOnlyTheSmallestScale: the boundary is the horizon
// covering half the corpus — true at the study's scale 50 (and just past
// it), false from the first certifiable scale on.
func TestWithinCeilingMarksOnlyTheSmallestScale(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]bool{42: true, 50: true, 51: false, 500: false, 5000: false} {
		if got := WithinCeiling(n); got != want {
			t.Errorf("WithinCeiling(%d) = %t, want %t", n, got, want)
		}
	}
}

// TestCertifyDiscontinuityFailsInsideTheHorizon: with the horizon at the
// corpus size, no page can be absent and the reading records both the
// violations and that the horizon exceeds the corpus — the
// within-enumeration-ceiling condition, not an authoring failure.
func TestCertifyDiscontinuityFailsInsideTheHorizon(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	report, err := CertifyDiscontinuity(context.Background(), fakeEmbedder{discMarkers: discMarkers}, res, Scales[0], 25)
	if err != nil {
		t.Fatalf("CertifyDiscontinuity: %v", err)
	}
	if report.Pass {
		t.Fatal("certification passed with the horizon covering the whole corpus")
	}
	if !report.HorizonExceedsCorpus {
		t.Error("HorizonExceedsCorpus not recorded for a corpus smaller than the horizon")
	}
	violated := false
	for _, p := range report.Phrasings {
		if len(p.DiscontinuityViolations) > 0 {
			violated = true
		}
	}
	if !violated {
		t.Error("no phrasing recorded a discontinuity violation")
	}
}

// TestCertifyDiscontinuityRequiresAFindableEntry: a corpus whose entry page
// cannot be found from its own prompt has no search-arm entry point, and the
// certification refuses it however distant the discontinuities are.
func TestCertifyDiscontinuityRequiresAFindableEntry(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	entryHostile := fakeEmbedder{discMarkers: append([]string{"runbook"}, discMarkers...)}
	report, err := CertifyDiscontinuity(context.Background(), entryHostile, res, 5, 1)
	if err != nil {
		t.Fatalf("CertifyDiscontinuity: %v", err)
	}
	if report.Pass {
		t.Fatal("certification passed with entry pages pushed out of reach")
	}
}
