package starlarkconv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColumnNames(t *testing.T) {
	cases := map[string]struct {
		in   any
		want []string
	}{
		"objects":           {[]any{map[string]any{"name": "b"}, map[string]any{"name": "a"}}, []string{"b", "a"}},
		"plain names":       {[]any{"b", "a"}, []string{"b", "a"}},
		"absent":            {nil, nil},
		"empty":             {[]any{}, nil},
		"entry names none":  {[]any{map[string]any{"type": "varchar"}}, nil},
		"entry of any kind": {[]any{"b", 3.0}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, ColumnNames(tc.in))
		})
	}
}

// TestRowsToStarlark covers what a column list does not say: a key it does
// not name follows in sorted order, a repeated name is set once, and a row
// that is not an object converts as itself.
func TestRowsToStarlark(t *testing.T) {
	rows := []any{
		map[string]any{"a": 1.0, "z": "x", "extra2": true, "extra1": nil},
		"not an object",
	}
	v, err := RowsToStarlark(rows, []string{"z", "missing", "a", "z"})
	require.NoError(t, err)
	assert.Equal(t, `[{"z": "x", "a": 1, "extra1": None, "extra2": True}, "not an object"]`, v.String())
}

func TestRowsToStarlark_RefusesAnUnconvertibleValue(t *testing.T) {
	_, err := RowsToStarlark([]any{map[string]any{"a": struct{}{}}}, []string{"a"})
	require.ErrorContains(t, err, `key "a"`)
	_, err = RowsToStarlark([]any{map[string]any{"b": struct{}{}}}, nil)
	require.ErrorContains(t, err, `key "b"`)
	_, err = RowsToStarlark([]any{struct{}{}}, nil)
	require.Error(t, err)
}

// TestResultToStarlark keeps every other field of the result, converted as
// ToStarlark converts it, and orders only the rows.
func TestResultToStarlark(t *testing.T) {
	out := map[string]any{
		"columns":   []any{map[string]any{"name": "z"}, map[string]any{"name": "a"}},
		"rows":      []any{map[string]any{"a": 1.0, "z": 2.0}},
		"row_count": 1.0,
	}
	v, err := ResultToStarlark(out)
	require.NoError(t, err)
	assert.Equal(t, `{"columns": [{"name": "z"}, {"name": "a"}], "row_count": 1, "rows": [{"z": 2, "a": 1}]}`, v.String())
}

func TestResultToStarlark_NonTabularIsToStarlark(t *testing.T) {
	out := map[string]any{"b": 1.0, "a": []any{"x"}}
	v, err := ResultToStarlark(out)
	require.NoError(t, err)
	want, err := ToStarlark(out)
	require.NoError(t, err)
	assert.Equal(t, want.String(), v.String())
}

func TestResultToStarlark_RefusesAnUnconvertibleField(t *testing.T) {
	cols := []any{map[string]any{"name": "a"}}
	_, err := ResultToStarlark(map[string]any{"columns": cols, "rows": []any{}, "stats": struct{}{}})
	require.Error(t, err)
	_, err = ResultToStarlark(map[string]any{"columns": cols, "rows": []any{struct{}{}}})
	require.Error(t, err)
}
