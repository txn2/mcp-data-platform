package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestList_BindsEveryElement is #1844's first criterion at the contract: a
// list<string> takes an array, refuses a scalar, and names the element a
// wrong value is at.
func TestList_BindsEveryElement(t *testing.T) {
	ids := []Param{{Name: "ids", Type: ParamTypeList, Items: ParamTypeString, Required: true}}
	require.NoError(t, ValidateParams(ids))

	got, err := BindParams(ids, map[string]any{"ids": []any{"a", "b"}})
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "b"}, got["ids"])

	_, err = BindParams(ids, map[string]any{"ids": "a,b"})
	require.ErrorContains(t, err, "expected a list of values")

	_, err = BindParams(ids, map[string]any{"ids": []any{"a", 7.0}})
	require.ErrorContains(t, err, "element 1")

	counts := []Param{{Name: "n", Type: ParamTypeList, Items: ParamTypeInt, MinItems: new(1), MaxItems: new(2), Min: 1.0, Max: 10.0}}
	require.NoError(t, ValidateParams(counts))
	got, err = BindParams(counts, map[string]any{"n": []any{1.0, 10.0}})
	require.NoError(t, err)
	assert.Equal(t, []any{int64(1), int64(10)}, got["n"])
	_, err = BindParams(counts, map[string]any{"n": []any{}})
	require.ErrorContains(t, err, "between 1 and 2")
	_, err = BindParams(counts, map[string]any{"n": []any{11.0}})
	require.ErrorContains(t, err, "above the maximum")

	regions := []Param{{Name: "r", Type: ParamTypeList, Items: ParamTypeEnum, Values: []string{"west", "east"}}}
	require.NoError(t, ValidateParams(regions))
	got, err = BindParams(regions, map[string]any{"r": []string{"west"}})
	require.NoError(t, err)
	assert.Equal(t, []any{"west"}, got["r"])
	got, err = BindParams(regions, nil)
	require.NoError(t, err)
	assert.Equal(t, []any{}, got["r"], "an omitted optional list is an empty list, never null")
}

func TestDateRange_BindsFromNotAfterTo(t *testing.T) {
	period := []Param{{Name: "period", Type: ParamTypeDateRange, Required: true, Min: "2026-01-01"}}
	require.NoError(t, ValidateParams(period))

	got, err := BindParams(period, map[string]any{"period": map[string]any{"from": "2026-09-01", "to": "2026-09-30"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"from": "2026-09-01", "to": "2026-09-30"}, got["period"])

	for name, value := range map[string]any{
		"reversed":      map[string]any{"from": "2026-09-30", "to": "2026-09-01"},
		"not a date":    map[string]any{"from": "yesterday", "to": "2026-09-01"},
		"missing end":   map[string]any{"from": "2026-09-01"},
		"extra key":     map[string]any{"from": "2026-09-01", "to": "2026-09-02", "at": "x"},
		"not a range":   "2026-09-01",
		"below the min": map[string]any{"from": "2025-12-31", "to": "2026-01-02"},
	} {
		_, err := BindParams(period, map[string]any{"period": value})
		assert.Error(t, err, name)
	}
	assert.ErrorContains(t, ValidateParams([]Param{{Name: "p", Type: ParamTypeDateRange}}), "must declare a default")
}

func TestScalarConstraints(t *testing.T) {
	params := []Param{
		{Name: "code", Type: ParamTypeString, Pattern: "[A-Z]{3}", Label: "Code", Group: "Filters", Order: 2, UI: map[string]any{"widget": "code-picker"}},
		{Name: "limit", Type: ParamTypeInt, Min: 1.0, Max: 100.0},
		{Name: "rate", Type: ParamTypeFloat, Min: 0.5},
		{Name: "day", Type: ParamTypeDate, Max: "2026-12-31", Default: "2026-01-01"},
	}
	require.NoError(t, ValidateParams(params))
	_, err := BindParams(params, map[string]any{"code": "ABC", "limit": 5.0, "rate": 0.5, "day": "2026-06-01"})
	require.NoError(t, err)

	for name, values := range map[string]map[string]any{
		"pattern is anchored": {"code": "ABCD"},
		"below min":           {"limit": 0.0},
		"above max":           {"limit": 101.0},
		"float below min":     {"rate": 0.1},
		"date above max":      {"day": "2027-01-01"},
	} {
		_, err := BindParams(params, values)
		assert.Error(t, err, name)
	}
}

func TestValidateParams_FormShape(t *testing.T) {
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'x'
	}
	for name, p := range map[string]Param{
		"list without items":          {Name: "a", Type: ParamTypeList},
		"list of bool":                {Name: "a", Type: ParamTypeList, Items: ParamTypeBool},
		"items on a string":           {Name: "a", Type: ParamTypeString, Items: ParamTypeString},
		"min_items on a string":       {Name: "a", Type: ParamTypeString, MinItems: new(1)},
		"min_items above max_items":   {Name: "a", Type: ParamTypeList, Items: ParamTypeInt, MinItems: new(3), MaxItems: new(2)},
		"negative min_items":          {Name: "a", Type: ParamTypeList, Items: ParamTypeInt, MinItems: new(-1)},
		"max_items past the cap":      {Name: "a", Type: ParamTypeList, Items: ParamTypeInt, MaxItems: new(1001)},
		"list of enum without values": {Name: "a", Type: ParamTypeList, Items: ParamTypeEnum},
		"pattern on an int":           {Name: "a", Type: ParamTypeInt, Pattern: "1"},
		"pattern that fails":          {Name: "a", Type: ParamTypeString, Pattern: "("},
		"pattern too long":            {Name: "a", Type: ParamTypeString, Pattern: string(long) + string(long) + string(long)},
		"min on a string":             {Name: "a", Type: ParamTypeString, Min: 1.0},
		"min not an int":              {Name: "a", Type: ParamTypeInt, Min: "x"},
		"min above max":               {Name: "a", Type: ParamTypeInt, Min: 5.0, Max: 1.0},
		"label too long":              {Name: "a", Type: ParamTypeString, Label: string(long)},
	} {
		assert.Error(t, ValidateParams([]Param{p}), name)
	}
}

// TestParamsEqual_CoversTheFormFields keeps an edit that only changes a
// label or a constraint from reading as no change.
func TestParamsEqual_CoversTheFormFields(t *testing.T) {
	a := []Param{{Name: "a", Type: ParamTypeString, Label: "A"}}
	b := []Param{{Name: "a", Type: ParamTypeString, Label: "B"}}
	assert.False(t, ParamsEqual(a, b))
	assert.True(t, ParamsEqual(a, []Param{{Name: "a", Type: ParamTypeString, Label: "A"}}))
}

func TestCompare(t *testing.T) {
	assert.Equal(t, 0, compare(true, false), "an unordered type compares equal")
	assert.Equal(t, -1, compare("2026-01-01", "2026-01-02"))
	assert.Equal(t, 1, compare(2.0, 1.0))
}

// A caller-bound parameter names a claim, is a scalar, and has no default
// (#1846); a schedule, which has no caller, refuses a script that declares one.
func TestBind_ContractAndSchedule(t *testing.T) {
	tenant := []Param{{Name: "tenant", Type: ParamTypeString, Bind: "caller.tenant"}}
	require.NoError(t, ValidateParams(tenant))
	require.NoError(t, ValidateParams([]Param{{Name: "org", Type: ParamTypeString, Bind: "caller.org.id"}}))

	for name, p := range map[string]Param{
		"no claim":   {Name: "t", Type: ParamTypeString, Bind: "caller."},
		"not caller": {Name: "t", Type: ParamTypeString, Bind: "request.tenant"},
		"a list":     {Name: "t", Type: ParamTypeList, Items: ParamTypeString, Bind: "caller.tenant"},
		"a default":  {Name: "t", Type: ParamTypeString, Bind: "caller.tenant", Default: "acme"},
	} {
		assert.Error(t, ValidateParams([]Param{p}), name)
	}

	sched := Schedule{ScriptID: "s", CronSpec: "0 6 * * *", Timezone: "UTC"}
	err := sched.Validate(tenant)
	require.ErrorContains(t, err, "a schedule has no caller")
}
