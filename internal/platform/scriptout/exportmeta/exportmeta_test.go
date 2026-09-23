package exportmeta

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func strList(values ...string) *starlark.List {
	out := make([]starlark.Value, 0, len(values))
	for _, v := range values {
		out = append(out, starlark.String(v))
	}
	return starlark.NewList(out)
}

func dict(kv map[string]starlark.Value) *starlark.Dict {
	d := starlark.NewDict(len(kv))
	for k, v := range kv {
		_ = d.SetKey(starlark.String(k), v)
	}
	return d
}

// tags= and metadata= (#1848): read, bounded, portal-only, and the platform's
// own keys are refused.
func TestDescribe(t *testing.T) {
	tags, meta, err := Describe(strList("report:sales", " tenant:x "), dict(map[string]starlark.Value{"region": starlark.String("west")}), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"report:sales", "tenant:x"}, tags)
	assert.Equal(t, map[string]any{"region": "west"}, meta)

	tags, meta, err = Describe(nil, starlark.None, false)
	require.NoError(t, err)
	assert.Nil(t, tags)
	assert.Nil(t, meta)

	tags, meta, err = Describe(strList("a"), nil, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, tags)
	assert.Nil(t, meta)

	many := make([]string, maxTags+1)
	for i := range many {
		many[i] = "t"
	}
	for name, tc := range map[string]struct {
		tags, meta starlark.Value
		portal     bool
		want       string
	}{
		"not the portal":   {strList("a"), nil, false, "portal output"},
		"tags not a list":  {starlark.String("a"), nil, true, "list of strings"},
		"too many tags":    {strList(many...), nil, true, "at most"},
		"an empty tag":     {strList(" "), nil, true, "tag 0"},
		"a long tag":       {strList(strings.Repeat("x", maxTagLen+1)), nil, true, "tag 0"},
		"a tag not string": {starlark.NewList([]starlark.Value{starlark.MakeInt(1)}), nil, true, "tag 0"},
		"meta not a dict":  {nil, starlark.String("x"), true, "is a dict"},
		"a platform key":   {nil, dict(map[string]starlark.Value{"run_id": starlark.String("x")}), true, "recorded by the platform"},
		"meta too large":   {nil, dict(map[string]starlark.Value{"blob": starlark.String(strings.Repeat("x", maxMetadataBytes))}), true, "at most"},
		"meta not JSON":    {nil, dict(map[string]starlark.Value{"f": starlark.NewBuiltin("f", nil)}), true, "metadata"},
	} {
		_, _, err := Describe(tc.tags, tc.meta, tc.portal)
		assert.ErrorContains(t, err, tc.want, name)
	}
}
