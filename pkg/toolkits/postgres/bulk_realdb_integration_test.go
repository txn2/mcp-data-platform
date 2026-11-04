//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// The criteria of #2454 against PostgreSQL itself.
func TestCursorReadsAResultPastMaxRows_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, server := newOrdersTarget(t, map[string]any{cfgKeyMaxRows: 1000, cfgKeyIterMaxRows: 300000})
		database := tk.connections["target"].cfg.Database
		ctx := context.Background()
		k, err := tk.OpenCursor(ctx, Statement{
			Connection: "target",
			SQL:        `SELECT g AS n FROM generate_series(1, 250000) g WHERE g > $1`, Params: []any{"0"},
		}, 5000)
		if err != nil {
			t.Fatal(err)
		}
		var rows, pages int
		for {
			page, done, err := k.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rows += len(page.Rows)
			pages++
			if len(page.Rows) > 5000 {
				t.Fatalf("a page of %d rows, want at most 5000", len(page.Rows))
			}
			if done {
				break
			}
		}
		k.Close()
		if rows != 250000 || pages < 50 {
			t.Fatalf("read %d rows in %d pages, want 250000 in at least 50", rows, pages)
		}
		if n := idleInTransaction(t, server, database); n != 0 {
			t.Fatalf("open transactions after close = %d", n)
		}

		// A loop that breaks after one page leaves no cursor or transaction.
		early, err := tk.OpenCursor(ctx, Statement{Connection: "target", SQL: `SELECT g FROM generate_series(1, 100000) g`}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := early.Next(ctx); err != nil {
			t.Fatal(err)
		}
		if n := idleInTransaction(t, server, database); n != 1 {
			t.Fatalf("open transactions while iterating = %d, want the cursor's own", n)
		}
		early.Close()
		waitFor(t, func() bool { return idleInTransaction(t, server, database) == 0 })
	})
}

func TestCursorPastTheLoopBoundFails_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyIterMaxRows: 1500})
		ctx := context.Background()
		x, err := tk.Begin(ctx, TxOptions{Connection: "target", ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer x.Rollback()
		k, err := x.OpenCursor(ctx, Statement{SQL: `SELECT g FROM generate_series(1, 5000) g`}, 1000)
		if err != nil {
			t.Fatal(err)
		}
		defer k.Close()
		var limit *IterLimitError
		for i := 0; ; i++ {
			_, done, err := k.Next(ctx)
			if errors.As(err, &limit) {
				if !strings.Contains(err.Error(), "iter_max_rows (1500)") {
					t.Fatalf("the refusal names %q", err)
				}
				return
			}
			if err != nil || done || i > 5 {
				t.Fatalf("page %d: err = %v, done = %v; want the loop bound", i, err, done)
			}
		}
	})
}

// The criteria of #2453 against PostgreSQL itself.
func TestCopyInAndOut_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newOrdersTarget(t, map[string]any{cfgKeyMaxRows: 1000})
		ctx := context.Background()
		rows := make([][]any, 50000)
		for i := range rows {
			rows[i] = []any{strconv.Itoa(i + 1), "1.50"}
		}
		n, err := tk.CopyIn(ctx, CopyIn{Connection: "target", Table: "public.orders", Columns: []string{"id", "total"}, Rows: rows})
		if err != nil {
			t.Fatal(err)
		}
		if n != 50000 {
			t.Fatalf("copied %d, want 50000", n)
		}
		if c := count(t, tk, "orders"); c != "50000" {
			t.Fatalf("orders = %s, want 50000", c)
		}

		// In the script's transaction, a function that fails afterwards
		// leaves no rows.
		x, err := tk.Begin(ctx, TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.CopyIn(ctx, CopyIn{Table: "items", Columns: []string{"order_id", "sku", "qty"}, Rows: [][]any{{"1", "a", "2"}}}); err != nil {
			t.Fatal(err)
		}
		x.Rollback()
		if c := count(t, tk, "items"); c != "0" {
			t.Fatalf("items = %s after the rollback, want 0", c)
		}

		// A row the table refuses fails the copy with the server's code, and
		// nothing of it is kept.
		_, err = tk.CopyIn(ctx, CopyIn{
			Connection: "target", Table: "items", Columns: []string{"order_id", "sku", "qty"},
			Rows: [][]any{{"1", "a", "1"}, {"1", "b", "0"}},
		})
		if state := sqlStateOf(t, err); state != "23514" {
			t.Fatalf("SQLSTATE = %s, want 23514", state)
		}

		// copy_out of more rows than max_rows writes every row.
		var buf bytes.Buffer
		res, err := tk.CopyOut(ctx, Statement{
			Connection: "target", SQL: `SELECT id, total FROM orders WHERE id > $1 ORDER BY id`,
			Params: []any{"0"},
		}, CopyFormatCSV, &buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		if res.Rows != 50000 || len(lines) != 50001 || lines[0] != "id,total" || lines[1] != "1,1.50" {
			t.Fatalf("copy_out wrote %d rows, %d lines, starting %q %q", res.Rows, len(lines), lines[0], lines[1])
		}
	})
}

func TestCopyInRunsAsThePersonasRole_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _, _, _ := roleToolkit(t)
		in := CopyIn{Connection: "target", Table: "ledger", Columns: []string{"id", "owner_id"}, Rows: [][]any{{"3", "sub-c"}}}
		if _, err := tk.CopyIn(as("granted", "sub-c"), in); err != nil {
			t.Fatalf("the granted persona's copy: %v", err)
		}
		in.Rows = [][]any{{"4", "sub-d"}}
		_, err := tk.CopyIn(as("refused", "sub-d"), in)
		if state := sqlStateOf(t, err); state != "42501" {
			t.Fatalf("SQLSTATE = %s, want 42501", state)
		}
	})
}
