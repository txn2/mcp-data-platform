// Package postgres provides a PostgreSQL-backed granular config entry store.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// Store persists config entries in PostgreSQL with change auditing.
type Store struct {
	db *sql.DB
}

// New creates a new PostgreSQL config entry store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns a single config entry by key.
func (s *Store) Get(ctx context.Context, key string) (*configstore.Entry, error) {
	var e configstore.Entry
	err := s.db.QueryRowContext(ctx,
		`SELECT key, value_text, updated_by, updated_at FROM config_entries WHERE key = $1`,
		key,
	).Scan(&e.Key, &e.Value, &e.UpdatedBy, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, configstore.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying config entry: %w", err)
	}
	return &e, nil
}

// Set creates or updates a config entry and logs the change atomically.
func (s *Store) Set(ctx context.Context, key, value, author string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config_entries (key, value_text, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET value_text = $2, updated_by = $3, updated_at = $4`,
		key, value, author, now,
	); err != nil {
		return fmt.Errorf("upserting config entry: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config_changelog (key, action, value_text, changed_by, changed_at)
		 VALUES ($1, 'set', $2, $3, $4)`,
		key, value, author, now,
	); err != nil {
		return fmt.Errorf("logging config change: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing config set: %w", err)
	}
	return nil
}

// Delete removes a config entry and logs the change atomically.
func (s *Store) Delete(ctx context.Context, key, author string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	result, err := tx.ExecContext(ctx,
		`DELETE FROM config_entries WHERE key = $1`,
		key,
	)
	if err != nil {
		return fmt.Errorf("deleting config entry: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking delete result: %w", err)
	}
	if affected == 0 {
		return configstore.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config_changelog (key, action, changed_by, changed_at)
		 VALUES ($1, 'delete', $2, $3)`,
		key, author, time.Now(),
	); err != nil {
		return fmt.Errorf("logging config delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing config delete: %w", err)
	}
	return nil
}

// List returns all config entries, ordered by key.
func (s *Store) List(ctx context.Context) ([]configstore.Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value_text, updated_by, updated_at FROM config_entries ORDER BY key`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying config entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var entries []configstore.Entry
	for rows.Next() {
		var e configstore.Entry
		if err := rows.Scan(&e.Key, &e.Value, &e.UpdatedBy, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning config entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating config entries: %w", err)
	}
	return entries, nil
}

// Changelog returns recent config changes, newest first.
func (s *Store) Changelog(ctx context.Context, limit int) ([]configstore.ChangelogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key, action, value_text, changed_by, changed_at
		 FROM config_changelog
		 ORDER BY changed_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying config changelog: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var entries []configstore.ChangelogEntry
	for rows.Next() {
		var e configstore.ChangelogEntry
		var value sql.NullString
		if err := rows.Scan(&e.ID, &e.Key, &e.Action, &value, &e.ChangedBy, &e.ChangedAt); err != nil {
			return nil, fmt.Errorf("scanning changelog entry: %w", err)
		}
		if value.Valid {
			e.Value = &value.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating changelog entries: %w", err)
	}
	return entries, nil
}

// Mode returns "database".
func (*Store) Mode() string {
	return "database"
}
