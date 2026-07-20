package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Prefs is one user's notification preferences. Absence of a stored row means
// DefaultPrefs applies (immediate delivery, all categories on), per the
// platform's important-features-default-on convention.
type Prefs struct {
	Email           string    `json:"email"`
	Mode            string    `json:"mode"`
	SharesEnabled   bool      `json:"shares_enabled"`
	CommentsEnabled bool      `json:"comments_enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DefaultPrefs returns the preferences applied to a user with no stored row.
func DefaultPrefs(email string) Prefs {
	return Prefs{
		Email:           email,
		Mode:            ModeImmediate,
		SharesEnabled:   true,
		CommentsEnabled: true,
	}
}

// PrefsUpdate carries the fields of a preferences write; nil fields keep the
// current (or default) value.
type PrefsUpdate struct {
	Mode            *string `json:"mode,omitempty"`
	SharesEnabled   *bool   `json:"shares_enabled,omitempty"`
	CommentsEnabled *bool   `json:"comments_enabled,omitempty"`
}

// ValidMode reports whether m is one of the delivery modes.
func ValidMode(m string) bool {
	return m == ModeOff || m == ModeImmediate || m == ModeDaily
}

// PrefsStore persists per-user notification preferences.
type PrefsStore interface {
	// Get returns the user's preferences, falling back to DefaultPrefs when
	// no row exists. It never returns ErrNotFound.
	Get(ctx context.Context, email string) (Prefs, error)
	// Set upserts the user's preferences, applying u over the current values.
	Set(ctx context.Context, email string, u PrefsUpdate) (Prefs, error)
}

// PostgresPrefsStore implements PrefsStore backed by user_notification_prefs.
type PostgresPrefsStore struct {
	db *sql.DB
}

// NewPostgresPrefsStore creates a PostgreSQL-backed preferences store.
func NewPostgresPrefsStore(db *sql.DB) *PostgresPrefsStore {
	return &PostgresPrefsStore{db: db}
}

// Get returns the stored preferences or DefaultPrefs when absent.
func (s *PostgresPrefsStore) Get(ctx context.Context, email string) (Prefs, error) {
	var p Prefs
	err := s.db.QueryRowContext(ctx,
		`SELECT email, mode, shares_enabled, comments_enabled, updated_at
		 FROM user_notification_prefs WHERE email = $1`, email).
		Scan(&p.Email, &p.Mode, &p.SharesEnabled, &p.CommentsEnabled, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPrefs(email), nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("querying notification prefs: %w", err)
	}
	return p, nil
}

// Set applies u over the user's current (or default) preferences and upserts
// the result.
func (s *PostgresPrefsStore) Set(ctx context.Context, email string, u PrefsUpdate) (Prefs, error) {
	current, err := s.Get(ctx, email)
	if err != nil {
		return Prefs{}, err
	}
	if u.Mode != nil {
		current.Mode = *u.Mode
	}
	if u.SharesEnabled != nil {
		current.SharesEnabled = *u.SharesEnabled
	}
	if u.CommentsEnabled != nil {
		current.CommentsEnabled = *u.CommentsEnabled
	}
	if !ValidMode(current.Mode) {
		return Prefs{}, fmt.Errorf("invalid notification mode %q", current.Mode)
	}

	err = s.db.QueryRowContext(ctx,
		`INSERT INTO user_notification_prefs (email, mode, shares_enabled, comments_enabled)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (email) DO UPDATE SET
		   mode = EXCLUDED.mode,
		   shares_enabled = EXCLUDED.shares_enabled,
		   comments_enabled = EXCLUDED.comments_enabled,
		   updated_at = NOW()
		 RETURNING updated_at`,
		email, current.Mode, current.SharesEnabled, current.CommentsEnabled).
		Scan(&current.UpdatedAt)
	if err != nil {
		return Prefs{}, fmt.Errorf("storing notification prefs: %w", err)
	}
	current.Email = email
	return current, nil
}

// Verify interface compliance.
var _ PrefsStore = (*PostgresPrefsStore)(nil)
