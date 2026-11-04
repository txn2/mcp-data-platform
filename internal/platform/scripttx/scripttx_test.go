package scripttx_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// releaseErr fails every savepoint release.
	releaseErr error
	// failSQL fails a tx.execute whose statement holds it, as a unique
	// violation.
	failSQL string
	// pages is how many pages a cursor serves, each of pageRows rows (one
	// when zero); fetchErr fails a fetch.
	pages    int
	pageRows int
	fetchErr error
	// replayed answers counts as a recording's JSON carries them to a
	// replay: float64.
	replayed bool
	// refuse fails every call of this name with a statement refusal.
	refuse string
}

func (c *txCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, call{name: name, args: args})
	if name == c.refuse {
		return nil, scriptsession.NewRefusal("statement failed with SQLSTATE 42501 (insufficient_privilege)",
			map[string]any{"code": "statement_failed", "postgres": map[string]any{"sqlstate": "42501"}})
	}
	switch name {
	case scriptsession.AppDBBegin:
		c.begun++
		return map[string]any{"transaction": fmt.Sprintf("tx%d", c.begun)}, nil
	case scriptsession.AppDBTxExecute:
		if c.execErr != nil {
			return nil, c.execErr
		}
		if sql, _ := args["sql"].(string); c.failSQL != "" && strings.Contains(sql, c.failSQL) {
			return nil, scriptsession.NewRefusal("statement failed with SQLSTATE 23505 (unique_violation)",
				map[string]any{"code": "statement_failed", "postgres": map[string]any{"sqlstate": "23505"}})
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
	case scriptsession.AppDBSavepoint:
		return map[string]any{"savepoint": fmt.Sprintf("mcp_sp_%d", c.count(scriptsession.AppDBSavepoint))}, nil
	case scriptsession.AppDBRelease:
		if c.releaseErr != nil {
			return nil, c.releaseErr
		}
		return map[string]any{"released": true}, nil
	case scriptsession.AppDBRollbackTo:
		return map[string]any{"released": false}, nil
	case scriptsession.AppDBCursorOpen:
		return map[string]any{"cursor": "cursor1"}, nil
	case scriptsession.AppDBCursorFetch:
		if c.fetchErr != nil {
			return nil, c.fetchErr
		}
		page, _ := args["page"].(int)
		rows := []any{map[string]any{"n": int64(page)}}
		for i := 1; i < c.pageRows; i++ {
			rows = append(rows, map[string]any{"n": int64(page), "pad": strings.Repeat("x", 64)})
		}
		return map[string]any{
			"columns": []any{map[string]any{"name": "n", "type": "integer"}},
			"rows":    rows, "done": page >= c.pages,
		}, nil
	case scriptsession.AppDBCursorClose:
		return map[string]any{"closed": true}, nil
	case scriptsession.AppDBCopyIn:
		rows, _ := args["rows"].([]any)
		var n int64
		for range rows {
			n++
		}
		if c.replayed {
			return map[string]any{"rows_written": float64(n)}, nil
		}
		return map[string]any{"rows_written": n}, nil
	case scriptsession.AppDBCopyOut:
		if c.replayed {
			return map[string]any{"rows": float64(2), "bytes": float64(6)}, nil
		}
		return map[string]any{"rows": int64(2), "bytes": int64(6), "data": []byte("n\n1\n2\n")}, nil
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

func TestSavepointReleasesWhenFnReturnsAndRollsBackWhenItFails(t *testing.T) {
	caller := &txCaller{failSQL: "dup"}
	res, err := run(t, `
def bad(tx):
    tx.execute("INSERT INTO orders VALUES (2) -- dup")

def good(tx):
    tx.execute("INSERT INTO orders VALUES (3)")
    return "kept"

def place(tx):
    tx.execute("INSERT INTO orders VALUES (1)")
    failed = tx.savepoint(bad, on_error = "return")
    kept = tx.savepoint(good)
    return [failed["error"]["postgres"]["sqlstate"], kept]

print(platform.transaction("shop", place))
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Contains(t, res.Log, "[\"23505\", \"kept\"]\n")
	assert.Equal(t, []string{
		scriptsession.AppDBBegin, scriptsession.AppDBTxExecute,
		scriptsession.AppDBSavepoint, scriptsession.AppDBTxExecute, scriptsession.AppDBRollbackTo,
		scriptsession.AppDBSavepoint, scriptsession.AppDBTxExecute, scriptsession.AppDBRelease,
		scriptsession.AppDBCommit,
	}, caller.names())
	rollback := caller.calls[4].args
	assert.Equal(t, "tx1", rollback["transaction"])
	assert.Equal(t, "mcp_sp_1", rollback["savepoint"])
}

func TestSavepointNests(t *testing.T) {
	caller := &txCaller{failSQL: "dup"}
	_, err := run(t, `
def inner(tx):
    tx.execute("INSERT INTO orders VALUES (9) -- dup")

def outer(tx):
    tx.savepoint(inner, on_error = "return")
    tx.execute("INSERT INTO orders VALUES (1)")

platform.transaction("shop", lambda tx: tx.savepoint(outer))
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, []string{
		scriptsession.AppDBBegin, scriptsession.AppDBSavepoint, scriptsession.AppDBSavepoint, scriptsession.AppDBTxExecute,
		scriptsession.AppDBRollbackTo, scriptsession.AppDBTxExecute, scriptsession.AppDBRelease, scriptsession.AppDBCommit,
	}, caller.names())
}

func TestSavepointWhoseStatementFailedIsTheSavepointsFailure(t *testing.T) {
	caller := &txCaller{releaseErr: scriptsession.NewRefusal("a statement in savepoint mcp_sp_1 failed",
		map[string]any{"code": "statement_failed", "postgres": map[string]any{"sqlstate": "23505"}})}
	res, err := run(t, `
def fn(tx):
    tx.execute("INSERT INTO orders VALUES (1)")
    return "returned"

def place(tx):
    got = tx.savepoint(fn, on_error = "return")
    return got["error"]["postgres"]["sqlstate"]

print(platform.transaction("shop", place))
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Contains(t, res.Log, "returned the failure to the script")
	assert.True(t, strings.HasSuffix(res.Log, "23505\n"))
}

func TestSavepointFnsOwnFailureEndsTheRunAfterRollingBack(t *testing.T) {
	caller := &txCaller{}
	_, err := run(t, `platform.transaction("shop", lambda tx: tx.savepoint(lambda tx: fail("mine"), on_error = "return"))`,
		caller, scriptrun.WritesMade)
	require.ErrorContains(t, err, "mine")
	assert.Equal(t, []string{
		scriptsession.AppDBBegin, scriptsession.AppDBSavepoint, scriptsession.AppDBRollbackTo,
		scriptsession.AppDBRollback,
	}, caller.names())
}

func TestSavepointOutsideItsFunctionIsRefused(t *testing.T) {
	_, err := run(t, `
kept = []
platform.transaction("shop", lambda tx: kept.append(tx))
kept[0].savepoint(lambda tx: None)
`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "this transaction has ended")
	_, err = run(t, `platform.transaction("shop", lambda tx: tx.savepoint(lambda tx: None, on_error = "bogus"))`, &txCaller{}, scriptrun.WritesMade)
	require.Error(t, err)
	_, err = run(t, `platform.transaction("shop", lambda tx: tx.savepoint())`, &txCaller{}, scriptrun.WritesMade)
	require.Error(t, err)
}

func TestQueryIterReadsEveryPageAndClosesTheCursor(t *testing.T) {
	caller := &txCaller{pages: 3}
	res, err := run(t, `
total = 0
for row in platform.query_iter("shop", "SELECT n FROM t WHERE n > :min", params = {"min": 0}, page_rows = 1):
    total += row["n"]
print(total)
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "6\n", res.Log)
	assert.Equal(t, []string{
		scriptsession.AppDBCursorOpen, scriptsession.AppDBCursorFetch, scriptsession.AppDBCursorFetch,
		scriptsession.AppDBCursorFetch, scriptsession.AppDBCursorClose,
	}, caller.names())
	open := caller.calls[0].args
	assert.Equal(t, "shop", open["connection"])
	assert.Equal(t, "SELECT n FROM t WHERE n > $1", open["sql"], "the value is bound, never written into the statement")
	assert.Equal(t, 1, open["page_rows"])
}

func TestQueryIterThatBreaksClosesTheCursor(t *testing.T) {
	caller := &txCaller{pages: 5}
	_, err := run(t, `
def place(tx):
    for row in tx.query_iter("SELECT n FROM t", page_rows = 1):
        if row["n"] == 2:
            break

platform.transaction("shop", place)
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, []string{
		scriptsession.AppDBBegin, scriptsession.AppDBCursorOpen, scriptsession.AppDBCursorFetch,
		scriptsession.AppDBCursorFetch, scriptsession.AppDBCursorClose, scriptsession.AppDBCommit,
	}, caller.names())
	assert.Equal(t, "tx1", caller.calls[1].args["transaction"])
}

func TestQueryIterWhosePageFailsFailsTheRun(t *testing.T) {
	caller := &txCaller{pages: 5, fetchErr: scriptsession.NewRefusal("the loop read more than iter_max_rows (3) rows",
		map[string]any{"code": "statement_failed"})}
	_, err := run(t, `
n = 0
for row in platform.query_iter("shop", "SELECT n FROM t"):
    n += 1
print("finished with", n)
`, caller, scriptrun.WritesMade)
	require.Error(t, err, "a loop never ends early as though the result were whole")
	assert.Contains(t, err.Error(), "iter_max_rows")
	assert.Equal(t, scriptsession.AppDBCursorClose, caller.names()[len(caller.calls)-1], "the cursor is closed")
}

func TestQueryIterRefusals(t *testing.T) {
	_, err := run(t, `
rows = platform.query_iter("shop", "SELECT n FROM t")
for r in rows:
    pass
for r in rows:
    pass
`, &txCaller{pages: 1}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "read by one loop")
	_, err = run(t, `platform.query_iter("shop", "SELECT 1", page_rows = -1)`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "page_rows")
	_, err = run(t, `platform.query_iter("shop", "SELECT :x")`, &txCaller{}, scriptrun.WritesMade)
	require.Error(t, err, "a placeholder with no value")
	_, err = run(t, `platform.transaction("shop", lambda tx: platform.query_iter("shop", "SELECT 1"), retry = True)`,
		&txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "retry = True")
	r := &txCaller{}
	got, err := run(t, `print(type(platform.query_iter("shop", "SELECT 1")))`, r, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "query_iter\n", got.Log)
}

func TestCopyInTakesListAndDictRows(t *testing.T) {
	caller := &txCaller{}
	res, err := run(t, `
n = platform.copy_in("shop", "items", ["sku", "qty"], [["a", 1], {"qty": 2, "sku": "b"}])
m = platform.transaction("shop", lambda tx: tx.copy_in("items", ["sku", "qty"], [["c", 3]]))
print(n, m)
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "2 1\n", res.Log)
	first := caller.calls[0].args
	assert.Equal(t, "shop", first["connection"])
	assert.Equal(t, []any{"sku", "qty"}, first["columns"])
	assert.Equal(t, []any{[]any{"a", int64(1)}, []any{"b", int64(2)}}, first["rows"])
	assert.Equal(t, "tx1", caller.calls[2].args["transaction"])

	for src, want := range map[string]string{
		`platform.copy_in("shop", "items", ["sku"], [{"nope": 1}])`:         "has no",
		`platform.copy_in("shop", "items", ["sku"], [{"sku": 1, "x": 2}])`:  "does not name",
		`platform.copy_in("shop", "items", ["sku"], ["flat"])`:              "a row is a list",
		`platform.copy_in("shop", "items", [1], [[1]])`:                     "columns is a list",
		`platform.copy_in("shop", "items", ["sku"], [[1]], on_error = "x")`: "on_error",
	} {
		_, err := run(t, src, &txCaller{}, scriptrun.WritesMade)
		require.ErrorContains(t, err, want, src)
	}
}

func TestCopyOutWritesTheCopiedBytesAsAnOutput(t *testing.T) {
	caller := &txCaller{}
	res, err := run(t, `
out = platform.copy_out("shop", "all-items", "SELECT n FROM t WHERE n > :min", params = {"min": 0}, format = "text")
print(out["row_count"])
`, caller, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "2\n", res.Log)
	require.Len(t, res.Exports, 1)
	assert.Equal(t, "all-items", res.Exports[0].Name)
	assert.Equal(t, 2, res.Exports[0].RowCount)
	assert.Equal(t, 6, res.Exports[0].Bytes, "the copied bytes are what the output measures")
	call := caller.calls[0].args
	assert.Equal(t, "SELECT n FROM t WHERE n > $1", call["sql"])
	assert.Equal(t, "text", call["format"])

	_, err = run(t, `platform.copy_out("shop", "x", "SELECT 1", format = "parquet")`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "not csv or text")
	// platform.export's own arguments pass through to it.
	_, err = run(t, `platform.copy_out("shop", "x", "SELECT 1", destination = "nowhere")`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "nowhere")
}

// The page a loop holds counts toward the run's peak and its budget (#2454),
// though no walk of the interpreter's values reaches it.
func TestQueryIterChargesThePageItHolds(t *testing.T) {
	src := `
n = 0
for row in platform.query_iter("shop", "SELECT n FROM t", page_rows = 2000):
    n += 1
print(n)
`
	res, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: src, Name: "test", RunID: "run_1", FireTime: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		Caller: &txCaller{pages: 3, pageRows: 2000}, Writes: scriptrun.WritesMade,
	})
	require.NoError(t, err)
	assert.Equal(t, "6000\n", res.Log)
	assert.Greater(t, res.PeakMemory, int64(100_000), "a page of 2000 rows is in the peak")

	_, err = scriptrun.Run(context.Background(), scriptrun.Options{
		Source: src, Name: "test", RunID: "run_1", FireTime: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		Caller: &txCaller{pages: 3, pageRows: 2000}, Writes: scriptrun.WritesMade, MaxMemoryBytes: 50_000,
	})
	require.Error(t, err, "a page past the budget stops the run")
	assert.Contains(t, err.Error(), "memory")
}

// A replay answers counts as JSON numbers and copy_out with no bytes; the
// counts read the same (#2453).
func TestCopyCountsReadTheSameFromAReplay(t *testing.T) {
	res, err := run(t, `
print(platform.copy_in("shop", "items", ["sku"], [["a"], ["b"], ["c"]]))
print(platform.copy_out("shop", "all", "SELECT 1")["row_count"])
`, &txCaller{replayed: true}, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "3\n2\n", res.Log)
}

// A cursor, copy or savepoint the database refuses is the call's failure,
// handed back with on_error = "return" and raised without it.
func TestRefusedBulkCallsAreTheCallsFailure(t *testing.T) {
	for tool, src := range map[string]string{
		scriptsession.AppDBCursorOpen: `print(platform.query_iter("shop", "SELECT 1", on_error = "return")["error"]["postgres"]["sqlstate"])`,
		scriptsession.AppDBCopyIn:     `print(platform.copy_in("shop", "t", ["a"], [[1]], on_error = "return")["error"]["postgres"]["sqlstate"])`,
		scriptsession.AppDBCopyOut:    `print(platform.copy_out("shop", "out", "SELECT 1", on_error = "return")["error"]["postgres"]["sqlstate"])`,
		scriptsession.AppDBSavepoint:  `print(platform.transaction("shop", lambda tx: tx.savepoint(lambda t: 1, on_error = "return"))["error"]["postgres"]["sqlstate"])`,
	} {
		res, err := run(t, src, &txCaller{refuse: tool}, scriptrun.WritesMade)
		require.NoError(t, err, tool)
		assert.Contains(t, res.Log, "42501", tool)
	}
	for tool, src := range map[string]string{
		scriptsession.AppDBCursorOpen: `platform.transaction("shop", lambda tx: tx.query_iter("SELECT 1"))`,
		scriptsession.AppDBCopyIn:     `platform.transaction("shop", lambda tx: tx.copy_in("t", ["a"], [[1]]))`,
		scriptsession.AppDBRelease:    `platform.transaction("shop", lambda tx: tx.savepoint(lambda t: 1))`,
		scriptsession.AppDBRollbackTo: `platform.transaction("shop", lambda tx: tx.savepoint(lambda t: fail("x")))`,
	} {
		_, err := run(t, src, &txCaller{refuse: tool}, scriptrun.WritesMade)
		require.Error(t, err, tool)
	}
}

func TestBulkBindingArgumentRefusals(t *testing.T) {
	for src, want := range map[string]string{
		`platform.query_iter("shop")`:                                     "missing argument",
		`platform.query_iter("shop", "SELECT 1", on_error = "x")`:         "on_error",
		`platform.transaction("shop", lambda tx: tx.query_iter())`:        "missing argument",
		`platform.copy_in("shop", "t")`:                                   "missing argument",
		`platform.copy_in("shop", "t", ["a"], [[1]], on_error = "bogus")`: "on_error",
		`platform.transaction("shop", lambda tx: tx.copy_in("t"))`:        "missing argument",
		`platform.copy_out("shop", "x")`:                                  "missing argument",
		`platform.copy_out("shop", "x", "SELECT 1", on_error = "bogus")`:  "on_error",
		`platform.copy_out("shop", "x", "SELECT :v")`:                     "placeholder",
		`platform.copy_in("shop", "t", ["a"], [{"a": 1}, {"a": func}])`:   "row 2",
	} {
		_, err := run(t, "def func():\n    pass\n"+src, &txCaller{}, scriptrun.WritesMade)
		require.Error(t, err, src)
		assert.Contains(t, err.Error(), want, src)
	}
	_, err := run(t, `
held = []
platform.transaction("shop", lambda tx: held.append(tx))
held[0].query_iter("SELECT 1")
`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "this transaction has ended")
	_, err = run(t, `
held = []
platform.transaction("shop", lambda tx: held.append(tx))
held[0].copy_in("t", ["a"], [[1]])
`, &txCaller{}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "this transaction has ended")
}

func TestQueryIterValueAndAFetchInATransaction(t *testing.T) {
	res, err := run(t, `
rows = platform.query_iter("shop", "SELECT 1")
print(str(rows), bool(rows))
`, &txCaller{pages: 1}, scriptrun.WritesMade)
	require.NoError(t, err)
	assert.Equal(t, "<query_iter cursor1> True\n", res.Log)
	_, err = run(t, `{platform.query_iter("shop", "SELECT 1"): 1}`, &txCaller{pages: 1}, scriptrun.WritesMade)
	require.ErrorContains(t, err, "unhashable")

	caller := &txCaller{pages: 3, fetchErr: conflict()}
	_, err = run(t, `
def body(tx):
    for row in tx.query_iter("SELECT n FROM t"):
        pass
platform.transaction("shop", body, retry = True)
`, caller, scriptrun.WritesMade)
	require.Error(t, err, "a failed page fails the run inside a transaction too")
}
