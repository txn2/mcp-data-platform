//go:build integration

package appdbhealth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/sqltag"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func realKit(t *testing.T, db testdb.AppDB) *postgres.Toolkit {
	t.Helper()
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{"shop": db.Target})
	require.NoError(t, err)
	tk := postgres.NewMulti(multi)
	tk.SetConnectGuard(postgres.OwnDatabaseGuard(db.Platform))
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// waitFor polls cond until it holds: the statistics collector reports a
// table's counts a moment after the statements that changed them.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 30*time.Second, 100*time.Millisecond)
}

// TestHealth_RealDB is #2409's criteria against PostgreSQL: a table with
// deleted rows reports its dead tuples, an index never scanned is listed
// unused and is a describe finding, a session left idle in a transaction is
// listed, and without pg_stat_statements the report says it is not installed
// and is otherwise complete. No section fails on a fresh database.
func TestHealth_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		ctx := context.Background()
		target := db.TargetDB(t)
		conn, err := target.Conn(ctx)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `CREATE TABLE orders (id bigint PRIMARY KEY, note text) WITH (autovacuum_enabled = false);
			CREATE INDEX orders_note_idx ON orders (note);
			INSERT INTO orders SELECT g, 'n' || g FROM generate_series(1, 4000) g;
			DELETE FROM orders WHERE id <= 2000`)
		require.NoError(t, err)
		// A backend hands its counts to the statistics a moment after a
		// transaction ends; ask it to at the next one.
		_, err = conn.ExecContext(ctx, `SELECT pg_stat_force_next_flush()`)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `SELECT 1`)
		require.NoError(t, err)
		require.NoError(t, conn.Close())

		idle, err := target.Begin()
		require.NoError(t, err)
		t.Cleanup(func() { _ = idle.Rollback() })
		_, err = idle.Exec(`SELECT 1`)
		require.NoError(t, err)

		var r *Report
		waitFor(t, func() bool {
			r = Read(ctx, tk, "shop", Options{})
			for _, tb := range r.Tables {
				if tb.Name == "orders" && tb.DeadTuples >= 2000 {
					return true
				}
			}
			return false
		})
		assert.Empty(t, r.Errors)
		assert.Equal(t, db.Database, r.Server.Database)
		assert.NotEmpty(t, r.Server.Version)
		assert.Positive(t, r.Connections.Max)
		assert.False(t, r.Statements.Installed)
		assert.Contains(t, r.Statements.Note, "not installed")

		var orders TableStats
		for _, tb := range r.Tables {
			if tb.Name == "orders" {
				orders = tb
			}
		}
		assert.Equal(t, int64(2000), orders.DeadTuples)
		assert.InDelta(t, 0.5, orders.DeadRatio, 0.01)

		names := []string{}
		for _, i := range r.Indexes.Unused {
			names = append(names, i.Name)
		}
		assert.Contains(t, names, "orders_note_idx")
		assert.NotContains(t, names, "orders_pkey", "an index backing a constraint is not unused")

		idleListed := false
		for _, s := range r.Sessions {
			idleListed = idleListed || s.State == "idle in transaction"
		}
		assert.True(t, idleListed, "the idle-in-transaction session is listed: %+v", r.Sessions)

		sum := Summarize(r)
		assert.GreaterOrEqual(t, sum.IdleInTransaction, 1)
		assert.InDelta(t, 0.5, sum.MaxDeadRatio, 0.01)
		assert.Equal(t, "public.orders", sum.MaxDeadTable)

		findings := Findings(ctx, tk, "shop", "public", 0.2)
		kinds := map[string]string{}
		for _, f := range findings {
			kinds[f.Kind] = f.Table + "/" + f.Index
		}
		assert.Equal(t, "orders/orders_note_idx", kinds[FindingUnusedIndex])
		assert.Equal(t, "orders/", kinds[FindingDeadTuples])
		assert.Empty(t, Findings(ctx, tk, "shop", "billing", 0.2), "another schema has nothing to report")
	})
}

// TestHealth_RealDB_Statements reads the statements pg_stat_statements ranks
// first once the extension is installed in the database (the gate's servers
// load it by shared_preload_libraries), and the rest of the report stands.
func TestHealth_RealDB_Statements(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		target := db.TargetDB(t)
		_, err := target.Exec(`CREATE EXTENSION pg_stat_statements; CREATE TABLE t (id int); INSERT INTO t SELECT generate_series(1, 100)`)
		require.NoError(t, err)
		// The report ranks by total time and keeps 20, and its own catalog
		// reads land in pg_stat_statements before it reads the view. A
		// statement of a few microseconds is outranked by them whenever they
		// run slower (#2516, on pg18), so each call waits 20 ms: 100 ms in all,
		// ahead of anything else in this database.
		for range 5 {
			_, err = target.Exec(`SELECT count(*), pg_sleep(0.02) FROM t WHERE id > 10`)
			require.NoError(t, err)
		}
		r := Read(context.Background(), tk, "shop", Options{})
		assert.Empty(t, r.Errors)
		assert.True(t, r.Statements.Installed)
		assert.Empty(t, r.Statements.Note)
		require.NotEmpty(t, r.Statements.Top)
		found := false
		for _, s := range r.Statements.Top {
			found = found || (s.Calls >= 5 && strings.Contains(s.Query, "FROM t WHERE id >"))
		}
		assert.True(t, found, "the repeated statement is ranked with its calls: %+v", r.Statements.Top)
	})
}

// TestHealth_RealDB_DuplicateAndInvalidIndexes lists two indexes over the same
// column the same way, and an index a failed concurrent build left invalid.
func TestHealth_RealDB_DuplicateAndInvalidIndexes(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		target := db.TargetDB(t)
		_, err := target.Exec(`CREATE TABLE items (id int, sku text);
			CREATE INDEX items_sku_a ON items (sku); CREATE INDEX items_sku_b ON items (sku);
			INSERT INTO items VALUES (1, 'x'), (1, 'y')`)
		require.NoError(t, err)
		_, err = target.Exec(`CREATE UNIQUE INDEX CONCURRENTLY items_id_unique ON items (id)`)
		require.Error(t, err, "the duplicate id fails the concurrent build, leaving it invalid")

		r := Read(context.Background(), tk, "shop", Options{})
		require.Len(t, r.Indexes.Duplicate, 1)
		assert.Equal(t, []string{"items_sku_a", "items_sku_b"}, r.Indexes.Duplicate[0].Names)
		require.Len(t, r.Indexes.Invalid, 1)
		assert.Equal(t, "items_id_unique", r.Indexes.Invalid[0].Name)
		assert.Equal(t, 1, Summarize(r).InvalidIndexes)
	})
}

// TestHealth_RealDB_StatementsRecordedBeforeTheTag is #2536: pg_stat_statements
// keeps the text of an entry's first execution, and a comment does not change
// the query id, so a platform statement first run before the platform tagged
// its statements keeps its untagged text while every tagged run adds to it.
// The ranking leaves those entries out by query id: the health report's own
// server and table reads, each first run untagged, are absent after two
// reads, and the application's statement is ranked. The toolkit's own
// statements are held to the same by TestPlatformQueryIDs_RealDB.
func TestHealth_RealDB_StatementsRecordedBeforeTheTag(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		target := db.TargetDB(t)
		_, err := target.Exec(`CREATE EXTENSION pg_stat_statements; CREATE TABLE t (id int); INSERT INTO t SELECT generate_series(1, 100)`)
		require.NoError(t, err)
		// This database's entries only: the servers are shared by the tests
		// of every package running at once.
		_, err = target.Exec(`SELECT pg_stat_statements_reset(0, (SELECT oid FROM pg_database WHERE datname = current_database()), 0)`)
		require.NoError(t, err)

		untagged := func(sql string) string { return strings.TrimSpace(strings.TrimPrefix(sql, sqltag.Platform)) }
		_, err = target.Exec(untagged(serverSQL))
		require.NoError(t, err)
		_, err = target.Exec(untagged(tablesSQL))
		require.NoError(t, err)
		for range 3 {
			_, err = target.Exec(`SELECT count(*), pg_sleep(0.02) FROM t WHERE id > 10`)
			require.NoError(t, err)
		}

		ctx := context.Background()
		_ = Read(ctx, tk, "shop", Options{})
		r := Read(ctx, tk, "shop", Options{})
		require.Empty(t, r.Errors)
		require.True(t, r.Statements.Installed)

		var stored []string
		rows, err := target.Query(`SELECT query FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
			 AND (query LIKE '%current_setting%' OR query LIKE '%pg_stat_user_tables%') AND query NOT LIKE '%EXPLAIN%'
			 AND query NOT LIKE '%pg_stat_statements%'`)
		require.NoError(t, err)
		for rows.Next() {
			var q string
			require.NoError(t, rows.Scan(&q))
			stored = append(stored, q)
		}
		require.NoError(t, rows.Err())
		require.NotEmpty(t, stored)
		for _, q := range stored {
			assert.NotContains(t, q, sqltag.Platform, "the entries kept their first, untagged text: %v", stored)
		}

		app := false
		for _, s := range r.Statements.Top {
			assert.NotContains(t, s.Query, "current_setting", "the server read is the platform's own: %+v", s)
			assert.NotContains(t, s.Query, "pg_stat_user_tables", "the table read is the platform's own: %+v", s)
			app = app || strings.Contains(s.Query, "FROM t WHERE id >")
		}
		assert.True(t, app, "the application's statement is ranked: %+v", r.Statements.Top)
	})
}
