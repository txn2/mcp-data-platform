package knowledge

import "context"

// DataHubWriter provides write-back operations to DataHub.
type DataHubWriter interface {
	GetCurrentMetadata(ctx context.Context, urn string) (*EntityMetadata, error)
	UpdateDescription(ctx context.Context, urn string, description string) error
	UpdateColumnDescription(ctx context.Context, urn string, fieldPath string, description string) error
	AddTag(ctx context.Context, urn string, tag string) error
	RemoveTag(ctx context.Context, urn string, tag string) error
	AddGlossaryTerm(ctx context.Context, urn string, termURN string) error
	AddDocumentationLink(ctx context.Context, urn string, url string, description string) error
}

// NoopDataHubWriter is a no-op implementation for when DataHub write-back is not configured.
type NoopDataHubWriter struct{}

// GetCurrentMetadata returns empty metadata.
func (*NoopDataHubWriter) GetCurrentMetadata(_ context.Context, _ string) (*EntityMetadata, error) {
	return &EntityMetadata{
		Tags:          []string{},
		GlossaryTerms: []string{},
		Owners:        []string{},
	}, nil
}

// UpdateDescription is a no-op.
func (*NoopDataHubWriter) UpdateDescription(_ context.Context, _, _ string) error { return nil }

// UpdateColumnDescription is a no-op.
func (*NoopDataHubWriter) UpdateColumnDescription(_ context.Context, _, _, _ string) error {
	return nil
}

// AddTag is a no-op.
func (*NoopDataHubWriter) AddTag(_ context.Context, _, _ string) error { return nil }

// RemoveTag is a no-op.
func (*NoopDataHubWriter) RemoveTag(_ context.Context, _, _ string) error { return nil }

// AddGlossaryTerm is a no-op.
func (*NoopDataHubWriter) AddGlossaryTerm(_ context.Context, _, _ string) error { return nil }

// AddDocumentationLink is a no-op.
func (*NoopDataHubWriter) AddDocumentationLink(_ context.Context, _, _, _ string) error { return nil }

// Verify interface compliance.
var _ DataHubWriter = (*NoopDataHubWriter)(nil)
