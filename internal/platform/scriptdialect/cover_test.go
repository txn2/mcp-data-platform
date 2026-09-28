package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// branchy is a script with a branch, a loop, a docstring, a pass and a test.
const branchy = `LIMIT = 3

def half(n):
    """Half of n."""
    if n % 2:
        fail("odd")
    else:
        pass
    return n // 2

def main():
    """Halves a few."""
    total = 0
    for i in [2, 4]:
        total += half(i)
    print(total)

def test_main():
    """Not counted."""
    main()
`

func execSource(t *testing.T, source string, hooks Hooks) (starlark.StringDict, error) {
	t.Helper()
	thread := &starlark.Thread{Print: func(*starlark.Thread, string) {}}
	env := starlark.StringDict{"fail": starlark.Universe["fail"]}
	return Exec(thread, "s", source, env, hooks)
}

// Coverage counts every statement that compiles to code outside the tests,
// and reports by line the ones that never ran.
func TestCoverageCountsTheStatementsThatRan(t *testing.T) {
	cover := NewCoverage()
	_, err := execSource(t, branchy, Hooks{Cover: cover})
	require.NoError(t, err)
	// LIMIT =, def half, if, fail, return, def main, total =, for, total +=,
	// print: the docstrings and pass are not code, and a test, its def
	// included, is not counted.
	assert.Equal(t, 10, cover.Total())
	assert.Equal(t, 9, cover.Covered(), "only the fail never ran")
	assert.Equal(t, []int{6}, cover.MissedLines())
	assert.InDelta(t, 90.0, cover.Percent(), 0.001)
	assert.Positive(t, cover.Calls())

	// A second run of the same source shares the table.
	_, err = execSource(t, branchy, Hooks{Cover: cover, Entry: "test_main"})
	require.NoError(t, err)
	assert.Equal(t, 10, cover.Total())

	assert.InDelta(t, 100.0, NewCoverage().Percent(), 0, "a script with no statements is all covered")
}

// Exec calls the entry it is asked for instead of main, and refuses one the
// script does not define.
func TestExecCallsTheNamedEntry(t *testing.T) {
	var printed []string
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { printed = append(printed, msg) }}
	source := "def main():\n    print(\"main\")\n\ndef test_it():\n    print(\"test\")\n"
	_, err := Exec(thread, "s", source, starlark.StringDict{}, Hooks{Entry: "test_it"})
	require.NoError(t, err)
	assert.Equal(t, []string{"test"}, printed)

	_, err = execSource(t, source, Hooks{Entry: "test_missing"})
	assert.EqualError(t, err, "the script defines no function test_missing")

	_, err = execSource(t, "x = (\n", Hooks{})
	assert.Error(t, err, "a parse error is the parser's")
	_, err = execSource(t, "print(undefined)\n", Hooks{})
	assert.Error(t, err, "a resolve error is the resolver's")
	globals, err := execSource(t, "fail(\"at load\")\n", Hooks{})
	assert.Error(t, err)
	assert.NotNil(t, globals)
	_, err = execSource(t, "X = 1\n", Hooks{})
	assert.NoError(t, err, "a script with no main calls nothing")
}

// A test is a top-level def whose name starts with test_, in source order.
func TestTestsAreTheTopLevelTestDefs(t *testing.T) {
	file, err := Options.Parse("s", branchy+"\ndef test_other():\n    pass\n", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"test_main", "test_other"}, Tests(file))
	assert.False(t, IsTest(&syntax.ExprStmt{X: &syntax.Ident{Name: "test_x"}}))
}

// The cover builtin ignores what it was not handed.
func TestCoverIgnoresAnArgumentItDidNotNumber(t *testing.T) {
	c := NewCoverage()
	for _, args := range []starlark.Tuple{nil, {starlark.String("x")}, {starlark.MakeInt(-1)}, {starlark.MakeInt(9)}} {
		_, err := c.cover(nil, nil, args, nil)
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(4), c.Calls())
	assert.Zero(t, c.Covered())
}

// The end-of-main hook runs as main ends, and a hook that fails fails the run.
func TestExecRunsTheEndOfMainHook(t *testing.T) {
	ended := 0
	_, err := execSource(t, "def main():\n    print(1)\n", Hooks{AtMainEnd: func(*starlark.Thread) error {
		ended++
		return nil
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, ended)

	_, err = execSource(t, "def main():\n    return 1\n", Hooks{AtMainEnd: func(*starlark.Thread) error {
		return assert.AnError
	}})
	assert.ErrorIs(t, err, assert.AnError)

	_, err = execSource(t, "test_x = 1\n", Hooks{Entry: "test_x"})
	assert.EqualError(t, err, "the script defines no function test_x", "an entry that is not a function is not one to call")
}

// A main ending in an if with no else is instrumented and still measured as
// main ends: the absent else stays absent.
func TestAnIfWithNoElseEndingMainIsInstrumented(t *testing.T) {
	cover := NewCoverage()
	ended := 0
	_, err := execSource(t, "def main():\n    \"\"\"Ends in an if.\"\"\"\n    if True:\n        print(1)\n", Hooks{
		Cover: cover, AtMainEnd: func(*starlark.Thread) error { ended++; return nil },
	})
	require.NoError(t, err)
	assert.Equal(t, 1, ended)
	assert.Equal(t, 3, cover.Total())
	assert.Equal(t, 3, cover.Covered())
}
