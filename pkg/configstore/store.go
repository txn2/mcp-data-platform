// Package configstore provides granular key/value storage for platform
// configuration entries. DB entries override file config for whitelisted
// keys, with per-key hot-reload support.
package configstore

import (
	"context"
	"errors"
	"time"
)

// ErrReadOnly is returned when a write operation is attempted on a read-only store.
var ErrReadOnly = errors.New("config store is read-only")

// ErrNotFound is returned when a requested config entry does not exist.
var ErrNotFound = errors.New("config entry not found")

// Entry represents a single config key/value pair.
type Entry struct {
	Key       string    `json:"key" example:"server.description"`
	Value     string    `json:"value" example:"ACME Corp analytics platform"`
	UpdatedBy string    `json:"updated_by" example:"admin@example.com"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-15T14:30:00Z"`
}

// ChangelogEntry records a single config change for audit purposes.
type ChangelogEntry struct {
	ID        int       `json:"id" example:"1"`
	Key       string    `json:"key" example:"server.description"`
	Action    string    `json:"action" example:"set"`
	Value     *string   `json:"value,omitempty"`
	ChangedBy string    `json:"changed_by" example:"admin@example.com"`
	ChangedAt time.Time `json:"changed_at" example:"2026-01-15T14:30:00Z"`
}

// Store provides granular key/value config storage with audit logging.
type Store interface {
	// Get returns a single config entry by key, or ErrNotFound if absent.
	Get(ctx context.Context, key string) (*Entry, error)
	// Set creates or updates a config entry and logs the change.
	Set(ctx context.Context, key, value, author string) error
	// Delete removes a config entry and logs the change. Returns ErrNotFound if absent.
	Delete(ctx context.Context, key, author string) error
	// List returns all config entries, ordered by key.
	List(ctx context.Context) ([]Entry, error)
	// Changelog returns recent config changes, newest first.
	Changelog(ctx context.Context, limit int) ([]ChangelogEntry, error)
	// Mode returns the store mode: "file" or "database".
	Mode() string
}
