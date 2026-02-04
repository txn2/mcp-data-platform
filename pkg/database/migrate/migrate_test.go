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
