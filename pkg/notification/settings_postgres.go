package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PostgresSettingsStore implements SettingsStore backed by the
// platform_settings table. Secrets inside a section value are encrypted with
// the injected StringEncryptor before they reach the database.
type PostgresSettingsStore struct {
	db  *sql.DB
	enc StringEncryptor
}

// NewPostgresSettingsStore creates a PostgreSQL-backed settings store. The
// encryptor may be nil-safe (encryption disabled) but must not be nil.
func NewPostgresSettingsStore(db *sql.DB, enc StringEncryptor) *PostgresSettingsStore {
	return &PostgresSettingsStore{db: db, enc: enc}
}

// GetSMTP returns the stored SMTP settings with the password decrypted.
func (s *PostgresSettingsStore) GetSMTP(ctx context.Context) (*SMTPSettings, error) {
	settings, err := s.readSMTP(ctx)
	if err != nil {
		return nil, err
	}
	password, err := s.enc.Decrypt(settings.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypting smtp password: %w", err)
	}
	settings.Password = password
	return settings, nil
}

// readSMTP loads the raw SMTP row without decrypting the password.
func (s *PostgresSettingsStore) readSMTP(ctx context.Context) (*SMTPSettings, error) {
	var raw []byte
	var updatedBy string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		SettingsSectionSMTP).Scan(&raw, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying smtp settings: %w", err)
	}
	var settings SMTPSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decoding smtp settings: %w", err)
	}
	settings.UpdatedBy = updatedBy
	settings.UpdatedAt = updatedAt
	return &settings, nil
}

// SetSMTP upserts the SMTP settings. An empty incoming password keeps the
// previously stored (still encrypted) one so the admin UI never round-trips
// the secret.
func (s *PostgresSettingsStore) SetSMTP(ctx context.Context, in SMTPSettings, author string) error {
	stored, err := s.encryptedPassword(ctx, in.Password)
	if err != nil {
		return err
	}
	in.Password = stored
	// The audit columns are authoritative; keep stale copies out of the JSON.
	in.UpdatedBy = ""
	in.UpdatedAt = time.Time{}

	raw, err := json.Marshal(in) // #nosec G117 -- Password is ciphertext here: encryptedPassword ran above
	if err != nil {
		return fmt.Errorf("encoding smtp settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		SettingsSectionSMTP, raw, author)
	if err != nil {
		return fmt.Errorf("storing smtp settings: %w", err)
	}
	return nil
}

// encryptedPassword resolves the password to store: the encrypted incoming
// value when one was provided, otherwise the existing stored ciphertext.
func (s *PostgresSettingsStore) encryptedPassword(ctx context.Context, incoming string) (string, error) {
	if incoming != "" {
		encrypted, err := s.enc.Encrypt(incoming)
		if err != nil {
			return "", fmt.Errorf("encrypting smtp password: %w", err)
		}
		return encrypted, nil
	}
	existing, err := s.readSMTP(ctx)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return existing.Password, nil
}

// Verify interface compliance.
var _ SettingsStore = (*PostgresSettingsStore)(nil)
