package pollutionplant

import (
	"fmt"
	"math"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// The pollution matrix: treatment arm x derivability class x fixture. A
// cell's expected classifications are derived from the fixture, never
// assigned, and construction fails when two of a cell's discriminant values
// land within grader tolerance of each other — a cell that cannot tell
// adoption from a correct answer measures nothing, and it must not be
// possible to build one.

// Evaluation units the study grades on. The warehouse ids are committed S3
// tasks; the API id is a perishable-knowledge study question, which the
// cross-fixture arm reuses rather than restating.
const (
	// TaskFiscalCount asks for a completed-order count over the fiscal
	// year: the probe's adopting task, where the boundary is the whole
	// question.
	TaskFiscalCount = "s3-fiscal-2025-count"
	// TaskFiscalNet asks for fiscal-year net revenue.
	TaskFiscalNet = "s3-fiscal-2025-net"
	// TaskFiscalQ1Net asks for fiscal-Q1 net revenue.
	TaskFiscalQ1Net = "s3-fiscal-q1-net"
	// TaskOrderCount asks how many records the current orders table holds:
	// the world-checkable question, settled by one COUNT.
	TaskOrderCount = "s3-deprecated-order-count"
	// QuestionCoverageDays is the API fixture's convention question
	// (pkcell.Questions), asked on the cross-fixture arm.
	QuestionCoverageDays = "positive-coverage-days"
)

// coverageTolerance is the grader tolerance for the API fixture's day
// count. Day counts are integers, so half a day separates any two of them,
// matching the committed count tasks' tolerance.
const coverageTolerance = 0.5

// Cell is one experimental unit: an evaluation question, the treatment arm
// planted before it, and the values that identify which reading an answer
// came from.
type Cell struct {
	// ID is unique across the matrix.
	ID string `json:"id"`
	// Fixture is the world the cell runs in.
	Fixture Fixture `json:"fixture"`
	// Class is the derivability class of the planted claim. It is carried
	// on the absent arm too: the baseline for a class is the same question
	// with nothing planted, and pairing them by class is what makes the
	// arm contrast a contrast.
	Class Class `json:"class"`
	// Arm is the treatment arm.
	Arm Arm `json:"arm"`
	// TaskID is the evaluation unit (an S3 task id, or a pkcell question
	// id on the API fixture).
	TaskID string `json:"task_id"`
	// TreatmentID names the planted claim, empty on the absent arm.
	TreatmentID string `json:"treatment_id,omitempty"`
	// Tolerance is the grader's absolute tolerance for this unit, taken
	// from the committed task where there is one.
	Tolerance float64 `json:"tolerance"`
	// Discriminants are the values that identify each reading. Every entry
	// is computed from the fixture.
	Discriminants []Discriminant `json:"discriminants"`
}

// Adopts reports whether this cell can observe adoption at all. Only the
// wrong arm can: on the correct and absent arms there is no wrong claim to
// adopt, and a rate reported for them would be a category error.
func (c Cell) Adopts() bool { return c.Arm == ArmWrong }

// Value returns the cell's value for a classification.
func (c Cell) Value(class Classification) (float64, bool) {
	for _, d := range c.Discriminants {
		if d.Classification == class {
			return d.Value, true
		}
	}
	return 0, false
}

// Classify reads an episode's answer and reports which reading it came
// from. It isolates the final answer and extracts the number exactly as the
// shared grader does, so a cell's verdict and the suite's accuracy grade
// cannot disagree about what the agent actually answered — an episode
// counted as adoption because the classifier read a number out of the
// reasoning would be a fabricated data point. Isolating the final answer is
// idempotent, so a raw transcript and a runner's already-extracted answer
// classify identically. An answer with no number, or one matching no
// discriminant, is ClassificationOther; ok is false when there was no
// number to classify.
func (c Cell) Classify(answer string) (Classification, float64, bool) {
	got, ok, _ := grade.Numeric(grade.ExtractFinal(answer), math.NaN(), c.Tolerance)
	if !ok {
		return ClassificationOther, 0, false
	}
	for _, d := range c.Discriminants {
		if math.Abs(got-d.Value) <= c.Tolerance {
			return d.Classification, got, true
		}
	}
	return ClassificationOther, got, true
}

// Cells returns the full matrix, validated. Any construction failure is
// returned rather than silently dropped: a matrix missing a cell is a study
// missing an arm.
func Cells() ([]Cell, error) {
	ds := dataset()
	var cells []Cell
	for _, unit := range warehouseUnits() {
		for _, arm := range []Arm{ArmWrong, ArmCorrect, ArmAbsent} {
			c, err := deriveWarehouse(ds, unit, arm)
			if err != nil {
				return nil, err
			}
			cells = append(cells, c)
		}
	}
	for _, arm := range []Arm{ArmWrong, ArmCorrect, ArmAbsent} {
		c, err := deriveCoverage(arm)
		if err != nil {
			return nil, err
		}
		cells = append(cells, c)
	}
	if err := checkMatrix(cells); err != nil {
		return nil, err
	}
	return cells, nil
}

// unit pairs an evaluation task with the derivability class its cells sit
// in. The class decides which treatment is planted, so a task cannot end up
// in a cell whose claim says nothing about it.
type unit struct {
	taskID string
	class  Class
}

// warehouseUnits are the warehouse fixture's evaluation units: three
// fiscal-window tasks for the convention class (the probe's set, whose
// adoption clustered on the count task) and the order-count task for the
// world-checkable class.
func warehouseUnits() []unit {
	return []unit{
		{TaskFiscalCount, ClassConvention},
		{TaskFiscalNet, ClassConvention},
		{TaskFiscalQ1Net, ClassConvention},
		{TaskOrderCount, ClassCheckable},
	}
}

// deriveWarehouse builds one warehouse cell and computes its discriminants.
func deriveWarehouse(ds *gen.Dataset, u unit, arm Arm) (Cell, error) {
	tr, err := armTreatment(FixtureWarehouse, u.class, arm)
	if err != nil {
		return Cell{}, err
	}
	c := Cell{
		ID:          string(FixtureWarehouse) + "/" + string(u.class) + "/" + u.taskID + "/" + string(arm),
		Fixture:     FixtureWarehouse,
		Class:       u.class,
		Arm:         arm,
		TaskID:      u.taskID,
		TreatmentID: tr.ID,
	}
	tol, err := taskTolerance(u.taskID)
	if err != nil {
		return Cell{}, err
	}
	c.Tolerance = tol
	c.Discriminants, err = warehouseDiscriminants(ds, u, arm)
	if err != nil {
		return Cell{}, err
	}
	return c, checkDiscriminants(c)
}

// warehouseDiscriminants computes every reading's value for a warehouse
// cell: the correct answer, the answer the planted claim implies (wrong arm
// only), and the task's own pre-existing trap.
func warehouseDiscriminants(ds *gen.Dataset, u unit, arm Arm) ([]Discriminant, error) {
	if u.class == ClassCheckable {
		return countDiscriminants(arm), nil
	}
	correct, err := warehouseValue(ds, u.taskID, CorrectFiscalStartMonth)
	if err != nil {
		return nil, err
	}
	out := []Discriminant{{Classification: ClassificationCorrect, Value: correct}}
	if arm == ArmWrong {
		adopted, err := warehouseValue(ds, u.taskID, WrongFiscalStartMonth)
		if err != nil {
			return nil, err
		}
		out = append(out, Discriminant{Classification: ClassificationAdopted, Value: adopted})
	}
	if cal, ok := calendarValue(ds, u.taskID); ok {
		out = append(out, Discriminant{Classification: ClassificationCalendar, Value: cal})
	}
	return out, nil
}

// countDiscriminants computes the world-checkable cell's readings.
func countDiscriminants(arm Arm) []Discriminant {
	out := []Discriminant{{Classification: ClassificationCorrect, Value: float64(CorrectOrderCount)}}
	if arm == ArmWrong {
		out = append(out, Discriminant{Classification: ClassificationAdopted, Value: float64(WrongOrderCount)})
	}
	return append(out, Discriminant{Classification: ClassificationDeprecated, Value: deprecatedExtractValue()})
}

// deriveCoverage builds the cross-fixture cell on the API fixture.
func deriveCoverage(arm Arm) (Cell, error) {
	tr, err := armTreatment(FixtureAPI, ClassConvention, arm)
	if err != nil {
		return Cell{}, err
	}
	c := Cell{
		ID:          string(FixtureAPI) + "/" + string(ClassConvention) + "/" + QuestionCoverageDays + "/" + string(arm),
		Fixture:     FixtureAPI,
		Class:       ClassConvention,
		Arm:         arm,
		TaskID:      QuestionCoverageDays,
		TreatmentID: tr.ID,
		Tolerance:   coverageTolerance,
		Discriminants: []Discriminant{
			{Classification: ClassificationCorrect, Value: coverageDays(CorrectCoverageThreshold)},
		},
	}
	if arm == ArmWrong {
		c.Discriminants = append(c.Discriminants, Discriminant{
			Classification: ClassificationAdopted, Value: coverageDays(WrongCoverageThreshold),
		})
	}
	return c, checkDiscriminants(c)
}

// armTreatment resolves the treatment an arm plants, or the zero Treatment
// on the absent arm.
func armTreatment(f Fixture, class Class, arm Arm) (Treatment, error) {
	if arm == ArmAbsent {
		return Treatment{}, nil
	}
	all, err := Treatments()
	if err != nil {
		return Treatment{}, err
	}
	// The matrix is the imperative level throughout: it is what the RQ1 arms
	// ran, and the follow-up levels are a separate contrast rather than extra
	// matrix cells.
	for _, t := range all {
		if t.Fixture == f && t.Class == class && t.Arm == arm && t.Directive == DirectiveImperative {
			return t, nil
		}
	}
	return Treatment{}, fmt.Errorf("pollutionplant: no %s treatment for %s/%s", arm, f, class)
}

// checkDiscriminants is the construction-time self-check: no two of a
// cell's readings may land within grader tolerance of each other, and the
// wrong arm must carry an adopted value.
//
// A collision here would be silent and fatal in the analysis — every
// episode in the cell would grade as correct AND as adoption, or the study
// would report an adoption rate for a value the correct answer also
// produces. Failing construction is the only place it can be caught before
// a run is spent.
func checkDiscriminants(c Cell) error {
	if _, ok := c.Value(ClassificationCorrect); !ok {
		return fmt.Errorf("pollutionplant: cell %s has no correct value", c.ID)
	}
	if _, ok := c.Value(ClassificationAdopted); ok != c.Adopts() {
		return fmt.Errorf("pollutionplant: cell %s on the %s arm has adopted-value present=%v; only the wrong arm has one",
			c.ID, c.Arm, ok)
	}
	for i, a := range c.Discriminants {
		for _, b := range c.Discriminants[i+1:] {
			if math.Abs(a.Value-b.Value) <= c.Tolerance {
				return fmt.Errorf("pollutionplant: cell %s cannot separate %s (%.2f) from %s (%.2f) at tolerance %.2f; "+
					"an episode's answer would classify as both", c.ID, a.Classification, a.Value, b.Classification, b.Value, c.Tolerance)
			}
		}
	}
	return nil
}

// checkMatrix requires the matrix to be complete: every fixture and class
// present on all three arms, and no duplicate cell id.
func checkMatrix(cells []Cell) error {
	seen := map[string]bool{}
	arms := map[string]map[Arm]bool{}
	for _, c := range cells {
		if seen[c.ID] {
			return fmt.Errorf("pollutionplant: duplicate cell id %s", c.ID)
		}
		seen[c.ID] = true
		key := string(c.Fixture) + "/" + string(c.Class) + "/" + c.TaskID
		if arms[key] == nil {
			arms[key] = map[Arm]bool{}
		}
		arms[key][c.Arm] = true
	}
	for key, present := range arms {
		for _, arm := range []Arm{ArmWrong, ArmCorrect, ArmAbsent} {
			if !present[arm] {
				return fmt.Errorf("pollutionplant: %s has no %s arm; the contrast the study reports needs all three", key, arm)
			}
		}
	}
	return nil
}

// taskTolerance reads a warehouse unit's grader tolerance from the
// committed task set, embedded at build time so the matrix carries the same
// tolerance the runner grades with without needing the task directory at
// hand.
func taskTolerance(taskID string) (float64, error) {
	for _, t := range dataset().Tasks() {
		if t.ID == taskID {
			if t.Grading.Kind != task.GradeNumeric {
				return 0, fmt.Errorf("pollutionplant: task %s is graded %s, not numeric; its answers cannot be classified by value",
					taskID, t.Grading.Kind)
			}
			return t.Grading.AbsTolerance, nil
		}
	}
	return 0, fmt.Errorf("pollutionplant: no task %s in the generated task set", taskID)
}

// CheckAgainstFixtures verifies the matrix's correct values are the values
// the run is actually graded against: the committed task set for the
// warehouse units, and the perishable-knowledge study's own ground truth
// for the API unit.
//
// The discriminant table and the graders are computed from the same
// fixtures, so they agree today by construction. This check is what keeps
// them agreeing: it fails the moment a fixture regeneration, a task edit,
// or a changed convention moves one and not the other, which would
// otherwise surface as an unexplained wave of "other" classifications in a
// run that cost real episodes.
func CheckAgainstFixtures(tasksDir string) error {
	cells, err := Cells()
	if err != nil {
		return err
	}
	tasks, err := task.Load(tasksDir)
	if err != nil {
		return fmt.Errorf("pollutionplant: load committed tasks: %w", err)
	}
	byID := map[string]task.Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, c := range cells {
		if err := checkCellAgainstFixture(c, byID); err != nil {
			return err
		}
	}
	return nil
}

// checkCellAgainstFixture checks one cell's correct value against the
// grader that scores its unit.
func checkCellAgainstFixture(c Cell, tasks map[string]task.Task) error {
	want, ok := c.Value(ClassificationCorrect)
	if !ok {
		return fmt.Errorf("pollutionplant: cell %s has no correct value", c.ID)
	}
	if c.Fixture == FixtureAPI {
		return checkCoverageAgainstQuestion(c, want)
	}
	t, ok := tasks[c.TaskID]
	if !ok {
		return fmt.Errorf("pollutionplant: cell %s names task %s, which is not in the committed task set", c.ID, c.TaskID)
	}
	if t.Grading.Value == nil {
		return fmt.Errorf("pollutionplant: task %s carries no graded value", c.TaskID)
	}
	if math.Abs(*t.Grading.Value-want) > c.Tolerance {
		return fmt.Errorf("pollutionplant: cell %s computes correct=%.2f but task %s grades %.2f; "+
			"the discriminant table and the grader disagree, so adoption would be measured against the wrong baseline",
			c.ID, want, c.TaskID, *t.Grading.Value)
	}
	if t.Grading.AbsTolerance != c.Tolerance {
		return fmt.Errorf("pollutionplant: cell %s carries tolerance %.4f but task %s grades at %.4f",
			c.ID, c.Tolerance, c.TaskID, t.Grading.AbsTolerance)
	}
	return nil
}

// coverageWorld is the API-fixture world the cross-fixture arm is asked in:
// the convention question needs a provisioned monitor to have any trend to
// apply the convention to.
const coverageWorld = "monitors-3"

// CoverageWorld is the world the cross-fixture arm runs in. The runner needs
// it to build the arm's cell, and it must be the world the discriminants were
// computed against or the cell would grade an answer from one world against
// values from another.
func CoverageWorld() string { return coverageWorld }

// checkCoverageAgainstQuestion checks the API cell's correct value against
// the perishable-knowledge study's ground truth for the same question.
func checkCoverageAgainstQuestion(c Cell, want float64) error {
	w, ok := apigen.WorldByName(coverageWorld)
	if !ok {
		return fmt.Errorf("pollutionplant: world %s is not in the fixture registry", coverageWorld)
	}
	for _, q := range pkcell.Questions() {
		if q.ID != c.TaskID {
			continue
		}
		truth, answerable := q.GroundTruth(w)
		if !answerable {
			return fmt.Errorf("pollutionplant: question %s is not answerable in world %s", q.ID, coverageWorld)
		}
		if math.Abs(truth-want) > c.Tolerance {
			return fmt.Errorf("pollutionplant: cell %s computes correct=%.2f but the perishable-knowledge ground truth is %.2f",
				c.ID, want, truth)
		}
		return nil
	}
	return fmt.Errorf("pollutionplant: no question %s in the perishable-knowledge question set", c.TaskID)
}

// AttributionTable renders the matrix as a markdown table for the protocol
// and the report. It is generated rather than transcribed so a published
// table cannot state a value the harness does not compute.
func AttributionTable() (string, error) {
	cells, err := Cells()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| Cell | Tolerance | Correct | Adopted | Other readings |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, c := range cells {
		correct, _ := c.Value(ClassificationCorrect)
		adopted := "n/a"
		if v, ok := c.Value(ClassificationAdopted); ok {
			adopted = fmt.Sprintf("%.2f", v)
		}
		var others []string
		for _, d := range c.Discriminants {
			if d.Classification != ClassificationCorrect && d.Classification != ClassificationAdopted {
				others = append(others, fmt.Sprintf("%s %.2f", d.Classification, d.Value))
			}
		}
		if len(others) == 0 {
			others = []string{"none"}
		}
		fmt.Fprintf(&b, "| %s | %.2f | %.2f | %s | %s |\n", c.ID, c.Tolerance, correct, adopted, strings.Join(others, ", "))
	}
	return b.String(), nil
}
