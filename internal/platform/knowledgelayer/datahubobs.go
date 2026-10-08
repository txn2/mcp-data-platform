package knowledgelayer

import (
	"context"

	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/internal/dhobs"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// observedWriter records every read and write apply_knowledge makes in
// DataHub (#1896) through internal/dhobs, under the writer method's name.
type observedWriter struct {
	w knowledgekit.DataHubWriter
}

var _ knowledgekit.DataHubWriter = observedWriter{}

// GetCurrentMetadata reads an entity's metadata.
func (o observedWriter) GetCurrentMetadata(ctx context.Context, urn string) (*knowledgekit.EntityMetadata, error) {
	return dhobs.Do(ctx, "get_current_metadata", func(ctx context.Context) (*knowledgekit.EntityMetadata, error) { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.GetCurrentMetadata(ctx, urn)
	})
}

// UpdateDescription writes an entity's description.
func (o observedWriter) UpdateDescription(ctx context.Context, urn, description string) error {
	return dhobs.Call(ctx, "update_description", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.UpdateDescription(ctx, urn, description)
	})
}

// UpdateColumnDescription writes one column's description.
func (o observedWriter) UpdateColumnDescription(ctx context.Context, urn, fieldPath, description string) error {
	return dhobs.Call(ctx, "update_column_description", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.UpdateColumnDescription(ctx, urn, fieldPath, description)
	})
}

// UpdateColumnDescriptionBatch writes several columns' descriptions.
func (o observedWriter) UpdateColumnDescriptionBatch(ctx context.Context, urn string, columns map[string]string) error {
	return dhobs.Call(ctx, "update_column_description_batch", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.UpdateColumnDescriptionBatch(ctx, urn, columns)
	})
}

// ApplyTagChanges adds and removes tags.
func (o observedWriter) ApplyTagChanges(ctx context.Context, urn string, add, remove []string) error {
	return dhobs.Call(ctx, "apply_tag_changes", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.ApplyTagChanges(ctx, urn, add, remove)
	})
}

// ApplyGlossaryTermChanges adds and removes glossary terms.
func (o observedWriter) ApplyGlossaryTermChanges(ctx context.Context, urn string, add, remove []string) error {
	return dhobs.Call(ctx, "apply_glossary_term_changes", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.ApplyGlossaryTermChanges(ctx, urn, add, remove)
	})
}

// AddDocumentationLink adds a documentation link.
func (o observedWriter) AddDocumentationLink(ctx context.Context, urn, url, description string) error {
	return dhobs.Call(ctx, "add_documentation_link", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.AddDocumentationLink(ctx, urn, url, description)
	})
}

// RemoveDocumentationLink removes a documentation link.
func (o observedWriter) RemoveDocumentationLink(ctx context.Context, urn, url string) error {
	return dhobs.Call(ctx, "remove_documentation_link", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.RemoveDocumentationLink(ctx, urn, url)
	})
}

// CreateCuratedQuery creates a Query entity.
func (o observedWriter) CreateCuratedQuery(ctx context.Context, datasetURNs []string, name, sql, description string) (string, error) {
	return dhobs.Do(ctx, "create_curated_query", func(ctx context.Context) (string, error) { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.CreateCuratedQuery(ctx, datasetURNs, name, sql, description)
	})
}

// UpsertStructuredProperties writes structured property values.
func (o observedWriter) UpsertStructuredProperties(ctx context.Context, urn, propertyURN string, values []any) error {
	return dhobs.Call(ctx, "upsert_structured_properties", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.UpsertStructuredProperties(ctx, urn, propertyURN, values)
	})
}

// RemoveStructuredProperty removes a structured property.
func (o observedWriter) RemoveStructuredProperty(ctx context.Context, urn, propertyURN string) error {
	return dhobs.Call(ctx, "remove_structured_property", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.RemoveStructuredProperty(ctx, urn, propertyURN)
	})
}

// DeleteTag removes a tag definition.
func (o observedWriter) DeleteTag(ctx context.Context, tagURN string) error {
	return dhobs.Call(ctx, "delete_tag", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.DeleteTag(ctx, tagURN)
	})
}

// SetCustomProperties writes custom properties.
func (o observedWriter) SetCustomProperties(ctx context.Context, urn string, properties map[string]string) error {
	return dhobs.Call(ctx, "set_custom_properties", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.SetCustomProperties(ctx, urn, properties)
	})
}

// RemoveCustomProperties removes custom properties.
func (o observedWriter) RemoveCustomProperties(ctx context.Context, urn string, keys []string) error {
	return dhobs.Call(ctx, "remove_custom_properties", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.RemoveCustomProperties(ctx, urn, keys)
	})
}

// RaiseIncident raises an incident.
func (o observedWriter) RaiseIncident(ctx context.Context, entityURN, title, description string) (string, error) {
	return dhobs.Do(ctx, "raise_incident", func(ctx context.Context) (string, error) { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.RaiseIncident(ctx, entityURN, title, description)
	})
}

// ResolveIncident resolves an incident.
func (o observedWriter) ResolveIncident(ctx context.Context, incidentURN, message string) error {
	return dhobs.Call(ctx, "resolve_incident", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.ResolveIncident(ctx, incidentURN, message)
	})
}

// GetIncidents reads an entity's incidents.
func (o observedWriter) GetIncidents(ctx context.Context, entityURN string) ([]types.Incident, error) {
	return dhobs.Do(ctx, "get_incidents", func(ctx context.Context) ([]types.Incident, error) { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.GetIncidents(ctx, entityURN)
	})
}

// UpsertContextDocument writes a context document.
func (o observedWriter) UpsertContextDocument(
	ctx context.Context, entityURN string, doc types.ContextDocumentInput,
) (*types.ContextDocument, error) {
	return dhobs.Do(ctx, "upsert_context_document", func(ctx context.Context) (*types.ContextDocument, error) { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.UpsertContextDocument(ctx, entityURN, doc)
	})
}

// DeleteContextDocument removes a context document.
func (o observedWriter) DeleteContextDocument(ctx context.Context, documentID string) error {
	return dhobs.Call(ctx, "delete_context_document", func(ctx context.Context) error { //nolint:wrapcheck // a transparent decorator: the writer's error is the caller's to wrap
		return o.w.DeleteContextDocument(ctx, documentID)
	})
}
