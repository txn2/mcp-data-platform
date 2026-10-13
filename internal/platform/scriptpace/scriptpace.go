// Package scriptpace is a managed script's clock: platform.remaining_ms, the
// time the run has before its deadline (#2004), and platform.sleep, a bounded
// wait that lets a script poll a slow upstream with short calls instead of
// one call held open until the upstream answers (#2112).
//
// A wait is held to the run: one call waits at most MaxSeconds, so a loop of
// waits stays visible in the run's step and time budgets, and a wait that
// would carry the run past its deadline is refused before it starts. A test
// and a replay do not wait at all -- the call returns at once and records
// nothing -- so a polling script's tests stay fast and replay the same calls
// in the same order a run made them. remaining_ms is recorded with the run
// and read back from the recording, or from testing.set_run, in a test.
//
// It is kept out of internal/platform/scriptrun for that package's size
// budget: scriptrun binds the two members over a Clock and owns nothing else
// of them.
package scriptpace

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.starlark.net/starlark"
)

// SleepCapability is platform.sleep's name as the run reports and validate
// spells it.
const SleepCapability = "platform.sleep"

// MaxSeconds is the longest one call waits. A longer wait is a loop of
// shorter ones, which is what keeps it visible.
const MaxSeconds = 60

// remainingKey is the reserved name the time left is recorded under, beside
// the run's tool calls, so a replay answers it in the order it was read.
const remainingKey = "platform.remaining_ms"

// Clock is a run's time as its script reads it.
type Clock struct {
	// Ctx is the run's context, whose deadline is the run's.
	Ctx context.Context //nolint:containedctx // the run's lifetime, which both members read
	// Testing is a test or a replay: remaining_ms reads a value back and
	// sleep returns at once.
	Testing bool
	// Pinned reads the value testing.set_run(remaining_ms=...) set, nil when
	// a test set none. It is read at each call, because a test sets it after
	// the run's environment is built.
	Pinned func() *int64
	// Recorded reads a value a recorded run read from the host, nil when
	// there is no recording.
	Recorded func(name string) (map[string]any, error)
	// Record records a value the run read, nil when nothing records.
	Record func(tool string, args, out map[string]any, err error)
}

// RemainingMS reports the milliseconds the run has before its deadline. A
// script has no clock, so a long job that budgets its time would otherwise
// add up duration_ms from its answers, which misses everything that is not a
// tool answer.
func (c Clock) RemainingMS(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	if c.Testing {
		return c.testRemaining(b)
	}
	ms := time.Hour.Milliseconds()
	if left, ok := remaining(c.Ctx); ok {
		ms = max(left.Milliseconds(), 0)
	}
	if c.Record != nil {
		c.Record(remainingKey, map[string]any{}, map[string]any{"remaining_ms": ms}, nil)
	}
	return starlark.MakeInt64(ms), nil
}

// testRemaining is remaining_ms in a test: the value testing.set_run set, or
// the next one the recording holds.
func (c Clock) testRemaining(b *starlark.Builtin) (starlark.Value, error) {
	if c.Pinned != nil {
		if ms := c.Pinned(); ms != nil {
			return starlark.MakeInt64(*ms), nil
		}
	}
	if c.Recorded == nil {
		return nil, fmt.Errorf("in %s: this test has no recording to read the value from; "+
			"set it with testing.set_run(remaining_ms=...)", b.Name())
	}
	out, err := c.Recorded(remainingKey)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w; set the value a test reads with testing.set_run(remaining_ms=...)", b.Name(), err)
	}
	switch ms := out["remaining_ms"].(type) {
	case float64:
		return starlark.MakeInt64(int64(ms)), nil
	case int64:
		return starlark.MakeInt64(ms), nil
	default:
		return nil, fmt.Errorf("in %s: the recording holds no value", b.Name())
	}
}

// Sleep waits the seconds a call asks for, within the run's deadline.
func (c Clock) Sleep(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var seconds starlark.Value
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &seconds); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	d, err := duration(b, seconds)
	if err != nil {
		return nil, err
	}
	if c.Testing {
		return starlark.None, nil
	}
	if left, ok := remaining(c.Ctx); ok && d >= left {
		return nil, fmt.Errorf("in %s: %s would pass the run's deadline, which is %d ms away; "+
			"check platform.remaining_ms() before waiting and stop the loop in time",
			b.Name(), d, max(left.Milliseconds(), 0))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-c.Ctx.Done():
		return nil, fmt.Errorf("in %s: the run ended while waiting: %w", b.Name(), c.Ctx.Err())
	case <-timer.C:
		return starlark.None, nil
	}
}

// duration reads the seconds a call asks for: a positive number no greater
// than MaxSeconds.
func duration(b *starlark.Builtin, v starlark.Value) (time.Duration, error) {
	var seconds float64
	switch n := v.(type) {
	case starlark.Int:
		i, ok := n.Int64()
		if !ok {
			return 0, fmt.Errorf("in %s: %s seconds is more than one call waits (%d)", b.Name(), n, MaxSeconds)
		}
		seconds = float64(i)
	case starlark.Float:
		seconds = float64(n)
	default:
		return 0, fmt.Errorf("in %s: seconds must be a number, not %s", b.Name(), v.Type())
	}
	if math.IsNaN(seconds) || seconds <= 0 {
		return 0, fmt.Errorf("in %s: seconds must be greater than zero", b.Name())
	}
	if seconds > MaxSeconds {
		return 0, fmt.Errorf("in %s: %g seconds is more than one call waits (%d); "+
			"wait in a loop of shorter calls, checking platform.remaining_ms() between them", b.Name(), seconds, MaxSeconds)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// remaining is how long the run has before its deadline, when it has one.
func remaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}
