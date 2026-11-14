package scriptdialect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// TestDefOf finds a source's last top-level def of a name with the number of
// parameters a handler kind's contract calls it with.
func TestDefOf(t *testing.T) {
	parse := func(src string) *syntax.File {
		t.Helper()
		f, err := (&syntax.FileOptions{}).Parse("s.star", src, 0)
		require.NoError(t, err)
		return f
	}
	f := parse("def transform(call):\n    pass\n\ndef transform(call, result):\n    return result\n")
	d := DefOf(f, "transform", 2)
	require.NotNil(t, d, "the last def is the one that runs")
	assert.Len(t, d.Params, 2)
	assert.Nil(t, DefOf(f, "transform", 1), "the last def takes two")
	assert.Nil(t, DefOf(parse("def main():\n    pass\n"), "decide", 1))
	assert.Nil(t, DefOf(nil, "decide", 1))
	assert.Nil(t, HandlerOf(nil))
}
