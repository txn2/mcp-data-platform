//go:build integration

package appdbconsole

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestConsole_RealDB is #2427's criteria against PostgreSQL: a SELECT answers
// its rows; with the toggle on an UPDATE is refused by the database and
// changes nothing, the server's DETAIL-bearing text answered to the caller
// alone; with it off the UPDATE commits.
func TestConsole_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		ctx := context.Background()
		target := db.TargetDB(t)
		_, err := target.Exec(`CREATE TABLE orders (id bigint PRIMARY KEY, total numeric(10,2) NOT NULL);
			INSERT INTO orders VALUES (7, 12.50)`)
		require.NoError(t, err)

		res, err := RunConsole(ctx, Toolkit{tk}, "shop", ConsoleRequest{SQL: "SELECT id, total FROM orders WHERE id = $1", Params: []any{7}})
		require.NoError(t, err)
		require.Nil(t, res.Error)
		require.Len(t, res.Results, 1)
		assert.Equal(t, [][]any{{int64(7), "12.50"}}, res.Results[0].Rows)
		assert.True(t, res.ReadOnly)
		assert.False(t, res.Committed)

		res, err = RunConsole(ctx, Toolkit{tk}, "shop", ConsoleRequest{SQL: "UPDATE orders SET total = 99 WHERE id = 7"})
		require.NoError(t, err)
		require.NotNil(t, res.Error)
		assert.Contains(t, res.Error.Summary, "25006")
		assert.Contains(t, res.Error.Detail, "read-only transaction")
		assert.False(t, res.Committed)
		var total string
		require.NoError(t, target.QueryRow(`SELECT total::text FROM orders WHERE id = 7`).Scan(&total))
		assert.Equal(t, "12.50", total, "the refused UPDATE changed nothing")

		off := false
		res, err = RunConsole(ctx, Toolkit{tk}, "shop", ConsoleRequest{
			Statements: []ConsoleStatement{
				{SQL: "UPDATE orders SET total = @t WHERE id = @id", Params: map[string]any{"t": "99.00", "id": 7}},
				{SQL: "SELECT total FROM orders WHERE id = 7"},
			},
			ReadOnly: &off,
		})
		require.NoError(t, err)
		require.Nil(t, res.Error, "%+v", res.Error)
		assert.True(t, res.Committed)
		assert.Equal(t, int64(1), res.Results[0].RowsAffected)
		require.NoError(t, target.QueryRow(`SELECT total::text FROM orders WHERE id = 7`).Scan(&total))
		assert.Equal(t, "99.00", total)

		res, err = RunConsole(ctx, Toolkit{tk}, "shop", ConsoleRequest{SQL: "INSERT INTO orders VALUES (7, 1)", ReadOnly: &off})
		require.NoError(t, err)
		require.NotNil(t, res.Error)
		assert.Contains(t, res.Error.Summary, "23505")
		assert.NotContains(t, res.Error.Summary, "(id)=(7)", "the summary repeats no value")
		assert.Contains(t, res.Error.Detail, "(id)=(7)", "the detail is the caller's")
	})
}

// TestSignal_RealDB is #2410's criteria against PostgreSQL: terminate ends an
// idle-in-transaction session of the connection's role, and a backend of
// another role is refused and left running.
func TestSignal_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk := realKit(t, db)
		ctx := context.Background()
		target := db.TargetDB(t)

		own := openSession(t, db, fmt.Sprint(db.Target["user"]), fmt.Sprint(db.Target["password"]))
		ownPID := idleInTransaction(t, own)
		res, err := Signal(ctx, tk, "shop", SignalTerminate, ownPID)
		require.NoError(t, err)
		assert.True(t, res.Sent)
		require.Eventually(t, func() bool { return !alive(t, target, ownPID) }, 10*time.Second, 50*time.Millisecond)

		role := fmt.Sprintf("other_%d", time.Now().UnixNano())
		_, err = target.Exec(fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'pw'`, role))
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = db.Server.Exec(fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role)) })
		other := openSession(t, db, role, "pw")
		otherPID := idleInTransaction(t, other)
		_, err = Signal(ctx, tk, "shop", SignalTerminate, otherPID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not the platform's to terminate")
		assert.True(t, alive(t, target, otherPID), "another role's backend keeps running")

		_, err = Signal(ctx, tk, "shop", "kill", otherPID)
		require.Error(t, err)
		_, err = Signal(ctx, tk, "shop", SignalCancel, 1)
		require.Error(t, err)
	})
}

func openSession(t *testing.T, db testdb.AppDB, user, password string) *sql.DB {
	t.Helper()
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(user, password),
		Host: fmt.Sprintf("%v:%v", db.Target["host"], db.Target["port"]), Path: "/" + db.Database, RawQuery: "sslmode=disable",
	}
	s, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	s.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// idleInTransaction opens a transaction on s, leaves it idle, and returns its
// backend's pid.
func idleInTransaction(t *testing.T, s *sql.DB) int64 {
	t.Helper()
	tx, err := s.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	var pid int64
	require.NoError(t, tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid))
	return pid
}

func alive(t *testing.T, db *sql.DB, pid int64) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&n))
	return n == 1
}
