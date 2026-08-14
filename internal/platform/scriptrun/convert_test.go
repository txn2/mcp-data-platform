package scriptrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestToStarlark_Scalars(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "None"},
		{"bool", true, "True"},
		{"string", "x", `"x"`},
		{"int", 7, "7"},
		{"int64", int64(7), "7"},
		{"whole float becomes an int", float64(42), "42"},
		{"fractional float stays a float", 1.5, "1.5"},
		{"huge float stays a float", 1e300, "1e+300"},
		{"list", []any{int64(1), "a"}, `[1, "a"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toStarlark(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

// TestToStarlark_DictKeysAreSorted is the determinism guarantee at the
// conversion boundary: Go map iteration is randomized, so an unsorted
// conversion would give a script a different insertion order every run and
// json.encode would follow it.
func TestToStarlark_DictKeysAreSorted(t *testing.T) {
	in := map[string]any{"z": int64(1), "a": int64(2), "m": int64(3)}
	for range 25 {
		got, err := toStarlark(in)
		require.NoError(t, err)
		assert.Equal(t, `{"a": 2, "m": 3, "z": 1}`, got.String())
	}
}

func TestToStarlark_Refusals(t *testing.T) {
	_, err := toStarlark(struct{ A int }{1})
	assert.ErrorContains(t, err, "unsupported value of type")

	// Nesting is bounded rather than recursed to a stack overflow.
	deep := any("leaf")
	for range maxConvertDepth + 2 {
		deep = []any{deep}
	}
	_, err = toStarlark(deep)
	assert.ErrorContains(t, err, "nests deeper than")

	deepDict := any("leaf")
	for range maxConvertDepth + 2 {
		deepDict = map[string]any{"k": deepDict}
	}
	_, err = toStarlark(deepDict)
	assert.ErrorContains(t, err, "nests deeper than")
}

func TestFromStarlark_RoundTrip(t *testing.T) {
	in := map[string]any{
		"s": "x", "n": int64(3), "f": 1.5, "b": true, "none": nil,
		"list": []any{int64(1), map[string]any{"k": "v"}},
	}
	sv, err := toStarlark(in)
	require.NoError(t, err)
	back, err := fromStarlark(sv)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestFromStarlark_Refusals(t *testing.T) {
	_, err := fromStarlark(starlark.Tuple{starlark.String("a")})
	assert.ErrorContains(t, err, "cannot leave the script")

	d := starlark.NewDict(1)
	require.NoError(t, d.SetKey(starlark.MakeInt(1), starlark.String("v")))
	_, err = fromStarlark(d)
	assert.ErrorContains(t, err, "keys must be strings")

	nested := starlark.NewDict(1)
	require.NoError(t, nested.SetKey(starlark.String("k"), starlark.Tuple{}))
	_, err = fromStarlark(nested)
	assert.ErrorContains(t, err, `key "k"`)

	list := starlark.NewList([]starlark.Value{starlark.Tuple{}})
	_, err = fromStarlark(list)
	assert.ErrorContains(t, err, "cannot leave the script")

	// An integer past 64 bits has no Go counterpart to carry it.
	huge, err := starlark.Call(&starlark.Thread{}, starlark.Universe["int"], starlark.Tuple{starlark.String("9" + "0000000000000000000")}, nil)
	require.NoError(t, err)
	_, err = fromStarlark(huge)
	assert.ErrorContains(t, err, "does not fit in 64 bits")
}

func TestSortedSet(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, sortedSet(map[string]bool{"c": true, "a": true, "b": true}))
}
