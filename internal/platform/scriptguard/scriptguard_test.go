package scriptguard

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

func TestCause(t *testing.T) {
	budget := NewMeter(1)
	budget.estimate = 10
	cases := map[string]struct {
		err  error
		want string
	}{
		"none":                   {nil, ""},
		"a script error":         {errors.New("key not found"), runstate.CauseScript},
		"an upstream error":      {NewUpstreamError("api_export", errors.New("timeout")), runstate.CauseUpstream},
		"a wrapped upstream":     {fmt.Errorf("halted: %w", NewUpstreamError("api_export", errors.New("x"))), runstate.CauseUpstream},
		"a memory budget":        {budget.refusal("platform.call"), runstate.CauseMemory},
		"a wrapped memory error": {fmt.Errorf("in platform.query: %w", ErrMemoryBudget), runstate.CauseMemory},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Cause(tc.err))
		})
	}
}

func TestUpstreamError(t *testing.T) {
	inner := errors.New("upstream request: connection refused")
	err := NewUpstreamError("api_invoke_endpoint", inner)
	assert.Equal(t, inner.Error(), err.Error(), "the author reads the tool's own words")
	assert.ErrorIs(t, err, inner)
	assert.Equal(t, "api_invoke_endpoint", err.Tool)
}

func TestFormatBytes(t *testing.T) {
	assert.Equal(t, "12 bytes", FormatBytes(12))
	assert.Equal(t, "3 KiB", FormatBytes(3*kib))
	assert.Equal(t, "128 MiB", FormatBytes(128*mib))
	assert.Equal(t, "1.5 GiB", FormatBytes(3*gib/2))
}

func TestSize(t *testing.T) {
	list := starlark.NewList([]starlark.Value{starlark.String("ab"), starlark.MakeInt(7)})
	dict := starlark.NewDict(1)
	require.NoError(t, dict.SetKey(starlark.String("k"), list))
	set := starlark.NewSet(1)
	require.NoError(t, set.Insert(starlark.String("member")))
	st := starlarkstruct.FromStringDict(starlark.String("s"), starlark.StringDict{"f": starlark.Float(1)})
	huge := starlark.MakeBigInt(new(big.Int).Lsh(big.NewInt(1), 200))

	assert.Zero(t, Size(starlark.None))
	assert.Zero(t, Size(starlark.True))
	assert.Zero(t, Size(starlark.MakeInt(3)), "a small int lives in the interface")
	assert.Positive(t, Size(huge), "a big int carries its digits")
	assert.Equal(t, int64(boxedBytes+minDataBytes), Size(starlark.String("x")))
	assert.Equal(t, int64(boxedBytes+1024), Size(starlark.String(strings.Repeat("x", 1024))))
	assert.Equal(t, int64(boxedBytes+minDataBytes), Size(starlark.Bytes("b")))
	assert.Equal(t, int64(boxedBytes), Size(starlark.Float(1.5)))
	assert.Equal(t, int64(tupleBytes+2*slotBytes+boxedBytes+minDataBytes), Size(starlark.Tuple{starlark.String("t"), starlark.None}))
	assert.Equal(t, int64(listBytes+2*slotBytes+boxedBytes+minDataBytes), Size(list))
	assert.Equal(t, tableBytes(1)+Size(starlark.String("k"))+Size(list), Size(dict))
	assert.Equal(t, tableBytes(1)+Size(starlark.String("member")), Size(set))
	assert.Positive(t, Size(st))
	assert.Zero(t, Size(starlark.NewBuiltin("f", nil)), "code is not data the script built")

	shared := starlark.NewList(nil)
	twice := starlark.NewList([]starlark.Value{shared, shared})
	assert.Equal(t, int64(listBytes+2*slotBytes+listBytes), Size(twice), "a container reached twice is counted once")
}

// TestSizeTracksTheHeap holds the calibration the constants were taken from:
// the estimate of values a script builds -- a page of row dicts, a list of
// kilobyte strings -- is within a quarter of what the Go heap holds for them.
func TestSizeTracksTheHeap(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes what an allocation costs")
	}
	cases := map[string]func() starlark.Value{
		"a page of 33-column rows": func() starlark.Value {
			rows := make([]starlark.Value, 0, 2000)
			for i := range 2000 {
				d := starlark.NewDict(33)
				for c := range 33 {
					_ = d.SetKey(starlark.String(fmt.Sprintf("column_%d", c)), starlark.String(fmt.Sprintf("value %d %d", i, c)))
				}
				rows = append(rows, d)
			}
			return starlark.NewList(rows)
		},
		"a list of kilobyte strings": func() starlark.Value {
			items := make([]starlark.Value, 0, 20000)
			for i := range 20000 {
				items = append(items, starlark.String(fmt.Sprintf("%01024d", i)))
			}
			return starlark.NewList(items)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			before := liveHeap()
			v := build()
			held := liveHeap() - before
			ratio := float64(Size(v)) / float64(held)
			runtime.KeepAlive(v)
			assert.InDelta(t, 1.0, ratio, 0.25, "estimate %d against %d live", Size(v), held)
		})
	}
}

func liveHeap() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc) //nolint:gosec // a heap size fits
}

// fileOptions is the dialect the managed-script engine parses under, as far
// as these scripts need it: top-level loops and comprehensions.
var fileOptions = &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}

// runScript executes source with a builtin "probe" that runs fn while the
// script is stopped in a host call, which is the only time the meter reads it.
func runScript(t *testing.T, source string, fn func(*starlark.Thread) (starlark.Value, error)) error {
	t.Helper()
	probe := starlark.NewBuiltin("probe", func(th *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return fn(th)
	})
	_, err := starlark.ExecFileOptions(fileOptions, &starlark.Thread{}, "t", source, starlark.StringDict{"probe": probe})
	return err //nolint:wrapcheck // the test reads it
}

func TestMeter_CheckWalksWhatTheScriptHolds(t *testing.T) {
	m := NewMeter(64 * kib)
	var checked error
	err := runScript(t, `
small = ["x"]
probe()
held = ["x" * 1024 + str(i) for i in range(200)]
probe()
`, func(th *starlark.Thread) (starlark.Value, error) {
		m.walked = time.Time{} // walk every call
		checked = m.Check(th, "platform.query")
		return starlark.None, checked
	})
	require.Error(t, err)
	require.ErrorIs(t, checked, ErrMemoryBudget)
	msg := checked.Error()
	for _, want := range []string{"in platform.query", "64 KiB memory budget", "append=True", "max_run_memory"} {
		assert.Contains(t, msg, want)
	}
	assert.Greater(t, m.Peak(), int64(200*1024))
}

func TestMeter_NoBudgetStillMeasuresThePeak(t *testing.T) {
	m := NewMeter(0)
	require.NoError(t, runScript(t, `held = ["y" * 4096 for i in range(50)]
probe()
`, func(th *starlark.Thread) (starlark.Value, error) {
		return starlark.None, m.Check(th, "platform.call")
	}))
	assert.Greater(t, m.Peak(), int64(50*4096))
}

func TestMeter_NilMeasuresNothing(t *testing.T) {
	var m *Meter
	require.NoError(t, m.Check(&starlark.Thread{}, "x"))
	require.NoError(t, m.Handed(nil, "x", starlark.String("v")))
	m.Called("tool")
	m.Holding(10)
	m.Settle(starlark.StringDict{"a": starlark.String("b")})
	assert.Zero(t, m.Peak())
	require.NoError(t, NewMeter(10).Check(nil, "x"), "no thread is nothing to measure")
}

func TestMeter_HandedConfirmsWithAWalkBeforeRefusing(t *testing.T) {
	m := NewMeter(32 * kib)
	// A running estimate over the budget from an earlier result the script
	// has since dropped: the walk finds it gone and the result fits.
	m.estimate, m.fresh = 64*kib, false
	var handed error
	require.NoError(t, runScript(t, "probe()\n", func(th *starlark.Thread) (starlark.Value, error) {
		handed = m.Handed(th, "platform.call", starlark.String("small"))
		return starlark.None, handed
	}))
	require.NoError(t, handed)

	// A result that is itself over the budget is refused, naming the tools
	// the run was handed results from.
	m.Called("api_invoke_endpoint")
	m.Called("api_invoke_endpoint")
	m.Called("trino_query")
	oversized := starlark.String(strings.Repeat("z", 64*kib))
	require.NoError(t, runScript(t, "probe()\n", func(th *starlark.Thread) (starlark.Value, error) {
		handed = m.Handed(th, "platform.call", oversized)
		return starlark.None, nil
	}))
	require.ErrorIs(t, handed, ErrMemoryBudget)
	assert.Contains(t, handed.Error(), "after 2 api_invoke_endpoint results and 1 trino_query result")
}

func TestMeter_CheckConfirmsAStaleOverEstimate(t *testing.T) {
	m := NewMeter(32 * kib)
	m.now = func() time.Time { return time.Unix(100, 0) }
	m.walked = m.now() // a recent walk: no walk on the way in
	m.estimate, m.fresh = 64*kib, false
	var checked error
	require.NoError(t, runScript(t, "probe()\n", func(th *starlark.Thread) (starlark.Value, error) {
		checked = m.Check(th, "platform.export")
		return starlark.None, nil
	}))
	require.NoError(t, checked, "the confirming walk found the run holds almost nothing")
}

func TestMeter_HoldingCountsMemoryOutsideTheInterpreter(t *testing.T) {
	m := NewMeter(32 * kib)
	m.Holding(40 * kib)
	assert.Equal(t, int64(40*kib), m.Peak())
	var checked error
	require.NoError(t, runScript(t, "probe()\n", func(th *starlark.Thread) (starlark.Value, error) {
		m.walked = time.Time{}
		checked = m.Check(th, "platform.export")
		return starlark.None, nil
	}))
	require.ErrorIs(t, checked, ErrMemoryBudget, "a walk keeps what the run holds outside it")
	m.Holding(0)
	assert.LessOrEqual(t, m.estimate, int64(32*kib))
}

func TestMeter_SettleRecordsWhatTheScriptEndedHolding(t *testing.T) {
	m := NewMeter(0)
	m.Settle(starlark.StringDict{"rows": starlark.String(strings.Repeat("r", 8*kib))})
	assert.Greater(t, m.Peak(), int64(8*kib))
}

func TestMeter_ResultSummaryWithNoResults(t *testing.T) {
	assert.Empty(t, NewMeter(1).resultSummary())
	m := NewMeter(1)
	m.estimate = 2
	assert.NotContains(t, m.refusal("platform.query").Error(), "after")
}
