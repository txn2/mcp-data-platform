//go:build integration

package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// latestMigrationVersion returns the highest version number in the embedded
// migration set. Deriving it here keeps the round-trip assertions correct as
// migrations are added, instead of hardcoding a count that silently rots (the
// gap that let this test go stale at version 2 while the set grew past 79).
func latestMigrationVersion(t *testing.T) uint {
	t.Helper()
	entries, err := migrations.ReadDir("migrations")
	require.NoError(t, err)
	var maxVersion uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		numStr, _, ok := strings.Cut(name, "_")
		require.True(t, ok, "unexpected migration filename %q", name)
		n, parseErr := strconv.ParseUint(numStr, 10, 64)
		require.NoError(t, parseErr, "parsing version from %q", name)
		if uint(n) > maxVersion {
			maxVersion = uint(n)
		}
	}
	require.NotZero(t, maxVersion, "should find at least one migration")
	return maxVersion
}

func TestMigrations_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	// Start PostgreSQL container. The pgvector image is required because the
	// migration set creates the `vector` extension (000031+); a plain postgres
	// image fails the CREATE EXTENSION with "extension vector is not available".
	pgContainer, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err)
	defer func() { _ = pgContainer.Terminate(ctx) }()

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Open database connection
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	latest := latestMigrationVersion(t)

	// Test Run (up)
	t.Run("Run applies migrations", func(t *testing.T) {
		err := Run(db)
		require.NoError(t, err)

		// Verify tables exist
		var exists bool
		err = db.QueryRow(`
			SELECT EXISTS (
				SELECT FROM information_schema.tables
				WHERE table_name = 'audit_logs'
			)
		`).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "audit_logs table should exist")

		err = db.QueryRow(`
			SELECT EXISTS (
				SELECT FROM information_schema.tables
				WHERE table_name = 'oauth_clients'
			)
		`).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "oauth_clients table should exist")
	})

	// Test Version
	t.Run("Version returns current version", func(t *testing.T) {
		version, dirty, err := Version(db)
		require.NoError(t, err)
		require.False(t, dirty)
		require.Equal(t, latest, version)
	})

	// Test Run is idempotent
	t.Run("Run is idempotent", func(t *testing.T) {
		err := Run(db)
		require.NoError(t, err)

		version, dirty, err := Version(db)
		require.NoError(t, err)
		require.False(t, dirty)
		require.Equal(t, latest, version)
	})

	// Test Down
	t.Run("Down rolls back migrations", func(t *testing.T) {
		err := Down(db)
		require.NoError(t, err)

		// Verify tables don't exist
		var exists bool
		err = db.QueryRow(`
			SELECT EXISTS (
				SELECT FROM information_schema.tables
				WHERE table_name = 'audit_logs'
			)
		`).Scan(&exists)
		require.NoError(t, err)
		require.False(t, exists, "audit_logs table should not exist after down")
	})

	// Test Steps
	t.Run("Steps applies n migrations", func(t *testing.T) {
		// Apply just first migration
		err := Steps(db, 1)
		require.NoError(t, err)

		version, _, err := Version(db)
		require.NoError(t, err)
		require.Equal(t, uint(1), version)

		// Apply remaining
		err = Steps(db, 1)
		require.NoError(t, err)

		version, _, err = Version(db)
		require.NoError(t, err)
		require.Equal(t, uint(2), version)
	})
}

// startPostgres spins up a throwaway PostgreSQL container and returns
// an open connection plus a cleanup function.
func startPostgres(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:15",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	return db, func() {
		_ = db.Close()
		_ = pg.Terminate(ctx)
	}
}

func execMigrationFile(t *testing.T, db *sql.DB, file string) {
	t.Helper()
	content, err := migrations.ReadFile("migrations/" + file)
	require.NoError(t, err)
	_, err = db.Exec(string(content))
	require.NoError(t, err)
}

func configKey(t *testing.T, db *sql.DB, name, key string) (string, bool) {
	t.Helper()
	var present bool
	require.NoError(t, db.QueryRow(
		`SELECT config ? $2 FROM connection_instances WHERE name = $1`, name, key).Scan(&present))
	if !present {
		return "", false
	}
	var v string
	require.NoError(t, db.QueryRow(
		`SELECT config->>$2 FROM connection_instances WHERE name = $1`, name, key).Scan(&v))
	return v, true
}

// TestMigration050_UnifyOAuthRoundTrip seeds a legacy oauth2_* api
// connection (plus an already-canonical mcp connection) and proves the
// 000050 migration rewrites the api row onto the canonical schema,
// leaves the mcp row untouched, preserves the encrypted secret blob
// verbatim, is idempotent, and reverses cleanly on down.
func TestMigration050_UnifyOAuthRoundTrip_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()

	// Create only the connection_instances table (mirrors migration
	// 000027). Running the full chain would require the pgvector
	// extension used by later migrations, which the plain postgres image
	// does not ship; this migration touches only connection_instances.
	_, err := db.Exec(`
		CREATE TABLE connection_instances (
			kind        TEXT        NOT NULL,
			name        TEXT        NOT NULL,
			config      JSONB       NOT NULL DEFAULT '{}',
			description TEXT        NOT NULL DEFAULT '',
			created_by  TEXT        NOT NULL DEFAULT '',
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (kind, name)
		)`)
	require.NoError(t, err)

	const encSecret = "enc:ZmFrZS1jaXBoZXJ0ZXh0" // opaque blob; must survive verbatim
	_, err = db.Exec(`
		INSERT INTO connection_instances (kind, name, config) VALUES
		('api', 'legacy-ac', $1::jsonb),
		('mcp', 'canonical-mcp', $2::jsonb)`,
		`{
			"base_url": "https://api.example.com",
			"auth_mode": "oauth2_authorization_code",
			"oauth2_token_url": "https://idp/token",
			"oauth2_authorization_url": "https://idp/auth",
			"oauth2_client_id": "cid",
			"oauth2_client_secret": "`+encSecret+`",
			"oauth2_scopes": ["openid", "offline_access"],
			"oauth2_prompt": "consent",
			"oauth2_endpoint_auth_style": "params"
		}`,
		`{
			"endpoint": "https://mcp.example",
			"auth_mode": "oauth",
			"oauth_grant": "client_credentials",
			"oauth_token_url": "https://idp/token",
			"oauth_client_id": "mcpid"
		}`,
	)
	require.NoError(t, err)

	// Apply the migration's up SQL to the seeded rows (idempotent; it
	// already ran during Run on the then-empty table).
	execMigrationFile(t, db, "000050_unify_oauth_connection_config.up.sql")

	// api row is now canonical.
	assertKey := func(name, key, want string) {
		got, ok := configKey(t, db, name, key)
		require.True(t, ok, "%s should have key %s", name, key)
		require.Equal(t, want, got, "%s[%s]", name, key)
	}
	assertKey("legacy-ac", "auth_mode", "oauth")
	assertKey("legacy-ac", "oauth_grant", "authorization_code")
	assertKey("legacy-ac", "oauth_token_url", "https://idp/token")
	assertKey("legacy-ac", "oauth_authorization_url", "https://idp/auth")
	assertKey("legacy-ac", "oauth_client_id", "cid")
	assertKey("legacy-ac", "oauth_client_secret", encSecret) // verbatim
	assertKey("legacy-ac", "oauth_scope", "openid offline_access")
	assertKey("legacy-ac", "oauth_prompt", "consent")
	assertKey("legacy-ac", "oauth_endpoint_auth_style", "params")
	for _, legacy := range []string{
		"oauth2_token_url", "oauth2_authorization_url", "oauth2_client_id",
		"oauth2_client_secret", "oauth2_scopes", "oauth2_prompt", "oauth2_endpoint_auth_style",
	} {
		_, ok := configKey(t, db, "legacy-ac", legacy)
		require.False(t, ok, "legacy key %s should be gone", legacy)
	}

	// mcp row untouched.
	assertKey("canonical-mcp", "auth_mode", "oauth")
	assertKey("canonical-mcp", "oauth_client_id", "mcpid")

	// Idempotent: re-running changes nothing.
	var before string
	require.NoError(t, db.QueryRow(`SELECT config::text FROM connection_instances WHERE name='legacy-ac'`).Scan(&before))
	execMigrationFile(t, db, "000050_unify_oauth_connection_config.up.sql")
	var after string
	require.NoError(t, db.QueryRow(`SELECT config::text FROM connection_instances WHERE name='legacy-ac'`).Scan(&after))
	require.JSONEq(t, before, after, "second up run must be a no-op")

	// Down reverts the api row to the legacy schema.
	execMigrationFile(t, db, "000050_unify_oauth_connection_config.down.sql")
	assertKey("legacy-ac", "auth_mode", "oauth2_authorization_code")
	assertKey("legacy-ac", "oauth2_token_url", "https://idp/token")
	assertKey("legacy-ac", "oauth2_client_secret", encSecret)
	_, hasGrant := configKey(t, db, "legacy-ac", "oauth_grant")
	require.False(t, hasGrant, "oauth_grant should be removed on down")
	var scopesJSON string
	require.NoError(t, db.QueryRow(
		`SELECT config->'oauth2_scopes' FROM connection_instances WHERE name='legacy-ac'`).Scan(&scopesJSON))
	require.JSONEq(t, `["openid","offline_access"]`, scopesJSON)
}

// TestMigration124_RepairsThumbnailStampedUpdatedAt_RealDB covers the data
// repair in 000124 (#1466): rows whose updated_at was moved by a thumbnail
// capture are reset to the date of their newest content version, and rows the
// capture never reached are left alone.
func TestMigration124_RepairsThumbnailStampedUpdatedAt_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()

	// Only the two tables the migration reads. The full chain needs the
	// pgvector extension, which this image does not ship.
	_, err := db.Exec(`
		CREATE TABLE portal_assets (
			id         TEXT        PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE portal_asset_versions (
			id         TEXT        PRIMARY KEY,
			asset_id   TEXT        NOT NULL REFERENCES portal_assets(id),
			version    INT         NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		)`)
	require.NoError(t, err)

	const (
		march  = "2026-03-10T19:59:55Z" // the real content date
		june   = "2026-06-01T12:00:00Z"
		august = "2026-08-22T09:14:00Z" // the day a capture pass ran
	)

	_, err = db.Exec(`
		INSERT INTO portal_assets (id, created_at, updated_at) VALUES
			('stamped',    $1, $3),
			('untouched',  $1, $1),
			('revised',    $1, $2),
			('no_version', $1, $3)`, march, june, august)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO portal_asset_versions (id, asset_id, version, created_at) VALUES
			('stamped-v1',   'stamped',   1, $1),
			('untouched-v1', 'untouched', 1, $1),
			('revised-v1',   'revised',   1, $1),
			('revised-v2',   'revised',   2, $2)`, march, june)
	require.NoError(t, err)

	updatedAt := func(id string) time.Time {
		var got time.Time
		require.NoError(t, db.QueryRow(
			`SELECT updated_at FROM portal_assets WHERE id = $1`, id).Scan(&got))
		return got.UTC()
	}
	mustParse := func(s string) time.Time {
		parsed, parseErr := time.Parse(time.RFC3339, s)
		require.NoError(t, parseErr)
		return parsed
	}

	execMigrationFile(t, db, "000124_portal_asset_updated_at_repair.up.sql")

	require.WithinDuration(t, mustParse(march), updatedAt("stamped"), time.Second,
		"a row carrying the capture pass's date reads as its last content date again")
	require.WithinDuration(t, mustParse(march), updatedAt("untouched"), time.Second,
		"a row the pass never reached is left where it is")
	require.WithinDuration(t, mustParse(june), updatedAt("revised"), time.Second,
		"the newest version is what a repaired row is dated by, not the first")
	require.WithinDuration(t, mustParse(august), updatedAt("no_version"), time.Second,
		"an asset with no version row has nothing to be repaired from")

	// Idempotent: after the repair no row is later than its newest version,
	// so a second run has nothing to match.
	execMigrationFile(t, db, "000124_portal_asset_updated_at_repair.up.sql")
	require.WithinDuration(t, mustParse(march), updatedAt("stamped"), time.Second)
	require.WithinDuration(t, mustParse(june), updatedAt("revised"), time.Second)

	// The down migration is a no-op by design: what the column held before the
	// repair was the artifact the repair removes, and nothing records it.
	execMigrationFile(t, db, "000124_portal_asset_updated_at_repair.down.sql")
	require.WithinDuration(t, mustParse(march), updatedAt("stamped"), time.Second)
}

// TestMigration138_ConsolidatesS3ToolsInPersonas_RealDB pins acceptance 3 of
// #1591: a DB-backed persona that named the eight S3 tools, or the verb globs
// that only ever matched them, is rewritten to s3_list and s3_object in both
// its allow and deny lists, duplicates collapse to the first position, every
// other entry keeps its place, and the down migration expands the two names
// back to the tools they stood for.
func TestMigration138_ConsolidatesS3ToolsInPersonas_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()

	// Only the table the migration rewrites. The full chain needs the
	// pgvector extension, which this image does not ship.
	_, err := db.Exec(`
		CREATE TABLE persona_definitions (
			name        TEXT  PRIMARY KEY,
			tools_allow JSONB NOT NULL DEFAULT '[]',
			tools_deny  JSONB NOT NULL DEFAULT '[]'
		)`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO persona_definitions (name, tools_allow, tools_deny) VALUES
			('reader',   '["search", "s3_list_buckets", "fetch", "s3_list_objects", "s3_get_object_metadata", "s3_get_object", "s3_presign_url"]', '["s3_put_object", "trino_execute", "s3_delete_*", "s3_copy_object"]'),
			('globs',    '["s3_list_*", "s3_get_*"]', '["s3_put_*", "s3_copy_*", "s3_presign_*"]'),
			('wildcard', '["*"]', '["s3_*", "apply_knowledge"]'),
			('unrelated','["trino_query"]', '[]')`)
	require.NoError(t, err)

	lists := func(name string) (allow, deny string) {
		require.NoError(t, db.QueryRow(
			`SELECT tools_allow::text, tools_deny::text FROM persona_definitions WHERE name = $1`, name).Scan(&allow, &deny))
		return allow, deny
	}

	execMigrationFile(t, db, "000138_consolidate_s3_tools_in_personas.up.sql")

	allow, deny := lists("reader")
	require.JSONEq(t, `["search", "s3_list", "fetch", "s3_object"]`, allow,
		"exact names collapse to the consolidated tool at the first position, everything else keeps its place")
	require.JSONEq(t, `["s3_object", "trino_execute"]`, deny,
		"a deny of a write tool or its glob is a deny of s3_object: the migration fails closed")

	allow, deny = lists("globs")
	require.JSONEq(t, `["s3_list", "s3_object"]`, allow, "the verb globs that only matched the old tools are rewritten")
	require.JSONEq(t, `["s3_object"]`, deny)

	allow, deny = lists("wildcard")
	require.JSONEq(t, `["*"]`, allow)
	require.JSONEq(t, `["s3_*", "apply_knowledge"]`, deny, "a broader glob still matches the new names and is untouched")

	allow, deny = lists("unrelated")
	require.JSONEq(t, `["trino_query"]`, allow)
	require.JSONEq(t, `[]`, deny)

	// Idempotent: a second run finds nothing to rewrite.
	execMigrationFile(t, db, "000138_consolidate_s3_tools_in_personas.up.sql")
	allow, deny = lists("reader")
	require.JSONEq(t, `["search", "s3_list", "fetch", "s3_object"]`, allow)
	require.JSONEq(t, `["s3_object", "trino_execute"]`, deny)

	execMigrationFile(t, db, "000138_consolidate_s3_tools_in_personas.down.sql")
	allow, deny = lists("reader")
	require.JSONEq(t, `["search", "s3_list_buckets", "s3_list_objects", "fetch", "s3_get_object", "s3_get_object_metadata", "s3_presign_url", "s3_put_object", "s3_copy_object", "s3_delete_object"]`, allow,
		"down expands each consolidated name to every tool it stood for, in place")
	require.JSONEq(t, `["s3_get_object", "s3_get_object_metadata", "s3_presign_url", "s3_put_object", "s3_copy_object", "s3_delete_object", "trino_execute"]`, deny)
	allow, deny = lists("unrelated")
	require.JSONEq(t, `["trino_query"]`, allow)
	require.JSONEq(t, `[]`, deny)
}

// TestMigration139_ConsolidatesAPIDiscoveryToolsInPersonas_RealDB pins
// acceptance 5 of #1592 for stored personas: a DB-backed persona that named
// api_list_specs, api_list_endpoints or api_get_endpoint_schema, or the verb
// globs that only ever matched them, is rewritten to api_discover in both its
// allow and deny lists, duplicates collapse to the first position, every other
// entry keeps its place, and the down migration expands the name back to the
// three tools it stood for.
func TestMigration139_ConsolidatesAPIDiscoveryToolsInPersonas_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()

	// Only the table the migration rewrites. The full chain needs the
	// pgvector extension, which this image does not ship.
	_, err := db.Exec(`
		CREATE TABLE persona_definitions (
			name        TEXT  PRIMARY KEY,
			tools_allow JSONB NOT NULL DEFAULT '[]',
			tools_deny  JSONB NOT NULL DEFAULT '[]'
		)`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO persona_definitions (name, tools_allow, tools_deny) VALUES
			('operator', '["search", "api_list_specs", "fetch", "api_list_endpoints", "api_get_endpoint_schema", "api_invoke_endpoint"]', '["api_get_endpoint_schema", "trino_execute"]'),
			('globs',    '["api_list_*", "api_get_*"]', '["api_list_*"]'),
			('wildcard', '["*"]', '["api_*", "apply_knowledge"]'),
			('unrelated','["trino_query", "api_export"]', '[]')`)
	require.NoError(t, err)

	lists := func(name string) (allow, deny string) {
		require.NoError(t, db.QueryRow(
			`SELECT tools_allow::text, tools_deny::text FROM persona_definitions WHERE name = $1`, name).Scan(&allow, &deny))
		return allow, deny
	}

	execMigrationFile(t, db, "000139_consolidate_api_discovery_tools_in_personas.up.sql")

	allow, deny := lists("operator")
	require.JSONEq(t, `["search", "api_discover", "fetch", "api_invoke_endpoint"]`, allow,
		"the three names collapse to api_discover at the first position, everything else keeps its place")
	require.JSONEq(t, `["api_discover", "trino_execute"]`, deny,
		"a deny of one depth is a deny of api_discover: the migration fails closed")

	allow, deny = lists("globs")
	require.JSONEq(t, `["api_discover"]`, allow, "the verb globs that only matched the old tools are rewritten")
	require.JSONEq(t, `["api_discover"]`, deny)

	allow, deny = lists("wildcard")
	require.JSONEq(t, `["*"]`, allow)
	require.JSONEq(t, `["api_*", "apply_knowledge"]`, deny, "a broader glob still matches the new name and is untouched")

	allow, deny = lists("unrelated")
	require.JSONEq(t, `["trino_query", "api_export"]`, allow)
	require.JSONEq(t, `[]`, deny)

	// Idempotent: a second run finds nothing to rewrite.
	execMigrationFile(t, db, "000139_consolidate_api_discovery_tools_in_personas.up.sql")
	allow, deny = lists("operator")
	require.JSONEq(t, `["search", "api_discover", "fetch", "api_invoke_endpoint"]`, allow)
	require.JSONEq(t, `["api_discover", "trino_execute"]`, deny)

	execMigrationFile(t, db, "000139_consolidate_api_discovery_tools_in_personas.down.sql")
	allow, deny = lists("operator")
	require.JSONEq(t, `["search", "api_list_specs", "api_list_endpoints", "api_get_endpoint_schema", "fetch", "api_invoke_endpoint"]`, allow,
		"down expands api_discover to the three tools it stood for, in place")
	require.JSONEq(t, `["api_list_specs", "api_list_endpoints", "api_get_endpoint_schema", "trino_execute"]`, deny)
	allow, deny = lists("unrelated")
	require.JSONEq(t, `["trino_query", "api_export"]`, allow)
	require.JSONEq(t, `[]`, deny)
}
