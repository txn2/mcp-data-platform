package migrate

import (
	"database/sql"
	"os"
	"testing"

	"github.com/lib/pq"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// siteEnvironmentAccessVersion is the migration under test: default becomes
// dev, each environment carries its own access, and a grant names the
// environments it opens (#2604).
const siteEnvironmentAccessVersion = 239

// TestMigrationsAgainstRealPostgres_SiteEnvironmentAccess writes sites in the
// shape v1.157 left them and reads them after 000239: a site that had only
// default and one that also held an environment named dev from before default
// existed (000236), with a domain, a grant, everyone internal, an application
// part and its release pin.
//
// The file sorts before migrate_realpg_test.go, which runs last and hands the
// template database a clean schema at head (see migrate_output_key).
func TestMigrationsAgainstRealPostgres_SiteEnvironmentAccess(t *testing.T) {
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
	require.NoError(t, Steps(db, siteEnvironmentAccessVersion-1), "migrate to the prior revision")

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), q, args...)
		require.NoError(t, err, q)
	}
	for _, s := range []struct{ id, name string }{{"site_a", "alpha"}, {"site_b", "beta"}} {
		exec(`INSERT INTO sites (id, name, owner_email, created_by) VALUES ($1, $2, 'o@example.com', 'o@example.com')`, s.id, s.name)
	}
	exec(`UPDATE sites SET internal_view = TRUE WHERE id = 'site_a'`)
	// alpha: default, and prod on a domain. beta: the old dev with a domain,
	// default beside it, and a part on each.
	for _, e := range [][2]string{{"site_a", "default"}, {"site_a", "prod"}, {"site_b", "dev"}, {"site_b", "default"}} {
		exec(`INSERT INTO site_environments (site_id, name, updated_by) VALUES ($1, $2, 'o@example.com')`, e[0], e[1])
	}
	exec(`INSERT INTO site_domains (hostname, site_id, environment, created_by) VALUES ('www.alpha.test', 'site_a', 'prod', 'o@example.com')`)
	exec(`INSERT INTO site_domains (hostname, site_id, environment, created_by) VALUES ('dev.beta.test', 'site_b', 'dev', 'o@example.com')`)
	exec(`INSERT INTO site_deployments (site_id, environment, action, to_release, actor) VALUES ('site_b', 'default', 'publish', 1, 'o@example.com')`)
	exec(`INSERT INTO site_grants (site_id, email, level, granted_by) VALUES ('site_a', 'v@example.com', 'view', 'o@example.com')`)
	exec(`INSERT INTO applications (id, name, owner_email, created_by) VALUES ('app_1', 'shop', 'o@example.com', 'o@example.com')`)
	exec(`INSERT INTO application_parts (application_id, kind, part_id, added_by) VALUES ('app_1', 'site', 'beta/dev', 'o@example.com')`)
	exec(`INSERT INTO application_parts (application_id, kind, part_id, added_by) VALUES ('app_1', 'site', 'beta/default', 'o@example.com')`)

	require.NoError(t, Steps(db, 1), "apply 000239")

	envs := func(site string) map[string][2]bool {
		t.Helper()
		rows, err := db.QueryContext(t.Context(), `SELECT name, public, internal_view FROM site_environments WHERE site_id = $1`, site)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		out := map[string][2]bool{}
		for rows.Next() {
			var name string
			var public, internal bool
			require.NoError(t, rows.Scan(&name, &public, &internal))
			out[name] = [2]bool{public, internal}
		}
		require.NoError(t, rows.Err())
		return out
	}
	assert.Equal(t, map[string][2]bool{"dev": {false, true}, "prod": {true, false}}, envs("site_a"),
		"default is dev, keeps everyone internal and stays private; prod had a domain and stays public")
	assert.Equal(t, map[string][2]bool{"dev": {false, false}, "development": {true, false}}, envs("site_b"),
		"the old dev moved out of the way and kept answering on its domain")

	var main string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT main_environment FROM sites WHERE id = 'site_b'`).Scan(&main))
	assert.Equal(t, "dev", main, "the main address stays where it was: on dev")

	var domainEnv, deployEnv string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT environment FROM site_domains WHERE hostname = 'dev.beta.test'`).Scan(&domainEnv))
	assert.Equal(t, "development", domainEnv)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT environment FROM site_deployments WHERE site_id = 'site_b'`).Scan(&deployEnv))
	assert.Equal(t, "dev", deployEnv, "history follows the renamed environment")

	var parts pq.StringArray
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT array_agg(part_id ORDER BY part_id) FROM application_parts WHERE application_id = 'app_1'`).Scan(&parts))
	assert.Equal(t, pq.StringArray{"beta/dev", "beta/development"}, parts, "each part follows its environment")

	var persona string
	var grantEnvs pq.StringArray
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT persona, environments FROM site_grants WHERE site_id = 'site_a'`).Scan(&persona, &grantEnvs))
	assert.Empty(t, persona)
	assert.Empty(t, grantEnvs, "a grant from before names no environment, so it opens every private one")

	_, err = db.ExecContext(t.Context(), `UPDATE site_environments SET public = TRUE WHERE site_id = 'site_a' AND name = 'dev'`)
	require.Error(t, err, "dev can never be public")

	require.NoError(t, Steps(db, -1), "000239 goes down")
	var back string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT name FROM site_environments WHERE site_id = 'site_a' AND name IN ('default', 'dev')`).Scan(&back))
	assert.Equal(t, "default", back)
	require.NoError(t, Steps(db, 1), "and up again")
}
