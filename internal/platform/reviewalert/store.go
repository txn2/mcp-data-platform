package reviewalert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SettingsStore is the configuration half of one queue's alert persistence:
// the operator's threshold, cooldown, and recipients, held as that queue's
// section of the platform_settings table. (The SMTP section has its own
// contract over the same table rather than one widened store serving both.)
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

// PostgresStore is one queue's PostgreSQL persistence: the operator's
// configuration (SettingsStore, here) and the re-alert marker (StateStore, in
// state.go). One store because they are one queue's state, always built
// together over the same pool; two interfaces because the admin API and the
// checker each need only their half.
//
// It is bound to a Target rather than to a hardcoded section and row, which is
// what lets a second review queue reuse this implementation instead of copying
// it.
type PostgresStore struct {
	db     *sql.DB
	target Target
}

// NewPostgresStore creates the PostgreSQL-backed alert store for one queue.
func NewPostgresStore(db *sql.DB, target Target) *PostgresStore {
	return &PostgresStore{db: db, target: target}
}

// Get returns the stored configuration.
func (s *PostgresStore) Get(ctx context.Context) (*Settings, error) {
	var raw []byte
	var updatedBy string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		s.target.SettingsSection).Scan(&raw, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying %s alert settings: %w", s.target.Queue, err)
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decoding %s alert settings: %w", s.target.Queue, err)
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
		return fmt.Errorf("encoding %s alert settings: %w", s.target.Queue, err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		s.target.SettingsSection, raw, author)
	if err != nil {
		return fmt.Errorf("storing %s alert settings: %w", s.target.Queue, err)
	}
	return nil
}

// SettingsOf returns the stored configuration, or the target's defaults when
// none has been written. Every caller wants this rather than the raw
// ErrNotFound: an operator who has never opened the settings page still gets
// the platform's default threshold, and the recipient list is what actually
// gates delivery.
func SettingsOf(ctx context.Context, store SettingsStore, target Target) (Settings, error) {
	settings, err := store.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		return target.DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading %s alert settings: %w", target.Queue, err)
	}
	return *settings, nil
}

// Verify interface compliance.
var _ SettingsStore = (*PostgresStore)(nil)
