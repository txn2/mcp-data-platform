package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrConnectionNotFound is returned when a connection instance does not exist.
var ErrConnectionNotFound = errors.New("connection instance not found")

// ConnectionInstance represents a database-managed toolkit backend connection.
type ConnectionInstance struct {
	Kind        string         `json:"kind" example:"trino"`
	Name        string         `json:"name" example:"acme-warehouse"`
	Config      map[string]any `json:"config"`
	Description string         `json:"description" example:"Production data warehouse"`
	CreatedBy   string         `json:"created_by" example:"admin@example.com"`
	UpdatedAt   time.Time      `json:"updated_at" example:"2026-01-15T14:30:00Z"`
}

// ConnectionStore manages connection instance persistence.
type ConnectionStore interface {
	List(ctx context.Context) ([]ConnectionInstance, error)
	Get(ctx context.Context, kind, name string) (*ConnectionInstance, error)
	Set(ctx context.Context, inst ConnectionInstance) error
	Delete(ctx context.Context, kind, name string) error
}

// PostgresConnectionStore implements ConnectionStore backed by PostgreSQL.
// Sensitive config fields (password, token, secret_access_key, etc.) are
// encrypted at rest using AES-256-GCM when an encryption key is configured.
type PostgresConnectionStore struct {
	db        *sql.DB
	encryptor *FieldEncryptor
}

// NewPostgresConnectionStore creates a new PostgreSQL-backed connection store.
// The encryptor may be nil (encryption disabled — values stored in plain text).
func NewPostgresConnectionStore(db *sql.DB, encryptor *FieldEncryptor) *PostgresConnectionStore {
	return &PostgresConnectionStore{db: db, encryptor: encryptor}
}

// List returns all connection instances ordered by kind and name.
func (s *PostgresConnectionStore) List(ctx context.Context) ([]ConnectionInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, name, config, description, created_by, updated_at
		 FROM connection_instances ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("querying connection instances: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var instances []ConnectionInstance
	for rows.Next() {
		var inst ConnectionInstance
		var configBytes []byte
		if err := rows.Scan(&inst.Kind, &inst.Name, &configBytes,
			&inst.Description, &inst.CreatedBy, &inst.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning connection instance: %w", err)
		}
		if err := json.Unmarshal(configBytes, &inst.Config); err != nil {
			return nil, fmt.Errorf("unmarshaling connection config: %w", err)
		}
		if inst.Config, err = s.encryptor.DecryptSensitiveFields(inst.Config); err != nil {
			return nil, fmt.Errorf("decrypting connection %s/%s config: %w", inst.Kind, inst.Name, err)
		}
		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating connection instances: %w", err)
	}
	return instances, nil
}

// Get returns a single connection instance by kind and name.
func (s *PostgresConnectionStore) Get(ctx context.Context, kind, name string) (*ConnectionInstance, error) {
	var inst ConnectionInstance
	var configBytes []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, name, config, description, created_by, updated_at
		 FROM connection_instances WHERE kind = $1 AND name = $2`,
		kind, name,
	).Scan(&inst.Kind, &inst.Name, &configBytes,
		&inst.Description, &inst.CreatedBy, &inst.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying connection instance: %w", err)
	}
	if err := json.Unmarshal(configBytes, &inst.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling connection config: %w", err)
	}
	if inst.Config, err = s.encryptor.DecryptSensitiveFields(inst.Config); err != nil {
		return nil, fmt.Errorf("decrypting connection config: %w", err)
	}
	return &inst, nil
}

// Set creates or updates a connection instance.
// Sensitive config fields are encrypted before storage.
func (s *PostgresConnectionStore) Set(ctx context.Context, inst ConnectionInstance) error {
	configToStore, err := s.encryptor.EncryptSensitiveFields(inst.Config)
	if err != nil {
		return fmt.Errorf("encrypting sensitive fields: %w", err)
	}
	configBytes, err := json.Marshal(configToStore)
	if err != nil {
		return fmt.Errorf("marshaling connection config: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO connection_instances (kind, name, config, description, created_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (kind, name) DO UPDATE
		 SET config = $3, description = $4, created_by = $5, updated_at = $6`,
		inst.Kind, inst.Name, configBytes, inst.Description, inst.CreatedBy, inst.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting connection instance: %w", err)
	}
	return nil
}

// Delete removes a connection instance by kind and name.
func (s *PostgresConnectionStore) Delete(ctx context.Context, kind, name string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM connection_instances WHERE kind = $1 AND name = $2`,
		kind, name,
	)
	if err != nil {
		return fmt.Errorf("deleting connection instance: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking delete result: %w", err)
	}
	if affected == 0 {
		return ErrConnectionNotFound
	}
	return nil
}

// NoopConnectionStore is a no-op implementation for when no database is available.
type NoopConnectionStore struct{}

// List returns an empty slice.
func (*NoopConnectionStore) List(_ context.Context) ([]ConnectionInstance, error) {
	return nil, nil
}

// Get always returns ErrConnectionNotFound.
func (*NoopConnectionStore) Get(_ context.Context, _, _ string) (*ConnectionInstance, error) {
	return nil, ErrConnectionNotFound
}

// Set is a no-op.
func (*NoopConnectionStore) Set(_ context.Context, _ ConnectionInstance) error {
	return nil
}

// Delete always returns ErrConnectionNotFound.
func (*NoopConnectionStore) Delete(_ context.Context, _, _ string) error {
	return ErrConnectionNotFound
}
