package configstore

import (
	"context"

	"gopkg.in/yaml.v3"
)

// FileStore wraps an in-memory config loaded from a YAML file.
// It is read-only: Save always returns ErrReadOnly.
type FileStore struct {
	data []byte
}

// NewFileStore creates a FileStore by marshaling the given config to YAML.
// The config parameter is typed as any to avoid import cycles with the platform package.
func NewFileStore(cfg any) *FileStore {
	data, _ := yaml.Marshal(cfg)
	return &FileStore{data: data}
}

// Load returns the config as YAML bytes.
func (s *FileStore) Load(_ context.Context) ([]byte, error) {
	return s.data, nil
}

// Save returns ErrReadOnly because file-based config is immutable.
func (*FileStore) Save(_ context.Context, _ []byte, _ SaveMeta) error {
	return ErrReadOnly
}

// History returns nil because file-based config has no version history.
func (*FileStore) History(_ context.Context, _ int) ([]Revision, error) {
	return nil, nil
}

// Mode returns "file".
func (*FileStore) Mode() string {
	return "file"
}
