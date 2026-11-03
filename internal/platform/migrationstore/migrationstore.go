// Package migrationstore keeps the migrations an application database's
// schema is built from, and their history, in the platform's database
// (#2404): the appdb_migrations and appdb_migration_events tables. The target
// database's own ledger records what was applied; these rows are the source
// of truth it is compared with.
package migrationstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Store is the PostgreSQL MigrationStore.
type Store struct {
	db *sql.DB
}

// New returns the store over the platform's database.
func New(db *sql.DB) *Store { return &Store{db: db} }

// List implements postgres.MigrationStore. An empty schema lists every
// schema of the connection, in schema order.
func (s *Store) List(ctx context.Context, connection, schema string) ([]postgres.Migration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT connection, schema_name, version, name, up_sql, down_sql, transactional,
		checksum, author, created_at, updated_at FROM appdb_migrations
		WHERE connection = $1 AND ($2 = '' OR schema_name = $2) ORDER BY schema_name, version`, connection, schema)
	if err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []postgres.Migration{}
	for rows.Next() {
		var m postgres.Migration
		if err := rows.Scan(&m.Connection, &m.Schema, &m.Version, &m.Name, &m.Up, &m.Down, &m.Transactional,
			&m.Checksum, &m.Author, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("reading a migration: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}
	return out, nil
}

// Put implements postgres.MigrationStore: it adds the migration, or replaces
// the one with its version, keeping when that one was first added.
func (s *Store) Put(ctx context.Context, m postgres.Migration) (bool, error) {
	var inserted bool
	err := s.db.QueryRowContext(ctx, `INSERT INTO appdb_migrations
		(connection, schema_name, version, name, up_sql, down_sql, transactional, checksum, author)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (connection, schema_name, version) DO UPDATE SET
			name = EXCLUDED.name, up_sql = EXCLUDED.up_sql, down_sql = EXCLUDED.down_sql,
			transactional = EXCLUDED.transactional, checksum = EXCLUDED.checksum,
			author = EXCLUDED.author, updated_at = NOW()
		RETURNING (xmax = 0)`,
		m.Connection, m.Schema, m.Version, m.Name, m.Up, m.Down, m.Transactional, m.Checksum, m.Author).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("saving migration %d: %w", m.Version, err)
	}
	return !inserted, nil
}

// Record implements postgres.MigrationStore.
func (s *Store) Record(ctx context.Context, e postgres.MigrationEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO appdb_migration_events
		(connection, schema_name, version, event, actor, checksum, duration_ms, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.Connection, e.Schema, e.Version, e.Event, e.Actor, e.Checksum, e.DurationMS, e.Detail)
	if err != nil {
		return fmt.Errorf("recording a migration event: %w", err)
	}
	return nil
}

// Events implements postgres.MigrationStore.
func (s *Store) Events(ctx context.Context, connection, schema string, limit int) ([]postgres.MigrationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT connection, schema_name, version, event, actor, checksum, duration_ms, detail, at
		FROM appdb_migration_events WHERE connection = $1 AND schema_name = $2
		ORDER BY at DESC, id DESC LIMIT $3`, connection, schema, limit)
	if err != nil {
		return nil, fmt.Errorf("listing migration events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []postgres.MigrationEvent{}
	for rows.Next() {
		var e postgres.MigrationEvent
		if err := rows.Scan(&e.Connection, &e.Schema, &e.Version, &e.Event, &e.Actor, &e.Checksum,
			&e.DurationMS, &e.Detail, &e.At); err != nil {
			return nil, fmt.Errorf("reading a migration event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing migration events: %w", err)
	}
	return out, nil
}

var _ postgres.MigrationStore = (*Store)(nil)
