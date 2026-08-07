package pollutionplant

// The cells' correctness is that a colliding discriminant cannot be built.
// Every check here drives a cell into a state the study must never run in,
// and requires construction to fail rather than a run to produce numbers
// nobody can interpret.

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

func TestMatrixIsCompleteAndSeparable(t *testing.T) {
	cells, err := Cells()
	if err != nil {
		t.Fatalf("cells: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cells {
		if err := checkDiscriminants(c); err != nil {
			t.Errorf("cell %s does not separate its readings: %v", c.ID, err)
		}
		seen[string(c.Fixture)+"/"+string(c.Class)] = true
	}
	for _, want := range []string{"warehouse/convention", "warehouse/checkable", "api/convention"} {
		if !seen[want] {
			t.Errorf("the matrix has no %s cells; the study's moderator axis is incomplete", want)
		}
	}
	if err := checkMatrix(cells); err != nil {
		t.Errorf("matrix: %v", err)
	}
}

// TestOnlyTheWrongArmCarriesAnAdoptedValue pins the arm semantics: an
// adoption rate reported for an arm with nothing wrong planted would be a
// category error, and the derivation is what prevents one.
func TestOnlyTheWrongArmCarriesAnAdoptedValue(t *testing.T) {
	cells, err := Cells()
	if err != nil {
		t.Fatalf("cells: %v", err)
	}
	for _, c := range cells {
		_, has := c.Value(ClassificationAdopted)
		if has != (c.Arm == ArmWrong) {
			t.Errorf("cell %s on the %s arm has adopted value present=%v", c.ID, c.Arm, has)
		}
		if (c.TreatmentID == "") != (c.Arm == ArmAbsent) {
			t.Errorf("cell %s on the %s arm names treatment %q", c.ID, c.Arm, c.TreatmentID)
		}
	}
}

// TestCollidingDiscriminantsFailConstruction is the gate the ticket asks
// for: a cell whose readings land within grader tolerance of each other
// cannot be built, because every episode in it would classify as two
// things at once.
func TestCollidingDiscriminantsFailConstruction(t *testing.T) {
	base := Cell{ID: "test/collision", Arm: ArmWrong, Tolerance: 0.5, Discriminants: []Discriminant{
		{Classification: ClassificationCorrect, Value: 873},
		{Classification: ClassificationAdopted, Value: 873.4},
	}}
	err := checkDiscriminants(base)
	if err == nil || !strings.Contains(err.Error(), "cannot separate") {
		t.Fatalf("a cell that cannot tell adoption from a correct answer was accepted: %v", err)
	}
	// Just outside tolerance is fine: the rule is about the grader's
	// resolution, not about the numbers looking different.
	base.Discriminants[1].Value = 873.6
	if err := checkDiscriminants(base); err != nil {
		t.Errorf("a separable cell was refused: %v", err)
	}
}

func TestCheckDiscriminantsRefusesMalformedCells(t *testing.T) {
	cases := map[string]Cell{
		"no correct value": {ID: "a", Arm: ArmAbsent, Discriminants: []Discriminant{
			{Classification: ClassificationCalendar, Value: 1},
		}},
		"adopted on the absent arm": {ID: "b", Arm: ArmAbsent, Discriminants: []Discriminant{
			{Classification: ClassificationCorrect, Value: 1}, {Classification: ClassificationAdopted, Value: 2},
		}},
		"no adopted on the wrong arm": {ID: "c", Arm: ArmWrong, Discriminants: []Discriminant{
			{Classification: ClassificationCorrect, Value: 1},
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := checkDiscriminants(c); err == nil {
				t.Fatal("a malformed cell was accepted")
			}
		})
	}
}

func TestCheckMatrixRequiresEveryArm(t *testing.T) {
	full := make([]Cell, 0, 3)
	for _, arm := range []Arm{ArmWrong, ArmCorrect, ArmAbsent} {
		full = append(full, Cell{
			ID: "x/" + string(arm), Fixture: FixtureWarehouse, Class: ClassConvention, TaskID: "t", Arm: arm,
		})
	}
	if err := checkMatrix(full); err != nil {
		t.Fatalf("a complete unit was refused: %v", err)
	}
	if err := checkMatrix(full[:2]); err == nil {
		t.Error("a unit with no baseline arm was accepted")
	}
	if err := checkMatrix(append(full, full[0])); err == nil {
		t.Error("a duplicate cell id was accepted")
	}
}

// TestClassifyReadsTheAnswerTheGraderReads keeps the cell's verdict and the
// suite's accuracy grade talking about the same number.
func TestClassifyReadsTheAnswerTheGraderReads(t *testing.T) {
	c := cellByID(t, "warehouse/convention/"+TaskFiscalCount+"/wrong")
	cases := map[string]struct {
		answer string
		want   Classification
	}{
		"correct":              {"FINAL ANSWER: 873", ClassificationCorrect},
		"adopted":              {"FINAL ANSWER: 724 completed orders", ClassificationAdopted},
		"calendar trap":        {"FINAL ANSWER: 948", ClassificationCalendar},
		"something else":       {"FINAL ANSWER: 1200", ClassificationOther},
		"thousands separators": {"FINAL ANSWER: 1,200", ClassificationOther},
		"reasoning then final": {"I considered 724 first.\nFINAL ANSWER: 873", ClassificationCorrect},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, _, ok := c.Classify(tc.answer)
			if !ok {
				t.Fatal("an answer carrying a number was reported unclassifiable")
			}
			if got != tc.want {
				t.Errorf("classified %q as %s, want %s", tc.answer, got, tc.want)
			}
		})
	}
	if _, _, ok := c.Classify("FINAL ANSWER: the data is unavailable"); ok {
		t.Error("an answer with no number was classified anyway")
	}
}

// TestAdoptedValuesComeFromTheFixture is the ticket's no-hand-entry rule.
// It recomputes each adopted value the long way, from the generated rows,
// and requires the cell to agree — so a hand-entered number could not
// survive here.
func TestAdoptedValuesComeFromTheFixture(t *testing.T) {
	ds := gen.Generate()
	from, to := fiscalWindow(fiscalYear, WrongFiscalStartMonth)
	var wantCount int
	var wantCents int64
	byID := map[int]string{}
	for _, cu := range ds.Customers {
		byID[cu.ID] = cu.Region
	}
	for _, o := range ds.Orders {
		if o.Status != "completed" || o.TS.Before(from) || !o.TS.Before(to) {
			continue
		}
		wantCount++
		wantCents += o.Amount - o.Discount
	}
	countCell := cellByID(t, "warehouse/convention/"+TaskFiscalCount+"/wrong")
	assertValue(t, countCell, ClassificationAdopted, float64(wantCount))
	netCell := cellByID(t, "warehouse/convention/"+TaskFiscalNet+"/wrong")
	assertValue(t, netCell, ClassificationAdopted, float64(wantCents)/100)

	// The checkable claim's adopted value is the count it states, which is
	// itself computed from the fixture rather than typed.
	orderCell := cellByID(t, "warehouse/checkable/"+TaskOrderCount+"/wrong")
	assertValue(t, orderCell, ClassificationAdopted, float64(ds.OrderCount()-gen.LegacyExtractCount()))
	assertValue(t, orderCell, ClassificationCorrect, float64(ds.OrderCount()))
}

// assertValue requires a cell's discriminant to equal a value.
func assertValue(t *testing.T, c Cell, class Classification, want float64) {
	t.Helper()
	got, ok := c.Value(class)
	if !ok {
		t.Fatalf("cell %s has no %s value", c.ID, class)
	}
	if math.Abs(got-want) > c.Tolerance {
		t.Errorf("cell %s %s value is %.2f, recomputed from the fixture as %.2f", c.ID, class, got, want)
	}
}

// TestAdoptedValueIsNotThePreExistingTrap keeps the study's headline
// measure interpretable: if a planted claim licensed exactly the answer
// agents already give unaided, an adoption count could not be told apart
// from the baseline trap rate.
func TestAdoptedValueIsNotThePreExistingTrap(t *testing.T) {
	cells, err := Cells()
	if err != nil {
		t.Fatalf("cells: %v", err)
	}
	for _, c := range cells {
		adopted, ok := c.Value(ClassificationAdopted)
		if !ok {
			continue
		}
		for _, d := range c.Discriminants {
			if d.Classification == ClassificationCorrect || d.Classification == ClassificationAdopted {
				continue
			}
			if math.Abs(adopted-d.Value) <= c.Tolerance {
				t.Errorf("cell %s: adoption (%.2f) is indistinguishable from its %s trap (%.2f)",
					c.ID, adopted, d.Classification, d.Value)
			}
		}
	}
}

// TestCheckAgainstFixturesCatchesADriftedGrader is the drift alarm: the
// discriminant table and the grader are computed from the same fixture, and
// this is what fails when one moves without the other.
func TestCheckAgainstFixturesCatchesADriftedGrader(t *testing.T) {
	if err := CheckAgainstFixtures("../../tasks"); err != nil {
		t.Fatalf("the committed task set and the matrix disagree: %v", err)
	}
	if err := CheckAgainstFixtures("../../tasks-api"); err == nil {
		t.Error("a task directory containing none of the study's units was accepted")
	}
	if err := CheckAgainstFixtures("does-not-exist"); err == nil {
		t.Error("a missing task directory was accepted")
	}
}

// TestCheckCellAgainstFixtureCatchesEveryKindOfDrift drives the drift alarm
// with each way the matrix and its grader can come apart. Without these the
// alarm's own failure mode — passing on a mismatch — would be invisible.
func TestCheckCellAgainstFixtureCatchesEveryKindOfDrift(t *testing.T) {
	cell := cellByID(t, "warehouse/convention/"+TaskFiscalCount+"/wrong")
	correct, _ := cell.Value(ClassificationCorrect)
	good := task.Task{ID: cell.TaskID, Grading: task.Grading{
		Kind: task.GradeNumeric, Value: &correct, AbsTolerance: cell.Tolerance,
	}}
	if err := checkCellAgainstFixture(cell, map[string]task.Task{cell.TaskID: good}); err != nil {
		t.Fatalf("an agreeing task was reported as drift: %v", err)
	}

	drifted := correct + 100
	noValue := good
	noValue.Grading.Value = nil
	wrongValue := good
	wrongValue.Grading.Value = &drifted
	wrongTolerance := good
	wrongTolerance.Grading.AbsTolerance = cell.Tolerance + 1
	cases := map[string]map[string]task.Task{
		"task missing":    {},
		"no graded value": {cell.TaskID: noValue},
		"value moved":     {cell.TaskID: wrongValue},
		"tolerance moved": {cell.TaskID: wrongTolerance},
	}
	for name, tasks := range cases {
		t.Run(name, func(t *testing.T) {
			if err := checkCellAgainstFixture(cell, tasks); err == nil {
				t.Fatal("drift between the matrix and its grader went unreported")
			}
		})
	}
	// A cell with no correct value at all cannot be checked against
	// anything, and must say so rather than pass vacuously.
	if err := checkCellAgainstFixture(Cell{ID: "empty"}, nil); err == nil {
		t.Error("a cell with no correct value passed the drift check")
	}
}

func TestCheckCoverageAgainstQuestionRefusesAMismatch(t *testing.T) {
	cell := cellByID(t, "api/convention/"+QuestionCoverageDays+"/wrong")
	correct, _ := cell.Value(ClassificationCorrect)
	if err := checkCoverageAgainstQuestion(cell, correct); err != nil {
		t.Fatalf("the API cell disagrees with the study's own ground truth: %v", err)
	}
	if err := checkCoverageAgainstQuestion(cell, correct+5); err == nil {
		t.Error("a coverage value the fixture does not produce was accepted")
	}
	unknown := cell
	unknown.TaskID = "no-such-question"
	if err := checkCoverageAgainstQuestion(unknown, correct); err == nil {
		t.Error("a cell naming no known question was accepted")
	}
}

func TestAttributionTableRendersEveryCell(t *testing.T) {
	table, err := AttributionTable()
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	cells, err := Cells()
	if err != nil {
		t.Fatalf("cells: %v", err)
	}
	for _, c := range cells {
		if !strings.Contains(table, c.ID) {
			t.Errorf("the table omits cell %s", c.ID)
		}
	}
	// A published table must state the values the harness computes, so
	// the wrong arm's adopted figure has to appear in it.
	countCell := cellByID(t, "warehouse/convention/"+TaskFiscalCount+"/wrong")
	adopted, _ := countCell.Value(ClassificationAdopted)
	if !strings.Contains(table, fmt.Sprintf("%.2f", adopted)) {
		t.Error("the table omits the adopted value it is published to state")
	}
}

func TestTaskToleranceRejectsUnknownAndNonNumericUnits(t *testing.T) {
	if _, err := taskTolerance("s3-no-such-task"); err == nil {
		t.Error("a task outside the generated set was accepted")
	}
	if _, err := taskTolerance("s3-net-top-region"); err == nil {
		t.Error("an entity-graded task was accepted as a classifiable unit")
	}
}

// cellByID resolves one cell from the matrix.
func cellByID(t *testing.T, id string) Cell {
	t.Helper()
	cells, err := Cells()
	if err != nil {
		t.Fatalf("cells: %v", err)
	}
	for _, c := range cells {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no cell %s", id)
	return Cell{}
}

// The generalization cells (6.5) must separate every reading they enumerate,
// and must not disturb the RQ1 matrix, which has already run.
func TestGeneralizationCellsSeparateAndLeaveTheMatrixAlone(t *testing.T) {
	before, err := Cells()
	if err != nil {
		t.Fatalf("Cells: %v", err)
	}
	cells, err := GeneralizationCells()
	if err != nil {
		t.Fatalf("GeneralizationCells: %v", err)
	}
	if len(cells) != 4 {
		t.Fatalf("got %d generalization cells, want the sink control plus the API arms", len(cells))
	}
	// checkDiscriminants already ran inside the constructor; assert the
	// property that matters most explicitly, since a collision here would
	// grade an episode as two classifications at once.
	for _, c := range cells {
		seen := map[Classification]bool{}
		for _, d := range c.Discriminants {
			if seen[d.Classification] {
				t.Errorf("%s enumerates %s twice", c.ID, d.Classification)
			}
			seen[d.Classification] = true
		}
		if _, ok := c.Value(ClassificationAdopted); ok != (c.Arm == ArmWrong) {
			t.Errorf("%s has an adopted value on the %s arm", c.ID, c.Arm)
		}
	}
	// The API wrong arm must tell adoption from the pool reading, which is
	// the only other value the world admits.
	api, ok := pickCell(cells, "api/checkable/monitor-count/wrong")
	if !ok {
		t.Fatal("no API wrong cell")
	}
	adopted, _ := api.Value(ClassificationAdopted)
	pool, _ := api.Value(ClassificationDeprecated)
	correct, _ := api.Value(ClassificationCorrect)
	if adopted == pool || adopted == correct {
		t.Errorf("the planted count %v collides with an unaided reading (correct %v, pool %v)", adopted, correct, pool)
	}
	// The matrix is untouched: RQ1 has run and its cells must not move.
	after, err := Cells()
	if err != nil {
		t.Fatalf("Cells after: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("the matrix changed size: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Errorf("matrix cell %d changed id", i)
		}
	}
}

// pickCell resolves one cell from a supplied slice, which the matrix helper
// above cannot do: it reads the matrix itself.
func pickCell(cells []Cell, id string) (Cell, bool) {
	for _, c := range cells {
		if c.ID == id {
			return c, true
		}
	}
	return Cell{}, false
}
