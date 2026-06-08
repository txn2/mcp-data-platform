//go:build integration

// Package testdb provides a shared real-Postgres harness for integration tests.
//
// It exists because store write paths were historically tested only with
// sqlmock, which rubber-stamps SQL that real Postgres rejects (e.g. binding a
// nil slice via pq.Array into a NOT NULL column, error 23502). That class of
// defect shipped prompt creation broken to production. New tells a test
// container with the full embedded migration set applied, so write paths run
// against the actual schema (NOT NULL constraints, defaults, column types).
//
// The package is integration-tagged so it is invisible to the default build
// (and thus to dead-code analysis); it is consumed only by *RealDB* tests run
// under `make test-realdb`.
package testdb

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq" // postgres driver for database/sql
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// New starts a pgvector Postgres container, applies every embedded migration,
// and returns a connected *sql.DB. Container and connection cleanup are
// registered on t. The test is skipped in -short mode. The pgvector image is
// required because the migration set creates the `vector` extension (000031+).
func New(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real-DB integration test in short mode")
	}
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate.Run(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}
