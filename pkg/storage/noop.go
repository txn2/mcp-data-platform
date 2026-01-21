package storage

import "context"

// NoopProvider is a no-op implementation for testing.
type NoopProvider struct{}

// NewNoopProvider creates a new no-op provider.
func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

// Name returns the provider name.
func (p *NoopProvider) Name() string {
	return "noop"
}

// ResolveDataset returns nil for no-op.
func (p *NoopProvider) ResolveDataset(_ context.Context, _ string) (*DatasetIdentifier, error) {
	return nil, nil
}

// GetDatasetAvailability returns unavailable for no-op.
func (p *NoopProvider) GetDatasetAvailability(_ context.Context, _ string) (*DatasetAvailability, error) {
	return &DatasetAvailability{Available: false}, nil
}

// GetAccessExamples returns empty for no-op.
func (p *NoopProvider) GetAccessExamples(_ context.Context, _ string) ([]AccessExample, error) {
	return nil, nil
}

// ListObjects returns empty for no-op.
func (p *NoopProvider) ListObjects(_ context.Context, _ DatasetIdentifier, _ int) ([]ObjectInfo, error) {
	return nil, nil
}

// Close is a no-op.
func (p *NoopProvider) Close() error {
	return nil
}

// Verify interface compliance.
var _ Provider = (*NoopProvider)(nil)
