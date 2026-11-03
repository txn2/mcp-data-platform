//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const ordersDDL = `CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL);
CREATE TABLE items (order_id int REFERENCES orders(id), sku text NOT NULL, qty int NOT NULL CHECK (qty > 0))`

func newOrdersTarget(t *testing.T, extra map[string]any) (*Toolkit, *sql.DB) {
	t.Helper()
	rt := realTarget(t)
	platform, target := rt.platform, rt.target
	for k, v := range extra {
		target[k] = v
	}
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	for stmt := range strings.SplitSeq(ordersDDL, ";") {
		execOK(t, tk, Statement{Connection: "target", SQL: stmt})
	}
	return tk, rt.server
}

func count(t *testing.T, tk *Toolkit, table string) string {
	t.Helper()
	got := queryOK(t, tk, Statement{Connection: "target", SQL: "SELECT count(*) FROM " + table})
	n, _ := json.Marshal(got.Rows[0][0])
	return string(n)
}

func TestTransactionIsAtomic_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES ($1, $2)`, Params: []any{"1", "9.50"}}); err != nil {
			t.Fatal(err)
		}
		for _, qty := range []string{"1", "2"} {
			if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO items VALUES (1, 'a', $1)`, Params: []any{qty}}); err != nil {
				t.Fatal(err)
			}
		}
		_, err = x.Execute(ctx, Statement{SQL: `INSERT INTO items VALUES (1, 'a', $1)`, Params: []any{"0"}})
		var failed *FailedError
		if !errors.As(err, &failed) || failed.Statement == nil || failed.Statement.Constraint != "items_qty_check" {
			t.Fatalf("the violating insert: err = %v", err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO items VALUES (1, 'b', 1)`}); err == nil ||
			!strings.Contains(err.Error(), "a statement in it failed") {
			t.Fatalf("a statement after the failure: err = %v", err)
		}
		if err := x.Commit(ctx); err == nil {
			t.Fatal("a commit after the failure succeeded")
		}
		if n := count(t, tk, "orders"); n != "0" {
			t.Fatalf("orders = %s, want 0", n)
		}
		if n := count(t, tk, "items"); n != "0" {
			t.Fatalf("items = %s, want 0", n)
		}
	})
}

func TestTransactionCommits_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target", Isolation: IsolationRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES (1, 1)`}); err != nil {
			t.Fatal(err)
		}
		got, err := x.Query(ctx, Statement{SQL: `SELECT current_setting('transaction_isolation'), count(*) FROM orders`})
		if err != nil {
			t.Fatal(err)
		}
		if got.Rows[0][0] != "repeatable read" {
			t.Fatalf("isolation = %v", got.Rows[0][0])
		}
		if n := count(t, tk, "orders"); n != "0" {
			t.Fatalf("an uncommitted row is visible outside: %s", n)
		}
		if err := x.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(t, tk, "orders"); n != "1" {
			t.Fatalf("orders = %s, want 1", n)
		}
		if _, err := x.Query(ctx, Statement{SQL: `SELECT 1`}); err == nil || !strings.Contains(err.Error(), "committed") {
			t.Fatalf("a call after the commit: err = %v", err)
		}
	})
}

func TestSerializableConflictIsReported_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		ctx := context.Background()
		execOK(t, tk, Statement{Connection: "target", SQL: `INSERT INTO orders VALUES (1, 0)`})
		a, err := tk.Begin(ctx, TxOptions{Connection: "target", Isolation: IsolationSerializable})
		if err != nil {
			t.Fatal(err)
		}
		b, err := tk.Begin(ctx, TxOptions{Connection: "target", Isolation: IsolationSerializable})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range []*Tx{a, b} {
			if _, err := x.Query(ctx, Statement{SQL: `SELECT total FROM orders WHERE id = 1`}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.Execute(ctx, Statement{SQL: `UPDATE orders SET total = total + 1 WHERE id = 1`}); err != nil {
			t.Fatal(err)
		}
		if err := a.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		_, err = b.Execute(ctx, Statement{SQL: `UPDATE orders SET total = total + 1 WHERE id = 1`})
		var failed *FailedError
		if !errors.As(err, &failed) || failed.Statement == nil || failed.Statement.SQLState != "40001" {
			t.Fatalf("the second writer: err = %v, want SQLSTATE 40001", err)
		}
	})
}

// idleInTransaction counts the target's sessions holding a transaction.
func idleInTransaction(t *testing.T, server *sql.DB, database string) int {
	t.Helper()
	var n int
	if err := server.QueryRow(`SELECT count(*) FROM pg_stat_activity
		WHERE datname = $1 AND application_name = $2 AND state LIKE 'idle in transaction%'`,
		database, applicationName).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEndedRunRollsTheTransactionBack_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, server := newOrdersTarget(t, nil)
		database := tk.connections["target"].cfg.Database
		ctx, cancel := context.WithCancel(context.Background())
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES (1, 1)`}); err != nil {
			t.Fatal(err)
		}
		if n := idleInTransaction(t, server, database); n != 1 {
			t.Fatalf("open transactions = %d, want 1", n)
		}
		cancel()
		waitFor(t, func() bool { return idleInTransaction(t, server, database) == 0 })
		if n := count(t, tk, "orders"); n != "0" {
			t.Fatalf("orders = %s, want 0", n)
		}
		if _, err := x.Query(context.Background(), Statement{SQL: `SELECT 1`}); err == nil || !strings.Contains(err.Error(), "run that opened it ended") {
			t.Fatalf("a call after the run ended: err = %v", err)
		}
	})
}

func TestCloseRollsBackOpenTransactions_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		x, err := tk.Begin(context.Background(), TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = tk.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Close waited on an open transaction")
		}
		if _, err := x.Query(context.Background(), Statement{SQL: `SELECT 1`}); err == nil || !strings.Contains(err.Error(), "shutting down") {
			t.Fatalf("a call after Close: err = %v", err)
		}
	})
}

func TestIdleTransactionIsRolledBack_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyTransactionIdle: "150ms"})
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES (1, 1)`}); err != nil {
			t.Fatal(err)
		}
		// Polling with a statement would keep the transaction from going idle,
		// so the wait reads its state and the statement is sent once after.
		waitFor(t, func() bool {
			x.mu.Lock()
			defer x.mu.Unlock()
			return x.ended != ""
		})
		if _, err := x.Query(ctx, Statement{SQL: `SELECT 1`}); err == nil || !strings.Contains(err.Error(), "transaction_idle_timeout") {
			t.Fatalf("a call after the idle bound: err = %v", err)
		}
		if n := count(t, tk, "orders"); n != "0" {
			t.Fatalf("orders = %s, want 0", n)
		}
	})
}

func TestPoolWaitFailsNamingThePool_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyMaxConns: 1, cfgKeyPoolWait: "100ms"})
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		defer x.Rollback()
		_, err = tk.Query(ctx, Statement{Connection: "target", SQL: `SELECT 1`})
		var failed *FailedError
		if !errors.As(err, &failed) || failed.Code != CodePoolWait || !strings.Contains(err.Error(), "max_conns") {
			t.Fatalf("a statement with the pool held: err = %v", err)
		}
	})
}

func TestBatchIsPipelinedAndAtomic_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		for _, cache := range []bool{true, false} {
			t.Run(map[bool]string{true: "cache", false: "no cache"}[cache], func(t *testing.T) {
				tk, _ := newOrdersTarget(t, map[string]any{cfgKeyStatementCache: cache})
				ctx := context.Background()
				res, err := tk.Batch(ctx, "target", []Statement{
					{SQL: `INSERT INTO orders VALUES ($1, $2)`, Params: []any{"1", "2.50"}},
					{SQL: `INSERT INTO items VALUES (1, $1, 3) RETURNING sku, qty`, Params: []any{"x"}},
					{SQL: `SELECT interval '1 day' AS d, '10.0.0.1'::inet AS ip`},
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(res) != 3 || res[0].Command != "INSERT" || res[0].RowsAffected != 1 {
					t.Fatalf("results = %+v", res)
				}
				if res[1].Rows[0][0] != "x" || res[2].Rows[0][0] != "1 day" || res[2].Rows[0][1] != "10.0.0.1" {
					t.Fatalf("rows = %v / %v", res[1].Rows, res[2].Rows)
				}
				_, err = tk.Batch(ctx, "target", []Statement{
					{SQL: `INSERT INTO orders VALUES (2, 1)`},
					{SQL: `INSERT INTO items VALUES (2, 'y', 0)`},
				})
				if err == nil || !strings.Contains(err.Error(), "statement 2 of the batch") {
					t.Fatalf("a failing batch: err = %v", err)
				}
				if n := count(t, tk, "orders"); n != "1" {
					t.Fatalf("orders = %s, want 1 (the failing batch committed part of itself)", n)
				}
			})
		}
	})
}

func TestBatchInsideATransaction_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Batch(ctx, []Statement{
			{SQL: `INSERT INTO orders VALUES (1, 1)`},
			{SQL: `INSERT INTO items VALUES (1, 'a', 1)`},
		}); err != nil {
			t.Fatal(err)
		}
		if err := x.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(t, tk, "items"); n != "1" {
			t.Fatalf("items = %s, want 1", n)
		}
	})
}

func TestStatementCacheSurvivesTheReset_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyMaxConns: 1})
		c := tk.connections["target"]
		q := Statement{Connection: "target", SQL: `SELECT count(*) FROM orders WHERE id = $1`, Params: []any{"1"}}
		queryOK(t, tk, q)
		queryOK(t, tk, q)
		c.cache.mu.Lock()
		var held int
		for _, set := range c.cache.conns {
			for _, s := range set.order {
				if s == q.SQL {
					held++
				}
			}
		}
		c.cache.mu.Unlock()
		if held != 1 {
			t.Fatalf("the statement is held prepared on %d connections, want 1", held)
		}
		var prepared int
		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT count(*) FROM pg_prepared_statements WHERE statement = $1`, Params: []any{q.SQL}})
		b, _ := json.Marshal(got.Rows[0][0])
		_ = json.Unmarshal(b, &prepared)
		if prepared != 1 {
			t.Fatalf("pg_prepared_statements holds the statement %d times after the reset, want 1", prepared)
		}
		// Session state is still reset.
		execOK(t, tk, Statement{Connection: "target", SQL: `SET search_path TO pg_catalog`})
		if v := queryOK(t, tk, Statement{Connection: "target", SQL: `SHOW search_path`}).Rows[0][0]; v == "pg_catalog" {
			t.Fatal("a session-level SET outlived the statement with the cache on")
		}
		for _, refused := range []string{`DEALLOCATE ALL`, `DISCARD ALL`, `PREPARE p AS SELECT 1`} {
			if _, err := tk.Execute(context.Background(), Statement{Connection: "target", SQL: refused}); err == nil ||
				!strings.Contains(err.Error(), "is not run") {
				t.Fatalf("%s: err = %v", refused, err)
			}
		}
	})
}

func TestStatementCacheFollowsASchemaChange_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyMaxConns: 1})
		execOK(t, tk, Statement{Connection: "target", SQL: `INSERT INTO orders VALUES (1, 1)`})
		q := Statement{Connection: "target", SQL: `SELECT * FROM orders`}
		if cols := len(queryOK(t, tk, q).Columns); cols != 2 {
			t.Fatalf("columns = %d", cols)
		}
		execOK(t, tk, Statement{Connection: "target", SQL: `ALTER TABLE orders ADD COLUMN note text`})
		tk.InvalidateStatements("target")
		if cols := len(queryOK(t, tk, q).Columns); cols != 3 {
			t.Fatalf("columns after the change = %d, want 3", cols)
		}
		// A change the platform did not make: the first call is refused by the
		// server, and the next prepares the statement again.
		execOK(t, tk, Statement{Connection: "target", SQL: `ALTER TABLE orders ADD COLUMN extra int`})
		if _, err := tk.Query(context.Background(), q); err == nil || !strings.Contains(err.Error(), "0A000") {
			t.Fatalf("the first call after an outside change: err = %v", err)
		}
		if cols := len(queryOK(t, tk, q).Columns); cols != 4 {
			t.Fatalf("columns after the outside change = %d, want 4", cols)
		}
	})
}

// waitFor polls cond until it holds, failing after ten seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not hold within 10s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
