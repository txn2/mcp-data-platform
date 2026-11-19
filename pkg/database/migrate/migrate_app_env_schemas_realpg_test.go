package migrate

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appEnvironmentSchemasVersion is the migration under test: each application
// environment names its schema, and a site part whose environment the
// application lacks is given it (#2709).
const appEnvironmentSchemasVersion = 260

// appEnv is one application environment row as 000260 leaves it.
type appEnv struct {
	Name     string
	Position int
	Release  int
	Schema   string
}

// TestMigrationsAgainstRealPostgres_AppEnvironmentSchemas writes applications
// in the shape v1.164 left them and reads them after 000260. A site part's
// pages call the environment of its name from 000260 on, so each site keeps
// answering as it did: shop has only prod and site parts on dev, prod and
// stage, so dev and stage are added ahead of prod running prod's release on
// its schema, and positions are numbered from 0; blog's dev ran no release
// and a site part calls it, so it runs prod's; news's dev ran none and no
// site calls it, so it is left.
//
// The file sorts before migrate_realpg_test.go, which runs last and hands the
// template database a clean schema at head (see migrate_output_key).
func TestMigrationsAgainstRealPostgres_AppEnvironmentSchemas(t *testing.T) {
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
	require.NoError(t, Steps(db, appEnvironmentSchemasVersion-1), "migrate to the prior revision")

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), q, args...)
		require.NoError(t, err, q)
	}
	exec(`INSERT INTO applications (id, name, owner_email, created_by, schema_name, current_release)
	      VALUES ('app_1', 'shop', 'o@example.com', 'o@example.com', 'shop', 3),
	             ('app_2', 'blog', 'o@example.com', 'o@example.com', 'blog', 5),
	             ('app_3', 'news', 'o@example.com', 'o@example.com', 'news', 7)`)
	exec(`INSERT INTO application_environments (application_id, name, position, release, updated_by)
	      VALUES ('app_1', 'prod', 0, 3, 'p@example.com'),
	             ('app_2', 'dev', 0, 0, 'o@example.com'), ('app_2', 'prod', 1, 5, 'o@example.com'),
	             ('app_3', 'dev', 0, 0, 'o@example.com'), ('app_3', 'prod', 1, 7, 'o@example.com')`)
	for _, p := range [][2]string{{"app_1", "front/dev"}, {"app_1", "front/prod"}, {"app_1", "docs/stage"}, {"app_2", "home/dev"}} {
		exec(`INSERT INTO application_parts (application_id, kind, part_id, added_by, release_mode) VALUES ($1, 'site', $2, 'o@example.com', 'coupled')`, p[0], p[1])
	}

	require.NoError(t, Steps(db, 1), "apply 000260")

	envs := func(app string) []appEnv {
		t.Helper()
		rows, err := db.QueryContext(t.Context(),
			`SELECT name, position, release, schema_name FROM application_environments WHERE application_id = $1 ORDER BY position`, app)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var out []appEnv
		for rows.Next() {
			var e appEnv
			require.NoError(t, rows.Scan(&e.Name, &e.Position, &e.Release, &e.Schema))
			out = append(out, e)
		}
		require.NoError(t, rows.Err())
		return out
	}
	assert.Equal(t, []appEnv{{"dev", 0, 3, "shop"}, {"stage", 1, 3, "shop"}, {"prod", 2, 3, "shop"}}, envs("app_1"),
		"each site part's environment exists, ahead of prod, running prod's release on its schema, numbered from 0")
	assert.Equal(t, []appEnv{{"dev", 0, 5, "blog"}, {"prod", 1, 5, "blog"}}, envs("app_2"),
		"a dev a site part calls that ran no release runs prod's, so the site answers as it did")
	assert.Equal(t, []appEnv{{"dev", 0, 0, "news"}, {"prod", 1, 7, "news"}}, envs("app_3"),
		"a dev no site part calls keeps running no release")

	var by string
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT updated_by FROM application_environments WHERE application_id = 'app_1' AND name = 'dev'`).Scan(&by))
	assert.Equal(t, "p@example.com", by)

	require.NoError(t, Steps(db, -1), "000260 goes down")
	require.NoError(t, Steps(db, 1), "and up again")
	assert.Len(t, envs("app_1"), 3, "a second up adds nothing")
}
