package migrate

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationsAgainstRealPostgres_DevRenameNotice pins #2608: read before
// 000239 runs, the startup check names exactly the sites whose environment
// named dev has a domain and that 000239 renames, with the name it gives
// them; once 000239 has run it names none.
//
// The file sorts before migrate_realpg_test.go, which runs last and hands the
// template database a clean schema at head.
func TestMigrationsAgainstRealPostgres_DevRenameNotice(t *testing.T) {
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
	// alpha: dev on a domain, default beside it. beta: the same, with
	// development taken. gamma: dev with no domain. delta: default only.
	for _, s := range []struct{ id, name string }{{"s_a", "alpha"}, {"s_b", "beta"}, {"s_c", "gamma"}, {"s_d", "delta"}} {
		exec(`INSERT INTO sites (id, name, owner_email, created_by) VALUES ($1, $2, 'o@example.com', 'o@example.com')`, s.id, s.name)
	}
	for _, e := range [][2]string{
		{"s_a", "dev"},
		{"s_a", "default"},
		{"s_b", "dev"},
		{"s_b", "default"},
		{"s_b", "development"},
		{"s_c", "dev"},
		{"s_c", "default"},
		{"s_d", "default"},
	} {
		exec(`INSERT INTO site_environments (site_id, name, updated_by) VALUES ($1, $2, 'o@example.com')`, e[0], e[1])
	}
	for _, d := range [][2]string{{"dev.alpha.test", "s_a"}, {"b.beta.test", "s_b"}, {"a.beta.test", "s_b"}} {
		exec(`INSERT INTO site_domains (hostname, site_id, environment, created_by) VALUES ($1, $2, 'dev', 'o@example.com')`, d[0], d[1])
	}

	got, err := PendingDevRenames(t.Context(), db)
	require.NoError(t, err)
	assert.Equal(t, []DevRename{
		{Site: "alpha", Domains: "dev.alpha.test", NewName: "development"},
		{Site: "beta", Domains: "a.beta.test, b.beta.test", NewName: "dev-previous"},
	}, got, "a dev with no domain and a site with no dev are not named")

	require.NoError(t, Steps(db, 1), "apply 000239")
	renamed := func() []string {
		rows, err := db.QueryContext(t.Context(), `SELECT s.name || ':' || e.name FROM site_environments e JOIN sites s ON s.id = e.site_id
			WHERE e.name IN ('development', 'dev-previous') ORDER BY 1`)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var r string
			require.NoError(t, rows.Scan(&r))
			out = append(out, r)
		}
		require.NoError(t, rows.Err())
		return out
	}()
	assert.ElementsMatch(t, []string{"alpha:development", "beta:dev-previous", "beta:development", "gamma:development"}, renamed,
		"000239 renamed what the check named, and gamma's dev with no domain, which it does not warn of")

	got, err = PendingDevRenames(t.Context(), db)
	require.NoError(t, err)
	assert.Empty(t, got, "once 000239 has run there is nothing to warn of")
}
