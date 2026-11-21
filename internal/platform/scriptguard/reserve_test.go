package scriptguard

import (
	"errors"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// runPaced runs src with m attached and paced to limit steps, as a run does,
// and returns the error the run would report.
func runPaced(t *testing.T, m *Meter, src string, limit uint64) (overStep bool, err error) {
	t.Helper()
	thread := &starlark.Thread{Name: "test"}
	Attach(thread, m)
	over := false
	m.PaceSteps(thread, limit, func(th *starlark.Thread) {
		over = true
		th.Cancel("too many steps")
	})
	opts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true, While: true}
	_, execErr := starlark.ExecFileOptions(opts, thread, "test.star", src, nil)
	return over, m.Explain(execErr)
}

// TestPaceSteps_MeasuresWhatALoopBuildsWithoutAHostCall: a loop that calls
// nothing is measured every CheckpointEvery steps and stopped with a memory
// failure once it holds more than its budget.
func TestPaceSteps_MeasuresWhatALoopBuildsWithoutAHostCall(t *testing.T) {
	m := NewMeter(256 * kib)
	over, err := runPaced(t, m, `
held = []
for i in range(200000):
    held.append("x" * 1024 + str(i))
`, 50_000_000)
	if over {
		t.Fatal("the run hit its step limit rather than its budget")
	}
	if !errors.Is(err, ErrMemoryBudget) || !strings.Contains(err.Error(), checkpointAt) {
		t.Fatalf("err = %v, want a memory refusal at a step checkpoint", err)
	}
	if m.Peak() <= 256*kib {
		t.Errorf("peak %d not over the budget it was refused at", m.Peak())
	}
}

func TestPaceSteps_StillStopsAtTheStepLimit(t *testing.T) {
	m := NewMeter(0)
	over, err := runPaced(t, m, "x = 0\nwhile True:\n    x += 1\n", 3*CheckpointEvery+7)
	if !over || err == nil || !strings.Contains(err.Error(), "too many steps") {
		t.Fatalf("over=%v err=%v, want the step limit", over, err)
	}
}

func TestPaceSteps_LeavesARunInsideItsBudget(t *testing.T) {
	m := NewMeter(64 * mib)
	over, err := runPaced(t, m, "held = []\nfor i in range(20000):\n    held.append(i)\n", 10_000_000)
	if err != nil || over {
		t.Fatalf("err=%v over=%v", err, over)
	}
}

func TestReserve(t *testing.T) {
	thread := &starlark.Thread{}
	if err := Reserve(thread, 1<<40, "x"); err != nil {
		t.Errorf("a thread with no meter refused: %v", err)
	}
	Attach(thread, NewMeter(1*mib))
	if Budget(thread) != 1*mib {
		t.Errorf("Budget = %d", Budget(thread))
	}
	if err := Reserve(thread, reserveFloor-1, "small"); err != nil {
		t.Errorf("a value under the floor was checked: %v", err)
	}
	if err := Reserve(thread, 256*kib, "fits"); err != nil {
		t.Errorf("a value inside the budget was refused: %v", err)
	}
	for _, n := range []int64{2 * mib, 1<<63 - 1} {
		err := Reserve(thread, n, "a repeat (*)")
		if !errors.Is(err, ErrMemoryBudget) || !strings.Contains(err.Error(), "a repeat (*)") {
			t.Errorf("Reserve(%d) = %v", n, err)
		}
	}
	Attach(thread, NewMeter(0))
	if err := Reserve(thread, 1<<40, "x"); err != nil {
		t.Errorf("a run with no budget refused: %v", err)
	}
	if Budget(&starlark.Thread{}) != 0 || Budget(nil) != 0 {
		t.Error("Budget of a thread with no meter is not zero")
	}
	Attach(nil, NewMeter(1))
}

func TestExplain(t *testing.T) {
	var none *Meter
	base := errors.New("stopped")
	if !errors.Is(none.Explain(base), base) {
		t.Error("a nil meter changed the error")
	}
	m := NewMeter(1)
	if !errors.Is(m.Explain(base), base) || m.Explain(nil) != nil {
		t.Error("a meter with no refusal changed the error")
	}
	m.refused = errors.New("over")
	if got := m.Explain(base); !errors.Is(got, m.refused) || errors.Is(got, base) {
		t.Error("the refusal was not reported")
	}
	if m.Explain(nil) != nil {
		t.Error("a run that succeeded was failed")
	}
}
