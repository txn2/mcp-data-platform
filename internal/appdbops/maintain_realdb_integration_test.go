//go:build integration

package appdbops

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// TestMaintain_RealDB is #2410's criteria against PostgreSQL: analyze sets the
// table's last_analyze, vacuum reduces its dead tuples, vacuum_full and
// reindex run outside a transaction, a lock held elsewhere fails the command
// at its lock timeout rather than queueing, and an index change is refused
// naming manage_schema with nothing run.
func TestMaintain_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{"shop": db.Target})
		require.NoError(t, err)
		tk := postgres.NewMulti(multi)
		tk.SetConnectGuard(postgres.OwnDatabaseGuard(db.Platform))
		t.Cleanup(func() { _ = tk.Close() })
		ctx := context.Background()
		target := db.TargetDB(t)
		_, err = target.Exec(`CREATE TABLE orders (id bigint PRIMARY KEY, note text) WITH (autovacuum_enabled = false);
			INSERT INTO orders SELECT g, 'n' FROM generate_series(1, 3000) g;
			DELETE FROM orders WHERE id <= 1500`)
		require.NoError(t, err)

		out, err := Maintain(ctx, tk, Request{Connection: "shop", Command: CommandAnalyze, Table: "public.orders"})
		require.NoError(t, err)
		assert.Equal(t, `ANALYZE "public"."orders"`, out.Statement)
		require.Eventually(t, func() bool { return stat(t, target, "last_analyze IS NOT NULL") }, 10*time.Second, 50*time.Millisecond)

		require.Eventually(t, func() bool { return stat(t, target, "n_dead_tup >= 1500") }, 10*time.Second, 50*time.Millisecond)
		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: CommandVacuum, Table: "orders"})
		require.NoError(t, err)
		require.Eventually(t, func() bool { return stat(t, target, "n_dead_tup = 0") }, 10*time.Second, 50*time.Millisecond)

		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: CommandVacuumFull, Table: "orders"})
		require.NoError(t, err)
		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: CommandReindex, Table: "orders"})
		require.NoError(t, err)
		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: CommandAnalyze, Table: ""})
		require.NoError(t, err, "analyze of the whole database")

		lock, err := target.Begin()
		require.NoError(t, err)
		_, err = lock.Exec(`LOCK TABLE orders IN ACCESS EXCLUSIVE MODE`)
		require.NoError(t, err)
		start := time.Now()
		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: CommandVacuumFull, Table: "orders", LockTimeout: 200 * time.Millisecond})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "55P03", "the lock timeout ends the wait")
		assert.Less(t, time.Since(start), 5*time.Second)
		require.NoError(t, lock.Rollback())

		_, err = Maintain(ctx, tk, Request{Connection: "shop", Command: "create_index", Table: "orders"})
		require.ErrorIs(t, err, ErrSchemaChange)
		assert.Contains(t, err.Error(), "manage_schema")

		err = tk.RunMaintenance(ctx, "shop", "DROP TABLE orders", time.Second, 0)
		require.Error(t, err, "the toolkit runs nothing but maintenance")
		err = tk.RunMaintenance(ctx, "shop", "VACUUM orders; DROP TABLE orders", time.Second, 0)
		require.Error(t, err, "one statement, never a list")
		var n int
		require.NoError(t, target.QueryRow(`SELECT count(*) FROM orders`).Scan(&n))
		assert.Equal(t, 1500, n)
	})
}

func stat(t *testing.T, db *sql.DB, cond string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, db.QueryRow(`SELECT `+cond+` FROM pg_stat_user_tables WHERE relname = 'orders'`).Scan(&ok))
	return ok
}
