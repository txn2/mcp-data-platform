// Package notifyprefs is the persistence layer for per-user notification
// preferences: the PostgreSQL implementation of notification.PrefsStore,
// backed by the user_notification_prefs table.
//
// The preference model itself (modes, categories, defaults) lives in
// pkg/notification, so the enqueue path and the HTTP surface share one
// vocabulary without depending on where the rows are kept.
package notifyprefs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// PostgresStore implements notification.PrefsStore backed by
// user_notification_prefs.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed preferences store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Get returns the stored preferences or notification.DefaultPrefs when absent.
func (s *PostgresStore) Get(ctx context.Context, email string) (notification.Prefs, error) {
	var p notification.Prefs
	err := s.db.QueryRowContext(ctx,
		`SELECT email, mode, shares_enabled, comments_enabled, mentions_enabled, updated_at
		 FROM user_notification_prefs WHERE email = $1`, email).
		Scan(&p.Email, &p.Mode, &p.SharesEnabled, &p.CommentsEnabled, &p.MentionsEnabled, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return notification.DefaultPrefs(email), nil
	}
	if err != nil {
		return notification.Prefs{}, fmt.Errorf("querying notification prefs: %w", err)
	}
	return p, nil
}

// Set applies u over the user's current (or default) preferences and upserts
// the result.
func (s *PostgresStore) Set(ctx context.Context, email string, u notification.PrefsUpdate) (notification.Prefs, error) {
	current, err := s.Get(ctx, email)
	if err != nil {
		return notification.Prefs{}, err
	}
	u.Apply(&current)
	if !notification.ValidMode(current.Mode) {
		return notification.Prefs{}, fmt.Errorf("invalid notification mode %q", current.Mode)
	}

	err = s.db.QueryRowContext(ctx,
		`INSERT INTO user_notification_prefs (email, mode, shares_enabled, comments_enabled, mentions_enabled)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (email) DO UPDATE SET
		   mode = EXCLUDED.mode,
		   shares_enabled = EXCLUDED.shares_enabled,
		   comments_enabled = EXCLUDED.comments_enabled,
		   mentions_enabled = EXCLUDED.mentions_enabled,
		   updated_at = NOW()
		 RETURNING updated_at`,
		email, current.Mode, current.SharesEnabled, current.CommentsEnabled, current.MentionsEnabled).
		Scan(&current.UpdatedAt)
	if err != nil {
		return notification.Prefs{}, fmt.Errorf("storing notification prefs: %w", err)
	}
	current.Email = email
	return current, nil
}

// Verify interface compliance.
var _ notification.PrefsStore = (*PostgresStore)(nil)
