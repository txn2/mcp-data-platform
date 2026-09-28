package toolanswer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

type row struct {
	Name string `json:"name"`
}

type body struct {
	ID     string            `json:"id"`
	Count  int               `json:"count"`
	Ratio  float64           `json:"ratio"`
	Rows   []row             `json:"rows"`
	Extra  map[string]any    `json:"extra,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Maybe  *row              `json:"maybe,omitempty"`
	Any    any               `json:"any,omitempty"`
}

func set() *Set {
	return New(
		toolkit.ContractFor[body]("write", "action", "make", ""),
		toolkit.ContractFor[row]("plain", ""),
		toolkit.AnswerContract{Tool: "no schema"},
	)
}

func TestCheckHoldsAnAnswerToItsContract(t *testing.T) {
	good := map[string]any{"id": "a", "count": 2.0, "ratio": 2.0, "rows": []any{map[string]any{"name": "x"}}}
	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		answer  map[string]any
		checked bool
		want    string
	}{
		{name: "complete", tool: "write", args: map[string]any{"action": "make"}, answer: good, checked: true},
		{name: "absent arg matches the empty value", tool: "write", args: map[string]any{}, answer: good, checked: true},
		{name: "other action is not covered", tool: "write", args: map[string]any{"action": "drop"}, answer: map[string]any{}},
		{name: "unknown tool", tool: "nothing", answer: map[string]any{}},
		{name: "a contract with no schema is dropped", tool: "no schema", answer: map[string]any{}},
		{
			name: "missing fields", tool: "write", args: map[string]any{"action": "make"}, checked: true,
			answer: map[string]any{"id": "a"},
			want:   `the answer declared for write (action=make) lacks "count", "ratio", "rows"; the tool never answers that way`,
		},
		{
			name: "every call when no argument selects", tool: "plain", checked: true,
			answer: map[string]any{}, want: `the answer declared for plain lacks "name"`,
		},
		{
			name: "wrong types", tool: "write", checked: true,
			answer: map[string]any{"id": true, "count": 1.5, "ratio": 1.0, "rows": "x"},
			want:   `has "count" as number, where the tool answers integer, has "id" as boolean, where the tool answers string, has "rows" as string, where the tool answers null or array`,
		},
		{
			name: "nested object is closed and holds its required fields", tool: "write", checked: true,
			answer: map[string]any{"id": "a", "count": 1.0, "ratio": 1.5, "rows": []any{map[string]any{"nme": "x"}}},
			want:   `lacks "rows[0].name", has "rows[0].nme", which the tool never returns`,
		},
		{
			name: "top level admits the keys the platform adds", tool: "write", checked: true,
			answer: map[string]any{
				"id": "a", "count": 1.0, "ratio": 1.0, "rows": nil, "call_reference": "mcp:call:1",
				"extra": map[string]any{"any": []any{1.0}}, "labels": map[string]any{"a": "b"}, "maybe": nil, "any": 3.0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checked, err := set().Check(tt.tool, tt.args, tt.answer)
			assert.Equal(t, tt.checked, checked)
			if tt.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestANilSetChecksNothing(t *testing.T) {
	var s *Set
	checked, err := s.Check("write", nil, nil)
	assert.False(t, checked)
	assert.NoError(t, err)
}

type contractKit struct{ registry.Toolkit }

func (contractKit) AnswerContracts() []toolkit.AnswerContract {
	return []toolkit.AnswerContract{toolkit.ContractFor[row]("plain", "")}
}

type lister []registry.Toolkit

func (l lister) All() []registry.Toolkit { return l }

func TestLiveReadsTheRegistryAtTheCheck(t *testing.T) {
	checked, err := Live{}.Check("plain", nil, map[string]any{})
	assert.False(t, checked)
	assert.NoError(t, err)

	live := Live{Kits: lister{contractKit{}, nil}}
	checked, err = live.Check("plain", nil, map[string]any{})
	assert.True(t, checked)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `lacks "name"`)

	extra := Live{Extra: []toolkit.AnswerContract{toolkit.ContractFor[row]("notify", "action", "send")}}
	checked, err = extra.Check("notify", map[string]any{"action": "send"}, map[string]any{"name": "x"})
	assert.True(t, checked, "a tool outside every toolkit is checked through Extra")
	assert.NoError(t, err)
}

func TestContractForPanicsOnATypeWithNoSchema(t *testing.T) {
	assert.Panics(t, func() { toolkit.ContractFor[chan int]("t", "") })
}
