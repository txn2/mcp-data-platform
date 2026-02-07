package storage

import "context"

// NoopProvider is a no-op implementation for testing.
type NoopProvider struct{}

// NewNoopProvider creates a new no-op provider.
func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

// Name returns the provider name.
func (*NoopProvider) Name() string {
	return "noop"
}

// ResolveDataset returns an empty identifier for no-op.
func (*NoopProvider) ResolveDataset(_ context.Context, _ string) (*DatasetIdentifier, error) {
	return &DatasetIdentifier{}, nil
}

// GetDatasetAvailability returns unavailable for no-op.
func (*NoopProvider) GetDatasetAvailability(_ context.Context, _ string) (*DatasetAvailability, error) {
	return &DatasetAvailability{Available: false}, nil
}

// GetAccessExamples returns empty for no-op.
func (*NoopProvider) GetAccessExamples(_ context.Context, _ string) ([]AccessExample, error) {
	return []AccessExample{}, nil
}

// ListObjects returns empty for no-op.
func (*NoopProvider) ListObjects(_ context.Context, _ DatasetIdentifier, _ int) ([]ObjectInfo, error) {
	return []ObjectInfo{}, nil
}

// Close is a no-op.
func (*NoopProvider) Close() error {
	return nil
}

// Verify interface compliance.
var _ Provider = (*NoopProvider)(nil)
