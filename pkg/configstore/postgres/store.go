// Package postgres provides a PostgreSQL-backed granular config entry store.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/opsobs"
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
	ctx, op := opsobs.Start(ctx, opsobs.OpConfigStoreRead)
	e, err := s.readEntry(ctx, key)
	op.End(ctx, notFoundIsAnswer(err))
	return e, err
}

// readEntry is the read Get counts.
func (s *Store) readEntry(ctx context.Context, key string) (*configstore.Entry, error) {
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
	ctx, op := opsobs.Start(ctx, opsobs.OpConfigStoreWrite)
	err := s.writeEntry(ctx, key, value, author)
	op.End(ctx, err)
	logWrite(ctx, "set", key, author, err)
	return err
}

// writeEntry is the write Set counts.
func (s *Store) writeEntry(ctx context.Context, key, value, author string) error {
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
	ctx, op := opsobs.Start(ctx, opsobs.OpConfigStoreWrite)
	err := s.removeEntry(ctx, key, author)
	op.End(ctx, notFoundIsAnswer(err))
	// Deleting a key that is not set changed nothing and failed at nothing:
	// the metric reads it as an answer, and the log stays quiet the same way.
	if !errors.Is(err, configstore.ErrNotFound) {
		logWrite(ctx, "delete", key, author, err)
	}
	return err
}

// removeEntry is the write Delete counts.
func (s *Store) removeEntry(ctx context.Context, key, author string) error {
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
	ctx, op := opsobs.Start(ctx, opsobs.OpConfigStoreRead)
	entries, err := s.listEntries(ctx)
	op.End(ctx, err)
	return entries, err
}

// listEntries is the read List counts.
func (s *Store) listEntries(ctx context.Context) ([]configstore.Entry, error) {
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

// Changelog returns a page of config changes, newest first: at most `limit`
// entries starting at `offset`, plus the total number recorded so callers can
// page through the full history.
func (s *Store) Changelog(ctx context.Context, limit, offset int) ([]configstore.ChangelogEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_changelog`,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting config changelog: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key, action, value_text, changed_by, changed_at
		 FROM config_changelog
		 ORDER BY changed_at DESC
		 LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("querying config changelog: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var entries []configstore.ChangelogEntry
	for rows.Next() {
		var e configstore.ChangelogEntry
		var value sql.NullString
		if err := rows.Scan(&e.ID, &e.Key, &e.Action, &value, &e.ChangedBy, &e.ChangedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning changelog entry: %w", err)
		}
		if value.Valid {
			e.Value = &value.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating changelog entries: %w", err)
	}
	return entries, total, nil
}

// Mode returns "database".
func (*Store) Mode() string {
	return "database"
}

// notFoundIsAnswer is err with configstore.ErrNotFound read as a successful
// answer: a key that is not set is not a failing store.
func notFoundIsAnswer(err error) error {
	if errors.Is(err, configstore.ErrNotFound) {
		return nil
	}
	return err
}

// logWrite records a config entry change. The config store had no log line
// of its own (#1898); the changelog table holds the value, the log names who
// changed which key and whether it took.
func logWrite(ctx context.Context, action, key, author string, err error) {
	if err != nil {
		slog.WarnContext(ctx, "config store: write failed", "action", action,
			"key", logsan.SanitizeForLog(key), "author", logsan.SanitizeForLog(author),
			"error", logsan.SanitizeForLog(err.Error()))
		return
	}
	slog.InfoContext(ctx, "config store: entry changed", "action", action,
		"key", logsan.SanitizeForLog(key), "author", logsan.SanitizeForLog(author))
}
