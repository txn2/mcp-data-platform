package scriptshare

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dateWindows = `
def last_week(day):
    """The seven days before day, as [start, end]."""
    end = day - 1
    start = end - 6
    return [start, end]
`

// priorWeek is last_week under another name, with other local names, another
// docstring, comments and other layout.
const priorWeek = `
load("lib:other@1", "x")

def prior_week(d):
    # the week before d
    stop = d - 1   # yesterday
    begin = stop - 6
    return [begin,
            stop]

def main():
    """Runs."""
    return prior_week(10)
`

const pageThrough = `
def page_through(fetch, size = 100):
    """Reads every page."""
    rows = []
    offset = 0
    for _ in range(1000):
        page = fetch(offset, size)
        rows.extend(page)
        if len(page) < size:
            break
        offset += size
    return rows

def test_page_through():
    """Tests it."""
    return page_through(lambda o, s: [], 10)

def main():
    """Runs."""
    return page_through(lambda o, s: [], 10)
`

// TestFind_ALibraryFunctionNamesTheLoadLine is #2273's first criterion: a
// function that is a library's function under another name, with other local
// names, comments and layout, is reported with the library, its version and
// the exact load line, and the entry point and a shared module name are not.
func TestFind_ALibraryFunctionNamesTheLoadLine(t *testing.T) {
	others := []Source{{ID: "lib1", Name: "date-windows", Library: true, Version: 1, Code: dateWindows}}
	got := Find(priorWeek, "s2", false, others)
	require.Len(t, got, 1)
	assert.Equal(t, Match{
		Function: "prior_week", Library: "date-windows", Version: 1, LibraryFunction: "last_week",
		Load: `load("lib:date-windows@1", "last_week")`,
		Next: `Remove prior_week and add load("lib:date-windows@1", "last_week"); the library holds the same function.`,
	}, got[0])
}

// TestFind_AnotherScriptsFunction is #2273's second criterion: a helper
// another script also defines is reported naming that script, with a next
// step offering a library; test functions never are; a script's own earlier
// version is not another script.
func TestFind_AnotherScriptsFunction(t *testing.T) {
	a := Source{ID: "a", Name: "script-a", Code: pageThrough}
	assert.Equal(t, []Match{}, Find(pageThrough, "a", false, []Source{a}), "a script is not compared with itself")

	got := Find(strings.ReplaceAll(pageThrough, "rows", "acc"), "b", false, []Source{a})
	require.Len(t, got, 1)
	assert.Equal(t, "page_through", got[0].Function)
	assert.Equal(t, []string{"script-a"}, got[0].Scripts)
	assert.Contains(t, got[0].Next, "It can move into a library")
	assert.Contains(t, got[0].Next, "their owners' agreement")
	assert.Empty(t, got[0].Library)

	lib := Find(pageThrough, "lib", true, []Source{a, {ID: "c", Name: "script-c", Code: pageThrough}})
	require.Len(t, lib, 1)
	assert.Equal(t, []string{"script-a", "script-c"}, lib[0].Scripts)
	assert.Contains(t, lib[0].Next, "load it from this library")
}

// TestFind_WhatCountsAsTheSame is #2273's fourth and sixth criteria: one
// changed expression is another function; comments, formatting and local
// names are not; and a match carries names, never source.
func TestFind_WhatCountsAsTheSame(t *testing.T) {
	lib := []Source{{ID: "lib1", Name: "date-windows", Library: true, Version: 3, Code: dateWindows}}
	for name, src := range map[string]string{
		"one constant changed":   strings.Replace(dateWindows, "end - 6", "end - 7", 1),
		"one operator changed":   strings.Replace(dateWindows, "day - 1", "day + 1", 1),
		"a global read in place": strings.Replace(dateWindows, "end = day - 1", "end = TODAY - 1", 1),
		"a statement more":       strings.Replace(dateWindows, "return [start, end]", "start = start\n    return [start, end]", 1),
		"too small to matter":    "def f(x):\n    return x + 1\n",
		"does not parse":         "def broken(:\n",
	} {
		assert.Empty(t, Find(src, "s", false, lib), name)
	}
	same := "def lw(x):\n    \"\"\"Other words.\"\"\"\n    e = x - 1\n    s = e - 6\n    return [s, e]\n"
	got := Find(same, "s", false, lib)
	require.Len(t, got, 1)
	assert.NotContains(t, got[0].Next, "- 6", "a match carries no source")
	assert.Equal(t, 3, got[0].Version)
}

func TestFind_TheOwnNameAndAttributesAndKeywords(t *testing.T) {
	recursive := func(name string) string {
		return "def " + name + "(n, acc = []):\n    if n <= 0:\n        return acc\n    acc.append(n)\n    return " + name + "(n - 1, acc = acc)\n"
	}
	got := Find(recursive("countdown"), "s", false, []Source{{ID: "o", Name: "other", Code: recursive("down")}})
	require.Len(t, got, 1, "a recursive call to the function's own name is the same call under any name")
	attr := "def get(row):\n    value = row.value\n    total = value + row.extra\n    return dict(value = total, row = row)\n"
	renamed := "def get(r):\n    v = r.value\n    t = v + r.extra\n    return dict(value = t, row = r)\n"
	other := "def get(r):\n    v = r.other\n    t = v + r.extra\n    return dict(value = t, row = r)\n"
	assert.Len(t, Find(renamed, "s", false, []Source{{ID: "o", Name: "o", Code: attr}}), 1, "attribute and keyword names stay as written")
	assert.Empty(t, Find(other, "s", false, []Source{{ID: "o", Name: "o", Code: attr}}), "another attribute is another function")
}

// Every way a function binds a name is a local, so renaming any of them is the
// same function: a parameter with a default, *args and **kwargs, a tuple, list
// or parenthesised assignment, loop and comprehension variables, a nested def
// and a lambda's parameter. A literal counts by its value, not how it is
// written, and a keyword argument's name is never a local.
func TestFind_EveryBindingIsALocal(t *testing.T) {
	shape := func(n []string, quote, sixteen string) string {
		r := strings.NewReplacer("A", n[0], "B", n[1], "C", n[2], "D", n[3], "E", n[4], "F", n[5], "G", n[6], "H", n[7], "Q", quote, "X", sixteen)
		return r.Replace(`def tally(A, B = 2, *C, **D):
    E, F = A, B
    [G] = [E]
    (H) = F
    for k, v in D.items():
        if not k:
            continue
        H = H + -v
    seen = [w for w in C if w > X]
    def bump(z):
        return z + G
    twice = lambda y: bump(y) * 2
    return {QtotalQ: twice(H), QseenQ: seen[1:], QkeyQ: sorted(seen, reverse = True)}
`)
	}
	lib := []Source{{
		ID: "o", Name: "counts", Library: true, Version: 1,
		Code: shape([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, `"`, "16"),
	}}
	renamed := shape([]string{"p", "q", "rest", "opts", "m", "n", "o", "s"}, `'`, "0x10")
	got := Find(renamed, "s", false, lib)
	require.Len(t, got, 1, "renamed locals, another quote and another spelling of 16 are one function")
	assert.Equal(t, "tally", got[0].LibraryFunction)

	kw := strings.Replace(renamed, "reverse = True", "key = True", 1)
	assert.Empty(t, Find(kw, "s", false, lib), "a keyword argument's name is not a local")
	sliced := strings.Replace(renamed, "seen[1:]", "seen[:1]", 1)
	assert.Empty(t, Find(sliced, "s", false, lib), "another slice is another function")
}

func TestListProperties_DescribesTheLibraryFilter(t *testing.T) {
	props := ListProperties()
	lib, ok := props["library"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", lib["type"])
	assert.Contains(t, lib["description"], "true lists only libraries")
}
