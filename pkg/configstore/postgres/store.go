// Package postgres provides a PostgreSQL-backed config store with versioning.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// Store persists configuration versions in PostgreSQL.
type Store struct {
	db *sql.DB
}

// New creates a new PostgresStore.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Load returns the active configuration as YAML bytes, or nil if no config exists yet (first boot).
func (s *Store) Load(ctx context.Context) ([]byte, error) {
	var yamlText string
	err := s.db.QueryRowContext(ctx,
		`SELECT config_yaml FROM config_versions WHERE is_active = TRUE`,
	).Scan(&yamlText)
	if err == sql.ErrNoRows {
		return nil, nil //nolint:nilnil // nil data means first boot
	}
	if err != nil {
		return nil, fmt.Errorf("loading active config: %w", err)
	}

	return []byte(yamlText), nil
}

// Save persists a new configuration version, deactivating the previous one.
func (s *Store) Save(ctx context.Context, data []byte, meta configstore.SaveMeta) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Get next version number
	var nextVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM config_versions`,
	).Scan(&nextVersion)
	if err != nil {
		return fmt.Errorf("getting next version: %w", err)
	}

	// Deactivate current active config
	_, err = tx.ExecContext(ctx,
		`UPDATE config_versions SET is_active = FALSE WHERE is_active = TRUE`,
	)
	if err != nil {
		return fmt.Errorf("deactivating current config: %w", err)
	}

	// Insert new active config
	_, err = tx.ExecContext(ctx,
		`INSERT INTO config_versions (version, config_yaml, author, comment, is_active)
		 VALUES ($1, $2, $3, $4, TRUE)`,
		nextVersion, string(data), meta.Author, meta.Comment,
	)
	if err != nil {
		return fmt.Errorf("inserting config version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing config version: %w", err)
	}
	return nil
}

// History returns recent configuration revisions, newest first.
func (s *Store) History(ctx context.Context, limit int) ([]configstore.Revision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, version, author, comment, created_at
		 FROM config_versions
		 ORDER BY created_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying config history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var revisions []configstore.Revision
	for rows.Next() {
		var r configstore.Revision
		if err := rows.Scan(&r.ID, &r.Version, &r.Author, &r.Comment, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning revision: %w", err)
		}
		revisions = append(revisions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating revisions: %w", err)
	}
	return revisions, nil
}

// Mode returns "database".
func (*Store) Mode() string {
	return "database"
}
