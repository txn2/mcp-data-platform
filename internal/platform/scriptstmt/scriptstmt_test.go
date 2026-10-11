package scriptstmt

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

func TestConfig_ValidateAndMaxTimeout(t *testing.T) {
	assert.NoError(t, Config{}.Validate())
	assert.NoError(t, Config{MaxTimeoutSeconds: 300}.Validate())
	assert.NoError(t, Config{MaxTimeoutSeconds: 1}.Validate())
	for _, v := range []int{301, -1} {
		err := Config{MaxTimeoutSeconds: v}.Validate()
		require.Error(t, err, v)
		assert.Contains(t, err.Error(), "scripts.sql.max_timeout_seconds")
		assert.Contains(t, err.Error(), "the most trino_query and trino_execute accept")
	}
	assert.Equal(t, ToolMaxTimeout, Config{}.MaxTimeout())
	assert.Equal(t, 90*time.Second, Config{MaxTimeoutSeconds: 90}.MaxTimeout())
}

func TestStatementLimit(t *testing.T) {
	hour := time.Hour
	cases := []struct {
		name      string
		requested int
		ceiling   time.Duration
		remaining time.Duration
		want      Limit
	}{
		{"none named takes the tools' default", 0, ToolMaxTimeout, hour, Limit{ToolDefaultTimeout, LimitDefault}},
		{"a ceiling under the default lowers it", 0, time.Minute, hour, Limit{time.Minute, LimitDefault}},
		{"a request within the ceiling is honored", 300, ToolMaxTimeout, hour, Limit{300 * time.Second, LimitRequested}},
		{"an unset ceiling is the tools' cap", 300, 0, hour, Limit{300 * time.Second, LimitRequested}},
		{"the run's remaining time cuts it, less the margin", 300, ToolMaxTimeout, 65 * time.Second, Limit{time.Minute, LimitRunRemaining}},
		{"never under a second", 0, ToolMaxTimeout, time.Second, Limit{time.Second, LimitRunRemaining}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := StatementLimit(tc.requested, tc.ceiling, tc.remaining)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := StatementLimit(301, ToolMaxTimeout, hour)
	require.Error(t, err)
	assert.Equal(t, "timeout_seconds is 301; the most a statement may run on this deployment is 300 (scripts.sql.max_timeout_seconds)", err.Error())
	_, err = StatementLimit(-5, ToolMaxTimeout, hour)
	assert.ErrorContains(t, err, "above 0")
}

func TestLimit_SecondsRoundsUp(t *testing.T) {
	assert.Equal(t, 55, Limit{Timeout: 54*time.Second + time.Millisecond}.Seconds())
	assert.Equal(t, 120, Limit{Timeout: 120 * time.Second}.Seconds())
}

func TestDecide_WritesTheLimitOnlyWhenItIsNotTheDefault(t *testing.T) {
	args := map[string]any{"sql": "SELECT 1"}
	limit, err := Decide(ToolQuery, args, ToolMaxTimeout, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, LimitDefault, limit.Source)
	assert.NotContains(t, args, ArgTimeout, "a call naming no timeout is the call it always was")

	args = map[string]any{}
	_, err = Decide(ToolQuery, args, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 60, args[ArgTimeout], "a lowered ceiling reaches the tool, whose default is longer")

	for _, v := range []any{int64(200), float64(200), 200} {
		args = map[string]any{ArgTimeout: v}
		_, err = Decide(ToolExecute, args, ToolMaxTimeout, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, 200, args[ArgTimeout])
	}

	for _, v := range []any{"300", 1.5, int64(1 << 40)} {
		_, err = Decide(ToolQuery, map[string]any{ArgTimeout: v}, ToolMaxTimeout, time.Hour)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trino_query: timeout_seconds is")
		assert.Contains(t, err.Error(), "whole number of seconds")
	}
}

func TestBeginAndEnd(t *testing.T) {
	stmt, err := Begin("api_invoke_endpoint", map[string]any{ArgTimeout: 999}, ToolMaxTimeout, time.Hour)
	require.NoError(t, err)
	assert.Nil(t, stmt, "a tool that runs no statement is left alone")
	assert.Nil(t, stmt.End(nil, nil))

	_, err = Begin(ToolExecute, map[string]any{ArgTimeout: 999}, ToolMaxTimeout, time.Hour)
	require.Error(t, err)

	stmt, err = Begin(ToolQuery, map[string]any{}, ToolMaxTimeout, time.Hour)
	require.NoError(t, err)
	timing := stmt.End(nil, []string{"3:5"})
	require.NotNil(t, timing)
	assert.Equal(t, ToolQuery, timing.Tool)
	assert.Equal(t, int64(120000), timing.LimitMS)
	assert.Equal(t, []string{"3:5"}, timing.CallSite)
	assert.False(t, timing.TimedOut)
}

func TestTime_AStoppedStatementCarriesItsLimit(t *testing.T) {
	refusal := scriptsession.NewRefusal("Query failed: context deadline exceeded", map[string]any{
		"code": "trino_query_failed", "retryable": false, "category": "client_input", "query_id": "q9",
		"statement_timeout": map[string]any{"elapsed_ms": float64(61000), "cancel_requested": true, "cancel_confirmed": true},
	})
	original := refusal.Envelope
	timing := Time(ToolExecute, Limit{time.Minute, LimitRequested}, 62*time.Second, refusal, nil)

	assert.Equal(t, runstate.StatementTiming{
		Tool: ToolExecute, ElapsedMS: 61000, LimitMS: 60000, LimitSource: LimitRequested, Share: 1.02,
		TimedOut: true, QueryID: "q9",
	}, *timing, "the elapsed time is the one the tool measured")
	st, _ := refusal.Envelope["statement_timeout"].(map[string]any)
	assert.Equal(t, int64(60000), st["limit_ms"])
	assert.Equal(t, LimitRequested, st["limit_source"])
	originalStopped, ok := original["statement_timeout"].(map[string]any)
	require.True(t, ok)
	_, touched := originalStopped["limit_ms"]
	assert.False(t, touched, "the envelope the tool answered with is not rewritten in place")

	other := Time(ToolQuery, Limit{time.Minute, LimitDefault}, 30*time.Second, errors.New("boom"), nil)
	assert.Equal(t, 0.5, other.Share)
	assert.False(t, other.TimedOut)
}

func TestSlowest(t *testing.T) {
	a := &runstate.StatementTiming{ElapsedMS: 10}
	b := &runstate.StatementTiming{ElapsedMS: 20}
	assert.Same(t, a, Slowest(nil, a))
	assert.Same(t, a, Slowest(a, nil))
	assert.Same(t, b, Slowest(a, b))
	assert.Same(t, b, Slowest(b, a))
	assert.Same(t, a, Slowest(a, &runstate.StatementTiming{ElapsedMS: 10}), "a tie keeps the first")
	assert.True(t, IsStatementTool(ToolQuery))
	assert.False(t, IsStatementTool("trino_export"))
}
