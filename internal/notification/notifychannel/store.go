// Package notifychannel persists the operator's notification channels: the
// PostgreSQL store behind notification.ChannelStore.
//
// It sits beside notifyprefs and notifyqueue, and is the same kind of thing:
// one table, read and written through one contract. The transports a channel
// delivers over are internal/notification/notifypost, which this package does
// not import and does not know about.
package notifychannel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// ErrChannelNotFound is returned by Get for a name no channel is stored
// under, and by the worker when a queued row names a channel the operator has
// since deleted.
var ErrChannelNotFound = errors.New("notifychannel: channel not found")

// channelConfig is the kind-specific half of a channel, stored as the row's
// JSONB. The name, kind, enabled flag and authorship are columns; everything
// a kind needs to deliver is here, so adding a kind adds fields rather than
// columns.
type channelConfig struct {
	Description string   `json:"description,omitempty"`
	Connection  string   `json:"connection,omitempty"`
	Target      string   `json:"target,omitempty"`
	Recipients  []string `json:"recipients,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	// RepeatAfterSeconds and MaxPerHour are stored by this stage and
	// enforced by the suppression stage. They are stored as seconds rather
	// than as a Go duration string so the value in the row is readable by
	// anything that opens the database.
	RepeatAfterSeconds int64 `json:"repeat_after_seconds,omitempty"`
	MaxPerHour         int   `json:"max_per_hour,omitempty"`
}

// PostgresStore implements notification.ChannelStore over the
// notification_channels table.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed channel store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// List returns every channel in name order.
func (s *PostgresStore) List(ctx context.Context) ([]notification.Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, kind, config, enabled, created_by, updated_at
		   FROM notification_channels
		  ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing notification channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []notification.Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning notification channel: %w", err)
		}
		out = append(out, *ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating notification channels: %w", err)
	}
	return out, nil
}

// Get returns one channel, or ErrChannelNotFound.
func (s *PostgresStore) Get(ctx context.Context, name string) (*notification.Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, kind, config, enabled, created_by, updated_at
		   FROM notification_channels
		  WHERE name = $1`, name)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrChannelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading notification channel: %w", err)
	}
	return ch, nil
}

// Set creates or replaces a channel. created_by is preserved on an update:
// the administrator who created a channel stays recorded when a later one
// edits it, which is what the settings card reports.
func (s *PostgresStore) Set(ctx context.Context, ch notification.Channel) error {
	cfg, err := json.Marshal(channelConfig{
		Description:        ch.Description,
		Connection:         ch.Connection,
		Target:             ch.Target,
		Recipients:         ch.Recipients,
		Mode:               ch.Mode,
		RepeatAfterSeconds: int64(ch.RepeatAfter / time.Second),
		MaxPerHour:         ch.MaxPerHour,
	})
	if err != nil {
		return fmt.Errorf("encoding notification channel config: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO notification_channels (name, kind, config, enabled, created_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (name) DO UPDATE
		    SET kind = EXCLUDED.kind,
		        config = EXCLUDED.config,
		        enabled = EXCLUDED.enabled,
		        updated_at = NOW()`,
		ch.Name, ch.Kind, cfg, ch.Enabled, ch.CreatedBy)
	if err != nil {
		return fmt.Errorf("writing notification channel: %w", err)
	}
	return nil
}

// Delete removes a channel. Deleting an absent channel is not an error: the
// administrator asked for it to be gone and it is.
func (s *PostgresStore) Delete(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_channels WHERE name = $1`, name); err != nil {
		return fmt.Errorf("deleting notification channel: %w", err)
	}
	return nil
}

// scanChannel reads one notification_channels row.
func scanChannel(row interface{ Scan(dest ...any) error }) (*notification.Channel, error) {
	var ch notification.Channel
	var cfgJSON []byte
	if err := row.Scan(&ch.Name, &ch.Kind, &cfgJSON, &ch.Enabled, &ch.CreatedBy, &ch.UpdatedAt); err != nil {
		return nil, err //nolint:wrapcheck // callers add context per call site
	}
	var cfg channelConfig
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return nil, fmt.Errorf("decoding notification channel config: %w", err)
	}
	ch.Description = cfg.Description
	ch.Connection = cfg.Connection
	ch.Target = cfg.Target
	ch.Recipients = cfg.Recipients
	ch.Mode = cfg.Mode
	ch.RepeatAfter = time.Duration(cfg.RepeatAfterSeconds) * time.Second
	ch.MaxPerHour = cfg.MaxPerHour
	return &ch, nil
}

// Verify interface compliance.
var _ notification.ChannelStore = (*PostgresStore)(nil)
