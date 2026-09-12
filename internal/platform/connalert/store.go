package connalert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SettingsStore is the configuration half of this feature's persistence: the
// escalation window and its recipients, held as a section of the
// platform_settings table.
//
// It is a contract of its own because the admin settings surface needs nothing
// else — it writes the configuration without being handed the open revocations.
type SettingsStore interface {
	// Get returns the stored configuration, or ErrNotFound when the alert has
	// never been configured.
	Get(ctx context.Context) (*Settings, error)
	// Set upserts the configuration.
	Set(ctx context.Context, s Settings, author string) error
}

// AlertStore is the open-revocation half: which connections have lost their
// credential and which of those have been escalated.
type AlertStore interface {
	// Open records a revocation and reports whether this call is the one that
	// recorded it. It loses when the connection already has an open
	// revocation, which is the whole de-duplication mechanism: a connection
	// that keeps being called is announced once rather than once per rejected
	// call.
	Open(ctx context.Context, a Alert) (bool, error)
	// Clear forgets a connection's open revocation. Called when the connection
	// is authorized again, which is what makes a later revocation news.
	Clear(ctx context.Context, kind, name string) error
	// ClaimEscalations stamps and returns every revocation older than window
	// that has not been escalated and whose connection is still unauthorized.
	// Stamping and selecting are one statement, so two replicas sweeping at
	// once cannot both claim a row.
	ClaimEscalations(ctx context.Context, window time.Duration, now time.Time) ([]Alert, error)
}

// PostgresStore is this feature's PostgreSQL persistence: the operator's
// configuration (SettingsStore) and the open revocations (AlertStore). One
// store because they are one feature's state, always built over the same pool;
// two interfaces because the admin API and the sweep each need only their half.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates the PostgreSQL-backed store.
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

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
		return nil, fmt.Errorf("querying connection alert settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decoding connection alert settings: %w", err)
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
		return fmt.Errorf("encoding connection alert settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		SettingsSection, raw, author)
	if err != nil {
		return fmt.Errorf("storing connection alert settings: %w", err)
	}
	return nil
}

// openSQL records a revocation only when the connection has none outstanding.
// The conflict clause is the de-duplication: a connection whose credential is
// already known to be gone is not announced again, and the original
// revoked_at is kept so the escalation window runs from the first failure
// rather than being pushed back by every later one.
const openSQL = `
INSERT INTO connection_auth_alerts
       (connection_kind, connection_name, authorized_by, idp_host, reason, revoked_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (connection_kind, connection_name) DO NOTHING`

// Open records a revocation, reporting whether it was this call that recorded
// it.
func (s *PostgresStore) Open(ctx context.Context, a Alert) (bool, error) {
	res, err := s.db.ExecContext(ctx, openSQL,
		a.Kind, a.Name, a.AuthorizedBy, a.IDPHost, a.Reason, a.RevokedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("recording a connection revocation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("reading the connection revocation write: %w", err)
	}
	return affected > 0, nil
}

// Clear forgets a connection's open revocation.
func (s *PostgresStore) Clear(ctx context.Context, kind, name string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM connection_auth_alerts
		  WHERE connection_kind = $1 AND connection_name = $2`, kind, name)
	if err != nil {
		return fmt.Errorf("clearing a connection revocation: %w", err)
	}
	return nil
}

// claimEscalationsSQL stamps every due, unescalated revocation and returns what
// it stamped. One statement, so the stamp is the claim: a second replica
// sweeping the same instant finds the rows already stamped and returns none.
//
// The NOT EXISTS is the truth check. Clearing on reauthorization is what
// normally removes a row, and this asks the credential table directly rather
// than trusting that it happened: a connection holding a token is authorized,
// whatever this table still says, and telling a room of people to go and
// reconnect something that already works is worse than saying nothing.
const claimEscalationsSQL = `
UPDATE connection_auth_alerts a
   SET escalated_at = $1
 WHERE a.escalated_at IS NULL
   AND a.revoked_at <= $2
   AND NOT EXISTS (
         SELECT 1
           FROM connection_oauth_tokens t
          WHERE t.connection_kind = a.connection_kind
            AND t.connection_name = a.connection_name)
RETURNING connection_kind, connection_name, authorized_by, idp_host, reason, revoked_at`

// ClaimEscalations stamps and returns the revocations due for escalation.
func (s *PostgresStore) ClaimEscalations(ctx context.Context, window time.Duration, now time.Time) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, claimEscalationsSQL, now.UTC(), now.UTC().Add(-window))
	if err != nil {
		return nil, fmt.Errorf("claiming connection revocation escalations: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read errors surface through rows.Err
	var out []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.Kind, &a.Name, &a.AuthorizedBy, &a.IDPHost, &a.Reason, &a.RevokedAt); err != nil {
			return nil, fmt.Errorf("reading a claimed connection revocation: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the claimed connection revocations: %w", err)
	}
	return out, nil
}

// SettingsOf returns the stored configuration, or the defaults when none has
// been written. Every caller wants this rather than the raw ErrNotFound: an
// operator who has never opened the settings page still gets the alert, and the
// recipient list is what gates the escalation.
func SettingsOf(ctx context.Context, store SettingsStore) (Settings, error) {
	settings, err := store.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading connection alert settings: %w", err)
	}
	return *settings, nil
}

// Verify interface compliance.
var (
	_ SettingsStore = (*PostgresStore)(nil)
	_ AlertStore    = (*PostgresStore)(nil)
)
