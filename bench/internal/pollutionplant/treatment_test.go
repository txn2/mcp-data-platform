package pollutionplant

// A treatment is prose that an agent will read and a needle a machine will
// match. Both have to hold: prose that differs from its control in more
// than the discriminant confounds the arm, and a needle that also occurs in
// the control reports a claim as delivered when only the correct one ever
// arrived.

import (
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

func TestTreatmentsAreWellFormed(t *testing.T) {
	all, err := Treatments()
	if err != nil {
		t.Fatalf("treatments: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("expected a wrong/correct pair per fixture-and-class, got %d treatments", len(all))
	}
	ids := map[string]bool{}
	for _, tr := range all {
		if ids[tr.ID] {
			t.Errorf("duplicate treatment id %s", tr.ID)
		}
		ids[tr.ID] = true
		if err := tr.Validate(); err != nil {
			t.Errorf("treatment %s: %v", tr.ID, err)
		}
	}
}

// TestArmsDifferOnlyInTheDiscriminant is the minimal-pair property the
// design rests on: everything an agent could react to other than the
// discriminant is identical between the arms.
func TestArmsDifferOnlyInTheDiscriminant(t *testing.T) {
	all, err := Treatments()
	if err != nil {
		t.Fatalf("treatments: %v", err)
	}
	for _, tr := range all {
		if tr.Arm != ArmWrong {
			continue
		}
		other, err := Counterpart(tr)
		if err != nil {
			t.Fatalf("counterpart of %s: %v", tr.ID, err)
		}
		if tr.Text == other.Text {
			t.Errorf("treatment %s is byte-identical to its control", tr.ID)
		}
		if strings.Contains(other.Text, tr.Needle) {
			t.Errorf("treatment %s's needle also occurs in its control's text", tr.ID)
		}
		if other.EntityURN != tr.EntityURN || other.Sink != tr.Sink {
			t.Errorf("treatment %s and its control land in different places", tr.ID)
		}
		// A pair whose prose differs in length by more than the
		// discriminant itself is not a minimal pair.
		if diff := len(tr.Text) - len(other.Text); diff > len(tr.Needle) || -diff > len(other.Needle) {
			t.Errorf("treatment %s differs from its control by %d characters, more than the discriminant", tr.ID, diff)
		}
	}
}

// TestTreatmentsStateTheFixtureTruth keeps the correct arm honest: it must
// state what the fixture actually is, or the study's control would itself
// be a treatment.
func TestTreatmentsStateTheFixtureTruth(t *testing.T) {
	ds := gen.Generate()
	fiscal, err := TreatmentByID("fiscal-boundary-correct")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	month := gen.FiscalYearStartMonth().String()
	if !strings.Contains(fiscal.Text, month+" 1") {
		t.Errorf("the correct fiscal claim does not state the seeded boundary %s: %q", month, fiscal.Text)
	}
	count, err := TreatmentByID("order-count-correct")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	if !strings.Contains(count.Text, itoa(ds.OrderCount())) {
		t.Errorf("the correct count claim does not state the seeded row count %d: %q", ds.OrderCount(), count.Text)
	}
	coverage, err := TreatmentByID("coverage-threshold-correct")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	if !strings.Contains(coverage.Text, itoa(CorrectCoverageThreshold)) {
		t.Errorf("the correct coverage claim does not state the study's threshold: %q", coverage.Text)
	}
}

// itoa keeps the assertions above free of fmt noise.
func itoa(n int) string {
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if digits == "" {
		return "0"
	}
	return digits
}

// TestWrongArmsAreActuallyWrong guards the failure that would make the
// study meaningless without failing anything: a "wrong" claim that happens
// to be true.
func TestWrongArmsAreActuallyWrong(t *testing.T) {
	ds := gen.Generate()
	if WrongFiscalStartMonth == CorrectFiscalStartMonth {
		t.Error("the planted fiscal boundary is the seeded one")
	}
	if WrongOrderCount == ds.OrderCount() {
		t.Error("the planted row count is the true one")
	}
	if WrongCoverageThreshold == CorrectCoverageThreshold {
		t.Error("the planted coverage threshold is the study's own")
	}
	// The planted count must also not be some other table's true count, or
	// an agent could reach it by an honest route.
	if WrongOrderCount == gen.LegacyExtractCount() {
		t.Error("the planted row count equals the deprecated extract's count")
	}
}

func TestValidateRefusesUndeliverableTreatments(t *testing.T) {
	good, err := TreatmentByID("fiscal-boundary-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	cases := map[string]func(Treatment) Treatment{
		"no id":         func(tr Treatment) Treatment { tr.ID = ""; return tr },
		"no text":       func(tr Treatment) Treatment { tr.Text = "   "; return tr },
		"short needle":  func(tr Treatment) Treatment { tr.Needle = "April 1"; return tr },
		"absent needle": func(tr Treatment) Treatment { tr.Needle = "a needle nowhere in the text 1"; return tr },
		"generic needle": func(tr Treatment) Treatment {
			tr.Text += " fiscal year definition"
			tr.Needle = "fiscal year definition"
			return tr
		},
		"unknown sink":     func(tr Treatment) Treatment { tr.Sink = "carrier pigeon"; return tr },
		"datahub, no urn":  func(tr Treatment) Treatment { tr.EntityURN = ""; return tr },
		"page, no payload": func(tr Treatment) Treatment { tr.Sink = protocol.SinkKnowledgePage; return tr },
		"page summary drops the discriminant": func(tr Treatment) Treatment {
			tr.Sink = protocol.SinkKnowledgePage
			tr.Page = &protocol.PagePayload{Slug: "s", Title: "t", Summary: "a summary with no discriminant", Body: tr.Text}
			return tr
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := mutate(good).Validate(); err == nil {
				t.Fatal("an undeliverable treatment was accepted")
			}
		})
	}
	if err := good.Validate(); err != nil {
		t.Errorf("a well-formed treatment was refused: %v", err)
	}
}

// TestCheckMinimalPairsCatchesASharedNeedle drives the pair check with a
// needle both arms carry, which is the state that would make every
// reachability read a false positive.
func TestCheckMinimalPairsCatchesASharedNeedle(t *testing.T) {
	shared := "the same span in both 2025"
	pair := []Treatment{
		{ID: "a", Fixture: FixtureWarehouse, Class: ClassConvention, Arm: ArmWrong, Text: "x " + shared, Needle: shared},
		{ID: "b", Fixture: FixtureWarehouse, Class: ClassConvention, Arm: ArmCorrect, Text: "y " + shared, Needle: shared},
	}
	if err := checkMinimalPairs(pair); err == nil {
		t.Fatal("a pair whose arms share a needle was accepted")
	}
	if err := checkMinimalPairs(pair[:1]); err == nil {
		t.Fatal("a class with no control arm was accepted")
	}
}

func TestCounterpartRoundTrips(t *testing.T) {
	all, err := Treatments()
	if err != nil {
		t.Fatalf("treatments: %v", err)
	}
	for _, tr := range all {
		other, err := Counterpart(tr)
		if err != nil {
			t.Fatalf("counterpart of %s: %v", tr.ID, err)
		}
		back, err := Counterpart(other)
		if err != nil {
			t.Fatalf("counterpart of %s: %v", other.ID, err)
		}
		if back.ID != tr.ID {
			t.Errorf("counterpart of %s round-tripped to %s", tr.ID, back.ID)
		}
	}
	if _, err := Counterpart(Treatment{ID: "orphan", Arm: ArmWrong}); err == nil {
		t.Error("a treatment outside the committed set was given a counterpart")
	}
	if _, err := TreatmentByID("no-such-treatment"); err == nil {
		t.Error("an unknown treatment id resolved")
	}
}

// TestFiscalWindowsMoveWithTheBoundary pins the window arithmetic the
// adopted values are computed over.
func TestFiscalWindowsMoveWithTheBoundary(t *testing.T) {
	from, to := fiscalWindow(2025, time.April)
	if from != time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("fiscal year start is %v", from)
	}
	if to != time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("fiscal year end is %v", to)
	}
	qFrom, qTo := fiscalQuarterWindow(2025, time.April)
	if qFrom != from || qTo != time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("fiscal Q1 is %v..%v", qFrom, qTo)
	}
}

func TestWarehouseValueRejectsUnknownUnits(t *testing.T) {
	if _, err := warehouseValue(gen.Generate(), "s3-tier-key-count", time.February); err == nil {
		t.Error("a task with no fiscal window was accepted")
	}
	if _, ok := calendarValue(gen.Generate(), TaskFiscalQ1Net); ok {
		t.Error("fiscal Q1 was given a calendar-year counterpart, which is not defined")
	}
}
