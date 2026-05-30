//go:build integration

package migrate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx, "postgres:15",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
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
		require.Equal(t, uint(2), version)
	})

	// Test Run is idempotent
	t.Run("Run is idempotent", func(t *testing.T) {
		err := Run(db)
		require.NoError(t, err)

		version, dirty, err := Version(db)
		require.NoError(t, err)
		require.False(t, dirty)
		require.Equal(t, uint(2), version)
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
func TestMigration050_UnifyOAuthRoundTrip(t *testing.T) {
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
