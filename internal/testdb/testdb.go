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
//
// # Why there are two ways to get a database
//
// The harness has 163 call sites, and New is called per test rather than per
// package. Starting a container and replaying 113 migrations for each of them
// cost ~420s in the `verify` Docker lane and made it the slowest lane by a
// factor of eight.
//
// So when TESTDB_DSN names an already-running server, New carves a fresh
// database out of it with CREATE DATABASE ... TEMPLATE, which Postgres
// implements as a file copy of an already-migrated database. Isolation is
// unchanged: every test still gets its own database, with its own schema, that
// nothing else writes to. What is shared is one postgres process.
//
// `make test-realdb` sets that up (start one container, migrate the template,
// export the DSN). With the variable unset the harness falls back to a
// container per test, so a bare `go test -tags=integration ./...` still works
// with no orchestration.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq" // postgres driver for database/sql
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// EnvDSN names an already-running Postgres for the harness to carve per-test
// databases out of. EnvTemplate names the migrated database they are copied
// from; it defaults to DefaultTemplate. Both are set by `make test-realdb`.
const (
	EnvDSN      = "TESTDB_DSN"
	EnvTemplate = "TESTDB_TEMPLATE"

	// DefaultTemplate is the migrated database CREATE DATABASE copies from.
	DefaultTemplate = "testdb_template"
)

// dbSeq makes per-test database names unique within a process. Combined with
// the pid it is unique across the packages running concurrently against one
// server, which is what lets `go test -p N` share a single container.
var dbSeq atomic.Int64

// New returns a migrated, test-scoped database. Cleanup is registered on t.
// The test is skipped in -short mode.
func New(t *testing.T) *sql.DB {
	db, _ := NewWithDSN(t)
	return db
}

// NewWithDSN is New that also returns the connection string. Use it when a test
// needs to hand the DSN to a component that opens its own pool (for example
// platform.New, which wires stores only from config.Database.DSN). The returned
// *sql.DB is already migrated; a second opener re-runs the idempotent
// migrations against the same schema.
func NewWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real-DB integration test in short mode")
	}
	if dsn := os.Getenv(EnvDSN); dsn != "" {
		return fromSharedServer(t, dsn)
	}
	return fromOwnContainer(t)
}

// fromSharedServer clones the migrated template into a database of this test's
// own on the server TESTDB_DSN names.
func fromSharedServer(t *testing.T, adminDSN string) (*sql.DB, string) {
	t.Helper()
	template := os.Getenv(EnvTemplate)
	if template == "" {
		template = DefaultTemplate
	}
	name := fmt.Sprintf("testdb_%d_%d", os.Getpid(), dbSeq.Add(1))

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer admin.Close() //nolint:errcheck // best-effort close of a short-lived admin handle

	// Identifiers are process-generated (pid + counter) and the template comes
	// from the environment the Makefile sets, so neither is caller input; they
	// are quoted anyway because CREATE DATABASE takes no placeholders.
	if _, err := admin.Exec(fmt.Sprintf(
		`CREATE DATABASE %s TEMPLATE %s`, quoteIdent(name), quoteIdent(template),
	)); err != nil {
		t.Fatalf("create test database from template %q: %v\n"+
			"(%s names a server but its template is missing; `make test-realdb` creates it)",
			template, err, EnvDSN)
	}

	dsn := replaceDatabase(adminDSN, name)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	// Registered before the pool's own Close so it runs after it (t.Cleanup is
	// LIFO): DROP DATABASE is refused while a connection is open. FORCE covers
	// a pool a test handed to a component that outlives the assertion.
	t.Cleanup(func() {
		dropper, err := sql.Open("postgres", adminDSN)
		if err != nil {
			return
		}
		defer dropper.Close() //nolint:errcheck // best-effort cleanup
		_, _ = dropper.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, quoteIdent(name)))
	})
	t.Cleanup(func() { _ = db.Close() })

	return db, dsn
}

// fromOwnContainer is the no-orchestration path: one container for this test,
// migrated in place. Used when TESTDB_DSN is unset.
func fromOwnContainer(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()

	// The wait strategy requires BOTH the second "ready" log line (postgres
	// restarts once during init) AND the mapped port to be externally
	// reachable. The log line alone raced under parallel container load:
	// ConnectionString could run before Docker exposed the port mapping and
	// fail with `port "5432/tcp" not found`.
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithStartupTimeoutDefault(5*time.Minute),
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
	return db, dsn
}

// quoteIdent wraps a SQL identifier in double quotes, doubling any embedded
// quote. The names it is given are process-generated, so this guards the
// identifier grammar rather than a caller.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// replaceDatabase rewrites the database component of a postgres URL. The admin
// DSN and the per-test DSN differ only in that path segment, so the rest of the
// connection parameters (credentials, sslmode, port) carry over unchanged.
func replaceDatabase(dsn, name string) string {
	base, query, hasQuery := strings.Cut(dsn, "?")
	slash := strings.LastIndex(base, "/")
	if slash < 0 {
		return dsn
	}
	out := base[:slash+1] + name
	if hasQuery {
		out += "?" + query
	}
	return out
}
