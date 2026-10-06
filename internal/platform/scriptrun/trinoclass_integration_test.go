package scriptrun

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

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

// failingTrino is a Trino client every query of which fails with err, the
// value trino-go-client returns for that failure.
type failingTrino struct{ err error }

func (f failingTrino) Query(context.Context, string, trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	return nil, f.err
}

func (failingTrino) Explain(context.Context, string, trinoclient.ExplainType) (*trinoclient.ExplainResult, error) {
	return nil, errors.New("unused")
}
func (failingTrino) ListCatalogs(context.Context) ([]string, error)        { return nil, nil }
func (failingTrino) ListSchemas(context.Context, string) ([]string, error) { return nil, nil }
func (failingTrino) ListTables(context.Context, string, string) ([]trinoclient.TableInfo, error) {
	return nil, nil
}

func (failingTrino) DescribeTable(context.Context, string, string, string) (*trinoclient.TableInfo, error) {
	return nil, errors.New("unused")
}

func trinoFailure(errorType, errorName, sqlState, message string) error {
	return &trino.ErrQueryFailed{StatusCode: 200, Reason: &trino.ErrTrino{
		ErrorType: errorType, ErrorName: errorName, SqlState: sqlState, Message: message, ErrorCode: 1,
	}}
}

// trinoServer serves mcp-trino's own trino_query and trino_execute over a
// client failing with err, behind the platform's tool-call middleware and the
// error contract, and returns the Caller a script run is handed.
func trinoServer(t *testing.T, err error) Caller {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "trino", Version: "v0"}, nil)
	trinotools.NewToolkit(failingTrino{err}, trinotools.DefaultConfig()).
		Register(server, trinotools.ToolQuery, trinotools.ToolExecute)
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(limitedAuthn{middleware.AuthTypeOIDC}, limitedAuthz{}, limitedLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))
	caller, cleanup, connErr := scriptsession.Connect(context.Background(), server, "script-run")
	require.NoError(t, connErr)
	t.Cleanup(cleanup)
	return caller
}

// TestIntegration_TrinoFailuresAreRecordedByClass is #2032's acceptance
// through the assembled path: trino-go-client's error, classified by
// mcp-trino, kept by the platform's error contract, read by the session caller
// and recorded by the engine. A temporary failure is the upstream's and
// retryable; a statement's own failure stays the script's.
func TestIntegration_TrinoFailuresAreRecordedByClass(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		cause     string
		sentence  string
		retryable bool
	}{
		{
			"deadline exceeded while polling the statement",
			&trino.ErrQueryFailed{Reason: &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}},
			runstate.CauseUpstream, "Trino could not be reached (timeout); expected to pass on its next run.", true,
		},
		{
			"connection refused to the coordinator",
			&trino.ErrQueryFailed{Reason: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
			runstate.CauseUpstream, "Trino could not be reached (connection_refused); expected to pass on its next run.", true,
		},
		{
			"EXTERNAL JDBC_ERROR SQLSTATE 08001",
			trinoFailure("EXTERNAL", "JDBC_ERROR", "08001", "The connection attempt failed."),
			runstate.CauseUpstream, "Trino failed for a reason outside the script (EXTERNAL / JDBC_ERROR, SQLSTATE 08001); expected to pass on its next run.", true,
		},
		{
			"INSUFFICIENT_RESOURCES",
			trinoFailure("INSUFFICIENT_RESOURCES", "EXCEEDED_GLOBAL_MEMORY_LIMIT", "", "Query exceeded distributed user memory limit"),
			runstate.CauseUpstream, "Trino failed for a reason outside the script (INSUFFICIENT_RESOURCES / EXCEEDED_GLOBAL_MEMORY_LIMIT); expected to pass on its next run.", true,
		},
		{
			"EXTERNAL SQLSTATE 23505",
			trinoFailure("EXTERNAL", "JDBC_ERROR", "23505", "duplicate key value violates unique constraint"),
			runstate.CauseScript, "Trino refused the statement (EXTERNAL / JDBC_ERROR, SQLSTATE 23505)", false,
		},
		{
			"USER_ERROR TABLE_NOT_FOUND",
			trinoFailure("USER_ERROR", "TABLE_NOT_FOUND", "", "line 40:10: Table 'scratch.uploads.t' does not exist"),
			runstate.CauseScript, "Trino refused the statement (USER_ERROR / TABLE_NOT_FOUND)", false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := trinoServer(t, tc.err)
			_, err := execute(t, `platform.execute("MERGE INTO w.public.t USING s ON true WHEN MATCHED THEN DELETE", connection = "w")`+"\n", caller, nil)
			require.Error(t, err)
			cause := scriptguard.Cause(err)
			assert.Equal(t, tc.cause, cause)
			assert.Equal(t, tc.retryable, runstate.CauseRetryable(cause))
			assert.Contains(t, err.Error(), tc.sentence)
			assert.Contains(t, err.Error(), "Execution failed:", "the tool's own text is kept")
		})
	}
}

// The script handed the failure with on_error = "return" reads mcp-trino's
// envelope, details included.
func TestIntegration_OnErrorReturnReadsTheTrinoEnvelope(t *testing.T) {
	caller := trinoServer(t, trinoFailure("EXTERNAL", "JDBC_ERROR", "08001", "The connection attempt failed."))
	result, err := execute(t, `
res = platform.query("SELECT 1", connection = "w", on_error = "return", retry = False)
e = res["error"]
print(e["code"], e["category"], e["retryable"], e["trino"]["error_type"], e["trino"]["error_name"], e["trino"]["sql_state"])
`, caller, nil)
	require.NoError(t, err)
	assert.Contains(t, result.Log, "trino_query_failed upstream_unavailable True EXTERNAL JDBC_ERROR 08001\n")
}
