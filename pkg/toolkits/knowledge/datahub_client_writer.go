package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"
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

// UpdateColumnDescriptionBatch sets descriptions for multiple columns in a single
// read-modify-write cycle. This avoids the stale-read bug where back-to-back single
// UpdateColumnDescription calls lose all but the last column due to DataHub's
// eventual consistency.
func (w *DataHubClientWriter) UpdateColumnDescriptionBatch(ctx context.Context, urn string, columns map[string]string) error {
	if len(columns) == 0 {
		return nil
	}
	// For a single column, delegate to the standard method.
	if len(columns) == 1 {
		for fp, desc := range columns {
			return w.UpdateColumnDescription(ctx, urn, fp, desc)
		}
	}
	// Batch: read the current schema, apply all changes, write once.
	parsed, err := dhclient.ParseURN(urn)
	if err != nil {
		return fmt.Errorf("batch column description: invalid URN: %w", err)
	}
	schema, err := w.readEditableSchema(ctx, parsed.EntityType, urn)
	if err != nil {
		return fmt.Errorf("batch column description: read schema: %w", err)
	}
	for fieldPath, desc := range columns {
		found := false
		for i := range schema.EditableSchemaFieldInfo {
			if schema.EditableSchemaFieldInfo[i].FieldPath == fieldPath {
				schema.EditableSchemaFieldInfo[i].Description = desc
				found = true
				break
			}
		}
		if !found {
			schema.EditableSchemaFieldInfo = append(schema.EditableSchemaFieldInfo, editableFieldInfo{
				FieldPath:   fieldPath,
				Description: desc,
			})
		}
	}
	return w.postIngestProposal(ctx, parsed.EntityType, urn, "editableSchemaMetadata", schema)
}

// editableSchemaAspect mirrors the upstream unexported type for batch operations.
type editableSchemaAspect struct {
	EditableSchemaFieldInfo []editableFieldInfo `json:"editableSchemaFieldInfo"`
}

// editableFieldInfo mirrors the upstream unexported type for batch operations.
type editableFieldInfo struct {
	FieldPath     string          `json:"fieldPath"`
	Description   string          `json:"description,omitempty"`
	GlobalTags    json.RawMessage `json:"globalTags,omitempty"`
	GlossaryTerms json.RawMessage `json:"glossaryTerms,omitempty"`
}

// readEditableSchema reads the current editableSchemaMetadata aspect via REST.
func (w *DataHubClientWriter) readEditableSchema(ctx context.Context, entityType, urn string) (*editableSchemaAspect, error) {
	body, statusCode, err := w.doRESTGet(ctx, w.aspectGetURL(entityType, urn, "editableSchemaMetadata"))
	if err != nil {
		return nil, fmt.Errorf("rest get aspect: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return &editableSchemaAspect{}, nil
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("rest get status %d: %s", statusCode, truncateBody(body))
	}
	return parseEditableSchema(body)
}

// parseEditableSchema unmarshals the REST response body into an editableSchemaAspect.
func parseEditableSchema(body []byte) (*editableSchemaAspect, error) {
	var aspectResp struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &aspectResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(aspectResp.Value) == 0 || string(bytes.TrimSpace(aspectResp.Value)) == "null" {
		return &editableSchemaAspect{}, nil
	}
	var schema editableSchemaAspect
	if err := json.Unmarshal(aspectResp.Value, &schema); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return &schema, nil
}

// aspectGetURL builds the REST URL for reading an aspect.
func (w *DataHubClientWriter) aspectGetURL(entityType, urn, aspectName string) string {
	cfg := w.client.Config()
	base := strings.TrimSuffix(cfg.URL, "/api/graphql")
	if cfg.APIVersion == dhclient.APIVersionV3 {
		return fmt.Sprintf("%s/openapi/v3/entity/%s/%s/%s",
			base, entityType, url.PathEscape(urn), aspectName)
	}
	return fmt.Sprintf("%s/aspects/%s?aspect=%s&version=0", base, urn, aspectName)
}

// doRESTGet performs an authenticated GET request and returns the body and status code.
func (w *DataHubClientWriter) doRESTGet(ctx context.Context, reqURL string) (body []byte, statusCode int, err error) {
	cfg := w.client.Config()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	if cfg.APIVersion != dhclient.APIVersionV3 {
		req.Header.Set("X-RestLi-Protocol-Version", "2.0.0")
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from configured endpoint
	if err != nil {
		return nil, 0, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// postIngestProposal writes an aspect via the DataHub REST API.
func (w *DataHubClientWriter) postIngestProposal(ctx context.Context, entityType, urn, aspectName string, aspect any) error {
	cfg := w.client.Config()
	base := strings.TrimSuffix(cfg.URL, "/api/graphql")

	var reqURL string
	var jsonBody []byte
	var err error

	if cfg.APIVersion == dhclient.APIVersionV3 {
		reqURL = fmt.Sprintf("%s/openapi/v3/entity/%s/%s/%s",
			base, entityType, url.PathEscape(urn), aspectName)
		jsonBody, err = json.Marshal(struct {
			Value any `json:"value"`
		}{Value: aspect})
	} else {
		reqURL = fmt.Sprintf("%s/aspects?action=ingestProposal", base)
		aspectJSON, marshalErr := json.Marshal(aspect)
		if marshalErr != nil {
			return fmt.Errorf("marshal aspect: %w", marshalErr)
		}
		proposal := struct {
			Proposal struct {
				EntityType string `json:"entityType"`
				EntityURN  string `json:"entityUrn"`
				ChangeType string `json:"changeType"`
				AspectName string `json:"aspectName"`
				Aspect     struct {
					Value       string `json:"value"`
					ContentType string `json:"contentType"`
				} `json:"aspect"`
			} `json:"proposal"`
		}{}
		proposal.Proposal.EntityType = entityType
		proposal.Proposal.EntityURN = urn
		proposal.Proposal.ChangeType = "UPSERT"
		proposal.Proposal.AspectName = aspectName
		proposal.Proposal.Aspect.Value = string(aspectJSON)
		proposal.Proposal.Aspect.ContentType = "application/json"
		jsonBody, err = json.Marshal(proposal)
	}
	if err != nil {
		return fmt.Errorf("marshal proposal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIVersion != dhclient.APIVersionV3 {
		req.Header.Set("X-RestLi-Protocol-Version", "2.0.0")
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from configured endpoint
	if err != nil {
		return fmt.Errorf("rest post aspect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rest post status %d: %s", resp.StatusCode, truncateBody(body))
	}
	return nil
}

const maxBodyTruncate = 200

func truncateBody(b []byte) string {
	if len(b) <= maxBodyTruncate {
		return string(b)
	}
	return string(b[:maxBodyTruncate]) + "..."
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

// CreateCuratedQuery creates a Query entity in DataHub associated with the given dataset.
func (w *DataHubClientWriter) CreateCuratedQuery(ctx context.Context, entityURN, name, sqlText, description string) (string, error) {
	result, err := w.client.CreateQuery(ctx, dhclient.CreateQueryInput{
		Name:        name,
		Description: description,
		Statement:   sqlText,
		DatasetURNs: []string{entityURN},
	})
	if err != nil {
		return "", fmt.Errorf("creating curated query for %s: %w", entityURN, err)
	}
	return result.URN, nil
}

// UpsertStructuredProperties sets a structured property on an entity.
func (w *DataHubClientWriter) UpsertStructuredProperties(ctx context.Context, urn, propertyURN string, values []any) error {
	input := []types.StructuredPropertyInput{{PropertyURN: propertyURN, Values: values}}
	if err := w.client.UpsertStructuredProperties(ctx, urn, input); err != nil {
		return fmt.Errorf("upserting structured property %s on %s: %w", propertyURN, urn, err)
	}
	return nil
}

// RemoveStructuredProperty removes a structured property from an entity.
func (w *DataHubClientWriter) RemoveStructuredProperty(ctx context.Context, urn, propertyURN string) error {
	if err := w.client.RemoveStructuredProperties(ctx, urn, []string{propertyURN}); err != nil {
		return fmt.Errorf("removing structured property %s from %s: %w", propertyURN, urn, err)
	}
	return nil
}

// RaiseIncident creates a new incident on an entity.
func (w *DataHubClientWriter) RaiseIncident(ctx context.Context, entityURN, title, description string) (string, error) {
	incidentURN, err := w.client.RaiseIncident(ctx, types.RaiseIncidentInput{
		Type:         "OPERATIONAL",
		Title:        title,
		Description:  description,
		ResourceURNs: []string{entityURN},
	})
	if err != nil {
		return "", fmt.Errorf("raising incident on %s: %w", entityURN, err)
	}
	return incidentURN, nil
}

// ResolveIncident marks an incident as resolved.
func (w *DataHubClientWriter) ResolveIncident(ctx context.Context, incidentURN, message string) error {
	if err := w.client.ResolveIncident(ctx, incidentURN, message); err != nil {
		return fmt.Errorf("resolving incident %s: %w", incidentURN, err)
	}
	return nil
}

// UpsertContextDocument creates or updates a context document on an entity.
func (w *DataHubClientWriter) UpsertContextDocument(ctx context.Context, entityURN string, doc types.ContextDocumentInput) (*types.ContextDocument, error) {
	result, err := w.client.UpsertContextDocument(ctx, entityURN, doc)
	if err != nil {
		return nil, fmt.Errorf("upserting context document on %s: %w", entityURN, err)
	}
	return result, nil
}

// DeleteContextDocument removes a context document by its ID.
func (w *DataHubClientWriter) DeleteContextDocument(ctx context.Context, documentID string) error {
	if err := w.client.DeleteContextDocument(ctx, documentID); err != nil {
		return fmt.Errorf("deleting context document %s: %w", documentID, err)
	}
	return nil
}
