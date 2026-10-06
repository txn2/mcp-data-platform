package scriptrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// Envelopes as mcp-trino v1.7.0 classifies the failures #2032 was filed on.
// retry_after_seconds is set on the temporary ones only so a test's waits are
// milliseconds; mcp-trino names none and the host waits 1s, 2s, 4s.
var (
	externalConnection = map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "EXTERNAL: The connection attempt failed.", "retry_after_seconds": 0.001,
		"trino": map[string]any{"error_type": "EXTERNAL", "error_name": "JDBC_ERROR", "error_code": float64(65536), "sql_state": "08001"},
	}
	statementTimeout = map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "context deadline exceeded", "retry_after_seconds": 0.001,
		"transport": map[string]any{"kind": "timeout", "detail": "context deadline exceeded"},
	}
	tableNotFound = map[string]any{
		"code": "trino_query_failed", "category": "client_input", "retryable": false,
		"message": "USER_ERROR: line 40:10: Table 'scratch.uploads.t' does not exist",
		"trino":   map[string]any{"error_type": "USER_ERROR", "error_name": "TABLE_NOT_FOUND"},
	}
	uniqueViolation = map[string]any{
		"code": "trino_query_failed", "category": "client_input", "retryable": false,
		"message": "EXTERNAL: duplicate key value violates unique constraint",
		"trino":   map[string]any{"error_type": "EXTERNAL", "error_name": "JDBC_ERROR", "sql_state": "23505"},
	}
	insufficientResources = map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "INSUFFICIENT_RESOURCES: Query exceeded per-node memory limit", "retry_after_seconds": 0.001,
		"trino": map[string]any{"error_type": "INSUFFICIENT_RESOURCES", "error_name": "EXCEEDED_LOCAL_MEMORY_LIMIT"},
	}
)

// classifiedCaller fails the attempts listed in fail (1-based, across every
// call) with the envelope given, as the session caller hands a classified
// tool failure over, and answers every other attempt with one row.
type classifiedCaller struct {
	fail     map[int]map[string]any
	attempts int
	tools    []string
}

func (c *classifiedCaller) CallTool(_ context.Context, tool string, _ map[string]any) (map[string]any, error) {
	c.attempts++
	c.tools = append(c.tools, tool)
	if env, ok := c.fail[c.attempts]; ok {
		msg, _ := env["message"].(string)
		return nil, scriptsession.NewRefusal("Query failed: "+msg, env)
	}
	return map[string]any{"rows": []any{map[string]any{"n": float64(1)}}, "columns": []any{map[string]any{"name": "n", "type": "integer"}}}, nil
}

func every(env map[string]any) map[int]map[string]any {
	return map[int]map[string]any{1: env, 2: env, 3: env, 4: env, 5: env}
}

const executeOnce = `platform.execute("MERGE INTO warehouse.public.t USING s ON true WHEN MATCHED THEN DELETE", connection = "warehouse")` + "\n"

// A write that fails for a temporary reason is never issued again by the
// host, and ends the run as the upstream's, retryable, saying what failed.
func TestRun_ATemporaryWriteFailureIsTheUpstreamsAndNotRetried(t *testing.T) {
	for name, env := range map[string]map[string]any{
		"EXTERNAL / JDBC_ERROR, SQLSTATE 08001":   externalConnection,
		"deadline exceeded polling the statement": statementTimeout,
		"INSUFFICIENT_RESOURCES":                  insufficientResources,
	} {
		t.Run(name, func(t *testing.T) {
			caller := &classifiedCaller{fail: every(env)}
			_, err := execute(t, executeOnce, caller, nil)
			require.Error(t, err)
			assert.Equal(t, 1, caller.attempts, "a write is never re-issued by the host")
			assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
			assert.True(t, runstate.CauseRetryable(scriptguard.Cause(err)))
			assert.Contains(t, err.Error(), "expected to pass on its next run")
			msg, _ := env["message"].(string)
			assert.Contains(t, err.Error(), "Query failed: "+msg, "the tool's own text is kept")
		})
	}
}

func TestRun_AStatementFailureStaysTheScripts(t *testing.T) {
	for name, env := range map[string]map[string]any{"USER_ERROR / TABLE_NOT_FOUND": tableNotFound, "EXTERNAL / JDBC_ERROR, SQLSTATE 23505": uniqueViolation} {
		t.Run(name, func(t *testing.T) {
			caller := &classifiedCaller{fail: every(env)}
			_, err := execute(t, executeOnce, caller, nil)
			require.Error(t, err)
			assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
			assert.Contains(t, err.Error(), "Trino refused the statement ("+name+")")
			assert.Equal(t, 1, caller.attempts)
		})
	}
}

// A read that fails for a temporary reason is waited on and issued again, at
// most three times, and each wait is in the run's log. The envelopes name a
// 1ms interval, which is waited as named.
func TestRun_ATemporaryReadFailureIsRetried(t *testing.T) {
	caller := &classifiedCaller{fail: map[int]map[string]any{1: externalConnection, 2: insufficientResources}}
	result, err := execute(t, `print(platform.query("SELECT 1 AS n", connection = "warehouse")["row_count"])`+"\n", caller, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, caller.attempts)
	assert.Equal(t, "trino_query failed for a temporary reason (EXTERNAL / JDBC_ERROR, SQLSTATE 08001); waited 1ms and retried (1 of 3)\n"+
		"trino_query failed for a temporary reason (INSUFFICIENT_RESOURCES / EXCEEDED_LOCAL_MEMORY_LIMIT); waited 1ms and retried (2 of 3)\n1\n", result.Log)
}

// A read Trino never answered is not issued again: the Trino client has
// already retried the connection for up to its own limit (txn2/mcp-trino#106).
func TestRun_AReadTrinoNeverAnsweredIsNotRetried(t *testing.T) {
	caller := &classifiedCaller{fail: every(statementTimeout)}
	result, err := execute(t, `platform.query("SELECT 1 AS n", connection = "warehouse")`+"\n", caller, nil)
	require.Error(t, err)
	assert.Equal(t, 1, caller.attempts)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err), "it is still the upstream's")
	assert.NotContains(t, result.Log, "retried")
}

func TestRun_ARetriedReadThatKeepsFailingIsTheUpstreams(t *testing.T) {
	caller := &classifiedCaller{fail: every(externalConnection)}
	result, err := execute(t, `platform.query("SELECT 1 AS n", connection = "warehouse")`+"\n", caller, nil)
	require.Error(t, err)
	assert.Equal(t, 4, caller.attempts, "one call and three retries")
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
	assert.Contains(t, result.Log, "still failed for a temporary reason (EXTERNAL / JDBC_ERROR, SQLSTATE 08001) after 3 retries")
}

func TestRun_RetryFalseIssuesAReadOnce(t *testing.T) {
	caller := &classifiedCaller{fail: every(externalConnection)}
	_, err := execute(t, `platform.query("SELECT 1 AS n", connection = "warehouse", retry = False)`+"\n", caller, nil)
	require.Error(t, err)
	assert.Equal(t, 1, caller.attempts)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))

	caller = &classifiedCaller{fail: every(externalConnection)}
	_, err = execute(t, `platform.call("trino_query", {"sql": "SELECT 1"}, retry = False)`+"\n", caller, nil)
	require.Error(t, err)
	assert.Equal(t, 1, caller.attempts)
}

// A statement failure is not retried, read or not.
func TestRun_AStatementFailureOfAReadIsNotRetried(t *testing.T) {
	caller := &classifiedCaller{fail: every(tableNotFound)}
	_, err := execute(t, `platform.query("SELECT * FROM t", connection = "warehouse")`+"\n", caller, nil)
	require.Error(t, err)
	assert.Equal(t, 1, caller.attempts)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
}

// on_error = "return" hands the failure to the script as {"error": envelope},
// which the script branches on; fail(..., retryable = True) then records it as
// temporary.
func TestRun_OnErrorReturnHandsTheEnvelopeToTheScript(t *testing.T) {
	src := `
res = platform.execute("MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE", connection = "warehouse", on_error = "return")
e = res["error"]
print(e["code"], e["category"], e["retryable"], e["trino"]["sql_state"], sorted(res.keys()))
if e["retryable"]:
    fail("warehouse unreachable (%s); the next run retries" % e["trino"]["error_name"], retryable = True)
`
	caller := &classifiedCaller{fail: every(externalConnection)}
	result, err := execute(t, src, caller, nil)
	require.Error(t, err)
	assert.Equal(t, 1, caller.attempts)
	assert.Equal(t, runstate.CauseTransient, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "warehouse unreachable (JDBC_ERROR)")
	assert.Contains(t, result.Log, `trino_query_failed upstream_unavailable True 08001 ["error"]`)
}

func TestRun_OnErrorReturnOnEachBinding(t *testing.T) {
	for name, src := range map[string]string{
		"query":   `res = platform.query("SELECT 1", connection = "w", on_error = "return", retry = False)`,
		"execute": `res = platform.execute("DELETE FROM t", connection = "w", on_error = "return")`,
		"call":    `res = platform.call("trino_execute", {"sql": "DELETE FROM t"}, on_error = "return")`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := execute(t, src+"\nprint(res[\"error\"][\"message\"])\n", &classifiedCaller{fail: every(tableNotFound)}, nil)
			require.NoError(t, err)
			assert.Contains(t, result.Log, "returned the failure to the script")
			assert.Contains(t, result.Log, "USER_ERROR: line 40:10")
		})
	}
}

// A script that asked for the failure and reads a result field without
// checking error fails at that read, rather than proceeding.
func TestRun_AReturnedFailureIsNotAResult(t *testing.T) {
	_, err := execute(t, `rows = platform.query("SELECT 1", connection = "w", on_error = "return")["rows"]`+"\n",
		&classifiedCaller{fail: every(tableNotFound)}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `key "rows" not in dict`)
}

// The default stays raise, and a call that succeeds is unchanged by on_error.
func TestRun_OnErrorDefaultsAndValidation(t *testing.T) {
	_, err := execute(t, `platform.execute("DELETE FROM t", connection = "w")`+"\n", &classifiedCaller{fail: every(tableNotFound)}, nil)
	require.Error(t, err)

	result, err := execute(t, `print(platform.query("SELECT 1", connection = "w", on_error = "return")["row_count"])`+"\n", &classifiedCaller{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "1\n", result.Log)

	_, err = execute(t, `platform.query("SELECT 1", on_error = "ignore")`+"\n", &classifiedCaller{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `on_error is "raise" or "return", not "ignore"`)
}

// A failure that is not the call's -- here the caller's plain transport error
// -- ends the run even when the script asked for failures back.
func TestRun_OnErrorReturnKeepsANonToolFailure(t *testing.T) {
	caller := &refusingCaller{refuse: map[int]error{1: errors.New("calling trino_query: connection closed")}}
	_, err := execute(t, `platform.query("SELECT 1", on_error = "return")`+"\n", caller, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection closed")
}
