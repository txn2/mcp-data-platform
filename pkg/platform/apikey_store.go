package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrAPIKeyNotFound is returned when an API key does not exist in the database.
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKeyDefinition represents a database-managed API key.
type APIKeyDefinition struct {
	Name        string     `json:"name"`
	KeyHash     string     `json:"key_hash"`
	Email       string     `json:"email,omitempty"`
	Description string     `json:"description,omitempty"`
	Roles       []string   `json:"roles"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

// APIKeyStore manages API key persistence.
type APIKeyStore interface {
	List(ctx context.Context) ([]APIKeyDefinition, error)
	Set(ctx context.Context, def APIKeyDefinition) error
	Delete(ctx context.Context, name string) error
}

// PostgresAPIKeyStore implements APIKeyStore backed by PostgreSQL.
type PostgresAPIKeyStore struct {
	db *sql.DB
}

// NewPostgresAPIKeyStore creates a new PostgreSQL-backed API key store.
func NewPostgresAPIKeyStore(db *sql.DB) *PostgresAPIKeyStore {
	return &PostgresAPIKeyStore{db: db}
}

// List returns all API key definitions.
func (s *PostgresAPIKeyStore) List(ctx context.Context) ([]APIKeyDefinition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, key_hash, email, description, roles, expires_at, created_by, created_at
		 FROM api_keys ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing api keys: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var defs []APIKeyDefinition
	for rows.Next() {
		d, err := scanAPIKeyDef(rows)
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
func (s *PostgresAPIKeyStore) Set(ctx context.Context, def APIKeyDefinition) error {
	roles, _ := json.Marshal(def.Roles)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys
		 (name, key_hash, email, description, roles, expires_at, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (name) DO UPDATE SET
		  key_hash = $2, email = $3, description = $4, roles = $5,
		  expires_at = $6, created_by = $7, created_at = $8`,
		def.Name, def.KeyHash, def.Email, def.Description,
		roles, def.ExpiresAt, def.CreatedBy, def.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting api key: %w", err)
	}
	return nil
}

// Delete removes an API key definition by name.
func (s *PostgresAPIKeyStore) Delete(ctx context.Context, name string) error {
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
		return ErrAPIKeyNotFound
	}
	return nil
}

// scanAPIKeyDef scans a row into an APIKeyDefinition.
func scanAPIKeyDef(rows *sql.Rows) (APIKeyDefinition, error) {
	var d APIKeyDefinition
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

// NoopAPIKeyStore is a no-op implementation for when no database is available.
type NoopAPIKeyStore struct{}

// List returns nil for the noop store.
func (*NoopAPIKeyStore) List(_ context.Context) ([]APIKeyDefinition, error) {
	return nil, nil
}

// Set is a no-op.
func (*NoopAPIKeyStore) Set(_ context.Context, _ APIKeyDefinition) error { return nil }

// Delete returns ErrAPIKeyNotFound for the noop store.
func (*NoopAPIKeyStore) Delete(_ context.Context, _ string) error {
	return ErrAPIKeyNotFound
}
