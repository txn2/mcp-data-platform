//go:build integration

package helpers

import (
	"database/sql"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// ResetSchemaAndMigrate returns the shared e2e database to a clean, fully
// migrated state.
//
// The docker init script (01_init.sql) creates a few tables with schemas that
// predate the golang-migrate migrations, and prior tests in the same `go test`
// run leave the full platform schema behind. Dropping only schema_migrations
// would make golang-migrate restart from version 0 and collide with tables that
// still exist (e.g. "column owner_email of relation portal_assets already
// exists"). Dropping and recreating the entire public schema guarantees
// migrate.Run applies the canonical schema from scratch regardless of what any
// earlier test left, and it drops the pgvector extension too — the migrations
// recreate it (CREATE EXTENSION IF NOT EXISTS vector).
func ResetSchemaAndMigrate(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec("DROP SCHEMA public CASCADE"); err != nil {
		t.Fatalf("dropping public schema: %v", err)
	}
	if _, err := db.Exec("CREATE SCHEMA public"); err != nil {
		t.Fatalf("recreating public schema: %v", err)
	}
	if err := migrate.Run(db); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
}
