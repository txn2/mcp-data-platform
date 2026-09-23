package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selectOrderCaller answers every call with the result trino_query gives
// `SELECT 'x' AS zeta, 1 AS alpha, true AS mid`: columns in SELECT order, rows
// as JSON objects, whose keys carry no order.
type selectOrderCaller struct{}

func (selectOrderCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{
		"columns": []any{
			map[string]any{"name": "zeta", "type": "varchar"},
			map[string]any{"name": "alpha", "type": "integer"},
			map[string]any{"name": "mid", "type": "boolean"},
		},
		"rows":      []any{map[string]any{"alpha": float64(1), "mid": true, "zeta": "x"}},
		"row_count": float64(1),
	}, nil
}

// TestQueryRowsKeepSelectOrder is #1852: a row reaches the script with its
// keys in the order the SELECT named them. An export's columns are read from
// that order (starlarkconv.ColumnOrder), so rows exported straight from the
// query keep the query's columns where it put them.
func TestQueryRowsKeepSelectOrder(t *testing.T) {
	result, err := execute(t, `
res = platform.query(sql="SELECT 'x' AS zeta, 1 AS alpha, true AS mid")
print(list(res["rows"][0].keys()))
`, selectOrderCaller{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "[\"zeta\", \"alpha\", \"mid\"]\n", result.Log)
}

// TestCallQueryRowsKeepSelectOrder holds platform.call to the same order: it
// returns the query tool's own result, so a script reaching trino_query that
// way must not see a different row than platform.query gives it.
func TestCallQueryRowsKeepSelectOrder(t *testing.T) {
	result, err := execute(t, `
res = platform.call("trino_query", {"sql": "SELECT 'x' AS zeta, 1 AS alpha, true AS mid"})
print(list(res["rows"][0].keys()))
`, selectOrderCaller{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "[\"zeta\", \"alpha\", \"mid\"]\n", result.Log)
}
