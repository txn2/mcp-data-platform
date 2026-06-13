package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/require"
)

// expectedFinalVersion is the highest migration the embedded set defines. Bump
// this when adding a migration so the gate asserts the full set applied.
const expectedFinalVersion = 67

// TestMigrationsAgainstRealPostgres applies the embedded migrations to a real
// PostgreSQL (pgvector) instance and exercises the full lifecycle: up, seed,
// down, up again. It is the gate that catches what sqlmock and the embedded-file
// presence checks cannot — SQL the planner only rejects against a live engine
// (e.g. a non-IMMUTABLE function in an index expression), down-migration
// dependency-order bugs, and dev-seed rot against the current schema.
//
// It runs only when MIGRATE_TEST_DSN points at a disposable database (the
// `make migrate-check` target and CI provision one); otherwise it skips, so the
// default `go test ./...` needs no database. The target database is destroyed
// and rebuilt by this test, so MIGRATE_TEST_DSN must NEVER point at real data.
func TestMigrationsAgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN not set; skipping real-Postgres migration gate (run via `make migrate-check`)")
	}

	// Use the real migrator, not whatever a sibling mock test may have left in
	// the package-level factory.
	migratorFactory = newMigrator

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open test database")
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(t.Context()), "ping test database")

	// Start from a clean schema regardless of prior state. The target is a
	// disposable database (the Makefile/CI tears it down), so no end-of-test
	// reset is needed — and registering one as a t.Cleanup would run after the
	// deferred db.Close() above and hit a closed connection.
	resetSchema(t, db)

	// 1. Full up. This is what fails on a non-IMMUTABLE index expression or any
	//    other SQL the live engine rejects but sqlmock accepts.
	require.NoError(t, Run(db), "migrate up against real Postgres")

	version, dirty, err := Version(db)
	require.NoError(t, err, "read migration version")
	require.False(t, dirty, "migrations left the database dirty")
	require.Equal(t, uint(expectedFinalVersion), version, "did not reach the final migration")

	// 2. Seed against the migrated schema. Catches seed rot (a seed that
	//    references a dropped table or a changed constraint).
	applySeed(t, db)

	// 3. Full down with seeded data present. Catches down-migration
	//    dependency-order bugs (e.g. dropping a function an index still needs)
	//    and down steps that do not account for existing rows.
	require.NoError(t, Down(db), "migrate down against real Postgres")

	// 4. Up again, proving the set is re-applicable from a clean slate.
	require.NoError(t, Run(db), "migrate up again after down")
}

// resetSchema drops and recreates the public schema so each run starts empty,
// including the schema_migrations bookkeeping table.
func resetSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	require.NoError(t, err, "reset public schema")
}

// applySeed runs dev/seed.sql against the migrated database. The seed is pure
// SQL (no psql meta-commands), so a single multi-statement Exec mirrors how
// dev/start.sh applies it; any failing statement aborts the batch and fails the
// gate, which is the point.
func applySeed(t *testing.T, db *sql.DB) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve test file path")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	seedPath := filepath.Join(repoRoot, "dev", "seed.sql")

	seed, err := os.ReadFile(seedPath) //nolint:gosec // fixed in-repo dev seed path
	require.NoError(t, err, "read dev/seed.sql")

	_, err = db.ExecContext(t.Context(), string(seed))
	require.NoError(t, err, "apply dev/seed.sql against the migrated schema")
}
