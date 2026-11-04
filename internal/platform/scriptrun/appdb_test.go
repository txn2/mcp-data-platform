package scriptrun

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// shopOnly serves one application database, "shop". Only HasConnection is
// read here: the calls go through the run's Caller, as a session's would.
type shopOnly struct{}

func (shopOnly) HasConnection(name string) bool { return name == "shop" }
func (shopOnly) Query(context.Context, postgres.Statement) (*postgres.Result, error) {
	return &postgres.Result{}, nil
}

func (shopOnly) Execute(context.Context, postgres.Statement) (*postgres.Result, error) {
	return &postgres.Result{}, nil
}

func (shopOnly) Batch(context.Context, string, []postgres.Statement) ([]*postgres.Result, error) {
	return nil, nil
}

func (shopOnly) Begin(context.Context, postgres.TxOptions) (scriptsession.AppDBTx, error) {
	return nil, nil //nolint:nilnil // never called: the run's calls go through its Caller
}

func (shopOnly) OpenCursor(context.Context, postgres.Statement, int) (scriptsession.AppDBCursor, error) {
	return nil, nil //nolint:nilnil // never called
}

func (shopOnly) CopyIn(context.Context, postgres.CopyIn) (int64, error) { return 0, nil }

func (shopOnly) CopyOut(context.Context, postgres.Statement, string, io.Writer, int64) (*postgres.CopyOutResult, error) {
	return nil, nil //nolint:nilnil // never called
}

func installShop(t *testing.T) {
	t.Helper()
	scriptsession.InstallAppDB(scriptsession.AppDB{Runner: func() scriptsession.AppDBRunner { return shopOnly{} }})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })
}

func TestQuery_OnAnApplicationDatabaseBindsNatively(t *testing.T) {
	installShop(t)
	caller := &recordingCaller{}
	result, err := execute(t, `
res = platform.query("SELECT region FROM orders WHERE id = :id AND tag = ANY(:tags)", connection="shop", params={"id": 7, "tags": ["a", "b"]})
print(res["rows"][0]["region"])
`, caller, nil)
	require.NoError(t, err)
	assert.Equal(t, "west\n", result.Log)
	require.Len(t, caller.calls, 1)
	call := caller.calls[0]
	assert.Equal(t, scriptsession.AppDBQuery, call.name)
	assert.Equal(t, "SELECT region FROM orders WHERE id = $1 AND tag = ANY($2)", call.args["sql"],
		"no value is written into the statement")
	assert.Equal(t, []any{int64(7), []any{"a", "b"}}, call.args["params"])
	assert.Equal(t, "shop", call.args["connection"])
	assert.Equal(t, DraftMaxRows, call.args["limit"])
}

func TestQuery_OnTrinoStillBindsLiterals(t *testing.T) {
	installShop(t)
	caller := &recordingCaller{}
	_, err := execute(t, `platform.query("SELECT 1 WHERE r = :r", connection="warehouse", params={"r": "west"})`, caller, nil)
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, ToolQuery, caller.calls[0].name)
	assert.Equal(t, "SELECT 1 WHERE r = 'west'", caller.calls[0].args["sql"])
}

func TestExecute_OnAnApplicationDatabase(t *testing.T) {
	installShop(t)
	caller := &recordingCaller{}
	_, err := execute(t, `platform.execute("INSERT INTO carts (customer) VALUES (:c)", connection="shop", params={"c": "ann"}, timeout_seconds=4)`, caller, nil)
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, scriptsession.AppDBExecute, caller.calls[0].name)
	assert.Equal(t, map[string]any{
		"connection": "shop", "sql": "INSERT INTO carts (customer) VALUES ($1)", "params": []any{"ann"}, "timeout_seconds": 4,
	}, caller.calls[0].args)
}

func TestExecute_OnAnApplicationDatabaseIsADraftWrite(t *testing.T) {
	installShop(t)
	caller := &recordingCaller{}
	result, err := Run(context.Background(), Options{
		Source: `platform.execute("DELETE FROM carts", connection="shop")`, Name: "test", RunID: "run_1",
		FireTime: fireTime, Caller: caller, Writes: WritesRefused,
	})
	require.Error(t, err)
	assert.Empty(t, caller.calls, "the barrier refuses before the statement runs")
	require.NotNil(t, result)
	require.NotNil(t, result.RefusedWrite)
	assert.Equal(t, scriptsession.AppDBExecute, result.RefusedWrite.Tool)
}

func TestAppDB_BindingErrorsAndReservedNames(t *testing.T) {
	installShop(t)
	for _, tc := range []struct{ name, source, want string }{
		{"list after IN", `platform.query("SELECT 1 WHERE id IN :ids", connection="shop", params={"ids": [1]})`, "write = ANY(:ids)"},
		{"call by the reserved name", `platform.call("appdb.query", {"connection": "shop", "sql": "SELECT 1"})`, "appdb.query is not a tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &recordingCaller{}
			_, err := execute(t, tc.source, caller, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, caller.calls)
		})
	}
}

// TestTransaction_GoesThroughTheRunsCallPath holds platform.transaction to
// the run's call path (#2402): each step is a call the Caller sees, a draft
// that reports its writes lists the one inside the transaction, and a retried
// function's call with an outside effect is refused by the guard every
// binding passes through.
func TestTransaction_GoesThroughTheRunsCallPath(t *testing.T) {
	installShop(t)
	caller := &recordingCaller{}
	res, err := Run(context.Background(), Options{
		Source: `
def place(tx):
    tx.execute("INSERT INTO orders (id) VALUES (:id)", params = {"id": 1})

platform.transaction("shop", place)
platform.batch("shop", ["INSERT INTO a VALUES (1)"])
`, Name: "test", RunID: "run_1", FireTime: fireTime, Caller: caller, Writes: WritesReported,
	})
	require.NoError(t, err)
	names := make([]string, 0, len(caller.calls))
	for _, c := range caller.calls {
		names = append(names, c.name)
	}
	assert.Equal(t, []string{scriptsession.AppDBBegin, scriptsession.AppDBTxExecute, scriptsession.AppDBCommit, scriptsession.AppDBBatch}, names)
	writes := make([]string, 0, len(res.Writes))
	for _, w := range res.Writes {
		writes = append(writes, w.Tool)
	}
	assert.Equal(t, []string{scriptsession.AppDBTxExecute, scriptsession.AppDBBatch}, writes)

	_, err = execute(t, `
def bump(tx):
    platform.notify("ops", "x")

platform.transaction("shop", bump, retry = True)
`, &recordingCaller{}, nil)
	require.ErrorContains(t, err, "not allowed inside a platform.transaction with retry = True")

	_, err = execute(t, `platform.call("appdb.begin", {"connection": "shop"})`, &recordingCaller{}, nil)
	require.ErrorContains(t, err, "is not a tool")
}

func TestValidate_ReadsTheConnectionsATransactionAndABatchReach(t *testing.T) {
	report := Validate(`
def main():
    """Places an order."""
    platform.transaction("shop", lambda tx: tx.execute("INSERT INTO a VALUES (1)"))
    platform.batch(connection = "ledger", statements = ["INSERT INTO b VALUES (1)"])
`)
	assert.Equal(t, []string{"ledger", "shop"}, report.Connections)
	assert.False(t, report.DynamicConnections)
	report = Validate(`
def main():
    """Names a connection it computes."""
    platform.transaction(run.params["c"], lambda tx: None)
`)
	assert.True(t, report.DynamicConnections)
}

// The page a query_iter loop holds is what the run holds outside the
// interpreter, beside its appended outputs, and counts toward its peak
// (#2454).
func TestHoldPageCountsTowardThePeak(t *testing.T) {
	h := &hostState{mem: scriptguard.NewMeter(0)}
	page := starlark.NewList([]starlark.Value{starlark.String(strings.Repeat("x", 4096))})
	h.holdPage(page)
	if h.paging <= 4096 || h.mem.Peak() < h.paging {
		t.Fatalf("paging %d, peak %d: the page is not held", h.paging, h.mem.Peak())
	}
	h.holdPage(nil)
	if h.paging != 0 || h.appendedBytes() != 0 {
		t.Fatalf("a closed loop still holds %d", h.paging)
	}
}
