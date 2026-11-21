package scriptguard

import (
	"fmt"

	"go.starlark.net/starlark"
)

// A run's memory is measured at every host call, and that alone let a script
// build past its budget between two of them: in a loop that calls nothing, or
// in one operation that allocates a great deal at once ([0] * n, s + s
// repeated, a range turned into a list). Two more checks close that:
//
//   - PaceSteps measures the run every CheckpointEvery interpreter steps, on
//     the interpreter's own goroutine, so what a loop builds is measured
//     whether or not it calls the host;
//   - Reserve is asked by the operations that can build a large value in one
//     step (internal/scriptalloc) before they build it, so the value that
//     would take a run past its budget is refused rather than allocated.

// CheckpointEvery is how many interpreter steps a run takes between the
// memory checks made outside host calls. A check reads one runtime counter
// unless the process has allocated enough since the last walk to put the run
// over its budget, so it costs well under a step's worth of time per step.
const CheckpointEvery = 10_000

// reserveFloor is the smallest value Reserve checks. Anything smaller is
// left to the step checkpoints, which see what many small values add up to.
const reserveFloor = 64 << 10

// meterKey is the thread-local key a run's meter is attached under.
const meterKey = "scriptguard.meter"

// checkpointAt is where a run found over its budget at a step checkpoint is
// said to have been measured.
const checkpointAt = "the code between host calls"

// Attach makes m the meter Reserve and Budget consult for thread.
func Attach(thread *starlark.Thread, m *Meter) {
	if thread != nil && m != nil {
		thread.SetLocal(meterKey, m)
	}
}

// meterOf is the meter attached to thread, or nil.
func meterOf(thread *starlark.Thread) *Meter {
	if thread == nil {
		return nil
	}
	m, _ := thread.Local(meterKey).(*Meter)
	return m
}

// Budget is the memory budget of the run on thread, or zero when it has none.
func Budget(thread *starlark.Thread) int64 {
	if m := meterOf(thread); m != nil {
		return m.budget
	}
	return 0
}

// Reserve reports whether the run on thread may build a value of about n
// bytes, called by an operation before it allocates the value. It refuses
// when what the run holds plus n would be over its budget, confirming with a
// walk first; what refuses names the operation. A thread with no meter, a run
// with no budget and a value under reserveFloor are not checked.
func Reserve(thread *starlark.Thread, n int64, what string) error {
	m := meterOf(thread)
	if m == nil || m.budget <= 0 || n < reserveFloor {
		return nil
	}
	if n > m.budget {
		// Refused before any sum: a size saturated at the largest int64
		// would overflow one.
		return &reserveError{size: n, held: m.estimate, budget: m.budget, what: what}
	}
	if m.fits(m.estimate + m.grown() + n) {
		return nil
	}
	m.walk(thread)
	if m.fits(m.estimate + n) {
		return nil
	}
	return &reserveError{size: n, held: m.estimate, budget: m.budget, what: what}
}

// reserveError is the refusal of a value that would take a run past its
// budget. It is a memory failure like a budgetError.
type reserveError struct {
	size, held, budget int64
	what               string
}

func (e *reserveError) Error() string {
	return fmt.Sprintf("%s: building a value of about %s would take the run past its %s memory budget "+
		"(it holds about %s); build it in pieces and export each one, or keep less "+
		"(scripts.worker.max_run_memory sets the budget)",
		e.what, FormatBytes(e.size), FormatBytes(e.budget), FormatBytes(e.held))
}

func (*reserveError) Unwrap() error { return ErrMemoryBudget }

// PaceSteps runs thread to limit steps, measuring the run every
// CheckpointEvery steps on the way. At limit, over is called, as
// starlark.Thread.OnMaxSteps would be; it may cancel the thread or raise its
// step cap. A checkpoint that finds the run over its budget cancels the
// thread and keeps the refusal for Explain.
func (m *Meter) PaceSteps(thread *starlark.Thread, limit uint64, over func(*starlark.Thread)) {
	next := func(th *starlark.Thread) {
		th.SetMaxExecutionSteps(min(limit, th.ExecutionSteps()+CheckpointEvery))
	}
	next(thread)
	thread.OnMaxSteps = func(th *starlark.Thread) {
		if th.ExecutionSteps() >= limit {
			over(th)
			return
		}
		if err := m.checkpoint(th); err != nil {
			m.refused = err
			th.Cancel(err.Error())
			return
		}
		next(th)
	}
}

// checkpoint measures the run between host calls and refuses when what it
// holds is over its budget. It walks only when what the process allocated
// since the last walk could have put the run over, like Check, but it keeps
// its own baseline and leaves the host calls' estimate alone: a walk between
// two instructions cannot see a value still on the operand stack (a list a
// comprehension is building), so letting it reset the host calls' baseline
// would forget those bytes before the next host call could count them.
func (m *Meter) checkpoint(th *starlark.Thread) error {
	if m.budget <= 0 {
		return nil
	}
	base := max(m.allocatedAtWalk, m.checkpointAllocated)
	if m.fits(m.estimate + max(0, m.allocated()-base)) {
		return nil
	}
	w := walker{seen: map[any]bool{}}
	w.held(th)
	held := w.total + m.held
	m.peak = max(m.peak, held)
	m.checkpointAllocated = m.allocated()
	if m.fits(held) {
		return nil
	}
	return &budgetError{held: held, budget: m.budget, at: checkpointAt, results: m.resultSummary()}
}

// Explain is the error a run that ended with err failed with: the refusal a
// step checkpoint stopped it with, when one did, since the interpreter
// reports that only as a cancellation; err otherwise.
func (m *Meter) Explain(err error) error {
	if m != nil && m.refused != nil && err != nil {
		return m.refused
	}
	return err
}
