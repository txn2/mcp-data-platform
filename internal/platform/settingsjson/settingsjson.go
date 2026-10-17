// Package settingsjson stores one platform_settings section as a JSON value of
// a Go type: the read, the upsert, and the audit columns beside the value.
// Each alert and feature with an admin-written section used to carry its own
// copy of the two statements; a section added since #2148 is one of these.
package settingsjson

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Get when the section has never been written.
var ErrNotFound = errors.New("settingsjson: section not written")

// Stored is a section's value with who wrote it last and when.
type Stored[T any] struct {
	Value     T
	UpdatedBy string
	UpdatedAt time.Time
}

// Section is one platform_settings section holding a T.
type Section[T any] struct {
	db   *sql.DB
	name string
}

// New returns the section name over db.
func New[T any](db *sql.DB, name string) *Section[T] {
	return &Section[T]{db: db, name: name}
}

// Get returns the stored value, or ErrNotFound.
func (s *Section[T]) Get(ctx context.Context) (Stored[T], error) {
	var (
		raw []byte
		out Stored[T]
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		s.name).Scan(&raw, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("reading settings %s: %w", s.name, err)
	}
	if err := json.Unmarshal(raw, &out.Value); err != nil {
		return out, fmt.Errorf("decoding settings %s: %w", s.name, err)
	}
	return out, nil
}

// Set upserts the value, recording author as its writer.
func (s *Section[T]) Set(ctx context.Context, v T, author string) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding settings %s: %w", s.name, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		s.name, raw, author); err != nil {
		return fmt.Errorf("storing settings %s: %w", s.name, err)
	}
	return nil
}
