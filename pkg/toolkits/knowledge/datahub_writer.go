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
	// ApplyTagChanges adds and removes the given tags in a single read-modify-write
	// of the globalTags aspect. Per-tag writes are read-modify-write against
	// DataHub's eventually consistent store, so back-to-back single calls read stale
	// tag state and the last write clobbers the rest, silently dropping tags (#721).
	// Batching every add/remove for an entity into one read-modify-write is lossless.
	// add and remove hold full TagUrns; a tag in both add and remove is removed.
	ApplyTagChanges(ctx context.Context, urn string, add, remove []string) error
	// ApplyGlossaryTermChanges adds and removes the given glossary terms in a single
	// read-modify-write of the glossaryTerms aspect. Per-term writes are
	// read-modify-write against DataHub's eventually consistent store, so back-to-back
	// single calls read stale state and the last write clobbers the rest, silently
	// dropping terms (#729, same class of bug as #721). add and remove hold full
	// glossaryTerm URNs; a term in both add and remove is removed.
	ApplyGlossaryTermChanges(ctx context.Context, urn string, add, remove []string) error
	AddDocumentationLink(ctx context.Context, urn string, url string, description string) error
	// RemoveDocumentationLink removes a documentation link by URL. Used to revert add_documentation.
	RemoveDocumentationLink(ctx context.Context, urn string, url string) error
	// CreateCuratedQuery creates a Query entity associated with every dataset
	// the statement reads. It takes the set rather than one URN because a
	// query that joins three tables belongs to all three, and DataHub's own
	// write accepts the set; a promoted call record (#1321) carries exactly
	// that set as its targets.
	CreateCuratedQuery(ctx context.Context, datasetURNs []string, name, sql, description string) (string, error)

	// Structured properties (DataHub 1.4.x)
	UpsertStructuredProperties(ctx context.Context, urn string, propertyURN string, values []any) error
	RemoveStructuredProperty(ctx context.Context, urn string, propertyURN string) error

	// Curation (#726, mcp-datahub v1.11.0). DeleteTag removes a tag definition
	// entirely; SetCustomProperties/RemoveCustomProperties edit an entity's legacy
	// customProperties map.
	DeleteTag(ctx context.Context, tagURN string) error
	SetCustomProperties(ctx context.Context, urn string, properties map[string]string) error
	RemoveCustomProperties(ctx context.Context, urn string, keys []string) error

	// Incidents (DataHub 1.4.x)
	RaiseIncident(ctx context.Context, entityURN, title, description string) (string, error)
	ResolveIncident(ctx context.Context, incidentURN, message string) error
	// GetIncidents returns the incidents currently on an entity, used to avoid raising
	// a duplicate quality-issue incident.
	GetIncidents(ctx context.Context, entityURN string) ([]types.Incident, error)

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

// ApplyTagChanges is a no-op.
func (*NoopDataHubWriter) ApplyTagChanges(_ context.Context, _ string, _, _ []string) error {
	return nil
}

// ApplyGlossaryTermChanges is a no-op.
func (*NoopDataHubWriter) ApplyGlossaryTermChanges(_ context.Context, _ string, _, _ []string) error {
	return nil
}

// AddDocumentationLink is a no-op.
func (*NoopDataHubWriter) AddDocumentationLink(_ context.Context, _, _, _ string) error { return nil }

// RemoveDocumentationLink is a no-op.
func (*NoopDataHubWriter) RemoveDocumentationLink(_ context.Context, _, _ string) error { return nil }

// CreateCuratedQuery is a no-op.
func (*NoopDataHubWriter) CreateCuratedQuery(_ context.Context, _ []string, _, _, _ string) (string, error) {
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

// DeleteTag is a no-op.
func (*NoopDataHubWriter) DeleteTag(_ context.Context, _ string) error { return nil }

// SetCustomProperties is a no-op.
func (*NoopDataHubWriter) SetCustomProperties(_ context.Context, _ string, _ map[string]string) error {
	return nil
}

// RemoveCustomProperties is a no-op.
func (*NoopDataHubWriter) RemoveCustomProperties(_ context.Context, _ string, _ []string) error {
	return nil
}

// RaiseIncident is a no-op.
func (*NoopDataHubWriter) RaiseIncident(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

// GetIncidents returns no incidents.
func (*NoopDataHubWriter) GetIncidents(_ context.Context, _ string) ([]types.Incident, error) {
	return nil, nil
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

// noDataHubConfigured opens every refusal raised by an unresolvable DataHub
// connection, so the apply and rollback paths state the same condition.
const noDataHubConfigured = "no DataHub connection is configured"

// DataHubWritable reports whether w reaches a real DataHub instance, for a
// caller outside this package deciding whether a write path is worth wiring at
// all. The composition root uses it to leave the call-record promotion path
// unwired rather than let a promotion report success having written nothing
// (#1321).
func DataHubWritable(w DataHubWriter) bool { return datahubWritable(w) }

// datahubWritable reports whether w reaches a real DataHub instance. A nil writer
// and NoopDataHubWriter (substituted whenever no DataHub connection resolves) do
// not: their writes return nil having reached no system. Write paths must refuse
// on false rather than report a success that persisted nothing.
//
// This asks the concrete type rather than the interface because DataHubWriter is
// exported: adding a capability method would break external implementations. The
// noop type already exists to mean "no DataHub", so identifying it is the question.
func datahubWritable(w DataHubWriter) bool {
	if w == nil {
		return false
	}
	_, noop := w.(*NoopDataHubWriter)
	return !noop
}
