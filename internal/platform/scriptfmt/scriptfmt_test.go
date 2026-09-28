package scriptfmt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Format lays source out and keeps the program; source it cannot lay out
// without changing the program, or cannot read, is kept as sent.
func TestFormat(t *testing.T) {
	assert.Equal(t, "X = [1, 2]\n", Format("X=[1,2]\n"))
	assert.Equal(t, "def f(:\n", Format("def f(:\n"), "the printer cannot read it")
	assert.Equal(t, "x = \"a'b\"\n", Format("x = 'a\\'b'\n"), "an escaped quote keeps its value")

	// A formatted source that differed from the one sent in any value would
	// not be the same program.
	assert.True(t, sameProgram("x = 1  # one\n", "x = 1 # one\n"))
	assert.False(t, sameProgram("x = 1\n", "x = 2\n"), "a literal value differs")
	assert.False(t, sameProgram("x = 1  # one\n", "x = 1\n"), "a comment was dropped")
	assert.False(t, sameProgram("x = 1\n", "x = (\n"), "the output does not parse")
	assert.False(t, sameProgram("x = (\n", "x = 1\n"), "the input does not parse")
}
