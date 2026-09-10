package migrate

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runOutputTableChangesVersion is the migration under test: the one that
// renames the key script.RunOutput carries its table report under, from
// `tables` to `table_changes` (#1666).
const runOutputTableChangesVersion = 141

// TestMigrationsAgainstRealPostgres_RunOutputTableChanges proves the half a
// tag rename cannot do on its own.
//
// script_runs.outputs is a JSONB array of script.RunOutput, unmarshalled back
// into the same struct, so renaming the field's tag alone would leave every
// run already recorded reading back with no table report -- and a scheduled
// script's run history is exactly where a run that put a table behind its file
// has to say so. The rows are written at the revision before this one, in the
// shape that release wrote them, and read after it.
//
// The file name matters. `make test-realdb` builds the shared template
// database by running `-run TestMigrationsAgainstRealPostgres`, which matches
// every test here, and the template is left in whatever state the LAST one
// leaves. This file therefore sorts before migrate_realpg_test.go, so the
// full-lifecycle test runs after it and hands the template a clean schema at
// head -- the same ordering migrate_personal_scripts_realpg_test.go relies on.
// A test here that seeds rows and stops below head, as this one does, would
// otherwise be cloned into every real-DB test's database.
func TestMigrationsAgainstRealPostgres_RunOutputTableChanges(t *testing.T) {
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

	require.NoError(t, Steps(db, runOutputTableChangesVersion-1), "migrate to the prior revision")
	version, _, err := Version(db)
	require.NoError(t, err)
	require.Equal(t, uint(runOutputTableChangesVersion-1), version)

	owner := seedScriptForRun(t, db)

	// Two outputs in call order: the first reported what its version did to a
	// table, the second reported nothing because no table was registered over
	// its file. A run with no outputs at all is the third case, and the one a
	// rewrite over an empty array must not turn into NULL.
	seedRun(t, db, owner, "dpx_reported", `[
		{"name": "daily", "bytes": 12, "tables": ["scratch.uploads.daily on scratch now reads version 7."]},
		{"name": "weekly", "bytes": 34}
	]`)
	seedRun(t, db, owner, "dpx_empty", `[]`)

	require.NoError(t, Steps(db, 1), "apply the run-output migration")

	got := runOutputs(t, db, "dpx_reported")
	require.Len(t, got, 2, "the outputs keep their call order and their number")

	assert.Equal(t, []any{"scratch.uploads.daily on scratch now reads version 7."}, got[0]["table_changes"],
		"the recorded sentences are readable under the key the struct now carries")
	assert.NotContains(t, got[0], "tables", "the old key is rewritten, not duplicated")
	assert.Equal(t, "daily", got[0]["name"], "every other field of the output is untouched")
	assert.Equal(t, float64(12), got[0]["bytes"])

	assert.NotContains(t, got[1], "table_changes",
		"an output that reported no table gains no key")
	assert.Equal(t, "weekly", got[1]["name"])

	assert.Empty(t, runOutputs(t, db, "dpx_empty"),
		"a run with no outputs keeps an empty array rather than becoming null")

	// Down restores the shape the previous release reads, which is what makes
	// the upgrade reversible.
	require.NoError(t, Steps(db, -1), "roll the migration back")
	back := runOutputs(t, db, "dpx_reported")
	require.Len(t, back, 2)
	assert.Equal(t, []any{"scratch.uploads.daily on scratch now reads version 7."}, back[0]["tables"])
	assert.NotContains(t, back[0], "table_changes")

	// Leave the database at head. Run on its own this test would otherwise
	// finish a revision behind, which is not a state anything else here
	// expects to inherit.
	require.NoError(t, Steps(db, 1), "roll forward again")
}

// runOwner is the script and version a seeded run has to reference. They travel
// together because a run row names both.
type runOwner struct {
	scriptID  string
	versionID string
}

// seedScriptForRun writes the script and version a run has to reference.
func seedScriptForRun(t *testing.T, db *sql.DB) runOwner {
	t.Helper()
	var out runOwner
	require.NoError(t, db.QueryRowContext(t.Context(), `
		INSERT INTO scripts (name, source_code, owner_email)
		VALUES ('daily-sales', 'print(1)', 'jane@example.com')
		RETURNING id`).Scan(&out.scriptID), "seed script")
	require.NoError(t, db.QueryRowContext(t.Context(), `
		INSERT INTO script_versions (script_id, version, source_code, author)
		VALUES ($1, 1, 'print(1)', 'jane@example.com')
		RETURNING id`, out.scriptID).Scan(&out.versionID), "seed script version")
	return out
}

// seedRun writes one finished run carrying the outputs given as JSON.
func seedRun(t *testing.T, db *sql.DB, owner runOwner, id, outputs string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO script_runs (id, script_id, script_version_id, version, status, outputs)
		VALUES ($1, $2, $3, 1, 'succeeded', $4::jsonb)`,
		id, owner.scriptID, owner.versionID, outputs)
	require.NoError(t, err, "seed run %s", id)
}

// runOutputs reads one run's outputs back as the objects they are stored as.
func runOutputs(t *testing.T, db *sql.DB, id string) []map[string]any {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT outputs FROM script_runs WHERE id = $1`, id).Scan(&raw))
	var out []map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "outputs of %s are a JSON array", id)
	return out
}
