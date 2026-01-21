package query

import "context"

// NoopProvider is a no-op implementation for testing.
type NoopProvider struct{}

// NewNoopProvider creates a new no-op provider.
func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

// Name returns the provider name.
func (n *NoopProvider) Name() string {
	return "noop"
}

// ResolveTable returns nil.
func (n *NoopProvider) ResolveTable(_ context.Context, _ string) (*TableIdentifier, error) {
	return nil, nil
}

// GetTableAvailability returns unavailable.
func (n *NoopProvider) GetTableAvailability(_ context.Context, _ string) (*TableAvailability, error) {
	return &TableAvailability{
		Available: false,
		Error:     "no query provider configured",
	}, nil
}

// GetQueryExamples returns empty examples.
func (n *NoopProvider) GetQueryExamples(_ context.Context, _ string) ([]QueryExample, error) {
	return []QueryExample{}, nil
}

// GetExecutionContext returns empty context.
func (n *NoopProvider) GetExecutionContext(_ context.Context, _ []string) (*ExecutionContext, error) {
	return &ExecutionContext{
		Tables:      []TableInfo{},
		Connections: []string{},
	}, nil
}

// GetTableSchema returns empty schema.
func (n *NoopProvider) GetTableSchema(_ context.Context, _ TableIdentifier) (*TableSchema, error) {
	return &TableSchema{
		Columns: []Column{},
	}, nil
}

// Close does nothing.
func (n *NoopProvider) Close() error {
	return nil
}

// Verify interface compliance.
var _ Provider = (*NoopProvider)(nil)
