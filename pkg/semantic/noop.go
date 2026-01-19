package semantic

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

// GetTableContext returns empty context.
func (n *NoopProvider) GetTableContext(_ context.Context, _ TableIdentifier) (*TableContext, error) {
	return &TableContext{}, nil
}

// GetColumnContext returns empty context.
func (n *NoopProvider) GetColumnContext(_ context.Context, _ ColumnIdentifier) (*ColumnContext, error) {
	return &ColumnContext{}, nil
}

// GetColumnsContext returns empty map.
func (n *NoopProvider) GetColumnsContext(_ context.Context, _ TableIdentifier) (map[string]*ColumnContext, error) {
	return make(map[string]*ColumnContext), nil
}

// GetLineage returns empty lineage.
func (n *NoopProvider) GetLineage(_ context.Context, _ TableIdentifier, dir LineageDirection, maxDepth int) (*LineageInfo, error) {
	return &LineageInfo{
		Direction: dir,
		Entities:  []LineageEntity{},
		MaxDepth:  maxDepth,
	}, nil
}

// GetGlossaryTerm returns nil.
func (n *NoopProvider) GetGlossaryTerm(_ context.Context, _ string) (*GlossaryTerm, error) {
	return nil, nil
}

// SearchTables returns empty results.
func (n *NoopProvider) SearchTables(_ context.Context, _ SearchFilter) ([]TableSearchResult, error) {
	return []TableSearchResult{}, nil
}

// Close does nothing.
func (n *NoopProvider) Close() error {
	return nil
}

// Verify interface compliance.
var _ Provider = (*NoopProvider)(nil)
