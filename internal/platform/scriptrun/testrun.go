package scriptrun

import (
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// TestInputs is the parameters and state a test gave its run in place of the
// recording's (#1953): a branch that runs only when saved state is present is
// reached by a test that sets the state and declares what that branch's calls
// are answered. Set is false until the test sets either.
type TestInputs struct {
	Params map[string]any
	State  map[string]any
	Set    bool
}

// runBinding is what "run" is bound to: the frozen record, or in a test whose
// inputs can be set, a record read afresh from them on every access.
func (h *hostState) runBinding() starlark.Value {
	if h.opts.Test == nil || h.opts.Test.Inputs == nil {
		return h.runValue()
	}
	return &testRun{h: h}
}

// testRun is run in a test: the record the test's inputs make, rebuilt from
// them on each access and frozen like a run's, so a script reads it exactly
// as it reads a run's.
type testRun struct{ h *hostState }

func (r *testRun) record() *starlarkstruct.Struct {
	in := r.h.opts.Test.Inputs
	if !in.Set {
		return r.h.runRecord(r.h.opts.Params, r.h.opts.State)
	}
	return r.h.runRecord(in.Params, in.State)
}

// String, Type, Freeze, Truth and Hash make testRun read as the run record.
func (r *testRun) String() string { return r.record().String() }

// Type is the record's.
func (r *testRun) Type() string { return r.record().Type() }

// Freeze has nothing to freeze: every record handed out is frozen.
func (*testRun) Freeze() {}

// Truth is the record's.
func (*testRun) Truth() starlark.Bool { return starlark.True }

// Hash refuses, as the record's does.
func (r *testRun) Hash() (uint32, error) { return r.record().Hash() } //nolint:wrapcheck // the record's own refusal

// Attr reads one field of the record.
func (r *testRun) Attr(name string) (starlark.Value, error) {
	return r.record().Attr(name) //nolint:wrapcheck // the record's own "no such field"
}

// AttrNames is the record's.
func (r *testRun) AttrNames() []string { return r.record().AttrNames() }
