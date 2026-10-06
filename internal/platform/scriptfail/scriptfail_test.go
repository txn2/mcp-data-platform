package scriptfail

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

var (
	externalConnection = map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "EXTERNAL: The connection attempt failed.",
		"trino":   map[string]any{"error_type": "EXTERNAL", "error_name": "JDBC_ERROR", "sql_state": "08001"},
	}
	statementTimeout = map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "context deadline exceeded", "transport": map[string]any{"kind": "timeout"},
	}
	tableNotFound = map[string]any{
		"code": "trino_query_failed", "category": "client_input", "retryable": false,
		"message": "USER_ERROR: Table 't' does not exist",
		"trino":   map[string]any{"error_type": "USER_ERROR", "error_name": "TABLE_NOT_FOUND"},
	}
)

func TestClass(t *testing.T) {
	cases := []struct {
		env  map[string]any
		want string
	}{
		{externalConnection, "Trino failed for a reason outside the script (EXTERNAL / JDBC_ERROR, SQLSTATE 08001); expected to pass on its next run."},
		{statementTimeout, "Trino could not be reached (timeout); expected to pass on its next run."},
		{
			map[string]any{"code": "trino_query_failed", "retryable": true, "transport": map[string]any{"kind": "http_status", "http_status": float64(504)}},
			"Trino could not be reached (http_status, HTTP 504); expected to pass on its next run.",
		},
		{tableNotFound, "Trino refused the statement (USER_ERROR / TABLE_NOT_FOUND); the next run fails the same way until the script or the data it reads changes."},
		{
			map[string]any{"code": "trino_query_failed", "category": "internal", "retryable": false, "trino": map[string]any{"error_type": "INTERNAL_ERROR", "error_name": "GENERIC_INTERNAL_ERROR"}},
			"Trino failed for a reason it does not report as temporary (INTERNAL_ERROR / GENERIC_INTERNAL_ERROR); the next run is not expected to pass on its own.",
		},
		{
			map[string]any{"code": "other", "category": "upstream_unavailable", "retryable": true},
			"The tool failed for a reason outside the script (upstream_unavailable); expected to pass on its next run.",
		},
		{map[string]any{"code": "other", "retryable": false}, "The tool failed for a reason it does not report as temporary (unclassified); the next run is not expected to pass on its own."},
		{
			map[string]any{"code": "trino_query_failed", "retryable": false, "trino": map[string]any{"sql_state": "23505"}},
			"Trino failed for a reason it does not report as temporary (SQLSTATE 23505); the next run is not expected to pass on its own.",
		},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, Class(tc.env))
	}
}

func TestClassify(t *testing.T) {
	plain := errors.New("bare")
	assert.Equal(t, plain, Classify("t", plain), "a failure with no envelope is unchanged")
	assert.NoError(t, Classify("t", nil))
	unclassified := scriptsession.NewRefusal("x", map[string]any{"code": "unauthorized"})
	assert.Equal(t, error(unclassified), Classify("t", unclassified), "an envelope with no retryable is not a classification")
	already := scriptguard.NewUpstreamError("t", scriptsession.NewRefusal("x", externalConnection))
	assert.Equal(t, error(already), Classify("t", already), "a failure already attributed keeps its attribution")

	temporary := Classify("trino_query", scriptsession.NewRefusal("Query failed", externalConnection))
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(temporary))
	assert.Equal(t, "Query failed\n"+Class(externalConnection), temporary.Error())
	statement := Classify("trino_query", scriptsession.NewRefusal("Query failed", tableNotFound))
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(statement))
	var refusal *scriptsession.RefusalError
	assert.True(t, errors.As(statement, &refusal), "the refusal is still reachable under the class")
}

func TestRetries(t *testing.T) {
	assert.True(t, Retries(scriptsession.NewRefusal("x", externalConnection)), "Trino reported it temporary")
	assert.False(t, Retries(scriptsession.NewRefusal("x", statementTimeout)), "the client already retried the connection")
	assert.False(t, Retries(scriptsession.NewRefusal("x", tableNotFound)))
	assert.False(t, Retries(nil))
}

func TestOnError(t *testing.T) {
	for value, want := range map[string]bool{"": false, "raise": false, "return": true} {
		got, err := OnError("platform.query", value)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := OnError("platform.query", "ignore")
	assert.EqualError(t, err, `in platform.query: on_error is "raise" or "return", not "ignore"`)
}

func TestEnvelope(t *testing.T) {
	_, ok := Envelope(errors.New("calling: connection closed"))
	assert.False(t, ok, "a failure that is not a tool's answer is not handed back")

	env, ok := Envelope(scriptsession.NewRefusal("Query failed: x", tableNotFound))
	require.True(t, ok)
	assert.Equal(t, false, env["retryable"])
	assert.Equal(t, "trino_query_failed", env["code"])

	env, ok = Envelope(scriptsession.NewRefusal("the gateway's upstream timed out", map[string]any{"code": "upstream_unavailable"}))
	require.True(t, ok)
	assert.Equal(t, "the gateway's upstream timed out", env["message"], "the text stands in for an absent message")
	assert.Equal(t, false, env["retryable"])

	unanswered := scriptguard.NewUpstreamError("api_invoke_endpoint",
		scriptsession.NewRefusal("timed out", map[string]any{"code": "upstream_unavailable"}))
	env, ok = Envelope(unanswered)
	require.True(t, ok)
	assert.Equal(t, true, env["retryable"], "a call whose upstream did not answer is retryable, as its run would be")
}
