package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestValidateParams(t *testing.T) {
	cases := []struct {
		name    string
		params  []script.Param
		wantErr string
	}{
		{"ok", []script.Param{
			{Name: "day", Type: script.ParamTypeDate, Required: true},
			{Name: "grain", Type: script.ParamTypeEnum, Values: []string{"day", "week"}, Default: "day"},
		}, ""},
		{"bad name", []script.Param{{Name: "Day", Type: script.ParamTypeDate}}, "lowercase letter"},
		{"duplicate", []script.Param{
			{Name: "day", Type: script.ParamTypeDate, Required: true},
			{Name: "day", Type: script.ParamTypeString},
		}, "duplicate parameter"},
		// An omitted optional enum or date would bind to "", which is not one of
		// the enum's values and is not a date, so the contract must say what the
		// parameter means when it is not supplied.
		{"optional enum without a default", []script.Param{
			{Name: "grain", Type: script.ParamTypeEnum, Values: []string{"day"}},
		}, "must declare a default"},
		{"optional date without a default", []script.Param{
			{Name: "day", Type: script.ParamTypeDate},
		}, "must declare a default"},
		{"unknown type", []script.Param{{Name: "day", Type: "timestamp"}}, "invalid type"},
		{"enum without values", []script.Param{{Name: "grain", Type: script.ParamTypeEnum}}, "must list its allowed values"},
		{"values on non-enum", []script.Param{{Name: "day", Type: script.ParamTypeString, Values: []string{"a"}}}, "only an enum parameter"},
		{"bad default", []script.Param{{Name: "n", Type: script.ParamTypeInt, Default: "twelve"}}, "default for parameter"},
		{"too many", make([]script.Param, 33), "too many parameters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := script.ValidateParams(tc.params)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestBindParams_Contract covers what actually reaches a running script: every
// declared name present exactly once, defaults applied through the same
// coercion as caller input, and an undeclared name refused rather than dropped.
func TestBindParams_Contract(t *testing.T) {
	defs := []script.Param{
		{Name: "day", Type: script.ParamTypeDate, Required: true},
		{Name: "limit", Type: script.ParamTypeInt, Default: 100},
		{Name: "grain", Type: script.ParamTypeEnum, Values: []string{"day", "week"}, Default: "day"},
		{Name: "verbose", Type: script.ParamTypeBool},
		{Name: "ratio", Type: script.ParamTypeFloat},
		{Name: "label", Type: script.ParamTypeString},
	}

	bound, err := script.BindParams(defs, map[string]any{"day": "2026-08-13"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"day": "2026-08-13", "limit": int64(100), "grain": "day",
		"verbose": false, "ratio": float64(0), "label": "",
	}, bound, "omitted optionals are typed zeros, not None, so a script never branches on absence")

	// JSON decoding hands every number back as float64.
	bound, err = script.BindParams(defs, map[string]any{"day": "2026-08-13", "limit": float64(5)})
	require.NoError(t, err)
	assert.Equal(t, int64(5), bound["limit"])
}

func TestBindParams_Refusals(t *testing.T) {
	defs := []script.Param{
		{Name: "day", Type: script.ParamTypeDate, Required: true},
		{Name: "grain", Type: script.ParamTypeEnum, Values: []string{"day", "week"}, Default: "day"},
	}
	cases := []struct {
		name    string
		values  map[string]any
		wantErr string
	}{
		{"missing required", map[string]any{}, `parameter "day" is required`},
		{"undeclared name", map[string]any{"day": "2026-08-13", "dya": "x"}, "unknown parameter"},
		{"bad date", map[string]any{"day": "13/08/2026"}, "YYYY-MM-DD"},
		{"date not a string", map[string]any{"day": 20260813}, "YYYY-MM-DD"},
		{"enum not listed", map[string]any{"day": "2026-08-13", "grain": "month"}, "is not one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := script.BindParams(defs, tc.values)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBindParams_Coercion(t *testing.T) {
	cases := []struct {
		name  string
		param script.Param
		in    any
		want  any
	}{
		{"int from string", script.Param{Name: "n", Type: script.ParamTypeInt}, "42", int64(42)},
		{"int from int", script.Param{Name: "n", Type: script.ParamTypeInt}, 42, int64(42)},
		{"float from int", script.Param{Name: "f", Type: script.ParamTypeFloat}, 3, float64(3)},
		{"float from string", script.Param{Name: "f", Type: script.ParamTypeFloat}, "1.5", 1.5},
		{"bool from string", script.Param{Name: "b", Type: script.ParamTypeBool}, "true", true},
		{"date normalized", script.Param{Name: "d", Type: script.ParamTypeDate, Required: true}, "2026-08-13", "2026-08-13"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bound, err := script.BindParams([]script.Param{tc.param}, map[string]any{tc.param.Name: tc.in})
			require.NoError(t, err)
			assert.Equal(t, tc.want, bound[tc.param.Name])
		})
	}

	bad := []struct {
		name  string
		param script.Param
		in    any
	}{
		{"int from fraction", script.Param{Name: "n", Type: script.ParamTypeInt}, 1.5},
		{"int from junk", script.Param{Name: "n", Type: script.ParamTypeInt}, "x"},
		{"int from bool", script.Param{Name: "n", Type: script.ParamTypeInt}, true},
		{"float from junk", script.Param{Name: "f", Type: script.ParamTypeFloat}, "x"},
		{"float from bool", script.Param{Name: "f", Type: script.ParamTypeFloat}, true},
		{"bool from junk", script.Param{Name: "b", Type: script.ParamTypeBool}, "maybe"},
		{"bool from int", script.Param{Name: "b", Type: script.ParamTypeBool}, 1},
		{"string from int", script.Param{Name: "s", Type: script.ParamTypeString}, 1},
		{"enum from int", script.Param{Name: "e", Type: script.ParamTypeEnum, Values: []string{"a"}}, 1},
		{"date from int", script.Param{Name: "d", Type: script.ParamTypeDate, Required: true}, 20260813},
		{"string from bool", script.Param{Name: "s", Type: script.ParamTypeString}, true},
		// BindParams does not re-run ValidateParams, so a contract that was
		// never validated still has to fail closed rather than pass an unchecked
		// value through to the interpreter.
		{"unknown declared type", script.Param{Name: "x", Type: "timestamp"}, "2026-08-13"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := script.BindParams([]script.Param{tc.param}, map[string]any{tc.param.Name: tc.in})
			assert.Error(t, err)
		})
	}
}

func TestParamsEqual(t *testing.T) {
	a := []script.Param{{Name: "day", Type: script.ParamTypeDate, Default: "2026-01-01"}}
	b := []script.Param{{Name: "day", Type: script.ParamTypeDate, Default: "2026-01-01"}}
	assert.True(t, script.ParamsEqual(a, b))

	b[0].Default = "2026-01-02"
	assert.False(t, script.ParamsEqual(a, b), "a changed default is a changed contract")

	assert.False(t, script.ParamsEqual(a, nil))
	assert.True(t, script.ParamsEqual(nil, []script.Param{}))

	// Values participate in equality even though a slice makes Param
	// incomparable, which is the whole reason this function exists.
	c := []script.Param{{Name: "g", Type: script.ParamTypeEnum, Values: []string{"a"}}}
	d := []script.Param{{Name: "g", Type: script.ParamTypeEnum, Values: []string{"a", "b"}}}
	assert.False(t, script.ParamsEqual(c, d))
}
