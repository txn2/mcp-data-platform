package memory

import (
	"context"
	"fmt"
)

// NewNoopStore creates a no-op Store for use when no database is available.
func NewNoopStore() Store {
	return &noopStore{}
}

// noopStore is a no-op implementation of Store.
type noopStore struct{}

// Insert is a no-op.
func (*noopStore) Insert(_ context.Context, _ Record) error { return nil }

// Get always returns not-found.
func (*noopStore) Get(_ context.Context, _ string) (*Record, error) {
	return nil, fmt.Errorf("memory record not found")
}

// Update is a no-op.
func (*noopStore) Update(_ context.Context, _ string, _ RecordUpdate) error { return nil }

// Delete is a no-op.
func (*noopStore) Delete(_ context.Context, _ string) error { return nil }

// List returns an empty slice.
func (*noopStore) List(_ context.Context, _ Filter) ([]Record, int, error) {
	return nil, 0, nil
}

// VectorSearch returns an empty slice.
func (*noopStore) VectorSearch(_ context.Context, _ VectorQuery) ([]ScoredRecord, error) {
	return nil, nil
}

// EntityLookup returns an empty slice.
func (*noopStore) EntityLookup(_ context.Context, _, _ string) ([]Record, error) {
	return nil, nil
}

// MarkStale is a no-op.
func (*noopStore) MarkStale(_ context.Context, _ []string, _ string) error { return nil }

// MarkVerified is a no-op.
func (*noopStore) MarkVerified(_ context.Context, _ []string) error { return nil }

// Supersede is a no-op.
func (*noopStore) Supersede(_ context.Context, _, _ string) error { return nil }

// Verify interface compliance.
var _ Store = (*noopStore)(nil)
