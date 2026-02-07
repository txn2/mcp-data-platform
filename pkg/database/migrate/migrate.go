// Package migrate provides database migration support using golang-migrate.
package migrate

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrator defines the interface for database migrations.
// This allows for mocking in tests.
type migrator interface {
	Up() error
	Down() error
	Steps(n int) error
	Version() (uint, bool, error)
}

// migratorFactory creates a migrator instance.
// This is overridden in tests to return a mock.
var migratorFactory = newMigrator

// newMigrator creates a real migrate.Migrate instance.
func newMigrator(db *sql.DB) (migrator, error) {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, fmt.Errorf("creating postgres driver: %w", err)
	}

	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("creating migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return nil, fmt.Errorf("creating migrator: %w", err)
	}

	return m, nil
}

// Run executes all pending database migrations.
// It applies migrations in order and is idempotent - already applied migrations are skipped.
func Run(db *sql.DB) error {
	m, err := migratorFactory(db)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("running migrations: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("getting migration version: %w", err)
	}

	if dirty {
		slog.Warn("database migration state is dirty", "version", version)
	} else {
		slog.Info("database migrations complete", "version", version)
	}

	return nil
}

// Version returns the current migration version.
func Version(db *sql.DB) (version uint, dirty bool, err error) {
	m, err := migratorFactory(db)
	if err != nil {
		return 0, false, err
	}

	version, dirty, err = m.Version()
	if err != nil {
		return 0, false, fmt.Errorf("getting migration version: %w", err)
	}
	return version, dirty, nil
}

// Down rolls back all migrations.
// Use with caution - this will destroy all data.
func Down(db *sql.DB) error {
	m, err := migratorFactory(db)
	if err != nil {
		return err
	}

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("rolling back migrations: %w", err)
	}

	return nil
}

// Steps applies n migrations (positive = up, negative = down).
func Steps(db *sql.DB, n int) error {
	m, err := migratorFactory(db)
	if err != nil {
		return err
	}

	if err := m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("stepping migrations: %w", err)
	}

	return nil
}
