package scripttx_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

// platform.maintain sends one appdb.maintain call and answers what ran; a
// draft refuses it before it is sent, and a refusal is the run's failure or,
// with on_error = "return", the script's to read.
func TestMaintain(t *testing.T) {
	caller := &txCaller{}
	res, err := run(t, `
done = platform.maintain("shop", "analyze", table = "orders")
whole = platform.maintain(connection = "shop", command = "vacuum")
print("%s %d" % (done.statement, done.duration_ms))
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "ANALYZE orders 3\n", res.Log)
	require.Len(t, caller.calls, 2)
	assert.Equal(t, map[string]any{"connection": "shop", "command": "analyze", "table": "orders"}, caller.calls[0].args)
	assert.Equal(t, map[string]any{"connection": "shop", "command": "vacuum"}, caller.calls[1].args, "no table is the whole database")

	replayed := &txCaller{replayed: true}
	res, err = run(t, `print(platform.maintain("shop", "analyze", table = "t").duration_ms)`, replayed, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "3\n", res.Log)

	draft := &txCaller{}
	_, err = run(t, `platform.maintain("shop", "analyze")`, draft, scriptrun.WritesRefused)
	require.Error(t, err)
	assert.Empty(t, draft.calls, "a draft never maintains")

	refused := &txCaller{refuse: scriptsession.AppDBMaintain}
	_, err = run(t, `platform.maintain("shop", "analyze")`, refused, scriptrun.WritesMade)
	require.ErrorContains(t, err, "42501")
	res, err = run(t, `
out = platform.maintain("shop", "analyze", on_error = "return")
print(out["error"]["code"])
`, refused, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Contains(t, res.Log, "statement_failed")

	for _, src := range []string{
		`platform.maintain("shop", "analyze", table = 1)`,
		`platform.maintain("shop")`,
		`platform.maintain("shop", "analyze", on_error = "ignore")`,
	} {
		_, err = run(t, src, &txCaller{}, scriptrun.WritesMade)
		require.Error(t, err, src)
	}
}
