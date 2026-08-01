package reviewalert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SettingsStore is the configuration half of the alert's persistence: the
// operator's threshold, cooldown, and recipients, held as the
// review_queue_alert section of the platform_settings table. (The SMTP
// section has its own contract over the same table rather than one widened
// store serving both.)
//
// It is a contract of its own because the admin API needs nothing else: the
// settings surface can write the configuration without being handed the
// checker's claim.
type SettingsStore interface {
	// Get returns the stored configuration, or ErrNotFound when the alert has
	// never been configured.
	Get(ctx context.Context) (*Settings, error)
	// Set upserts the configuration.
	Set(ctx context.Context, s Settings, author string) error
}

// PostgresStore is the alert's PostgreSQL persistence: the operator's
// configuration (SettingsStore, here) and the re-alert marker (StateStore, in
// state.go). One store because they are one subsystem's state, always built
// together over the same pool; two interfaces because the admin API and the
// checker each need only their half.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates the PostgreSQL-backed alert store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Get returns the stored configuration.
func (s *PostgresStore) Get(ctx context.Context) (*Settings, error) {
	var raw []byte
	var updatedBy string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		SettingsSection).Scan(&raw, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying review queue alert settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decoding review queue alert settings: %w", err)
	}
	settings.UpdatedBy = updatedBy
	settings.UpdatedAt = updatedAt
	return &settings, nil
}

// Set upserts the configuration. The audit columns carry the author and the
// time; Settings marks its copies of them json:"-", so the section value holds
// the configuration alone.
func (s *PostgresStore) Set(ctx context.Context, in Settings, author string) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding review queue alert settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		SettingsSection, raw, author)
	if err != nil {
		return fmt.Errorf("storing review queue alert settings: %w", err)
	}
	return nil
}

// SettingsOf returns the stored configuration, or the defaults when none has
// been written. Every caller wants this rather than the raw ErrNotFound: an
// operator who has never opened the settings page still gets the platform's
// default threshold, and the recipient list is what actually gates delivery.
func SettingsOf(ctx context.Context, store SettingsStore) (Settings, error) {
	settings, err := store.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading review queue alert settings: %w", err)
	}
	return *settings, nil
}

// Verify interface compliance.
var _ SettingsStore = (*PostgresStore)(nil)
