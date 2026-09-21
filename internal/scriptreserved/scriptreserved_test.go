package scriptreserved

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// parseError parses src and returns the syntax error it produces.
func parseError(t *testing.T, src string) syntax.Error {
	t.Helper()
	_, err := (&syntax.FileOptions{}).Parse("s", src, 0)
	var e syntax.Error
	require.True(t, errors.As(err, &e), "no syntax error for %q: %v", src, err)
	return e
}

func TestMisused(t *testing.T) {
	cases := []struct {
		name, src, word string
	}{
		{"function", "def load(t):\n    pass\n", "load"},
		{"parameter", "def f(load):\n    pass\n", "load"},
		{"later parameter", "def f(x, pass):\n    pass\n", "pass"},
		{"attribute", "x = {}\ny = x.load\n", "load"},
		{"read as a value", "x = load\n", "load"},
		{"loop target", "for as in [1]:\n    pass\n", "as"},
		{"assignment", "load = 1\n", "load"},
		{"non-ASCII before the word", "s = \"é\"; y = s.lambda\n", "lambda"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			word, ok := Misused(tc.src, parseError(t, tc.src))
			assert.True(t, ok)
			assert.Equal(t, tc.word, word)
		})
	}
}

func TestMisused_OtherErrors(t *testing.T) {
	for _, src := range []string{"x = (\n", `load("m.star", "x")` + "\nload(\n", "def f(:\n    pass\n"} {
		_, ok := Misused(src, parseError(t, src))
		assert.False(t, ok, src)
	}
}

// TestWords_AreTheScannersKeywords pins the set to the parser: every word in
// it is refused as a name, and the ones an ETL author reaches for are in it.
func TestWords_AreTheScannersKeywords(t *testing.T) {
	got := Words()
	require.Contains(t, got, "load")
	require.Contains(t, got, "pass")
	require.NotContains(t, got, "not in")
	require.IsIncreasing(t, got)
	for _, w := range got {
		_, err := (&syntax.FileOptions{}).Parse("s", "def "+w+"():\n    pass\n", 0)
		require.Error(t, err, w)
	}
	got[0] = "mutated"
	assert.NotEqual(t, "mutated", Words()[0], "Words hands out a copy")
}

func TestWordBefore(t *testing.T) {
	assert.Equal(t, "load", wordBefore("def load(", 9))
	assert.Equal(t, "load", wordBefore("def load  (", 11))
	assert.Empty(t, wordBefore("(", 1))
	assert.Empty(t, wordBefore("", 5))
	assert.Equal(t, "ab", wordBefore("ab", 99))
}

func TestSourceLine(t *testing.T) {
	assert.Equal(t, "b", sourceLine("a\nb\n", 2))
	assert.Empty(t, sourceLine("a", 0))
	assert.Empty(t, sourceLine("a", 3))
}

func TestStartsWithLoadName(t *testing.T) {
	assert.True(t, startsWithLoadName("load = 1"))
	assert.True(t, startsWithLoadName("\tload.x = 1"))
	assert.False(t, startsWithLoadName(`load("m", "x")`))
	assert.False(t, startsWithLoadName(`load ("m", "x")`))
	assert.False(t, startsWithLoadName("loader = 1"))
	assert.False(t, startsWithLoadName("load"))
	assert.False(t, startsWithLoadName("x = load"))
}
