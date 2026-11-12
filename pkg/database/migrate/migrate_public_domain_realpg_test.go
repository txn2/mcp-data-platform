package migrate

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationsAgainstRealPostgres_OrderAndPublicDomains pins the upgrade of
// #2617 and #2619. 000244 stores each site's order by the rule the portal drew
// it by and adds an empty prod, last, to a site with none. Before 000245 runs,
// the startup check names each public environment with no domain; 000245 makes
// a public environment's domains public, drops the environment's own setting,
// and withdraws a request still waiting to make an environment public; once it
// has run the check names none.
//
// The file sorts before migrate_realpg_test.go, which runs last and hands the
// template database a clean schema at head.
func TestMigrationsAgainstRealPostgres_OrderAndPublicDomains(t *testing.T) {
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
	require.NoError(t, Steps(db, publicDomainVersion-2), "migrate to the revision before 000244")

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), q, args...)
		require.NoError(t, err, q)
	}
	// alpha: dev, stage (public, no domain) and prod (public, a domain).
	// beta: dev, qa, live (public, a domain) and no prod.
	exec(`INSERT INTO sites (id, name, owner_email, created_by, main_environment) VALUES ('s_a', 'alpha', 'o@example.com', 'o@example.com', 'dev')`)
	exec(`INSERT INTO sites (id, name, owner_email, created_by, main_environment) VALUES ('s_b', 'beta', 'o@example.com', 'o@example.com', 'live')`)
	for _, e := range []struct {
		site, name string
		public     bool
	}{
		{"s_a", "prod", true},
		{"s_a", "stage", true},
		{"s_a", "dev", false},
		{"s_b", "live", true},
		{"s_b", "qa", false},
		{"s_b", "dev", false},
	} {
		exec(`INSERT INTO site_environments (site_id, name, updated_by, public) VALUES ($1, $2, 'o@example.com', $3)`, e.site, e.name, e.public)
	}
	exec(`INSERT INTO site_domains (hostname, site_id, environment, created_by) VALUES ('alpha.example.com', 's_a', 'prod', 'o@example.com')`)
	exec(`INSERT INTO site_domains (hostname, site_id, environment, created_by) VALUES ('beta.example.com', 's_b', 'live', 'o@example.com')`)
	exec(`INSERT INTO approval_requests (id, subject_kind, purpose, site_id, target, version, source_sha256, requester_email)
	      VALUES ('apr_1', 'site_environment', 'site_public', 's_a', 'stage', 0, 'public:x', 'o@example.com')`)

	losses, err := PendingPublicLosses(t.Context(), db)
	require.NoError(t, err)
	assert.Equal(t, []PublicLoss{{Site: "alpha", Environment: "stage"}}, losses, "only a public environment with no domain is named")

	require.NoError(t, Steps(db, 1), "apply 000244")
	order := func(site string) []string {
		rows, err := db.QueryContext(t.Context(), `SELECT name FROM site_environments WHERE site_id = $1 ORDER BY position`, site)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var n string
			require.NoError(t, rows.Scan(&n))
			out = append(out, n)
		}
		require.NoError(t, rows.Err())
		return out
	}
	assert.Equal(t, []string{"dev", "stage", "prod"}, order("s_a"))
	assert.Equal(t, []string{"dev", "qa", "live", "prod"}, order("s_b"), "a site with no prod gets an empty one, last")
	var main string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT main_environment FROM sites WHERE id = 's_b'`).Scan(&main))
	assert.Equal(t, "live", main, "the main address stays where it was")
	losses, err = PendingPublicLosses(t.Context(), db)
	require.NoError(t, err)
	assert.Len(t, losses, 1, "000245 has not run yet")

	require.NoError(t, Steps(db, 1), "apply 000245")
	public := func(host string) bool {
		var p bool
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT public FROM site_domains WHERE hostname = $1`, host).Scan(&p))
		return p
	}
	assert.True(t, public("alpha.example.com"), "a public environment's domain answers anyone")
	assert.True(t, public("beta.example.com"))
	var column int
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'site_environments' AND column_name = 'public'`).Scan(&column))
	assert.Zero(t, column, "an environment has no public setting of its own")
	var status, comment string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT status, decision_comment FROM approval_requests WHERE id = 'apr_1'`).Scan(&status, &comment))
	assert.Equal(t, "withdrawn", status)
	assert.Contains(t, comment, "ask again to make a domain public")

	losses, err = PendingPublicLosses(t.Context(), db)
	require.NoError(t, err)
	assert.Empty(t, losses, "once 000245 has run there is nothing to warn of")
}
