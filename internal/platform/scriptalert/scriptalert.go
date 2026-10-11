// Package scriptalert routes the alert a failed scheduled run raises to the
// notification channels an operator names (#1724).
//
// The alert already reaches the script's owner by email: they are accountable
// for the automation and can fix it. What this adds is the operations channel
// beside them, configured once for every script in the platform_settings
// section this package owns, so a team watching a chat channel sees a nightly
// automation fail without each owner forwarding it.
package scriptalert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// SettingsSection is the platform_settings section holding the configuration.
// It is a stored key: renaming it would strand every deployment's channels
// behind a section nothing reads.
const SettingsSection = "script_run_alert"

// ErrNotFound is returned by the store when the alert has never been
// configured. Callers apply the zero Settings, which posts nowhere.
var ErrNotFound = errors.New("scriptalert: settings not found")

// Settings is the operator's configuration: the channels a failed scheduled
// run is posted to.
type Settings struct {
	Channels []string `json:"channels,omitempty"`
	// UpdatedBy and UpdatedAt describe the last admin write. They live in the
	// platform_settings audit columns, so they are excluded from the value.
	UpdatedBy string    `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// SettingsStore persists the configuration.
type SettingsStore interface {
	// Get returns the stored configuration, or ErrNotFound.
	Get(ctx context.Context) (*Settings, error)
	// Set upserts the configuration.
	Set(ctx context.Context, s Settings, author string) error
}

// postgresStore is the platform_settings section.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates the store over db.
func NewPostgresStore(db *sql.DB) SettingsStore {
	return &postgresStore{db: db}
}

// Get returns the stored configuration.
func (s *postgresStore) Get(ctx context.Context) (*Settings, error) {
	var (
		raw       []byte
		updatedBy string
		updatedAt time.Time
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		SettingsSection).Scan(&raw, &updatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying script run alert settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decoding script run alert settings: %w", err)
	}
	settings.UpdatedBy, settings.UpdatedAt = updatedBy, updatedAt
	return &settings, nil
}

// Set upserts the configuration.
func (s *postgresStore) Set(ctx context.Context, in Settings, author string) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding script run alert settings: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		SettingsSection, raw, author); err != nil {
		return fmt.Errorf("storing script run alert settings: %w", err)
	}
	return nil
}

// SettingsOf returns the stored configuration, or the zero Settings when none
// has been written.
func SettingsOf(ctx context.Context, store SettingsStore) (Settings, error) {
	settings, err := store.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading script run alert settings: %w", err)
	}
	return *settings, nil
}

// SettingsInput is the write shape. Absent channels keep the stored list; an
// empty list clears it.
type SettingsInput struct {
	Channels *[]string `json:"channels,omitempty"`
}

// Validate trims and de-duplicates the channel list in place and returns a
// non-empty message when it is invalid. Whether each name is a channel is
// checked by the admin route, which holds the channel store.
func (in *SettingsInput) Validate() string {
	if in.Channels == nil {
		return ""
	}
	names := notifyalert.Normalize(*in.Channels)
	if len(names) > notifyalert.MaxChannels {
		return fmt.Sprintf("at most %d channels may be configured", notifyalert.MaxChannels)
	}
	in.Channels = &names
	return ""
}

// Settings maps the validated input to stored settings.
func (in *SettingsInput) Settings() Settings {
	if in.Channels == nil {
		return Settings{Channels: []string{}}
	}
	return Settings{Channels: *in.Channels}
}

// SettingsView is the read shape.
type SettingsView struct {
	Channels  []string  `json:"channels"`
	UpdatedBy string    `json:"updated_by,omitempty" example:"admin@example.com"`
	UpdatedAt time.Time `json:"updated_at"`
}

// View maps stored settings to the read shape, with [] for no channels.
func (s Settings) View() SettingsView {
	channels := s.Channels
	if channels == nil {
		channels = []string{}
	}
	return SettingsView{Channels: channels, UpdatedBy: s.UpdatedBy, UpdatedAt: s.UpdatedAt}
}

// Poster posts a failed run's alert to the configured channels.
type Poster struct {
	settings SettingsStore
	sender   *notifyalert.Sender
}

// NewPoster builds the poster, or nil when either dependency is absent.
func NewPoster(settings SettingsStore, sender *notifyalert.Sender) *Poster {
	if settings == nil || sender == nil {
		return nil
	}
	return &Poster{settings: settings, sender: sender}
}

// Post sends the alert under key to the configured channels and returns how
// many it was queued for. A failure to read the settings is returned for the
// caller to log; it posts nowhere and fails nothing else.
func (p *Poster) Post(ctx context.Context, key string, payload notification.Payload) (int, error) {
	if p == nil {
		return 0, nil
	}
	settings, err := SettingsOf(ctx, p.settings)
	if err != nil {
		return 0, err
	}
	return p.sender.Send(ctx, settings.Channels, key, payload), nil
}

// Verify interface compliance.
var _ SettingsStore = (*postgresStore)(nil)
