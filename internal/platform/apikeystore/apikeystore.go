// Package apikeystore persists the database-managed API keys the platform
// loads alongside the keys declared in configuration.
//
// It is a facade-internal seam: pkg/platform constructs it and hands the
// resulting Store to the admin API through its own accessor, so the store has
// exactly two first-party callers and no business living on the module's
// supported import surface (docs/library/stability.md). pkg/platform keeps
// aliases for the two types the admin handler names, so the facade's published
// contract is unchanged by the move.
package apikeystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when an API key does not exist in the database.
var ErrNotFound = errors.New("api key not found")

// Definition represents a database-managed API key.
type Definition struct {
	Name        string     `json:"name"`
	KeyHash     string     `json:"key_hash"`
	Email       string     `json:"email,omitempty"`
	Description string     `json:"description,omitempty"`
	Roles       []string   `json:"roles"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Store manages API key persistence.
type Store interface {
	List(ctx context.Context) ([]Definition, error)
	Set(ctx context.Context, def Definition) error
	Delete(ctx context.Context, name string) error
}

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgres creates a new PostgreSQL-backed API key store.
func NewPostgres(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// List returns all API key definitions.
func (s *PostgresStore) List(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, key_hash, email, description, roles, expires_at, created_by, created_at
		 FROM api_keys ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing api keys: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var defs []Definition
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating api keys: %w", err)
	}
	return defs, nil
}

// Set creates or updates an API key definition.
func (s *PostgresStore) Set(ctx context.Context, def Definition) error {
	roles, _ := json.Marshal(def.Roles)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys
		 (name, key_hash, email, description, roles, expires_at, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		 ON CONFLICT (name) DO UPDATE SET
		  key_hash = $2, email = $3, description = $4, roles = $5,
		  expires_at = $6, created_by = $7`,
		def.Name, def.KeyHash, def.Email, def.Description,
		roles, def.ExpiresAt, def.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("upserting api key: %w", err)
	}
	return nil
}

// Delete removes an API key definition by name.
func (s *PostgresStore) Delete(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM api_keys WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("deleting api key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking delete result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// scanDefinition scans a row into an Definition.
func scanDefinition(rows *sql.Rows) (Definition, error) {
	var d Definition
	var roles []byte
	var expiresAt sql.NullTime
	if err := rows.Scan(&d.Name, &d.KeyHash, &d.Email, &d.Description,
		&roles, &expiresAt, &d.CreatedBy, &d.CreatedAt); err != nil {
		return d, fmt.Errorf("scanning api key: %w", err)
	}
	if expiresAt.Valid {
		d.ExpiresAt = &expiresAt.Time
	}
	if err := json.Unmarshal(roles, &d.Roles); err != nil {
		return d, fmt.Errorf("unmarshaling api key roles: %w", err)
	}
	return d, nil
}

// NoopStore is a no-op implementation for when no database is available.
type NoopStore struct{}

// List returns nil for the noop store.
func (*NoopStore) List(_ context.Context) ([]Definition, error) {
	return nil, nil
}

// Set is a no-op.
func (*NoopStore) Set(_ context.Context, _ Definition) error { return nil }

// Delete returns ErrNotFound for the noop store.
func (*NoopStore) Delete(_ context.Context, _ string) error {
	return ErrNotFound
}
