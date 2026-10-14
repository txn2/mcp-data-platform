package catalogimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// fakeGraphQL answers each query by the operation it names, from JSON the
// test supplies, the way DataHub's GraphQL endpoint answers.
type fakeGraphQL struct {
	answers map[string]func(vars map[string]any) string
	docs    []ContextDocument
	err     error
}

func (f *fakeGraphQL) Execute(_ context.Context, query string, vars map[string]any, result any) error {
	if f.err != nil {
		return f.err
	}
	for op, answer := range f.answers {
		if strings.Contains(query, "query "+op+"(") {
			if err := json.Unmarshal([]byte(answer(vars)), result); err != nil {
				return fmt.Errorf("decoding the fake answer: %w", err)
			}
			return nil
		}
	}
	return errors.New("unexpected query")
}

func (f *fakeGraphQL) GetContextDocuments(context.Context, string) ([]ContextDocument, error) {
	return f.docs, nil
}

// page answers a search with the URNs of one page of results.
func page(total int, urns ...string) string {
	results := make([]string, len(urns))
	for i, u := range urns {
		results[i] = `{"entity":{"urn":"` + u + `"}}`
	}
	return `{"searchAcrossEntities":{"total":` + itoa(total) + `,"searchResults":[` + strings.Join(results, ",") + `]}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestDatasetHeadsMarksRemovedRecords(t *testing.T) {
	gql := &fakeGraphQL{answers: map[string]func(map[string]any) string{
		"importSearch":  func(map[string]any) string { return page(2, ordersURN, ordersRemovedURN) },
		"importRemoved": func(map[string]any) string { return page(1, ordersRemovedURN) },
	}}
	heads, err := NewDataHub(gql).DatasetHeads(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []DatasetHead{{URN: ordersURN}, {URN: ordersRemovedURN, Removed: true}}, heads)

	gql.err = errors.New("down")
	_, err = NewDataHub(gql).DatasetHeads(context.Background())
	assert.ErrorContains(t, err, "down")
}

// A listing longer than a page is read a page at a time.
func TestListPages(t *testing.T) {
	gql := &fakeGraphQL{answers: map[string]func(map[string]any) string{
		"importSearch": func(v map[string]any) string {
			if v["start"] == 0 {
				return page(searchPage+1, make([]string, searchPage)...)
			}
			return page(searchPage+1, "last")
		},
	}}
	urns, err := NewDataHub(gql).list(context.Background(), searchQuery, "DATASET")
	require.NoError(t, err)
	assert.Len(t, urns, searchPage+1)
	assert.Equal(t, "last", urns[searchPage])
}

const datasetJSON = `{"dataset":{
  "status":{"removed":true},
  "properties":{"description":"ingested","customProperties":[{"key":"grain","value":"day"}]},
  "editableProperties":{"description":"edited"},
  "tags":{"tags":[{"tag":{"urn":"urn:li:tag:pii"}}]},
  "glossaryTerms":{"terms":[{"term":{"urn":"urn:li:glossaryTerm:t"}}]},
  "ownership":{"owners":[
    {"owner":{"urn":"urn:li:corpuser:ana","username":"ana"},"type":"DATAOWNER","ownershipType":{"info":{"name":"Technical Owner"}}},
    {"owner":{"urn":"urn:li:corpGroup:eng","name":"eng"},"type":"STEWARD"},
    {"owner":{"urn":"urn:li:corpuser:bo"},"type":"DATAOWNER"}]},
  "domain":{"domain":{"urn":"urn:li:domain:sales"}},
  "institutionalMemory":{"elements":[{"url":"https://wiki/orders","description":"Runbook"},{"url":" ","description":"blank"}]},
  "structuredProperties":{"properties":[
    {"structuredProperty":{"urn":"urn:li:structuredProperty:io.retention","definition":{"qualifiedName":"io.retention"}},"values":[{"numberValue":90},{"stringValue":"days"}]},
    {"structuredProperty":{"urn":"urn:li:structuredProperty:tier"},"values":[{"stringValue":"gold"}]}]},
  "schemaMetadata":{"fields":[
    {"fieldPath":"amount","description":"ingested amount","tags":{"tags":[{"tag":{"urn":"urn:li:tag:money"}}]}},
    {"fieldPath":"[version=2.0].[type=struct].address.[type=string].city","description":"nested"},
    {"fieldPath":"[version=2.0].[type=long].id","description":"key"}]},
  "editableSchemaMetadata":{"editableSchemaFieldInfo":[
    {"fieldPath":"amount","description":"edited amount","glossaryTerms":{"terms":[{"term":{"urn":"urn:li:glossaryTerm:t"}}]}}]},
  "incidents":{"incidents":[
    {"urn":"urn:li:incident:i1","incidentType":"OPERATIONAL","title":"Late","description":"d","status":{"state":"RESOLVED","message":"fixed"}},
    {"urn":"urn:li:incident:i2","incidentType":"FRESHNESS","title":"","status":{"state":"ACTIVE"}}]},
  "upstream":{"relationships":[{"entity":{"urn":"` + "urn:li:dataset:up" + `","type":"DATASET"}},{"entity":{"urn":"urn:li:dataJob:j","type":"DATA_JOB"}}]}
}}`

const queriesJSON = `{"listQueries":{"queries":[
  {"urn":"urn:li:query:q1","properties":{"name":"Daily","description":"per day","statement":{"value":"SELECT 1"}}},
  {"urn":"urn:li:query:q2","properties":{"name":"","statement":{"value":"SELECT 2"}}},
  {"urn":"urn:li:query:q3","properties":{"name":"empty","statement":{"value":" "}}}]}}`

// A dataset reads into the catalog's shapes: the edited description over the
// ingested one, owners by name and type, links, structured properties by
// qualified name, incidents with their state, queries, context documents,
// top-level fields only, and dataset upstreams only.
func TestDatasetReadsIntoCatalogShapes(t *testing.T) {
	gql := &fakeGraphQL{
		answers: map[string]func(map[string]any) string{
			"importDataset": func(map[string]any) string { return datasetJSON },
			"importQueries": func(map[string]any) string { return queriesJSON },
		},
		docs: []ContextDocument{{ID: "d1", Title: "Loading", Content: "nightly", Category: "ops"}, {ID: "", Title: "x"}},
	}
	rec, err := NewDataHub(gql).Dataset(context.Background(), ordersURN)
	require.NoError(t, err)
	a := rec.Aspects
	assert.True(t, rec.Removed)
	assert.Equal(t, "edited", a.Description)
	assert.Equal(t, map[string]string{"grain": "day"}, a.CustomProperties)
	assert.Equal(t, []string{"pii"}, a.Tags)
	assert.Equal(t, []string{"urn:li:glossaryTerm:t"}, a.GlossaryTerms)
	assert.Equal(t, []catalog.Owner{{Owner: "ana", Type: "technical owner"}, {Owner: "eng", Type: "steward"}, {Owner: "bo", Type: "dataowner"}}, a.Owners)
	assert.Equal(t, "urn:li:domain:sales", a.Domain)
	assert.Equal(t, []catalog.Link{{URL: "https://wiki/orders", Description: "Runbook"}}, a.DocumentationLinks)
	assert.Equal(t, map[string][]any{"io.retention": {float64(90), "days"}, "tier": {"gold"}}, a.StructuredProperties)
	require.Len(t, a.Incidents, 2)
	assert.Equal(t, catalog.Incident{ID: "i1", Type: "operational", Title: "Late", Description: "d", State: catalog.IncidentResolved, Resolution: "fixed"}, a.Incidents[0])
	assert.Equal(t, "Incident i2", a.Incidents[1].Title)
	assert.Equal(t, catalog.IncidentActive, a.Incidents[1].State)
	assert.Equal(t, []catalog.CuratedQuery{{Name: "Daily", SQL: "SELECT 1", Description: "per day"}, {Name: "q2", SQL: "SELECT 2"}}, a.CuratedQueries)
	assert.Equal(t, []catalog.ContextDocument{{ID: "d1", Title: "Loading", Content: "nightly", ContentType: "text/markdown", Category: "ops"}}, a.ContextDocuments)
	assert.Equal(t, map[string]catalog.Aspects{
		"amount": {Description: "edited amount", Tags: []string{"money"}, GlossaryTerms: []string{"urn:li:glossaryTerm:t"}},
		"id":     {Description: "key"},
	}, rec.Columns)
	assert.Equal(t, []string{"urn:li:dataset:up"}, rec.Upstreams)
}

func TestDatasetReadFailures(t *testing.T) {
	gql := &fakeGraphQL{answers: map[string]func(map[string]any) string{
		"importDataset": func(map[string]any) string { return `{"dataset":null}` },
	}}
	_, err := NewDataHub(gql).Dataset(context.Background(), ordersURN)
	assert.ErrorContains(t, err, "returned no dataset")

	gql.answers["importDataset"] = func(map[string]any) string { return `{"dataset":{}}` }
	_, err = NewDataHub(gql).Dataset(context.Background(), ordersURN)
	assert.Error(t, err, "the queries query is not answered")
}

// Definitions read nodes first, take a definition's name, description and
// parent, and fall back to its key for a name.
func TestDefinitions(t *testing.T) {
	gql := &fakeGraphQL{answers: map[string]func(map[string]any) string{
		"importLive": func(v map[string]any) string {
			types, _ := v["types"].([]string)
			switch types[0] {
			case "GLOSSARY_NODE":
				return page(1, nodeURN)
			case "GLOSSARY_TERM":
				return page(1, termURN)
			case "TAG":
				return page(1, "urn:li:tag:pii")
			}
			return page(0)
		},
		"importDefinition": func(v map[string]any) string {
			switch v["urn"] {
			case termURN:
				return `{"entity":{"properties":{"name":"Net revenue","description":"d"},"parentNodes":{"nodes":[{"urn":"` + nodeURN + `"}]},
				  "ownership":{"owners":[{"owner":{"urn":"urn:li:corpuser:ana","username":"ana"},"type":"DATAOWNER"}]},
				  "institutionalMemory":{"elements":[{"url":"https://wiki/t"}]}}}`
			case "urn:li:tag:pii":
				return `{"entity":null}`
			}
			return `{"entity":{"properties":{"name":"","description":"n"}}}`
		},
	}}
	defs, err := NewDataHub(gql).Definitions(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 3)
	assert.Equal(t, Definition{URN: nodeURN, Kind: catalog.KindGlossaryNode, Name: "finance", Aspects: catalog.Aspects{Description: "n"}}, defs[0])
	assert.Equal(t, "Net revenue", defs[1].Name)
	assert.Equal(t, nodeURN, defs[1].Parent)
	assert.Equal(t, []catalog.Owner{{Owner: "ana", Type: "dataowner"}}, defs[1].Aspects.Owners)
	assert.Equal(t, Definition{URN: "urn:li:tag:pii", Kind: catalog.KindTag, Name: "pii"}, defs[2])

	assert.Contains(t, definitionQuery(definitionKinds[2]), "tag(urn: $urn)")
	assert.NotContains(t, definitionQuery(definitionKinds[2]), "ownership", "a tag is read without ownership")
	assert.Equal(t, "plain", urnKey("plain"))
}

func TestTopLevelField(t *testing.T) {
	for path, want := range map[string]string{
		"amount": "amount", "[version=2.0].[type=long].id": "id",
	} {
		got, ok := topLevelField(path)
		assert.True(t, ok, path)
		assert.Equal(t, want, got)
	}
	for _, path := range []string{"a.b", "[version=2.0].[type=struct].a.[type=int].b", ""} {
		_, ok := topLevelField(path)
		assert.False(t, ok, path)
	}
}
