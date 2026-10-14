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
