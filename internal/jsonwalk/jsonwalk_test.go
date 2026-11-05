package jsonwalk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrings(t *testing.T) {
	upper := strings.ToUpper
	in := map[string]any{"a": "x", "n": 1.5, "l": []any{"y", 2.0, map[string]any{"b": "z"}}, "k": map[string]any{"c": true}}
	out, changed := Strings(in, upper)
	assert.True(t, changed)
	assert.Equal(t, map[string]any{"a": "X", "n": 1.5, "l": []any{"Y", 2.0, map[string]any{"b": "Z"}}, "k": map[string]any{"c": true}}, out)
	assert.Equal(t, "x", in["a"], "the value given is left as it was")
	list, ok := in["l"].([]any)
	require.True(t, ok)
	assert.Equal(t, "y", list[0])

	same := map[string]any{"a": "X", "l": []any{1.0}}
	out, changed = Strings(same, upper)
	assert.False(t, changed)
	assert.Equal(t, same, out)
	_, changed = Strings(nil, upper)
	assert.False(t, changed)
}
