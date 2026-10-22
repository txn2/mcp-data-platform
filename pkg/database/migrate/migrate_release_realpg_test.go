package migrate

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrationsAgainstRealPostgres_EachCallReturnsItsConnection is #2249: a
// migration call gives its connection back to the caller's pool. Each call
// used to keep one for the pool's life, so the platform's pool lost one to
// its startup migration and a test stepping down 130 revisions held 130,
// which exhausted the real-database lane's server. The pool stays open.
func TestMigrationsAgainstRealPostgres_EachCallReturnsItsConnection(t *testing.T) {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN not set; skipping real-Postgres migration gate (run via `make migrate-check`)")
	}
	migratorFactory = newMigrator

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open test database")
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(t.Context()), "ping test database")
	resetSchema(t, db)

	require.NoError(t, Run(db), "migrate")
	for range 5 {
		require.NoError(t, Steps(db, -1), "step down")
		require.NoError(t, Steps(db, 1), "step up")
		_, _, err := Version(db)
		require.NoError(t, err, "read the version")
	}
	require.Zero(t, db.Stats().InUse, "connections still checked out after the migration calls returned")
	require.NoError(t, db.PingContext(t.Context()), "the pool was closed by a migration call")
}
