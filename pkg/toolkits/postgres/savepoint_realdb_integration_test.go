//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
)

// The criteria of #2452 against PostgreSQL itself.
func TestSavepointUndoesOnlyItsOwnWork_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, nil)
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, x, `INSERT INTO orders VALUES (1, 1)`) // row A

		sp, err := x.Savepoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES (1, 2)`}) // a duplicate of A's key
		var failed *FailedError
		if !errors.As(err, &failed) || failed.Statement == nil || failed.Statement.SQLState != "23505" {
			t.Fatalf("the duplicate: err = %v, want 23505", err)
		}
		if err := x.Release(ctx, sp); !errors.As(err, &failed) {
			t.Fatalf("releasing the failed savepoint: err = %v, want the duplicate's failure", err)
		}
		mustExec(t, x, `INSERT INTO orders VALUES (2, 1)`) // row B

		// tx.savepoint's path when fn fails: the statement fails, then the
		// platform rolls back to the savepoint, which succeeds.
		failing, err := x.Savepoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Execute(ctx, Statement{SQL: `INSERT INTO orders VALUES (2, 9)`}); err == nil {
			t.Fatal("a duplicate of B's key was accepted")
		}
		if err := x.RollbackTo(ctx, failing); err != nil {
			t.Fatalf("rolling back to the savepoint a statement failed in: %v", err)
		}

		// Nested: the inner savepoint's failure undoes the inner work alone.
		outer, err := x.Savepoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, x, `INSERT INTO orders VALUES (3, 1)`)
		inner, err := x.Savepoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, x, `INSERT INTO orders VALUES (4, 1)`)
		if err := x.RollbackTo(ctx, inner); err != nil {
			t.Fatal(err)
		}
		if err := x.Release(ctx, outer); err != nil {
			t.Fatal(err)
		}
		if err := x.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT string_agg(id::text, ',' ORDER BY id) FROM orders`})
		if got.Rows[0][0] != "1,2,3" {
			t.Fatalf("committed ids = %v, want 1,2,3: A, B and the outer savepoint's row", got.Rows[0][0])
		}

		// The script cannot work a savepoint as SQL.
		for _, sql := range []string{`RELEASE SAVEPOINT x`, `ROLLBACK TO x`, `SAVEPOINT x`} {
			var refused *RefusedError
			if _, err := tk.Execute(ctx, Statement{Connection: "target", SQL: sql}); !errors.As(err, &refused) {
				t.Fatalf("%s: err = %v, want a refusal", sql, err)
			}
		}
	})
}

func TestRunEndingInsideASavepointLeavesNoTransaction_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, server := newOrdersTarget(t, nil)
		database := tk.connections["target"].cfg.Database
		ctx, cancel := context.WithCancel(context.Background())
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Savepoint(ctx); err != nil {
			t.Fatal(err)
		}
		mustExec(t, x, `INSERT INTO orders VALUES (1, 1)`)
		if n := idleInTransaction(t, server, database); n != 1 {
			t.Fatalf("open transactions = %d, want 1", n)
		}
		cancel() // the run failed inside the savepoint
		waitFor(t, func() bool { return idleInTransaction(t, server, database) == 0 })
		if n := count(t, tk, "orders"); n != "0" {
			t.Fatalf("orders = %s after the run ended, want 0", n)
		}
	})
}

func mustExec(t *testing.T, x *Tx, sql string) {
	t.Helper()
	if _, err := x.Execute(context.Background(), Statement{SQL: sql}); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
