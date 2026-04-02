package configstore

import (
	"context"
	"sort"
	"time"
)

// FileStore provides read-only config entries from a static map.
// Write operations return ErrReadOnly.
type FileStore struct {
	entries map[string]string
}

// NewFileStore creates a FileStore from a map of key/value pairs.
func NewFileStore(entries map[string]string) *FileStore {
	if entries == nil {
		entries = map[string]string{}
	}
	return &FileStore{entries: entries}
}

// Get returns the entry for the given key, or ErrNotFound if absent.
func (s *FileStore) Get(_ context.Context, key string) (*Entry, error) {
	val, ok := s.entries[key]
	if !ok {
		return nil, ErrNotFound
	}
	return &Entry{Key: key, Value: val}, nil
}

// Set returns ErrReadOnly because file-based config is immutable.
func (*FileStore) Set(_ context.Context, _, _, _ string) error {
	return ErrReadOnly
}

// Delete returns ErrReadOnly because file-based config is immutable.
func (*FileStore) Delete(_ context.Context, _, _ string) error {
	return ErrReadOnly
}

// List returns all entries from the static map, sorted by key.
func (s *FileStore) List(_ context.Context) ([]Entry, error) {
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, Entry{Key: k, Value: s.entries[k], UpdatedAt: time.Time{}})
	}
	return entries, nil
}

// Changelog returns an empty slice because file-based config has no change history.
func (*FileStore) Changelog(_ context.Context, _ int) ([]ChangelogEntry, error) {
	return []ChangelogEntry{}, nil
}

// Mode returns "file".
func (*FileStore) Mode() string {
	return "file"
}
