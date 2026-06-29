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
	"time"

	"golang.org/x/sync/errgroup"

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
//
// The generic entity query (client.GetEntity) populates description and owners only
// for dataset and dashboard entities, so glossaryTerm and dataProduct read those
// fields through dedicated getters. Tags and glossary terms come from the
// authoritative REST aspects for the entity types that expose them over REST, and
// from the entity query for the GraphQL-only types (domain, glossaryTerm,
// glossaryNode), which mcp-datahub >= v1.10.2 surfaces on GetEntity via the
// experimental aspects API. This keeps both resulting_state and the rollback
// before-image complete for non-dataset types, where they were previously empty
// (#723): an empty before-image otherwise let a rollback strip tags or glossary
// terms the entity already had.
//
// Description and owners are still empty for types that have neither a generic-query
// fragment nor a dedicated getter (e.g. container, chart, dataFlow, dataJob).
func (w *DataHubClientWriter) GetCurrentMetadata(ctx context.Context, urn string) (*EntityMetadata, error) {
	entityType, err := entityTypeFromURN(urn)
	if err != nil {
		// Unknown URN shape: best-effort generic read.
		return w.entityMetadata(ctx, urn)
	}
	switch entityType {
	case entityTypeGlossaryTerm:
		return w.glossaryTermMetadata(ctx, urn)
	case entityTypeDataProduct:
		return w.dataProductMetadata(ctx, urn)
	default:
		return w.genericMetadata(ctx, entityType, urn)
	}
}

// glossaryTermMetadata reads a glossary term's description and owners from the
// dedicated getter and its tags/glossary terms from the entity query (the getter
// does not return associations and they are not REST-readable for this type).
func (w *DataHubClientWriter) glossaryTermMetadata(ctx context.Context, urn string) (*EntityMetadata, error) {
	term, err := w.client.GetGlossaryTerm(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting glossary term %s: %w", urn, err)
	}
	meta := descriptionOwnersMetadata(term.Description, term.Owners)
	if err := w.fillAssociationsFromEntity(ctx, urn, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// dataProductMetadata reads a data product's description and owners from the
// dedicated getter and its tags/glossary terms from the authoritative REST aspects.
func (w *DataHubClientWriter) dataProductMetadata(ctx context.Context, urn string) (*EntityMetadata, error) {
	dp, err := w.client.GetDataProduct(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting data product %s: %w", urn, err)
	}
	meta := descriptionOwnersMetadata(dp.Description, dp.Owners)
	if err := w.fillAssociationsFromREST(ctx, entityTypeDataProduct, urn, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// genericMetadata reads metadata via the generic entity query (description and
// owners are populated for dataset and dashboard). For REST-readable types it then
// overwrites tags/glossary terms with the authoritative REST aspects; GraphQL-only
// types (domain, glossaryNode) already carry them from the entity query.
func (w *DataHubClientWriter) genericMetadata(ctx context.Context, entityType, urn string) (*EntityMetadata, error) {
	meta, err := w.entityMetadata(ctx, urn)
	if err != nil {
		return nil, err
	}
	if !graphQLWriteTypes[entityType] {
		if err := w.fillAssociationsFromREST(ctx, entityType, urn, meta); err != nil {
			return nil, err
		}
	}
	return meta, nil
}

// entityMetadata reads metadata via the generic entity query.
func (w *DataHubClientWriter) entityMetadata(ctx context.Context, urn string) (*EntityMetadata, error) {
	entity, err := w.client.GetEntity(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting entity %s: %w", urn, err)
	}
	return &EntityMetadata{
		Description:   entity.Description,
		Tags:          entityTagURNs(entity),
		GlossaryTerms: entityTermURNs(entity),
		Owners:        ownerURNs(entity.Owners),
	}, nil
}

// fillAssociationsFromEntity reads tags and glossary terms via the entity query and
// writes them onto meta. Used for entity types whose tag/term aspects are exposed
// only through the entity query (the GraphQL-only types).
func (w *DataHubClientWriter) fillAssociationsFromEntity(ctx context.Context, urn string, meta *EntityMetadata) error {
	entity, err := w.client.GetEntity(ctx, urn)
	if err != nil {
		return fmt.Errorf("getting entity %s: %w", urn, err)
	}
	meta.Tags = entityTagURNs(entity)
	meta.GlossaryTerms = entityTermURNs(entity)
	return nil
}

// fillAssociationsFromREST reads tags and glossary terms from the authoritative REST
// aspects and writes them onto meta. The two reads are independent GETs, so they run
// concurrently.
func (w *DataHubClientWriter) fillAssociationsFromREST(ctx context.Context, entityType, urn string, meta *EntityMetadata) error {
	var tags, terms []string
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		t, err := w.readTagURNs(gctx, entityType, urn)
		if err != nil {
			return err
		}
		tags = t
		return nil
	})
	g.Go(func() error {
		t, err := w.readGlossaryTermURNs(gctx, entityType, urn)
		if err != nil {
			return err
		}
		terms = t
		return nil
	})
	if err := g.Wait(); err != nil {
		return err //nolint:wrapcheck // readTagURNs/readGlossaryTermURNs already wrap their errors
	}
	meta.Tags = tags
	meta.GlossaryTerms = terms
	return nil
}

// entityTagURNs extracts the tag URNs from an entity.
func entityTagURNs(e *types.Entity) []string {
	urns := make([]string, 0, len(e.Tags))
	for _, t := range e.Tags {
		urns = append(urns, t.URN)
	}
	return urns
}

// entityTermURNs extracts the glossary-term URNs from an entity.
func entityTermURNs(e *types.Entity) []string {
	urns := make([]string, 0, len(e.GlossaryTerms))
	for _, gt := range e.GlossaryTerms {
		urns = append(urns, gt.URN)
	}
	return urns
}

// descriptionOwnersMetadata builds metadata for entity types read through a getter
// that exposes only description and owners; tag and glossary-term associations are
// filled in separately.
func descriptionOwnersMetadata(description string, owners []types.Owner) *EntityMetadata {
	return &EntityMetadata{
		Description:   description,
		Tags:          []string{},
		GlossaryTerms: []string{},
		Owners:        ownerURNs(owners),
	}
}

// ownerURNs extracts the owner URNs from a slice of DataHub owners.
func ownerURNs(owners []types.Owner) []string {
	urns := make([]string, 0, len(owners))
	for _, o := range owners {
		urns = append(urns, o.URN)
	}
	return urns
}

// readTagURNs reads the entity's tag URNs from the REST globalTags aspect.
func (w *DataHubClientWriter) readTagURNs(ctx context.Context, entityType, urn string) ([]string, error) {
	aspect, err := w.readGlobalTags(ctx, entityType, urn)
	if err != nil {
		return nil, fmt.Errorf("reading tags for %s: %w", urn, err)
	}
	urns := make([]string, 0, len(aspect.Tags))
	for _, raw := range aspect.Tags {
		if u := tagURNOf(raw); u != "" {
			urns = append(urns, u)
		}
	}
	return urns, nil
}

// readGlossaryTermURNs reads the entity's glossary-term URNs from the REST
// glossaryTerms aspect.
func (w *DataHubClientWriter) readGlossaryTermURNs(ctx context.Context, entityType, urn string) ([]string, error) {
	aspect, err := w.readGlossaryTerms(ctx, entityType, urn)
	if err != nil {
		return nil, fmt.Errorf("reading glossary terms for %s: %w", urn, err)
	}
	urns := make([]string, 0, len(aspect.Terms))
	for _, raw := range aspect.Terms {
		if u := glossaryTermURNOf(raw); u != "" {
			urns = append(urns, u)
		}
	}
	return urns, nil
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

// readAspect performs a REST aspect GET and parses the response into *T, returning a
// zero-valued *T when the aspect does not exist (404 or an empty/null value). Shared
// by the editableSchemaMetadata, globalTags, and glossaryTerms read paths.
func readAspect[T any](ctx context.Context, w *DataHubClientWriter, entityType, urn, aspectName string) (*T, error) {
	body, statusCode, err := w.doRESTGet(ctx, w.aspectGetURL(entityType, urn, aspectName))
	if err != nil {
		return nil, fmt.Errorf("rest get aspect: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return new(T), nil
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("rest get status %d: %s", statusCode, truncateBody(body))
	}
	return parseAspect[T](body)
}

// parseAspect unmarshals a REST aspect GET response (a {"value": <aspect>} envelope)
// into *T, returning a zero-valued *T when the aspect is absent (empty or null value).
func parseAspect[T any](body []byte) (*T, error) {
	var aspectResp struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &aspectResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	out := new(T)
	if len(aspectResp.Value) == 0 || string(bytes.TrimSpace(aspectResp.Value)) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(aspectResp.Value, out); err != nil {
		return nil, fmt.Errorf("unmarshal aspect: %w", err)
	}
	return out, nil
}

// readEditableSchema reads the current editableSchemaMetadata aspect via REST.
func (w *DataHubClientWriter) readEditableSchema(ctx context.Context, entityType, urn string) (*editableSchemaAspect, error) {
	return readAspect[editableSchemaAspect](ctx, w, entityType, urn, "editableSchemaMetadata")
}

// parseEditableSchema unmarshals the REST response body into an editableSchemaAspect.
func parseEditableSchema(body []byte) (*editableSchemaAspect, error) {
	return parseAspect[editableSchemaAspect](body)
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

// graphQLWriteTypes lists entity types whose globalTags/glossaryTerms aspects are
// not exposed over the REST aspect API, so the upstream client writes them with
// GraphQL mutations instead. Those mutations are server-side additive and not
// subject to the read-modify-write clobber, so for these types ApplyTagChanges and
// ApplyGlossaryTermChanges delegate per-item rather than batching a REST
// read-modify-write. Mirrors the upstream client's graphQLWriteTypes set.
var graphQLWriteTypes = map[string]bool{
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
	if graphQLWriteTypes[parsed.EntityType] {
		return w.applyTagChangesGraphQL(ctx, urn, add, remove)
	}
	return w.applyTagChangesREST(ctx, parsed.EntityType, urn, add, remove)
}

// applyAssociationChangesGraphQL removes then adds items via per-item GraphQL
// mutations, skipping an add that is also a remove (so an item present in both is
// left removed, matching the REST merge semantics and the Apply*Changes contracts).
// removeFn/addFn perform and wrap each per-item call.
func applyAssociationChangesGraphQL(add, remove []string, removeFn, addFn func(string) error) error {
	removeSet := make(map[string]bool, len(remove))
	for _, x := range remove {
		removeSet[x] = true
	}
	for _, x := range remove {
		if err := removeFn(x); err != nil {
			return err
		}
	}
	for _, x := range add {
		if removeSet[x] {
			continue
		}
		if err := addFn(x); err != nil {
			return err
		}
	}
	return nil
}

// applyTagChangesGraphQL applies tag changes for entity types whose globalTags
// aspect is only writable via GraphQL (domain, glossaryTerm, glossaryNode,
// document). The upstream AddTag/RemoveTag use the server-side additive GraphQL
// mutations for these types, which are already lossless, so we delegate per-tag.
func (w *DataHubClientWriter) applyTagChangesGraphQL(ctx context.Context, urn string, add, remove []string) error {
	return applyAssociationChangesGraphQL(add, remove,
		func(tag string) error {
			if err := w.client.RemoveTag(ctx, urn, tag); err != nil {
				return fmt.Errorf("removing tag %s from %s: %w", tag, urn, err)
			}
			return nil
		},
		func(tag string) error {
			if err := w.client.AddTag(ctx, urn, tag); err != nil {
				return fmt.Errorf("adding tag %s to %s: %w", tag, urn, err)
			}
			return nil
		})
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

// mergeRawAssociations returns the aspect associations after removing every URN in
// remove and appending every URN in add that is not already present, deduped. Each
// surviving existing association's full JSON is preserved, and a URN present in both
// add and remove is removed. urnOf extracts the URN from a raw association; newAssoc
// marshals a new association for an added URN. Shared by the globalTags and
// glossaryTerms read-modify-write paths.
func mergeRawAssociations(
	current []json.RawMessage,
	add, remove []string,
	urnOf func(json.RawMessage) string,
	newAssoc func(string) ([]byte, error),
) ([]json.RawMessage, error) {
	removeSet := make(map[string]bool, len(remove))
	for _, t := range remove {
		removeSet[t] = true
	}

	merged := make([]json.RawMessage, 0, len(current)+len(add))
	have := make(map[string]bool, len(current)+len(add))
	for _, raw := range current {
		u := urnOf(raw)
		// An association whose URN cannot be extracted is preserved as-is rather than
		// dropped: it cannot be deduped or matched for removal, but discarding it would
		// silently lose a tag/term on an unrelated change.
		if u == "" {
			merged = append(merged, raw)
			continue
		}
		if removeSet[u] || have[u] {
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
		assoc, err := newAssoc(t)
		if err != nil {
			return nil, err
		}
		merged = append(merged, assoc)
	}
	return merged, nil
}

// mergeGlobalTags merges tag adds/removes into the current globalTags associations,
// preserving each surviving association's full JSON (context/attribution).
func mergeGlobalTags(current []json.RawMessage, add, remove []string) ([]json.RawMessage, error) {
	return mergeRawAssociations(current, add, remove, tagURNOf, func(t string) ([]byte, error) {
		assoc, err := json.Marshal(tagAssociation{Tag: t})
		if err != nil {
			return nil, fmt.Errorf("marshal tag %s: %w", t, err)
		}
		return assoc, nil
	})
}

// readGlobalTags reads the current globalTags aspect via the REST aspect API,
// returning an empty aspect when none exists.
func (w *DataHubClientWriter) readGlobalTags(ctx context.Context, entityType, urn string) (*globalTagsAspect, error) {
	return readAspect[globalTagsAspect](ctx, w, entityType, urn, "globalTags")
}

// parseGlobalTags unmarshals the REST response body into a globalTagsAspect.
func parseGlobalTags(body []byte) (*globalTagsAspect, error) {
	return parseAspect[globalTagsAspect](body)
}

// glossaryTermsAspect mirrors the upstream glossaryTerms aspect. Associations are
// kept as raw JSON so fields beyond the term URN survive a read-modify-write. Per
// the GlossaryTerms PDL, auditStamp is required and is set on every write.
type glossaryTermsAspect struct {
	Terms      []json.RawMessage  `json:"terms"`
	AuditStamp glossaryAuditStamp `json:"auditStamp"`
}

// termAssociation is the minimal shape used to emit a new term association and to
// extract the term URN from an existing raw association for dedupe/removal.
type termAssociation struct {
	URN string `json:"urn"`
}

// glossaryAuditStamp is the required audit metadata on the glossaryTerms aspect.
type glossaryAuditStamp struct {
	Time  int64  `json:"time"`
	Actor string `json:"actor"`
}

// glossaryAuditActor mirrors the system actor the upstream client stamps writes with.
const glossaryAuditActor = "urn:li:corpuser:datahub"

// newGlossaryAuditStamp builds the required auditStamp for a glossaryTerms write.
func newGlossaryAuditStamp() glossaryAuditStamp {
	return glossaryAuditStamp{Time: time.Now().UnixMilli(), Actor: glossaryAuditActor}
}

// glossaryTermURNOf extracts the term URN from a raw glossaryTerms association,
// returning "" if it cannot be parsed.
func glossaryTermURNOf(raw json.RawMessage) string {
	var ta termAssociation
	if err := json.Unmarshal(raw, &ta); err != nil {
		return ""
	}
	return ta.URN
}

// glossaryTermSupportedTypes lists entity types DataHub registers the glossaryTerms
// aspect on. Mirrors the upstream client's glossaryTermsSupportedTypes set (kept
// separate from the tag set because DataHub may add support to each independently).
var glossaryTermSupportedTypes = map[string]bool{
	"dataset": true, "dashboard": true, "chart": true, "dataFlow": true,
	"dataJob": true, "container": true, "dataProduct": true,
	"domain": true, "glossaryTerm": true, "glossaryNode": true, "document": true,
}

// ApplyGlossaryTermChanges adds and removes glossary terms on an entity in a single
// read-modify-write of the glossaryTerms aspect. Delegating to the upstream client's
// per-term AddGlossaryTerm/RemoveGlossaryTerm issues a read-modify-write per term;
// because DataHub aspect writes are eventually consistent, back-to-back calls read
// stale state and the final write overwrites the whole aspect with a single term,
// silently dropping the rest (#729). Reading once, merging every add/remove, and
// writing once is lossless. A term present in both add and remove is removed.
func (w *DataHubClientWriter) ApplyGlossaryTermChanges(ctx context.Context, urn string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	parsed, err := dhclient.ParseURN(urn)
	if err != nil {
		return fmt.Errorf("apply glossary term changes: invalid URN: %w", err)
	}
	if !glossaryTermSupportedTypes[parsed.EntityType] {
		return fmt.Errorf("apply glossary term changes: entity type %q does not support glossary term operations", parsed.EntityType)
	}
	if graphQLWriteTypes[parsed.EntityType] {
		return w.applyGlossaryTermChangesGraphQL(ctx, urn, add, remove)
	}
	return w.applyGlossaryTermChangesREST(ctx, parsed.EntityType, urn, add, remove)
}

// applyGlossaryTermChangesGraphQL applies term changes for entity types whose
// glossaryTerms aspect is only writable via GraphQL (domain, glossaryTerm,
// glossaryNode, document), delegating per-term to the upstream lossless mutations.
func (w *DataHubClientWriter) applyGlossaryTermChangesGraphQL(ctx context.Context, urn string, add, remove []string) error {
	return applyAssociationChangesGraphQL(add, remove,
		func(term string) error {
			if err := w.client.RemoveGlossaryTerm(ctx, urn, term); err != nil {
				return fmt.Errorf("removing glossary term %s from %s: %w", term, urn, err)
			}
			return nil
		},
		func(term string) error {
			if err := w.client.AddGlossaryTerm(ctx, urn, term); err != nil {
				return fmt.Errorf("adding glossary term %s to %s: %w", term, urn, err)
			}
			return nil
		})
}

// applyGlossaryTermChangesREST reads the current glossaryTerms aspect once, applies
// all removes and adds, refreshes the required auditStamp, and writes once.
func (w *DataHubClientWriter) applyGlossaryTermChangesREST(ctx context.Context, entityType, urn string, add, remove []string) error {
	current, err := w.readGlossaryTerms(ctx, entityType, urn)
	if err != nil {
		return fmt.Errorf("apply glossary term changes: read glossaryTerms: %w", err)
	}
	merged, err := mergeGlossaryTerms(current.Terms, add, remove)
	if err != nil {
		return fmt.Errorf("apply glossary term changes: %w", err)
	}
	current.Terms = merged
	current.AuditStamp = newGlossaryAuditStamp()
	return w.postIngestProposal(ctx, entityType, urn, "glossaryTerms", current)
}

// mergeGlossaryTerms merges term adds/removes into the current glossaryTerms
// associations, preserving each surviving association's full JSON.
func mergeGlossaryTerms(current []json.RawMessage, add, remove []string) ([]json.RawMessage, error) {
	return mergeRawAssociations(current, add, remove, glossaryTermURNOf, func(t string) ([]byte, error) {
		assoc, err := json.Marshal(termAssociation{URN: t})
		if err != nil {
			return nil, fmt.Errorf("marshal glossary term %s: %w", t, err)
		}
		return assoc, nil
	})
}

// readGlossaryTerms reads the current glossaryTerms aspect via the REST aspect API,
// returning an empty aspect when none exists.
func (w *DataHubClientWriter) readGlossaryTerms(ctx context.Context, entityType, urn string) (*glossaryTermsAspect, error) {
	return readAspect[glossaryTermsAspect](ctx, w, entityType, urn, "glossaryTerms")
}

// parseGlossaryTerms unmarshals the REST response body into a glossaryTermsAspect.
func parseGlossaryTerms(body []byte) (*glossaryTermsAspect, error) {
	return parseAspect[glossaryTermsAspect](body)
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
