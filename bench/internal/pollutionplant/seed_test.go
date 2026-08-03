package pollutionplant

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestCorrectCoverageSourceCarriesTheThresholdInItsSummary(t *testing.T) {
	page := CorrectCoverageSource()
	if !strings.Contains(page.Summary, CorrectCoverageNeedle()) {
		t.Fatalf("summary %q does not carry the needle %q; as a search hit the page would not state the "+
			"threshold it competes on", page.Summary, CorrectCoverageNeedle())
	}
	if !strings.Contains(page.Body, CorrectCoverageNeedle()) {
		t.Errorf("body %q does not carry the threshold", page.Body)
	}
	if !page.ForceNew {
		t.Error("the seed must force past the near-duplicate gate; it states the same convention as the planted page")
	}
}

// The seed is the planted claim's competitor, so it must not sit on the slug
// the plant writes to: the plant would overwrite it and the wrong arm would
// silently run with no competing source.
func TestCorrectCoverageSourceUsesADistinctSlug(t *testing.T) {
	if CorrectCoverageSourceSlug == coveragePageSlug {
		t.Fatalf("the seeded source and the planted page share slug %q", coveragePageSlug)
	}
}

// The needle carries the discriminant, so a read-back cannot score the seeded
// source off the planted claim's text or the other way round.
func TestCorrectCoverageNeedleIsAbsentFromTheWrongTreatment(t *testing.T) {
	wrong := coverageTreatment(ArmWrong, WrongCoverageThreshold)
	if strings.Contains(wrong.Text, CorrectCoverageNeedle()) {
		t.Fatalf("the wrong treatment's text contains the correct source's needle %q", CorrectCoverageNeedle())
	}
	page := CorrectCoverageSource()
	if strings.Contains(page.Summary+page.Body, wrong.Needle) {
		t.Fatalf("the seeded source contains the wrong claim's needle %q", wrong.Needle)
	}
}

// The seeded threshold is the fixture's, not a number typed into the seed: a
// fixture change must move the correct source with it or the arm would grade
// against a threshold nobody serves.
func TestCorrectCoverageSourceStatesTheFixtureThreshold(t *testing.T) {
	if !strings.Contains(CorrectCoverageSource().Summary, strconv.Itoa(CorrectCoverageThreshold)) {
		t.Fatalf("the seed does not state the fixture threshold %d", CorrectCoverageThreshold)
	}
}

func TestSeedCorrectSourceStoresAndReadsBack(t *testing.T) {
	f := newFakePlatform()
	page, err := f.client().SeedCorrectSource(context.Background())
	if err != nil {
		t.Fatalf("SeedCorrectSource: %v", err)
	}
	if page.Slug != CorrectCoverageSourceSlug {
		t.Errorf("stored slug = %q", page.Slug)
	}
	if len(f.pages) != 1 {
		t.Fatalf("the store holds %d page(s) after the seed", len(f.pages))
	}
}

// A page whose summary lost the threshold in transit is a competitor that no
// longer states the claim it competes with, and every rate downstream would
// be computed against a fixture nobody had.
func TestSeedCorrectSourceRefusesASummaryWithoutTheThreshold(t *testing.T) {
	f := newFakePlatform()
	f.createPageSummary = "ACME reporting standard for positive coverage."
	if _, err := f.client().SeedCorrectSource(context.Background()); err == nil {
		t.Fatal("a stored summary without the threshold was accepted")
	}
}

func TestSeedCorrectSourceSurfacesCreateFailure(t *testing.T) {
	f := newFakePlatform()
	f.createPageErr = errors.New("409 conflict")
	if _, err := f.client().SeedCorrectSource(context.Background()); err == nil {
		t.Fatal("a failed create was reported as a successful seed")
	}
}
