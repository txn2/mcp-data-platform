package knowledge

import (
	"context"

	"github.com/txn2/mcp-datahub/pkg/types"
)

// DataHubWriter provides write-back operations to DataHub.
type DataHubWriter interface {
	GetCurrentMetadata(ctx context.Context, urn string) (*EntityMetadata, error)
	UpdateDescription(ctx context.Context, urn string, description string) error
	UpdateColumnDescription(ctx context.Context, urn string, fieldPath string, description string) error
	// UpdateColumnDescriptionBatch sets descriptions for multiple columns in a single
	// read-modify-write cycle, avoiding the stale-read bug where back-to-back single
	// calls lose all but the last column.
	UpdateColumnDescriptionBatch(ctx context.Context, urn string, columns map[string]string) error
	AddTag(ctx context.Context, urn string, tag string) error
	RemoveTag(ctx context.Context, urn string, tag string) error
	AddGlossaryTerm(ctx context.Context, urn string, termURN string) error
	AddDocumentationLink(ctx context.Context, urn string, url string, description string) error
	CreateCuratedQuery(ctx context.Context, entityURN, name, sql, description string) (string, error)

	// Structured properties (DataHub 1.4.x)
	UpsertStructuredProperties(ctx context.Context, urn string, propertyURN string, values []any) error
	RemoveStructuredProperty(ctx context.Context, urn string, propertyURN string) error

	// Incidents (DataHub 1.4.x)
	RaiseIncident(ctx context.Context, entityURN, title, description string) (string, error)
	ResolveIncident(ctx context.Context, incidentURN, message string) error

	// Context documents (DataHub 1.4.x with document support)
	UpsertContextDocument(ctx context.Context, entityURN string, doc types.ContextDocumentInput) (*types.ContextDocument, error)
	DeleteContextDocument(ctx context.Context, documentID string) error
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

// UpdateColumnDescriptionBatch is a no-op.
func (*NoopDataHubWriter) UpdateColumnDescriptionBatch(_ context.Context, _ string, _ map[string]string) error {
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

// CreateCuratedQuery is a no-op.
func (*NoopDataHubWriter) CreateCuratedQuery(_ context.Context, _, _, _, _ string) (string, error) {
	return "", nil
}

// UpsertStructuredProperties is a no-op.
func (*NoopDataHubWriter) UpsertStructuredProperties(_ context.Context, _, _ string, _ []any) error {
	return nil
}

// RemoveStructuredProperty is a no-op.
func (*NoopDataHubWriter) RemoveStructuredProperty(_ context.Context, _, _ string) error {
	return nil
}

// RaiseIncident is a no-op.
func (*NoopDataHubWriter) RaiseIncident(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

// ResolveIncident is a no-op.
func (*NoopDataHubWriter) ResolveIncident(_ context.Context, _, _ string) error { return nil }

// UpsertContextDocument is a no-op.
func (*NoopDataHubWriter) UpsertContextDocument(_ context.Context, _ string, _ types.ContextDocumentInput) (*types.ContextDocument, error) {
	return &types.ContextDocument{}, nil
}

// DeleteContextDocument is a no-op.
func (*NoopDataHubWriter) DeleteContextDocument(_ context.Context, _ string) error { return nil }

// Verify interface compliance.
var _ DataHubWriter = (*NoopDataHubWriter)(nil)
