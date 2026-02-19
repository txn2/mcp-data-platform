package knowledge

import (
	"context"
	"fmt"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
)

// DataHubClientWriter is a real DataHubWriter implementation that delegates
// to the mcp-datahub client for read and write operations against DataHub.
type DataHubClientWriter struct {
	client *dhclient.Client
}

// Verify interface compliance.
var _ DataHubWriter = (*DataHubClientWriter)(nil)

// NewDataHubClientWriter creates a DataHubClientWriter from an existing client.
func NewDataHubClientWriter(c *dhclient.Client) *DataHubClientWriter {
	return &DataHubClientWriter{client: c}
}

// GetCurrentMetadata retrieves current metadata for an entity from DataHub.
func (w *DataHubClientWriter) GetCurrentMetadata(ctx context.Context, urn string) (*EntityMetadata, error) {
	entity, err := w.client.GetEntity(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting entity %s: %w", urn, err)
	}

	meta := &EntityMetadata{
		Description:   entity.Description,
		Tags:          make([]string, 0, len(entity.Tags)),
		GlossaryTerms: make([]string, 0, len(entity.GlossaryTerms)),
		Owners:        make([]string, 0, len(entity.Owners)),
	}

	for _, t := range entity.Tags {
		meta.Tags = append(meta.Tags, t.URN)
	}
	for _, gt := range entity.GlossaryTerms {
		meta.GlossaryTerms = append(meta.GlossaryTerms, gt.URN)
	}
	for _, o := range entity.Owners {
		meta.Owners = append(meta.Owners, o.URN)
	}

	return meta, nil
}

// UpdateDescription sets the editable description for an entity.
func (w *DataHubClientWriter) UpdateDescription(ctx context.Context, urn, description string) error {
	if err := w.client.UpdateDescription(ctx, urn, description); err != nil {
		return fmt.Errorf("updating description for %s: %w", urn, err)
	}
	return nil
}

// UpdateColumnDescription sets the editable description for a specific column.
func (w *DataHubClientWriter) UpdateColumnDescription(ctx context.Context, urn, fieldPath, description string) error {
	if err := w.client.UpdateColumnDescription(ctx, urn, fieldPath, description); err != nil {
		return fmt.Errorf("updating column description for %s.%s: %w", urn, fieldPath, err)
	}
	return nil
}

// AddTag adds a tag to an entity.
func (w *DataHubClientWriter) AddTag(ctx context.Context, urn, tag string) error {
	if err := w.client.AddTag(ctx, urn, tag); err != nil {
		return fmt.Errorf("adding tag %s to %s: %w", tag, urn, err)
	}
	return nil
}

// RemoveTag removes a tag from an entity.
func (w *DataHubClientWriter) RemoveTag(ctx context.Context, urn, tag string) error {
	if err := w.client.RemoveTag(ctx, urn, tag); err != nil {
		return fmt.Errorf("removing tag %s from %s: %w", tag, urn, err)
	}
	return nil
}

// AddGlossaryTerm adds a glossary term to an entity.
func (w *DataHubClientWriter) AddGlossaryTerm(ctx context.Context, urn, termURN string) error {
	if err := w.client.AddGlossaryTerm(ctx, urn, termURN); err != nil {
		return fmt.Errorf("adding glossary term %s to %s: %w", termURN, urn, err)
	}
	return nil
}

// AddDocumentationLink adds a documentation link to an entity.
func (w *DataHubClientWriter) AddDocumentationLink(ctx context.Context, urn, linkURL, description string) error {
	if err := w.client.AddLink(ctx, urn, linkURL, description); err != nil {
		return fmt.Errorf("adding link to %s: %w", urn, err)
	}
	return nil
}
