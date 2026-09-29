package scriptflow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// dateWindows is a library in the shape #1941 saves one: no main(), public
// functions with docstrings, and a private helper a load cannot name.
const dateWindows = `WEEK = 7

def last_week(today, days = WEEK, *rest, **opts):
    """The seven days before today. A window ends the day before."""
    return _span(today, days)

def quarter_of(month):
    """Which quarter a month falls in."""
    return (month - 1) // 3 + 1

def undocumented(xs = [1, 2][0:1]):
    return xs

def _span(today, days):
    """Private."""
    return [today - days, today]

def test_last_week():
    """The window ends today."""
    assert.eq(last_week(10)[1], 10)
`

// #1970: a library names each function a load can name, with its parameters
// as written and its docstring's first sentence, in source order; the private
// helper and the library's test are not offered.
func TestLibraryOf_ListsTheFunctionsALoadCanName(t *testing.T) {
	require.True(t, Derive(dateWindows).OK)
	lib := LibraryOf(scriptlib.Ref{Name: "date-windows", Version: 2}, dateWindows)
	assert.Equal(t, []Function{
		{Name: "last_week", Params: []string{"today", "days=WEEK", "*rest", "**opts"}, Doc: "The seven days before today.", Line: 3},
		{Name: "quarter_of", Params: []string{"month"}, Doc: "Which quarter a month falls in.", Line: 7},
		{Name: "undocumented", Params: []string{"xs=[1, 2][0:1]"}, Line: 11},
	}, lib.Functions)
	assert.Equal(t, `load("lib:date-windows@2", "last_week", "quarter_of", "undocumented")`, lib.Load)

	// The statement is one a script can save: it parses, and names the
	// library version it was read from.
	file, err := scriptrun.Parse(lib.Load + "\n")
	require.NoError(t, err)
	loads := scriptlib.Loads(file)
	require.Len(t, loads, 1)
	require.NoError(t, loads[0].Err)
	assert.Equal(t, scriptlib.Ref{Name: "date-windows", Version: 2}, loads[0].Ref)
}

// Derive leaves Library unset, since the source alone does not say whether a
// script is a library; a library with no public function, and a source that
// does not parse, carry an empty list, never null, and no load statement.
func TestLibraryOf_EmptyIsAnEmptyList(t *testing.T) {
	raw, err := json.Marshal(Derive(dateWindows))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"library"`)

	for _, src := range []string{"def _only():\n    \"\"\"Private.\"\"\"\n    return 1\n", "def f(:\n"} {
		raw, err := json.Marshal(LibraryOf(scriptlib.Ref{Name: "x", Version: 1}, src))
		require.NoError(t, err)
		assert.JSONEq(t, `{"functions":[]}`, string(raw), src)
	}
}
