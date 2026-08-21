package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personalScriptsVersion is the migration under test: the one that makes a
// script personal (#1404) and moves name uniqueness from "platform-wide unless
// personal" to "unique within an owner".
const personalScriptsVersion = 119

// TestMigrationsAgainstRealPostgres_PersonalScriptCollisions is the case the
// full-lifecycle gate cannot reach: it applies the set to the revision BEFORE
// this one, writes the rows only the old rule allowed, and then applies this
// one. Its name carries the gate's prefix so `make migrate-check` runs it on
// every pinned Postgres major without a second target to remember.
//
// Two rows that were legal apart — a global "daily-sales" and its author's own
// personal "daily-sales" — collide under the per-owner unique index, and an
// upgrade that hit that collision would leave a real deployment's database
// dirty. The migration renames every row but the oldest of each group, which is
// what this proves against a live engine.
func TestMigrationsAgainstRealPostgres_PersonalScriptCollisions(t *testing.T) {
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

	// Up to the revision before this one, which is the schema a deployment
	// upgrading into this release is on.
	require.NoError(t, Steps(db, personalScriptsVersion-1), "migrate to the prior revision")
	version, _, err := Version(db)
	require.NoError(t, err)
	require.Equal(t, uint(personalScriptsVersion-1), version)

	// Three rows the old rule allowed: the shared script, its author's own
	// personal one of the same name, and a third person's personal one, which
	// must be left alone because it is in nobody else's group.
	seed := func(id, name, scope, owner, created string) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), `
			INSERT INTO scripts (id, name, source_code, scope, personas, owner_email, created_at)
			VALUES ($1, $2, 'print(1)', $3, '{}', $4, $5)`,
			id, name, scope, owner, created)
		require.NoError(t, err, "seed script %s", name)
	}
	seed("11111111-1111-1111-1111-111111111111", "daily-sales", "global", "jane@example.com", "2026-01-01")
	seed("22222222-2222-2222-2222-222222222222", "daily-sales", "personal", "jane@example.com", "2026-02-01")
	seed("33333333-3333-3333-3333-333333333333", "daily-sales", "personal", "bob@example.com", "2026-03-01")

	require.NoError(t, Steps(db, 1), "apply the personal-scripts migration")

	name := func(id string) string {
		t.Helper()
		var got string
		require.NoError(t, db.QueryRowContext(t.Context(),
			`SELECT name FROM scripts WHERE id = $1`, id).Scan(&got))
		return got
	}
	assert.Equal(t, "daily-sales", name("11111111-1111-1111-1111-111111111111"),
		"the oldest row of a colliding group keeps the name")
	assert.Equal(t, "daily-sales-22222222", name("22222222-2222-2222-2222-222222222222"),
		"every later row is suffixed with the head of its id, which is unique by construction")
	assert.Equal(t, "daily-sales", name("33333333-3333-3333-3333-333333333333"),
		"another owner's script of the same name was never a collision")

	// The new rule is in force: the same owner cannot take the name twice, and
	// two owners still can.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO scripts (name, source_code, owner_email)
		VALUES ('daily-sales', 'print(1)', 'jane@example.com')`)
	assert.Error(t, err, "a name is unique within its owner")
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO scripts (name, source_code, owner_email)
		VALUES ('daily-sales', 'print(1)', 'carol@example.com')`)
	assert.NoError(t, err, "another owner may keep the same name")

	// And the columns the rule was built on are gone.
	var columns int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'scripts' AND column_name IN ('scope', 'personas')`).Scan(&columns))
	assert.Zero(t, columns, "scope and personas are removed, not left unread")
}
