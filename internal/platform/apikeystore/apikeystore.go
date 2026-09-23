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

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// ErrNotFound is returned when an API key does not exist in the database.
var ErrNotFound = errors.New("api key not found")

// ErrExists is returned when Create is asked for a name the database holds.
var ErrExists = errors.New("api key already exists")

// Definition represents a database-managed API key.
type Definition struct {
	Name        string     `json:"name"`
	KeyHash     string     `json:"key_hash"`
	Email       string     `json:"email,omitempty"`
	Description string     `json:"description,omitempty"`
	Roles       []string   `json:"roles"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// UserEmail is the account the key is issued against, or "" for the
	// standalone service key this table has always held. Empty Roles on a
	// bound key means it carries whatever roles that person holds (#1759).
	UserEmail string    `json:"user_email,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	// Attributes are the named values the key carries as claims (#1846).
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Store manages API key persistence. It is the record of which
// database-managed keys exist for every replica of a deployment, which is why
// it is also the auth.HashedKeySource the authenticator confirms keys against.
type Store interface {
	auth.HashedKeySource
	List(ctx context.Context) ([]Definition, error)
	// Create adds a key, returning ErrExists when the name is taken. It never
	// replaces a key: two replicas creating one name at once must not both
	// succeed with the second write discarding the first key.
	Create(ctx context.Context, def Definition) error
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
		`SELECT name, key_hash, email, description, roles, expires_at, created_by, created_at, user_email, attributes
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

// Create adds an API key definition, or returns ErrExists when the name is
// taken.
func (s *PostgresStore) Create(ctx context.Context, def Definition) error {
	roles, _ := json.Marshal(def.Roles)
	attributes, _ := json.Marshal(orEmptyAttributes(def.Attributes))

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys
		 (name, key_hash, email, description, roles, expires_at, created_by, created_at, user_email, attributes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), $8, $9)
		 ON CONFLICT (name) DO NOTHING`,
		def.Name, def.KeyHash, def.Email, def.Description,
		roles, def.ExpiresAt, def.CreatedBy, def.UserEmail, attributes,
	)
	if err != nil {
		return fmt.Errorf("inserting api key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking insert result: %w", err)
	}
	if affected == 0 {
		return ErrExists
	}
	return nil
}

// HashedKeys returns every stored key in the form the authenticator holds.
func (s *PostgresStore) HashedKeys(ctx context.Context) ([]auth.APIKey, error) {
	defs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	return authKeys(defs), nil
}

// HoldsKey reports whether the key named name is stored with keyHash.
func (s *PostgresStore) HoldsKey(ctx context.Context, name, keyHash string) (bool, error) {
	var held bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM api_keys WHERE name = $1 AND key_hash = $2)`,
		name, keyHash,
	).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("checking api key: %w", err)
	}
	return held, nil
}

// authKeys converts stored definitions to the keys the authenticator holds.
func authKeys(defs []Definition) []auth.APIKey {
	keys := make([]auth.APIKey, 0, len(defs))
	for _, d := range defs {
		keys = append(keys, auth.APIKey{
			KeyHash:     d.KeyHash,
			Name:        d.Name,
			Email:       d.Email,
			Description: d.Description,
			Roles:       d.Roles,
			ExpiresAt:   d.ExpiresAt,
			UserEmail:   d.UserEmail,
			Attributes:  d.Attributes,
		})
	}
	return keys
}

// orEmptyAttributes stores a key with no attributes as {}, never null.
func orEmptyAttributes(a map[string]string) map[string]string {
	if a == nil {
		return map[string]string{}
	}
	return a
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
	var roles, attributes []byte
	var expiresAt sql.NullTime
	if err := rows.Scan(&d.Name, &d.KeyHash, &d.Email, &d.Description,
		&roles, &expiresAt, &d.CreatedBy, &d.CreatedAt, &d.UserEmail, &attributes); err != nil {
		return d, fmt.Errorf("scanning api key: %w", err)
	}
	if err := json.Unmarshal(attributes, &d.Attributes); err != nil {
		return d, fmt.Errorf("unmarshaling api key attributes: %w", err)
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

// Create is a no-op.
func (*NoopStore) Create(_ context.Context, _ Definition) error { return nil }

// HashedKeys returns nil for the noop store.
func (*NoopStore) HashedKeys(_ context.Context) ([]auth.APIKey, error) {
	return nil, nil
}

// HoldsKey reports false: the noop store holds no key.
func (*NoopStore) HoldsKey(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

// Delete returns ErrNotFound for the noop store.
func (*NoopStore) Delete(_ context.Context, _ string) error {
	return ErrNotFound
}
