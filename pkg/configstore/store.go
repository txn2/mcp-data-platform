// Package configstore provides storage backends for platform configuration.
// It supports two modes: file (read-only, config from YAML) and database
// (read-write, config persisted in PostgreSQL with versioning).
//
// The store works with raw YAML bytes to avoid import cycles with the
// platform package. The platform is responsible for marshaling/unmarshaling.
package configstore

import (
	"context"
	"errors"
	"time"
)

// ErrReadOnly is returned when a write operation is attempted on a read-only store.
var ErrReadOnly = errors.New("config store is read-only")

// Store provides configuration storage and retrieval using raw YAML bytes.
type Store interface {
	// Load returns the stored configuration as YAML bytes, or nil if no config exists (first boot).
	Load(ctx context.Context) ([]byte, error)
	// Save persists a configuration snapshot (YAML bytes) with metadata.
	Save(ctx context.Context, data []byte, meta SaveMeta) error
	// History returns recent configuration revisions, newest first.
	History(ctx context.Context, limit int) ([]Revision, error)
	// Mode returns the store mode: "file" or "database".
	Mode() string
}

// SaveMeta holds metadata for a config save operation.
type SaveMeta struct {
	Author  string
	Comment string
}

// Revision describes a historical configuration version.
type Revision struct {
	ID        int       `json:"id"`
	Version   int       `json:"version"`
	Author    string    `json:"author"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
}
