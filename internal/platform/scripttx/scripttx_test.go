package scripttx_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttx"
)

type call struct {
	name string
	args map[string]any
}

// txCaller answers the application-database calls a transaction makes, the
// way a session's appdb caller does, and records them.
type txCaller struct {
	calls []call
	begun int
	// commits are the answers to successive commits; nil commits.
	commits []error
	// execErr fails every tx.execute.
	execErr error
}

func (c *txCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, call{name: name, args: args})
	switch name {
	case scriptsession.AppDBBegin:
		c.begun++
		return map[string]any{"transaction": fmt.Sprintf("tx%d", c.begun)}, nil
	case scriptsession.AppDBTxExecute:
		if c.execErr != nil {
			return nil, c.execErr
		}
		return map[string]any{"command": "INSERT", "rows_affected": float64(1)}, nil
	case scriptsession.AppDBTxBatch, scriptsession.AppDBBatch:
		stmts, _ := args["statements"].([]any)
		results := make([]any, len(stmts))
		for i := range stmts {
			results[i] = map[string]any{"command": "INSERT", "rows_affected": float64(1)}
		}
		return map[string]any{"results": results}, nil
	case scriptsession.AppDBCommit:
		if len(c.commits) > 0 {
			err := c.commits[0]
			c.commits = c.commits[1:]
			if err != nil {
				return nil, err
			}
		}
		return map[string]any{"committed": true}, nil
	case scriptsession.AppDBRollback:
		return map[string]any{"committed": false}, nil
	}
	return map[string]any{
		"columns": []any{map[string]any{"name": "n", "type": "integer"}},
		"rows":    []any{map[string]any{"n": float64(1)}}, "row_count": float64(1),
	}, nil
}

func (c *txCaller) names() []string {
	out := make([]string, len(c.calls))
	for i, cl := range c.calls {
		out[i] = cl.name
	}
	return out
}

func (c *txCaller) count(name string) int {
	n := 0
	for _, cl := range c.calls {
		if cl.name == name {
			n++
		}
	}
	return n
}

// conflict is the refusal a serialization failure arrives as.
func conflict() error {
	return scriptsession.NewRefusal("statement failed with SQLSTATE 40001 (serialization_failure)", map[string]any{
		"code": "statement_failed", "postgres": map[string]any{"sqlstate": "40001"},
	})
}

func run(t *testing.T, source string, caller scriptrun.Caller, writes scriptrun.WriteBarrier) (*scriptrun.Result, error) {
	t.Helper()
	return scriptrun.Run(context.Background(), scriptrun.Options{ //nolint:wrapcheck // the run's own error is what each test reads
		Source: source, Name: "test", RunID: "run_1", FireTime: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		Caller: caller, Writes: writes,
	})
}

func TestTransactionCommitsAndReturnsWhatFnReturned(t *testing.T) {
	caller := &txCaller{}
	res, err := run(t, `
def place(tx):
    tx.execute("INSERT INTO orders (id) VALUES (:id)", params = {"id": 7})
    got = tx.query("SELECT count(*) AS n FROM orders WHERE id = :id", params = {"id": 7})
    return got["rows"][0]["n"]

n = platform.transaction("shop", place, isolation = "serializable")
print("n=%d" % n)
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "n=1\n", res.Log)
	assert.Equal(t, []string{scriptsession.AppDBBegin, scriptsession.AppDBTxExecute, scriptsession.AppDBTxQuery, scriptsession.AppDBCommit}, caller.names())
	begin := caller.calls[0].args
	assert.Equal(t, "shop", begin["connection"])
	assert.Equal(t, "serializable", begin["isolation"])
	assert.Equal(t, 1, begin["attempt"])
	exec := caller.calls[1].args
	assert.Equal(t, "tx1", exec["transaction"])
	assert.Equal(t, "INSERT INTO orders (id) VALUES ($1)", exec["sql"], "the value is bound, never written into the statement")
	assert.Equal(t, []any{int64(7)}, exec["params"])
}

func TestTransactionRollsBackWhenFnFails(t *testing.T) {
	caller := &txCaller{}
	_, err := run(t, `
def place(tx):
    tx.execute("INSERT INTO orders VALUES (1)")
    fail("out of stock")

platform.transaction("shop", place)
`, caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "out of stock")
	assert.Equal(t, []string{scriptsession.AppDBBegin, scriptsession.AppDBTxExecute, scriptsession.AppDBRollback}, caller.names())
}

func TestTransactionReportsAStatementsFailure(t *testing.T) {
	caller := &txCaller{execErr: scriptsession.NewRefusal("statement failed with SQLSTATE 23514 (check_violation); constraint items_qty_check", map[string]any{
		"code": "statement_failed", "postgres": map[string]any{"sqlstate": "23514", "constraint": "items_qty_check", "detail": "Failing row contains (1, a, 0)."},
	})}
	_, err := run(t, `
def place(tx):
    tx.execute("INSERT INTO items VALUES (1, 'a', 0)")

platform.transaction("shop", place)
`, caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "items_qty_check")
	assert.NotContains(t, err.Error(), "Failing row", "DETAIL reaches only the script")
	assert.Equal(t, 1, caller.count(scriptsession.AppDBRollback))

	res, err := run(t, `
def place(tx):
    tx.execute("INSERT INTO items VALUES (1, 'a', 0)")

out = platform.transaction("shop", place, on_error = "return")
print(out["error"]["postgres"]["constraint"])
`, &txCaller{execErr: caller.execErr}, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Contains(t, res.Log, "\nitems_qty_check\n")
}

func TestRetriedTransactionIsTriedAgainOnAConflict(t *testing.T) {
	caller := &txCaller{commits: []error{conflict(), nil}}
	res, err := run(t, `
def bump(tx):
    tx.execute("UPDATE stock SET n = n - 1 WHERE sku = 'a'")
    return "done"

print(platform.transaction("shop", bump, isolation = "serializable", retry = True))
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "done\n", res.Log)
	assert.Equal(t, 2, caller.count(scriptsession.AppDBBegin))
	assert.Equal(t, 2, caller.calls[3].args["attempt"], "the second try says it is one")
}

func TestRetryStopsAtMaxAttempts(t *testing.T) {
	caller := &txCaller{commits: []error{conflict(), conflict(), conflict(), conflict()}}
	_, err := run(t, `
def bump(tx):
    tx.execute("UPDATE stock SET n = n - 1")

platform.transaction("shop", bump, retry = True)
`, caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "40001")
	assert.Equal(t, scripttx.MaxAttempts, caller.count(scriptsession.AppDBBegin))
}

func TestNoRetryWithoutRetryTrue(t *testing.T) {
	caller := &txCaller{commits: []error{conflict()}}
	_, err := run(t, `
def bump(tx):
    tx.execute("UPDATE stock SET n = n - 1")

platform.transaction("shop", bump)
`, caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "40001")
	assert.Equal(t, 1, caller.count(scriptsession.AppDBBegin))
}

func TestRetryReadsAConflictTheFunctionTookAndReturnedPast(t *testing.T) {
	ended := scriptsession.NewRefusal("the transaction is over", map[string]any{"code": "transaction_ended"})
	caller := &txCaller{execErr: conflict(), commits: []error{ended, ended, ended}}
	_, err := run(t, `
def bump(tx):
    tx.execute("UPDATE stock SET n = n - 1", on_error = "return")

platform.transaction("shop", bump, retry = True)
`, caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "40001", "the attempt failed for the conflict, not for what followed it")
	assert.Equal(t, scripttx.MaxAttempts, caller.count(scriptsession.AppDBBegin))
}

func TestRetriedFunctionMayNotReachOutside(t *testing.T) {
	for _, line := range []string{
		`platform.query("SELECT 1", connection = "w")`,
		`platform.call("search", {"query": "x"})`,
		`platform.save_state({"a": 1})`,
		`platform.batch("shop", ["SELECT 1"])`,
	} {
		caller := &txCaller{}
		_, err := run(t, fmt.Sprintf(`
def bump(tx):
    %s

platform.transaction("shop", bump, retry = True)
`, line), caller, scriptrun.WritesMade)
		require.ErrorContains(t, err, "not allowed inside a platform.transaction with retry = True", line)
		assert.Equal(t, 1, caller.count(scriptsession.AppDBRollback), line)
	}
	// Without retry the same call is the script's to make.
	caller := &txCaller{}
	_, err := run(t, `
def bump(tx):
    platform.call("search", {"query": "x"})

platform.transaction("shop", bump)
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
}

func TestTransactionsDoNotNest(t *testing.T) {
	_, err := run(t, `
def inner(tx):
    pass

def outer(tx):
    platform.transaction("shop", inner)

platform.transaction("shop", outer)
`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "do not nest")
}

func TestATxIsUsedOnlyInsideItsFunction(t *testing.T) {
	_, err := run(t, `
kept = []
def place(tx):
    kept.append(tx)

platform.transaction("shop", place)
kept[0].query("SELECT 1")
`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "this transaction has ended")
}

func TestBatches(t *testing.T) {
	caller := &txCaller{}
	res, err := run(t, `
def place(tx):
    out = tx.batch(["INSERT INTO a VALUES (1)", {"sql": "INSERT INTO b VALUES (:v)", "params": {"v": "x"}}])
    print(len(out))

platform.transaction("shop", place)
res = platform.batch("shop", [{"sql": "INSERT INTO c VALUES (:n)", "params": {"n": 2}}, {"sql": "SELECT 1", "params": None}])
print(res[0]["command"])
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "2\nINSERT\n", res.Log)
	inTx, ok := caller.calls[1].args["statements"].([]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"sql": "INSERT INTO a VALUES (1)"}, inTx[0])
	assert.Equal(t, map[string]any{"sql": "INSERT INTO b VALUES ($1)", "params": []any{"x"}}, inTx[1])
	batch := caller.calls[len(caller.calls)-1]
	assert.Equal(t, scriptsession.AppDBBatch, batch.name)
	assert.Equal(t, "shop", batch.args["connection"])
}

func TestBatchRefusesWhatItCannotRead(t *testing.T) {
	for name, stmts := range map[string]string{
		"empty":          `[]`,
		"a number":       `[1]`,
		"no sql":         `[{"params": {}}]`,
		"params not map": `[{"sql": "SELECT :a", "params": [1]}]`,
		"unbound name":   `["SELECT :missing"]`,
	} {
		_, err := run(t, `platform.batch("shop", `+stmts+`)`, &txCaller{}, scriptrun.WritesMade)
		require.Error(t, err, name)
	}
}

func TestDraftRefusesAWriteInsideATransaction(t *testing.T) {
	caller := &txCaller{}
	_, err := run(t, `
def place(tx):
    tx.query("SELECT 1")
    tx.execute("INSERT INTO orders VALUES (1)")

platform.transaction("shop", place)
`, caller, scriptrun.WritesRefused)
	require.ErrorContains(t, err, "tx.execute")
	assert.Equal(t, 0, caller.count(scriptsession.AppDBTxExecute), "the write was refused before it was sent")
	assert.Equal(t, 1, caller.count(scriptsession.AppDBRollback))
}

func TestTransactionArgumentsAreChecked(t *testing.T) {
	for name, src := range map[string]string{
		"no fn":        `platform.transaction("shop")`,
		"bad on_error": `platform.transaction("shop", lambda tx: None, on_error = "ignore")`,
		"tx bad args":  "def f(tx):\n    tx.query()\nplatform.transaction(\"shop\", f)",
		"batch bad":    "def f(tx):\n    tx.batch(\"SELECT 1\")\nplatform.transaction(\"shop\", f)",
	} {
		_, err := run(t, src, &txCaller{}, scriptrun.WritesMade)
		require.Error(t, err, name)
	}
}

func TestRefuseOnlyWhileRetrying(t *testing.T) {
	var h *scripttx.Host
	require.NoError(t, h.Refuse("platform.query"))
	require.NoError(t, (&scripttx.Host{}).Refuse("platform.query"))
	assert.False(t, scripttx.Retryable(errors.New("plain")))
	assert.True(t, scripttx.Retryable(conflict()))
}

func TestRetryPauseEndsWithTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scriptrun.Run(ctx, scriptrun.Options{
		Source: `
def bump(tx):
    tx.execute("UPDATE t SET n = 1")

platform.transaction("shop", bump, retry = True)
`, Name: "test", RunID: "run_1", FireTime: time.Now(), Caller: &txCaller{commits: []error{conflict()}},
	})
	require.Error(t, err)
}

// failingBatch fails every batch call with a constraint violation.
type failingBatch struct{ txCaller }

func (f *failingBatch) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if name == scriptsession.AppDBBatch || name == scriptsession.AppDBTxBatch {
		f.calls = append(f.calls, call{name: name, args: args})
		return nil, scriptsession.NewRefusal("statement 2 of the batch: SQLSTATE 23505", map[string]any{
			"code": "statement_failed", "postgres": map[string]any{"sqlstate": "23505"},
		})
	}
	return f.txCaller.CallTool(ctx, name, args)
}

func TestBatchFailures(t *testing.T) {
	res, err := run(t, `
out = platform.batch("shop", ["INSERT INTO a VALUES (1)"], on_error = "return")
print(out["error"]["postgres"]["sqlstate"])

def place(tx):
    got = tx.batch(["INSERT INTO a VALUES (1)"], on_error = "return")
    print(got["error"]["code"])

platform.transaction("shop", place)
`, &failingBatch{}, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Contains(t, res.Log, "23505\n")
	assert.Contains(t, res.Log, "statement_failed\n")

	_, err = run(t, `platform.batch("shop", ["INSERT INTO a VALUES (1)"])`, &failingBatch{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "statement 2 of the batch")

	for name, src := range map[string]string{
		"batch on_error": `platform.batch("shop", ["SELECT 1"], on_error = "skip")`,
		"batch args":     `platform.batch("shop")`,
		"tx on_error":    "def f(tx):\n    tx.batch([\"SELECT 1\"], on_error = \"skip\")\nplatform.transaction(\"shop\", f)",
		"tx empty":       "def f(tx):\n    tx.batch([])\nplatform.transaction(\"shop\", f)",
		"tx stmt":        "def f(tx):\n    tx.query(\"SELECT :x\")\nplatform.transaction(\"shop\", f)",
		"tx stmt opt":    "def f(tx):\n    tx.query(\"SELECT 1\", on_error = \"skip\")\nplatform.transaction(\"shop\", f)",
	} {
		_, err := run(t, src, &txCaller{}, scriptrun.WritesMade)
		require.Error(t, err, name)
	}
}
