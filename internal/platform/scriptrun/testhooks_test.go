package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// An execution made a test calls the entry it names, binds the test modules
// it is given, and shows it every output and staged state (#1939).
func TestATestExecutionCallsItsEntryAndIsShownItsOutputs(t *testing.T) {
	var seen []any
	cover := scriptdialect.NewCoverage()
	res, err := Run(context.Background(), Options{
		Source: `def main():
    """Writes and stages."""
    platform.export(name = "w", rows = [{"a": 1}], format = "csv")
    platform.publish_data("d", {"n": 1})
    platform.save_state({"n": 1})

def test_main():
    """Runs main through the bound module."""
    main()
    print(testing)
`,
		Test: &TestHooks{
			Entry:   "test_main",
			Env:     starlark.StringDict{TestingName: starlark.String("bound")},
			Observe: func(v any) { seen = append(seen, v) },
			Cover:   cover,
		},
	})
	require.NoError(t, err)
	assert.Contains(t, res.Log, "bound")
	require.Len(t, seen, 3)
	assert.IsType(t, ExportRequest{}, seen[0])
	assert.IsType(t, PublishRequest{}, seen[1])
	assert.IsType(t, &script.StateWrite{}, seen[2])
	assert.Equal(t, 4, cover.Covered())
}

// Outside a test, the step cap is the cap: an uninstrumented run is stopped
// at it.
func TestTheStepCapHoldsOutsideATest(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Source:   "def main():\n    t = 0\n    for i in range(100000):\n        t += i\n",
		MaxSteps: 1000,
		Test:     &TestHooks{},
	})
	assert.ErrorIs(t, err, ErrStepLimit)
}

// A run binds testing and assert to a value that refuses every use.
func TestTheTestModulesRefuseARun(t *testing.T) {
	stub := testOnly(AssertName)
	assert.Equal(t, "assert", stub.String())
	assert.Equal(t, "module", stub.Type())
	assert.Equal(t, starlark.True, stub.Truth())
	assert.Nil(t, stub.AttrNames())
	stub.Freeze()
	h, err := stub.Hash()
	require.NoError(t, err)
	want, _ := starlark.String("assert").Hash()
	assert.Equal(t, want, h)
	_, err = stub.Attr("eq")
	assert.EqualError(t, err, "module assert.eq is available only inside a test_* function; a run never calls one")
}

// A test's call is answered once, with no pacing and nothing recorded.
func TestATestCallIsIssuedOnce(t *testing.T) {
	calls := 0
	res, err := Run(context.Background(), Options{
		Source: "def test_x():\n    print(platform.call(\"t\", {})[\"ok\"])\n",
		Caller: callerFunc(func(context.Context, string, map[string]any) (map[string]any, error) {
			calls++
			return map[string]any{"ok": true}, nil
		}),
		OnCall: func(string, map[string]any, map[string]any, error) { t.Fatal("a test is not recorded") },
		Test:   &TestHooks{Entry: "test_x"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Contains(t, res.Log, "True")
}

// callerFunc is a Caller made of a function.
type callerFunc func(context.Context, string, map[string]any) (map[string]any, error)

func (f callerFunc) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	return f(ctx, name, args)
}
