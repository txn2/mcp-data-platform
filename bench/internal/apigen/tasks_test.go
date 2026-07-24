package apigen

import (
	"reflect"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

func TestStateDeterministic(t *testing.T) {
	c := BuildCatalog()
	a, b := GenerateState(c), GenerateState(c)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("GenerateState is not deterministic")
	}
	if len(a.Distractors) != len(c.Resources) {
		t.Fatalf("state has %d distractor resources, want %d", len(a.Distractors), len(c.Resources))
	}
	for key, rows := range a.Distractors {
		if len(rows) < distractorRowsMin || len(rows) > distractorRowsMax {
			t.Errorf("resource %s has %d rows, want %d..%d", key, len(rows), distractorRowsMin, distractorRowsMax)
		}
	}
}

func TestTasksAreValid(t *testing.T) {
	c := BuildCatalog()
	s := GenerateState(c)
	tasks := Tasks(s)
	if len(tasks) != 50 {
		t.Fatalf("task set = %d tasks, want 50", len(tasks))
	}
	suites := map[string]int{}
	ids := map[string]bool{}
	for _, tk := range tasks {
		if err := tk.Validate(); err != nil {
			t.Errorf("task %s invalid: %v", tk.ID, err)
		}
		if ids[tk.ID] {
			t.Errorf("duplicate task id %s", tk.ID)
		}
		ids[tk.ID] = true
		suites[tk.Suite]++
		if !reflect.DeepEqual(tk.Arms, studyArms) {
			t.Errorf("task %s arms = %v, want %v", tk.ID, tk.Arms, studyArms)
		}
	}
	want := map[string]int{"p1": 12, "p2": 12, "p3": 10, "p4": 8, "p5": 8}
	if !reflect.DeepEqual(suites, want) {
		t.Errorf("suite split %v, want %v", suites, want)
	}
}

// TestGoldOperationsResolveInEveryTier asserts every task's gold
// operations exist in the tier-0 catalog (and, by nesting, every tier), so
// retrieval hit rate is computable at every catalog size. Irrelevance
// tasks must name no gold operation; every other task must name at least
// one.
func TestGoldOperationsResolveInEveryTier(t *testing.T) {
	c := BuildCatalog()
	s := GenerateState(c)
	t0 := map[string]bool{}
	for _, op := range c.TierOperations(Tier0) {
		t0[op.ID] = true
	}
	for _, tk := range Tasks(s) {
		if tk.Suite == "p5" {
			if len(tk.GoldOperations) != 0 {
				t.Errorf("irrelevance task %s names gold operations %v", tk.ID, tk.GoldOperations)
			}
			continue
		}
		if len(tk.GoldOperations) == 0 {
			t.Errorf("task %s names no gold operations", tk.ID)
		}
		for _, id := range tk.GoldOperations {
			if !t0[id] {
				t.Errorf("task %s gold operation %s not in tier 0", tk.ID, id)
			}
		}
	}
}

// TestGroundTruthsNonTrivial asserts numeric ground truths are positive
// and mutation targets are real changes: a cancel targets a pending
// order, and a region/tier move differs from the current value.
func TestGroundTruthsNonTrivial(t *testing.T) {
	c := BuildCatalog()
	s := GenerateState(c)
	tr := newTruths(s.Dataset)
	for _, tk := range Tasks(s) {
		switch tk.Grading.Kind {
		case task.GradeNumeric:
			if *tk.Grading.Value <= 0 {
				t.Errorf("task %s ground truth %v not positive", tk.ID, *tk.Grading.Value)
			}
		case task.GradeState:
			assertRealChange(t, tr, tk)
		}
	}
}

// assertRealChange verifies one state task's checks against pre-run state.
func assertRealChange(t *testing.T, tr *truths, tk task.Task) {
	t.Helper()
	for _, chk := range tk.Grading.StateChecks {
		if chk.ID == 0 {
			assertNoPreexistingMatch(t, tr, tk, chk)
			continue
		}
		switch chk.Resource {
		case "orders":
			o := tr.order(int(chk.ID))
			if want, ok := chk.Fields["status"]; ok {
				if o.Status == want {
					t.Errorf("task %s: order %d already has status %v", tk.ID, chk.ID, want)
				}
				if want == "canceled" && o.Status != "pending" {
					t.Errorf("task %s: cancel target %d is %s, not pending", tk.ID, chk.ID, o.Status)
				}
			}
		case "customers":
			cu := tr.customer(int(chk.ID))
			if want, ok := chk.Fields["region"]; ok && cu.Region == want {
				t.Errorf("task %s: customer %d already in region %v", tk.ID, chk.ID, want)
			}
			if want, ok := chk.Fields["tier"]; ok && cu.Tier == want {
				t.Errorf("task %s: customer %d already on tier %v", tk.ID, chk.ID, want)
			}
		default:
			t.Errorf("task %s: unknown state resource %q", tk.ID, chk.Resource)
		}
	}
}

// assertNoPreexistingMatch verifies an existence-mode check (a created
// row) matches nothing in pre-run state, so a do-nothing episode cannot
// pass by accident.
func assertNoPreexistingMatch(t *testing.T, tr *truths, tk task.Task, chk task.StateCheck) {
	t.Helper()
	if chk.Resource != "orders" {
		t.Errorf("task %s: existence check on unsupported resource %q", tk.ID, chk.Resource)
		return
	}
	for _, o := range tr.ds.Orders {
		if int64(o.CustomerID) == chk.Fields["customer_id"] &&
			o.Amount == chk.Fields["amount"] &&
			o.Status == chk.Fields["status"] {
			t.Errorf("task %s: pre-run order %d already matches the creation check", tk.ID, o.ID)
		}
	}
}

// TestTasksDeterministic asserts regeneration produces the identical task
// set (the property the committed-artifact drift check relies on).
func TestTasksDeterministic(t *testing.T) {
	c := BuildCatalog()
	a := Tasks(GenerateState(c))
	b := Tasks(GenerateState(c))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Tasks is not deterministic")
	}
	if task.Hash(a) != task.Hash(b) {
		t.Fatal("task-set hash is not stable")
	}
}
