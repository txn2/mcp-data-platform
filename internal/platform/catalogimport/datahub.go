package catalogimport

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// GraphQL is the part of the DataHub client the import reads through: raw
// GraphQL, and the context documents the client already reads.
type GraphQL interface {
	Execute(ctx context.Context, query string, variables map[string]any, result any) error
	GetContextDocuments(ctx context.Context, urn string) ([]types.ContextDocument, error)
}

// DataHub reads what the import carries over from a DataHub instance.
type DataHub struct {
	gql GraphQL
}

// NewDataHub reads through a DataHub client.
func NewDataHub(gql GraphQL) *DataHub { return &DataHub{gql: gql} }

// searchPage is how many search results one request asks for.
const searchPage = 200

// searchQuery lists entities of one type, removed ones included, a page at a
// time. includeSoftDeleted is what lists the records DataHub has marked
// removed beside the live ones.
const searchQuery = `query importSearch($types: [EntityType!], $start: Int!, $count: Int!) {
  searchAcrossEntities(input: { types: $types, query: "*", start: $start, count: $count,
    searchFlags: { includeSoftDeleted: true } }) {
    total
    searchResults { entity { urn } }
  }
}`

type searchResponse struct {
	SearchAcrossEntities struct {
		Total         int `json:"total"`
		SearchResults []struct {
			Entity struct {
				URN string `json:"urn"`
			} `json:"entity"`
		} `json:"searchResults"`
	} `json:"searchAcrossEntities"`
}

// removedQuery lists the entities of one type DataHub has marked removed.
const removedQuery = `query importRemoved($types: [EntityType!], $start: Int!, $count: Int!) {
  searchAcrossEntities(input: { types: $types, query: "*", start: $start, count: $count,
    orFilters: [{ and: [{ field: "removed", values: ["true"] }] }] }) {
    total
    searchResults { entity { urn } }
  }
}`

// list returns the URN of every entity of a type the query finds.
func (d *DataHub) list(ctx context.Context, query, entityType string) ([]string, error) {
	out := make([]string, 0)
	for start := 0; ; start += searchPage {
		var resp searchResponse
		err := d.gql.Execute(ctx, query, map[string]any{
			"types": []string{entityType}, "start": start, "count": searchPage,
		}, &resp)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", entityType, err)
		}
		for _, r := range resp.SearchAcrossEntities.SearchResults {
			out = append(out, r.Entity.URN)
		}
		page := len(resp.SearchAcrossEntities.SearchResults)
		if page == 0 || start+page >= resp.SearchAcrossEntities.Total {
			return out, nil
		}
	}
}

// DatasetHeads lists every dataset, removed ones included, saying which are
// removed.
func (d *DataHub) DatasetHeads(ctx context.Context) ([]DatasetHead, error) {
	all, err := d.list(ctx, searchQuery, "DATASET")
	if err != nil {
		return nil, err
	}
	removed, err := d.list(ctx, removedQuery, "DATASET")
	if err != nil {
		return nil, err
	}
	gone := make(map[string]bool, len(removed))
	for _, urn := range removed {
		gone[urn] = true
	}
	heads := make([]DatasetHead, 0, len(all))
	for _, urn := range all {
		heads = append(heads, DatasetHead{URN: urn, Removed: gone[urn]})
	}
	return heads, nil
}

// datasetQuery reads everything the import carries over from one dataset.
const datasetQuery = `query importDataset($urn: String!) {
  dataset(urn: $urn) {
    urn
    status { removed }
    properties { description customProperties { key value } }
    editableProperties { description }
    tags { tags { tag { urn } } }
    glossaryTerms { terms { term { urn } } }
    ownership { owners { owner { ... on CorpUser { urn username } ... on CorpGroup { urn name } } type ownershipType { urn info { name } } } }
    domain { domain { urn } }
    institutionalMemory { elements { url description } }
    structuredProperties { properties { structuredProperty { urn definition { qualifiedName } } values { ... on StringValue { stringValue } ... on NumberValue { numberValue } } } }
    schemaMetadata { fields { fieldPath description tags { tags { tag { urn } } } glossaryTerms { terms { term { urn } } } } }
    editableSchemaMetadata { editableSchemaFieldInfo { fieldPath description tags { tags { tag { urn } } } glossaryTerms { terms { term { urn } } } } }
    incidents(start: 0, count: 100) { incidents { urn incidentType title description status { state message } } }
    upstream: lineage(input: { direction: UPSTREAM, start: 0, count: 200 }) { relationships { entity { urn type } } }
  }
}`

// queriesQuery reads the curated queries DataHub keeps for a dataset.
const queriesQuery = `query importQueries($urn: String!) {
  listQueries(input: { start: 0, count: 100, datasetUrn: $urn }) {
    queries { urn properties { name description statement { value } } }
  }
}`

type tagsResponse struct {
	Tags []struct {
		Tag struct {
			URN string `json:"urn"`
		} `json:"tag"`
	} `json:"tags"`
}

type termsResponse struct {
	Terms []struct {
		Term struct {
			URN string `json:"urn"`
		} `json:"term"`
	} `json:"terms"`
}

type ownershipResponse struct {
	Owners []struct {
		Owner struct {
			URN      string `json:"urn"`
			Username string `json:"username"`
			Name     string `json:"name"`
		} `json:"owner"`
		Type          string `json:"type"`
		OwnershipType *struct {
			Info *struct {
				Name string `json:"name"`
			} `json:"info"`
		} `json:"ownershipType"`
	} `json:"owners"`
}

type linksResponse struct {
	Elements []struct {
		URL         string `json:"url"`
		Description string `json:"description"`
	} `json:"elements"`
}

type fieldResponse struct {
	FieldPath     string         `json:"fieldPath"`
	Description   string         `json:"description"`
	Tags          *tagsResponse  `json:"tags"`
	GlossaryTerms *termsResponse `json:"glossaryTerms"`
}

type propertiesResponse struct {
	Description      string `json:"description"`
	CustomProperties []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"customProperties"`
}

type structuredResponse struct {
	Properties []struct {
		StructuredProperty struct {
			URN        string `json:"urn"`
			Definition *struct {
				QualifiedName string `json:"qualifiedName"`
			} `json:"definition"`
		} `json:"structuredProperty"`
		Values []struct {
			StringValue *string  `json:"stringValue"`
			NumberValue *float64 `json:"numberValue"`
		} `json:"values"`
	} `json:"properties"`
}

// incidentStatus is an incident's state and the message it was resolved with.
type incidentStatus struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

type incidentsResponse struct {
	Incidents []struct {
		URN          string          `json:"urn"`
		IncidentType string          `json:"incidentType"`
		Title        string          `json:"title"`
		Description  string          `json:"description"`
		Status       *incidentStatus `json:"status"`
	} `json:"incidents"`
}

type lineageResponse struct {
	Relationships []struct {
		Entity struct {
			URN  string `json:"urn"`
			Type string `json:"type"`
		} `json:"entity"`
	} `json:"relationships"`
}

type datasetResponse struct {
	Dataset *struct {
		Status *struct {
			Removed bool `json:"removed"`
		} `json:"status"`
		Properties         *propertiesResponse `json:"properties"`
		EditableProperties *struct {
			Description string `json:"description"`
		} `json:"editableProperties"`
		Tags          *tagsResponse      `json:"tags"`
		GlossaryTerms *termsResponse     `json:"glossaryTerms"`
		Ownership     *ownershipResponse `json:"ownership"`
		Domain        *struct {
			Domain struct {
				URN string `json:"urn"`
			} `json:"domain"`
		} `json:"domain"`
		InstitutionalMemory  *linksResponse      `json:"institutionalMemory"`
		StructuredProperties *structuredResponse `json:"structuredProperties"`
		SchemaMetadata       *struct {
			Fields []fieldResponse `json:"fields"`
		} `json:"schemaMetadata"`
		EditableSchemaMetadata *struct {
			EditableSchemaFieldInfo []fieldResponse `json:"editableSchemaFieldInfo"`
		} `json:"editableSchemaMetadata"`
		Incidents *incidentsResponse `json:"incidents"`
		Upstream  *lineageResponse   `json:"upstream"`
	} `json:"dataset"`
}

// Dataset reads one dataset's curation.
func (d *DataHub) Dataset(ctx context.Context, urn string) (*DatasetRecord, error) {
	var resp datasetResponse
	if err := d.gql.Execute(ctx, datasetQuery, map[string]any{"urn": urn}, &resp); err != nil {
		return nil, fmt.Errorf("reading %s: %w", urn, err)
	}
	if resp.Dataset == nil {
		return nil, fmt.Errorf("reading %s: DataHub returned no dataset", urn)
	}
	ds := resp.Dataset
	rec := &DatasetRecord{URN: urn, Columns: map[string]catalog.Aspects{}}
	rec.Removed = ds.Status != nil && ds.Status.Removed
	rec.Aspects = datasetAspects(&resp)
	queries, err := d.queries(ctx, urn)
	if err != nil {
		return nil, err
	}
	rec.Aspects.CuratedQueries = queries
	docs, err := d.gql.GetContextDocuments(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("reading the context documents of %s: %w", urn, err)
	}
	rec.Aspects.ContextDocuments = contextDocuments(docs)
	readColumns(&resp, rec.Columns)
	rec.Upstreams = upstreams(ds.Upstream)
	return rec, nil
}

// readColumns adds the curation of each field to columns, the edited schema
// over the ingested one.
func readColumns(resp *datasetResponse, columns map[string]catalog.Aspects) {
	ds := resp.Dataset
	if ds.SchemaMetadata != nil {
		mergeFields(columns, ds.SchemaMetadata.Fields)
	}
	if ds.EditableSchemaMetadata != nil {
		mergeFields(columns, ds.EditableSchemaMetadata.EditableSchemaFieldInfo)
	}
}

// upstreams are the datasets a lineage answer names.
func upstreams(l *lineageResponse) []string {
	var out []string
	if l == nil {
		return out
	}
	for _, r := range l.Relationships {
		if r.Entity.Type == "DATASET" {
			out = append(out, r.Entity.URN)
		}
	}
	return out
}

// datasetAspects reads the dataset-level aspects of a response.
func datasetAspects(resp *datasetResponse) catalog.Aspects {
	ds := resp.Dataset
	var a catalog.Aspects
	if ds.Properties != nil {
		a.Description = ds.Properties.Description
		a.CustomProperties = customProperties(ds.Properties)
	}
	if ds.EditableProperties != nil && strings.TrimSpace(ds.EditableProperties.Description) != "" {
		a.Description = ds.EditableProperties.Description
	}
	a.Tags = tagNames(ds.Tags)
	a.GlossaryTerms = termURNs(ds.GlossaryTerms)
	a.Owners = owners(ds.Ownership)
	if ds.Domain != nil {
		a.Domain = ds.Domain.Domain.URN
	}
	a.DocumentationLinks = links(ds.InstitutionalMemory)
	a.StructuredProperties = structuredProperties(ds.StructuredProperties)
	a.Incidents = incidents(ds.Incidents)
	return a
}

// customProperties reads a dataset's custom properties, or nil for none.
func customProperties(p *propertiesResponse) map[string]string {
	if len(p.CustomProperties) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.CustomProperties))
	for _, kv := range p.CustomProperties {
		out[kv.Key] = kv.Value
	}
	return out
}

// structuredProperties reads structured properties by their qualified name,
// or nil for none.
func structuredProperties(sp *structuredResponse) map[string][]any {
	if sp == nil || len(sp.Properties) == 0 {
		return nil
	}
	out := make(map[string][]any, len(sp.Properties))
	for _, p := range sp.Properties {
		name := strings.TrimPrefix(p.StructuredProperty.URN, "urn:li:structuredProperty:")
		if p.StructuredProperty.Definition != nil && p.StructuredProperty.Definition.QualifiedName != "" {
			name = p.StructuredProperty.Definition.QualifiedName
		}
		values := make([]any, 0, len(p.Values))
		for _, v := range p.Values {
			switch {
			case v.StringValue != nil:
				values = append(values, *v.StringValue)
			case v.NumberValue != nil:
				values = append(values, *v.NumberValue)
			}
		}
		out[name] = values
	}
	return out
}

// incidents reads a dataset's incidents.
func incidents(in *incidentsResponse) []catalog.Incident {
	var out []catalog.Incident
	if in == nil {
		return out
	}
	for _, x := range in.Incidents {
		out = append(out, incident(x.URN, x.IncidentType, x.Title, x.Description, x.Status))
	}
	return out
}

// incident reads a DataHub incident as the catalog's.
func incident(urn, typ, title, description string, status *incidentStatus) catalog.Incident {
	in := catalog.Incident{
		ID: strings.TrimPrefix(urn, "urn:li:incident:"), Type: strings.ToLower(typ),
		Title: title, Description: description, State: catalog.IncidentActive,
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = "Incident " + in.ID
	}
	if status != nil && strings.EqualFold(status.State, "RESOLVED") {
		in.State, in.Resolution = catalog.IncidentResolved, status.Message
	}
	return in
}

// queries reads a dataset's curated queries.
func (d *DataHub) queries(ctx context.Context, urn string) ([]catalog.CuratedQuery, error) {
	var resp struct {
		ListQueries *struct {
			Queries []struct {
				URN        string `json:"urn"`
				Properties *struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Statement   *struct {
						Value string `json:"value"`
					} `json:"statement"`
				} `json:"properties"`
			} `json:"queries"`
		} `json:"listQueries"`
	}
	if err := d.gql.Execute(ctx, queriesQuery, map[string]any{"urn": urn}, &resp); err != nil {
		return nil, fmt.Errorf("reading the queries of %s: %w", urn, err)
	}
	var out []catalog.CuratedQuery
	if resp.ListQueries == nil {
		return out, nil
	}
	for _, q := range resp.ListQueries.Queries {
		if q.Properties == nil || q.Properties.Statement == nil || strings.TrimSpace(q.Properties.Statement.Value) == "" {
			continue
		}
		name := q.Properties.Name
		if strings.TrimSpace(name) == "" {
			name = strings.TrimPrefix(q.URN, "urn:li:query:")
		}
		out = append(out, catalog.CuratedQuery{Name: name, SQL: q.Properties.Statement.Value, Description: q.Properties.Description})
	}
	return out, nil
}

// contextDocuments reads DataHub's context documents as the catalog's.
func contextDocuments(docs []types.ContextDocument) []catalog.ContextDocument {
	var out []catalog.ContextDocument
	for _, d := range docs {
		if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Title) == "" {
			continue
		}
		out = append(out, catalog.ContextDocument{
			ID: d.ID, Title: d.Title, Content: d.Content, ContentType: d.ContentType, Category: d.Category,
		})
	}
	return out
}

// mergeFields adds the curation of each top-level field to columns. A later
// field record's description replaces an earlier one's, which is how DataHub
// layers the edited schema over the ingested one, and its tags and terms are
// added.
func mergeFields(columns map[string]catalog.Aspects, fields []fieldResponse) {
	for _, f := range fields {
		name, ok := topLevelField(f.FieldPath)
		if !ok {
			continue
		}
		a := columns[name]
		if strings.TrimSpace(f.Description) != "" {
			a.Description = f.Description
		}
		a.Tags = union(a.Tags, tagNames(f.Tags))
		a.GlossaryTerms = union(a.GlossaryTerms, termURNs(f.GlossaryTerms))
		columns[name] = a
	}
}

// topLevelField reads a DataHub field path as a column name. A version 2
// path carries [version=...] and [type=...] segments, which are dropped; a
// path that still has a dot names a nested field, which no catalog column is.
func topLevelField(fieldPath string) (string, bool) {
	var kept []string
	for p := range strings.SplitSeq(fieldAnnotation.ReplaceAllString(fieldPath, ""), ".") {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) != 1 {
		return "", false
	}
	return kept[0], true
}

// fieldAnnotation is a [version=...] or [type=...] segment of a version 2
// field path, which may itself hold a dot.
var fieldAnnotation = regexp.MustCompile(`\[[^\]]*\]`)

// tagNames reads tag associations as tag names.
func tagNames(t *tagsResponse) []string {
	var out []string
	if t == nil {
		return out
	}
	for _, x := range t.Tags {
		out = append(out, strings.TrimPrefix(x.Tag.URN, "urn:li:tag:"))
	}
	return out
}

// termURNs reads glossary term associations as their URNs.
func termURNs(t *termsResponse) []string {
	var out []string
	if t == nil {
		return out
	}
	for _, x := range t.Terms {
		out = append(out, x.Term.URN)
	}
	return out
}

// owners reads DataHub ownership as the catalog's owners: the user name or
// group name, and the ownership type in lower case.
func owners(o *ownershipResponse) []catalog.Owner {
	var out []catalog.Owner
	if o == nil {
		return out
	}
	for _, x := range o.Owners {
		who := x.Owner.Username
		if who == "" {
			who = x.Owner.Name
		}
		if who == "" {
			who = x.Owner.URN[strings.LastIndex(x.Owner.URN, ":")+1:]
		}
		typ := strings.ToLower(x.Type)
		if x.OwnershipType != nil && x.OwnershipType.Info != nil && x.OwnershipType.Info.Name != "" {
			typ = strings.ToLower(x.OwnershipType.Info.Name)
		}
		out = append(out, catalog.Owner{Owner: who, Type: typ})
	}
	return out
}

// links reads institutional memory as documentation links.
func links(l *linksResponse) []catalog.Link {
	var out []catalog.Link
	if l == nil {
		return out
	}
	for _, e := range l.Elements {
		if strings.TrimSpace(e.URL) != "" {
			out = append(out, catalog.Link{URL: e.URL, Description: e.Description})
		}
	}
	return out
}

// union appends the values of b not already in a.
func union(a, b []string) []string {
	for _, v := range b {
		if !slices.Contains(a, v) {
			a = append(a, v)
		}
	}
	return a
}
