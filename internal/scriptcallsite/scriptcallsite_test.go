package scriptcallsite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Of reads the script frames of the running thread, outermost first, at each
// call's opening parenthesis, and leaves the builtin out.
func TestOf(t *testing.T) {
	var got []string
	record := starlark.NewBuiltin("record", func(th *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		got = Of(th)
		return starlark.None, nil
	})
	src := "def f():\n    record()\n\ndef g():\n    f()\n\ng()\n"
	opts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}
	// A run executes under the script's own name, not a fixed file name.
	_, err := starlark.ExecFileOptions(opts, &starlark.Thread{}, "nightly-orders", src, starlark.StringDict{"record": record})
	require.NoError(t, err)
	assert.Equal(t, []string{"7:2", "5:6", "2:11"}, got)
	assert.Nil(t, Of(nil))
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, From(ctx))
	assert.Equal(t, ctx, With(ctx, nil), "no site, no value")
	assert.Equal(t, []string{"1:2"}, From(With(ctx, []string{"1:2"})))
}

func TestFromMeta(t *testing.T) {
	assert.Equal(t, []string{"1:2", "30:4"}, FromMeta(map[string]any{MetaKey: []any{"1:2", "30:4"}}))
	assert.Equal(t, []string{"1:2"}, FromMeta(map[string]any{MetaKey: []string{"1:2"}}))
	for name, v := range map[string]any{
		"absent":        nil,
		"not a list":    "1:2",
		"not strings":   []any{1, 2},
		"malformed":     []any{"1-2"},
		"zero line":     []any{"0:1"},
		"empty":         []any{},
		"far too deep":  make([]any, maxDepth+1),
		"typed garbage": []string{"x"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, FromMeta(map[string]any{MetaKey: v}))
		})
	}
	assert.Nil(t, FromMeta(nil))
}

func TestFromBacktrace(t *testing.T) {
	bt := "Traceback (most recent call last):\n  nightly-orders:40:14: in <toplevel>\n  nightly-orders:31:20: in summarize\n  <builtin>: in export\nError in export: refused"
	assert.Equal(t, []string{"40:14", "31:20"}, FromBacktrace(bt))
	assert.Nil(t, FromBacktrace("worker lost"))
	assert.Nil(t, FromBacktrace(""))
}
