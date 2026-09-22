package scriptsum_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptsum"
)

// run executes source with sum predeclared and returns what it printed.
func run(t *testing.T, source string) (string, error) {
	t.Helper()
	var out strings.Builder
	thread := &starlark.Thread{Print: func(_ *starlark.Thread, msg string) { _, _ = out.WriteString(msg + "\n") }}
	_, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "test.star", source,
		starlark.StringDict{scriptsum.Name: scriptsum.Builtin})
	return out.String(), err
}

// TestSum_Semantics pins the signature against Python's, which is the one the
// author already knows.
func TestSum_Semantics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"integers stay integers", `print(sum([1, 2, 3]))`, "6"},
		{"an empty iterable is zero", `print(sum([]))`, "0"},
		{"start is added", `print(sum([1, 2], 10))`, "13"},
		{"start may be named", `print(sum([1, 2], start = 10))`, "13"},
		{"a tuple is an iterable", `print(sum((1, 2, 3)))`, "6"},
		{"a range is an iterable", `print(sum(range(4)))`, "6"},
		{"floats fold to a float", `print(sum([1.5, 2.5]))`, "4.0"},
		{"a float start promotes", `print(sum([1, 2], 0.5))`, "3.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, tt.source+"\n")
			require.NoError(t, err)
			assert.Equal(t, tt.want+"\n", out)
		})
	}
}

// TestSum_RefusesANonNumberByPosition is the failure an author actually hits:
// the raw DECIMAL string. Concatenating it would report a wrong total as a
// right one, so the run fails and the message says which element and what to
// do about it.
func TestSum_RefusesANonNumberByPosition(t *testing.T) {
	_, err := run(t, `print(sum([1.0, "4.25"]))`+"\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 1 is string, not a number")
	assert.Contains(t, err.Error(), "float()")
}

// TestSum_RefusesABoolean guards the silent-wrong-answer case: Starlark will
// add True as 1, and a total that counted a flag as a unit is a number nobody
// can tell is wrong.
func TestSum_RefusesABoolean(t *testing.T) {
	_, err := run(t, `print(sum([1, True]))`+"\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 1 is bool, not a number")
}

// TestSum_RefusesANonNumberStart covers the other argument.
func TestSum_RefusesANonNumberStart(t *testing.T) {
	_, err := run(t, `print(sum([1], "x"))`+"\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start must be a number, got string")
}

// TestSum_RefusesANonIterable is starlark.UnpackArgs' own refusal, carried
// through with the builtin's name.
func TestSum_RefusesANonIterable(t *testing.T) {
	_, err := run(t, `print(sum(3))`+"\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in sum:")
}
