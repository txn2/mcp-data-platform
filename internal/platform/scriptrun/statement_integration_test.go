package scriptrun

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trinodb/trino-go-client/trino"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// timedTrino is a Trino client that records the timeout each query was given
// and answers with err, or an empty result when err is nil.
type timedTrino struct {
	failingTrino
	mu       sync.Mutex
	timeouts []time.Duration
}

func (c *timedTrino) Query(_ context.Context, _ string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	c.mu.Lock()
	c.timeouts = append(c.timeouts, opts.Timeout)
	c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return &trinoclient.QueryResult{Rows: []map[string]any{}}, nil
}

func (c *timedTrino) seen() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.timeouts...)
}

// timedServer serves mcp-trino's trino_query and trino_execute over client,
// behind the platform's tool-call middleware and error contract, as
// trinoServer does.
func timedServer(t *testing.T, client *timedTrino) Caller {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "trino", Version: "v0"}, nil)
	trinotools.NewToolkit(client, trinotools.DefaultConfig()).
		Register(server, trinotools.ToolQuery, trinotools.ToolExecute)
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(limitedAuthn{middleware.AuthTypeOIDC}, limitedAuthz{}, limitedLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))
	caller, cleanup, err := scriptsession.Connect(context.Background(), server, "script-run")
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return caller
}

// runTimed runs source as a platform run with a 15 minute deadline and the
// given statement ceiling.
func runTimed(t *testing.T, source string, caller Caller, ceiling time.Duration) (*Result, error) {
	t.Helper()
	opts := RunLimits(PlatformLimits{MaxStatementTimeout: ceiling})
	opts.Source, opts.Name, opts.RunID, opts.FireTime, opts.Caller = source, "test", "run_1", fireTime, caller
	return Run(context.Background(), opts)
}

// TestIntegration_AStatementStoppedAtItsTimeoutIsTheScripts is #2073's
// first criterion through the assembled path: a statement the coordinator
// accepted and then stopped at its timeout fails the run as the script's, not
// retryable, naming the limit, how long it ran, the query id and the
// confirmed cancel; the run's metrics carry it as the slowest statement.
func TestIntegration_AStatementStoppedAtItsTimeoutIsTheScripts(t *testing.T) {
	client := &timedTrino{failingTrino: failingTrino{err: &trinoclient.QueryError{
		QueryID: "20261009_120000_00001_abcde", Accepted: true, Elapsed: 121400 * time.Millisecond,
		CancelRequested: true, CancelConfirmed: true, Err: context.DeadlineExceeded,
	}}}
	result, err := runTimed(t, `platform.execute("CREATE TABLE s.t AS SELECT * FROM big", connection = "w")`+"\n",
		timedServer(t, client), 0)
	require.Error(t, err)
	cause := scriptguard.Cause(err)
	assert.Equal(t, runstate.CauseScript, cause)
	assert.False(t, runstate.CauseRetryable(cause))
	assert.Contains(t, err.Error(),
		"Trino stopped the statement at its 120s limit after 121.4s (statement timeout, query 20261009_120000_00001_abcde); the query was canceled.")
	assert.NotContains(t, err.Error(), "could not be reached")
	assert.Equal(t, []time.Duration{trinoclient.DefaultConfig().Timeout}, client.seen(),
		"a statement that names no timeout runs under the tools' default")

	require.NotNil(t, result.SlowestStatement)
	assert.Equal(t, runstate.StatementTiming{
		Tool: ToolExecute, ElapsedMS: 121400, LimitMS: 120000, LimitSource: "default", Share: 1.01,
		TimedOut: true, QueryID: "20261009_120000_00001_abcde", CallSite: []string{"1:17"},
	}, *result.SlowestStatement)
}

// TestIntegration_AStoppedStatementIsReturnedWithItsLimit: on_error =
// "return" hands the script the envelope with the limit the statement ran
// under, and a cancel that was not confirmed is said to leave the query
// possibly running.
func TestIntegration_AStoppedStatementIsReturnedWithItsLimit(t *testing.T) {
	client := &timedTrino{failingTrino: failingTrino{err: &trinoclient.QueryError{
		QueryID: "q1", Accepted: true, Elapsed: 30 * time.Second, CancelRequested: true, Err: context.DeadlineExceeded,
	}}}
	result, err := runTimed(t, `
res = platform.query("SELECT 1", connection = "w", timeout_seconds = 30, on_error = "return")
st = res["error"]["statement_timeout"]
print(res["error"]["retryable"], res["error"]["query_id"], st["limit_ms"], st["limit_source"], st["cancel_confirmed"])
`, timedServer(t, client), 0)
	require.NoError(t, err)
	assert.Contains(t, result.Log, "\nFalse q1 30000 requested False\n")
	assert.Equal(t, []time.Duration{30 * time.Second}, client.seen())
}

// TestIntegration_AStatementThatNeverReachedTrinoStaysTheUpstreams is the
// second criterion: a timeout before the coordinator accepted the statement
// is still the upstream's, and retryable.
func TestIntegration_AStatementThatNeverReachedTrinoStaysTheUpstreams(t *testing.T) {
	client := &timedTrino{failingTrino: failingTrino{err: &trinoclient.QueryError{
		Accepted: false, Elapsed: 120 * time.Second,
		Err: &trino.ErrQueryFailed{Reason: &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}},
	}}}
	_, err := runTimed(t, `platform.execute("CREATE TABLE s.t AS SELECT 1", connection = "w")`+"\n", timedServer(t, client), 0)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "Trino could not be reached (timeout); expected to pass on its next run.")
}

// TestIntegration_TimeoutSecondsReachesTheTool is the third criterion: a
// statement given timeout_seconds = 300 runs under 300 seconds where the
// ceiling allows it, through platform.execute, platform.query and
// platform.call alike, and the run reports its slowest statement.
func TestIntegration_TimeoutSecondsReachesTheTool(t *testing.T) {
	client := &timedTrino{}
	result, err := runTimed(t, `
platform.execute("CREATE TABLE s.t AS SELECT 1", connection = "w", timeout_seconds = 300)
platform.query("SELECT 1", connection = "w", timeout_seconds = 200)
platform.call("trino_query", {"connection": "w", "sql": "SELECT 1", "timeout_seconds": 250})
`, timedServer(t, client), 0)
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{300 * time.Second, 200 * time.Second, 250 * time.Second}, client.seen())
	require.NotNil(t, result.SlowestStatement)
	assert.Equal(t, "requested", result.SlowestStatement.LimitSource)
	assert.False(t, result.SlowestStatement.TimedOut)
}

// TestIntegration_ATimeoutAboveTheCeilingIsRefusedByName: a request above
// the deployment's ceiling ends the run naming the ceiling and its key, and
// reaches no tool; platform.call is held to it as the helpers are.
func TestIntegration_ATimeoutAboveTheCeilingIsRefusedByName(t *testing.T) {
	for _, source := range []string{
		`platform.execute("SELECT 1", connection = "w", timeout_seconds = 120)`,
		`platform.query("SELECT 1", connection = "w", timeout_seconds = 120, on_error = "return")`,
		`platform.call("trino_execute", {"connection": "w", "sql": "SELECT 1", "timeout_seconds": 120})`,
	} {
		client := &timedTrino{}
		_, err := runTimed(t, source+"\n", timedServer(t, client), time.Minute)
		require.Error(t, err, source)
		assert.Contains(t, err.Error(),
			"timeout_seconds is 120; the most a statement may run on this deployment is 60 (scripts.sql.max_timeout_seconds)", source)
		assert.Empty(t, client.seen(), "a refused statement reaches no tool")
	}
}
