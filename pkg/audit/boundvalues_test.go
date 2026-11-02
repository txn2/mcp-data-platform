package audit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeToolParameters_KeepsTheStatementAndTheParameterTypes(t *testing.T) {
	in := map[string]any{
		"connection": "orders",
		"sql":        "INSERT INTO people VALUES ($1, $2, $3, $4, $5, $6)",
		"params":     []any{"ada@example.com", 1.5, json.Number("2"), true, nil, map[string]any{"k": "v"}},
		"password":   "pw",
	}
	got := SanitizeToolParameters("postgres_execute", in)
	assert.Equal(t, map[string]any{
		"$1": "string", "$2": "number", "$3": "number", "$4": "boolean", "$5": "null", "$6": "object",
	}, got["params"])
	assert.Equal(t, in["sql"], got["sql"])
	assert.Equal(t, redactedSentinel, got["password"])
	assert.Equal(t, []any{"ada@example.com", 1.5, json.Number("2"), true, nil, map[string]any{"k": "v"}}, in["params"],
		"the caller's arguments are not changed")
}

func TestShapeBoundValues_StatementsAndNamedParams(t *testing.T) {
	got := ShapeBoundValues("postgres_execute", map[string]any{
		"statements": []any{
			map[string]any{"sql": "INSERT INTO t VALUES (@a)", "params": map[string]any{"a": []any{1.0}}},
			map[string]any{"sql": "DELETE FROM t"},
			"not a statement",
		},
	})
	assert.Equal(t, []any{
		map[string]any{"sql": "INSERT INTO t VALUES (@a)", "params": map[string]any{"a": "array"}},
		map[string]any{"sql": "DELETE FROM t"},
		"string",
	}, got["statements"])

	other := map[string]any{"params": []any{"kept"}}
	assert.Equal(t, other, ShapeBoundValues("run_script", other), "another tool's params are its own")
	assert.Nil(t, ShapeBoundValues("postgres_query", nil))
	assert.Equal(t, map[string]any{"params": nil}, ShapeBoundValues("postgres_query", map[string]any{"params": nil}))
	assert.Equal(t, "value", jsonTypeName(struct{}{}))
	assert.Equal(t, "string", ShapeBoundValues("postgres_query", map[string]any{"params": "x"})["params"])
}
