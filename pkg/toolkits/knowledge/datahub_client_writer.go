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

// globalTagsAspect mirrors the upstream globalTags aspect for REST read-modify-write.
// globalTagsAspect mirrors the upstream globalTags aspect. Associations are kept as
// raw JSON so that fields beyond the tag URN (e.g. a tag's propagation context or
// attribution) survive a read-modify-write instead of being stripped when an
// unrelated tag is added or removed.
type globalTagsAspect struct {
	Tags []json.RawMessage `json:"tags"`
}

// tagAssociation is the minimal shape used to emit a new tag association and to
// extract the tag URN from an existing raw association for dedupe/removal.
type tagAssociation struct {
	Tag string `json:"tag"`
}

// tagURNOf extracts the tag URN from a raw globalTags association, returning "" if
// it cannot be parsed.
func tagURNOf(raw json.RawMessage) string {
	var ta tagAssociation
	if err := json.Unmarshal(raw, &ta); err != nil {
		return ""
	}
	return ta.Tag
}

// tagSupportedTypes lists entity types DataHub registers the globalTags aspect on.
// Applying tags to any other type is rejected up front with a clear error, matching
// the guard the upstream client enforced before this batched path bypassed it.
// Mirrors the upstream client's globalTagsSupportedTypes set.
var tagSupportedTypes = map[string]bool{
	"dataset": true, "dashboard": true, "chart": true, "dataFlow": true,
	"dataJob": true, "container": true, "dataProduct": true,
	"domain": true, "glossaryTerm": true, "glossaryNode": true, "document": true,
}

// tagGraphQLWriteTypes lists entity types whose globalTags aspect is not exposed
// over the REST aspect API, so the upstream client writes them with the GraphQL
// addTag/removeTag mutations instead. Those mutations are server-side additive and
// not subject to the read-modify-write clobber, so for these types ApplyTagChanges
// delegates per-tag rather than batching a REST read-modify-write. Mirrors the
// upstream client's graphQLWriteTypes set.
var tagGraphQLWriteTypes = map[string]bool{
	"domain": true, "glossaryTerm": true, "glossaryNode": true, "document": true,
}

// ApplyTagChanges adds and removes tags on an entity in a single read-modify-write
// of the globalTags aspect. Delegating to the upstream client's per-tag AddTag/
// RemoveTag issues a read-modify-write per tag; because DataHub aspect writes are
// eventually consistent, back-to-back calls read stale tag state and the final
// write overwrites the whole aspect with a single tag, silently dropping the rest
// (#721). Reading once, merging every add/remove, and writing once is lossless. A
// tag present in both add and remove is removed.
func (w *DataHubClientWriter) ApplyTagChanges(ctx context.Context, urn string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	parsed, err := dhclient.ParseURN(urn)
	if err != nil {
		return fmt.Errorf("apply tag changes: invalid URN: %w", err)
	}
	if !tagSupportedTypes[parsed.EntityType] {
		return fmt.Errorf("apply tag changes: entity type %q does not support tag operations", parsed.EntityType)
	}
	if tagGraphQLWriteTypes[parsed.EntityType] {
		return w.applyTagChangesGraphQL(ctx, urn, add, remove)
	}
	return w.applyTagChangesREST(ctx, parsed.EntityType, urn, add, remove)
}

// applyTagChangesGraphQL applies tag changes for entity types whose globalTags
// aspect is only writable via GraphQL (domain, glossaryTerm, glossaryNode,
// document). The upstream AddTag/RemoveTag use the server-side additive GraphQL
// mutations for these types, which are already lossless, so we delegate per-tag.
func (w *DataHubClientWriter) applyTagChangesGraphQL(ctx context.Context, urn string, add, remove []string) error {
	removeSet := make(map[string]bool, len(remove))
	for _, tag := range remove {
		removeSet[tag] = true
	}
	for _, tag := range remove {
		if err := w.client.RemoveTag(ctx, urn, tag); err != nil {
			return fmt.Errorf("removing tag %s from %s: %w", tag, urn, err)
		}
	}
	for _, tag := range add {
		// A tag in both add and remove is left removed, matching the REST path and
		// the ApplyTagChanges contract.
		if removeSet[tag] {
			continue
		}
		if err := w.client.AddTag(ctx, urn, tag); err != nil {
			return fmt.Errorf("adding tag %s to %s: %w", tag, urn, err)
		}
	}
	return nil
}

// applyTagChangesREST reads the current globalTags aspect once, applies all
// removes and adds, and writes the merged aspect once.
func (w *DataHubClientWriter) applyTagChangesREST(ctx context.Context, entityType, urn string, add, remove []string) error {
	current, err := w.readGlobalTags(ctx, entityType, urn)
	if err != nil {
		return fmt.Errorf("apply tag changes: read globalTags: %w", err)
	}
	merged, err := mergeGlobalTags(current.Tags, add, remove)
	if err != nil {
		return fmt.Errorf("apply tag changes: %w", err)
	}
	current.Tags = merged
	return w.postIngestProposal(ctx, entityType, urn, "globalTags", current)
}

// mergeGlobalTags returns the globalTags associations after removing every tag in
// remove and appending every tag in add that is not already present, deduped. Each
// surviving existing association's full JSON is preserved (context/attribution),
// and a tag present in both add and remove is removed.
func mergeGlobalTags(current []json.RawMessage, add, remove []string) ([]json.RawMessage, error) {
	removeSet := make(map[string]bool, len(remove))
	for _, t := range remove {
		removeSet[t] = true
	}

	merged := make([]json.RawMessage, 0, len(current)+len(add))
	have := make(map[string]bool, len(current)+len(add))
	for _, raw := range current {
		u := tagURNOf(raw)
		if u == "" || removeSet[u] || have[u] {
			continue
		}
		have[u] = true
		merged = append(merged, raw)
	}
	for _, t := range add {
		if removeSet[t] || have[t] {
			continue
		}
		have[t] = true
		assoc, err := json.Marshal(tagAssociation{Tag: t})
		if err != nil {
			return nil, fmt.Errorf("marshal tag %s: %w", t, err)
		}
		merged = append(merged, assoc)
	}
	return merged, nil
}

// readGlobalTags reads the current globalTags aspect via the REST aspect API,
// returning an empty aspect when none exists.
func (w *DataHubClientWriter) readGlobalTags(ctx context.Context, entityType, urn string) (*globalTagsAspect, error) {
	body, statusCode, err := w.doRESTGet(ctx, w.aspectGetURL(entityType, urn, "globalTags"))
	if err != nil {
		return nil, fmt.Errorf("rest get aspect: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return &globalTagsAspect{}, nil
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("rest get status %d: %s", statusCode, truncateBody(body))
	}
	return parseGlobalTags(body)
}

// parseGlobalTags unmarshals the REST response body into a globalTagsAspect.
func parseGlobalTags(body []byte) (*globalTagsAspect, error) {
	var aspectResp struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &aspectResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(aspectResp.Value) == 0 || string(bytes.TrimSpace(aspectResp.Value)) == "null" {
		return &globalTagsAspect{}, nil
	}
	var aspect globalTagsAspect
	if err := json.Unmarshal(aspectResp.Value, &aspect); err != nil {
		return nil, fmt.Errorf("unmarshal globalTags: %w", err)
	}
	return &aspect, nil
}

// AddGlossaryTerm adds a glossary term to an entity.
func (w *DataHubClientWriter) AddGlossaryTerm(ctx context.Context, urn, termURN string) error {
	if err := w.client.AddGlossaryTerm(ctx, urn, termURN); err != nil {
		return fmt.Errorf("adding glossary term %s to %s: %w", termURN, urn, err)
	}
	return nil
}

// RemoveGlossaryTerm removes a glossary term association from an entity.
func (w *DataHubClientWriter) RemoveGlossaryTerm(ctx context.Context, urn, termURN string) error {
	if err := w.client.RemoveGlossaryTerm(ctx, urn, termURN); err != nil {
		return fmt.Errorf("removing glossary term %s from %s: %w", termURN, urn, err)
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

// RemoveDocumentationLink removes a documentation link from an entity by URL.
func (w *DataHubClientWriter) RemoveDocumentationLink(ctx context.Context, urn, linkURL string) error {
	if err := w.client.RemoveLink(ctx, urn, linkURL); err != nil {
		return fmt.Errorf("removing link from %s: %w", urn, err)
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
