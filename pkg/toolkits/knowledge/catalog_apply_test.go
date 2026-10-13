package knowledge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var testDataset = catalog.DatasetRef("trino", "acme", "test", "table")

// applyOK applies changes to testEntityURN and returns the response and the
// changeset recorded.
func applyOK(t *testing.T, tk *Toolkit, csStore *spyChangesetStore, changes ...ApplyChange) (map[string]any, Changeset) {
	t.Helper()
	res, _, err := tk.handleApplyKnowledge(ctxWithUser("u", "s", "admin"), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: changes,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	return parseJSONResult(t, res), csStore.Changesets[len(csStore.Changesets)-1]
}

// rollbackOK rolls a changeset back through the tool.
func rollbackOK(t *testing.T, tk *Toolkit, id string) {
	t.Helper()
	res, _, err := tk.handleApplyKnowledge(ctxWithUser("u", "s", "admin"), nil, applyKnowledgeInput{
		Action: "rollback", ChangesetID: id,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
}

// Every catalog change type is written to the catalog, read back as written,
// and undone by a rollback, leaving the entity as it was.
func TestApplyToCatalog_EveryChangeTypeAppliesAndRollsBack(t *testing.T) {
	column := catalog.ColumnRef(testDataset, "amount")
	cases := []struct {
		name    string
		seed    []ApplyChange // applied first and kept
		changes []ApplyChange
		ref     catalog.Ref
		check   func(is *assert.Assertions, a catalog.Aspects)
	}{
		{
			name: "update_description", changes: []ApplyChange{{ChangeType: "update_description", Detail: "Orders"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) { is.Equal("Orders", a.Description) },
		},
		{
			name: "column description", changes: []ApplyChange{{ChangeType: "update_description", Target: "column:amount", Detail: "In cents"}},
			ref: column, check: func(is *assert.Assertions, a catalog.Aspects) { is.Equal("In cents", a.Description) },
		},
		{
			name: "add_tag", changes: []ApplyChange{{ChangeType: "add_tag", Detail: "urn:li:tag:pii"}, {ChangeType: "add_tag", Detail: "mcp:tag:finance"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) { is.Equal([]string{"pii", "finance"}, a.Tags) },
		},
		{
			name: "column tag", changes: []ApplyChange{{ChangeType: "add_tag", Target: "column:amount", Detail: "money"}},
			ref: column, check: func(is *assert.Assertions, a catalog.Aspects) { is.Equal([]string{"money"}, a.Tags) },
		},
		{
			name: "remove_tag", seed: []ApplyChange{{ChangeType: "add_tag", Detail: "stale"}},
			changes: []ApplyChange{{ChangeType: "remove_tag", Detail: "stale"}},
			ref:     testDataset, check: func(is *assert.Assertions, a catalog.Aspects) { is.Empty(a.Tags) },
		},
		{
			name: "add_glossary_term", changes: []ApplyChange{{ChangeType: "add_glossary_term", Detail: "Net Revenue"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Len(a.GlossaryTerms, 1)
				is.Contains(a.GlossaryTerms[0], "mcp:glossary_term:")
			},
		},
		{
			name: "flag_quality_issue", changes: []ApplyChange{{ChangeType: "flag_quality_issue", Detail: "nulls in amount"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Equal([]string{qualityIssueTag}, a.Tags)
				is.Len(a.Incidents, 1)
				is.Equal(qualityIssueIncidentTitle, a.Incidents[0].Title)
				is.Equal(catalog.IncidentActive, a.Incidents[0].State)
			},
		},
		{
			name: "add_documentation", changes: []ApplyChange{{ChangeType: "add_documentation", Target: "https://wiki/orders", Detail: "Runbook"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Equal([]catalog.Link{{URL: "https://wiki/orders", Description: "Runbook"}}, a.DocumentationLinks)
			},
		},
		{
			name: "add_curated_query", changes: []ApplyChange{{ChangeType: "add_curated_query", Detail: "Daily", QuerySQL: "SELECT 1", QueryDescription: "d"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Equal([]catalog.CuratedQuery{{Name: "Daily", SQL: "SELECT 1", Description: "d"}}, a.CuratedQueries)
			},
		},
		{
			name: "set_structured_property", changes: []ApplyChange{{ChangeType: "set_structured_property", Target: "urn:li:structuredProperty:retention", Detail: "[90]"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Equal(map[string][]any{"retention": {float64(90)}}, a.StructuredProperties)
			},
		},
		{
			name: "remove_structured_property", seed: []ApplyChange{{ChangeType: "set_structured_property", Target: "retention", Detail: "90"}},
			changes: []ApplyChange{{ChangeType: "remove_structured_property", Target: "retention"}},
			ref:     testDataset, check: func(is *assert.Assertions, a catalog.Aspects) { is.Empty(a.StructuredProperties) },
		},
		{
			name: "raise_incident", changes: []ApplyChange{{ChangeType: "raise_incident", Target: "Late load", Detail: "06:00 load missed"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Len(a.Incidents, 1)
				is.Equal("Late load", a.Incidents[0].Title)
			},
		},
		{
			name: "set_custom_property", changes: []ApplyChange{{ChangeType: "set_custom_property", Target: "owner_team", Detail: "finance"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Equal(map[string]string{"owner_team": "finance"}, a.CustomProperties)
			},
		},
		{
			name: "remove_custom_property", seed: []ApplyChange{{ChangeType: "set_custom_property", Target: "k", Detail: "v"}},
			changes: []ApplyChange{{ChangeType: "remove_custom_property", Target: "k"}},
			ref:     testDataset, check: func(is *assert.Assertions, a catalog.Aspects) { is.Empty(a.CustomProperties) },
		},
		{
			name: "add_context_document", changes: []ApplyChange{{ChangeType: "add_context_document", Target: "Loading", Detail: "Nightly", QueryDescription: "ops"}},
			ref: testDataset, check: func(is *assert.Assertions, a catalog.Aspects) {
				is.Len(a.ContextDocuments, 1)
				is.Equal("Loading", a.ContextDocuments[0].Title)
				is.Equal("ops", a.ContextDocuments[0].Category)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			csStore := &spyChangesetStore{}
			cat := testCatalog()
			tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
			if len(tc.seed) > 0 {
				applyOK(t, tk, csStore, tc.seed...)
			}
			before := cat.Aspects(tc.ref)

			resp, cs := applyOK(t, tk, csStore, tc.changes...)
			assert.Equal(t, true, resp["revertible"])
			assert.Equal(t, testDatasetRef, cs.TargetURN)
			tc.check(assert.New(t), cat.Aspects(tc.ref))

			rollbackOK(t, tk, cs.ID)
			assert.Equal(t, before, cat.Aspects(tc.ref), "rollback restores the entity")
		})
	}
}

// resolve_incident, update_context_document and remove_context_document act on
// an entry an earlier apply created, by the id that apply reported.
func TestApplyToCatalog_EditsByCreatedID(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)

	resp, _ := applyOK(t, tk, csStore,
		ApplyChange{ChangeType: "raise_incident", Target: "Late", Detail: "missed"},
		ApplyChange{ChangeType: "add_context_document", Target: "Loading", Detail: "v1"})
	ids, ok := resp["created_ids"].([]any)
	require.True(t, ok)
	require.Len(t, ids, 2)
	incidentID, docID := text(t, ids[0]), text(t, ids[1])

	_, cs := applyOK(t, tk, csStore,
		ApplyChange{ChangeType: "resolve_incident", Target: incidentID, Detail: "backfilled"},
		ApplyChange{ChangeType: "update_context_document", Target: docID, Detail: "v2", QuerySQL: "Loading (nightly)"})
	a := cat.Aspects(testDataset)
	assert.Equal(t, catalog.IncidentResolved, a.Incidents[0].State)
	assert.Equal(t, "backfilled", a.Incidents[0].Resolution)
	assert.Equal(t, "v2", a.ContextDocuments[0].Content)
	assert.Equal(t, "Loading (nightly)", a.ContextDocuments[0].Title)

	rollbackOK(t, tk, cs.ID)
	a = cat.Aspects(testDataset)
	assert.Equal(t, catalog.IncidentActive, a.Incidents[0].State)
	assert.Equal(t, "v1", a.ContextDocuments[0].Content)

	applyOK(t, tk, csStore, ApplyChange{ChangeType: "remove_context_document", Target: docID})
	assert.Empty(t, cat.Aspects(testDataset).ContextDocuments)
}

// A change that cannot apply writes nothing, including the changes before it.
func TestApplyToCatalog_FailureWritesNothing(t *testing.T) {
	cases := []struct {
		name   string
		change ApplyChange
		want   string
	}{
		{"unknown incident", ApplyChange{ChangeType: "resolve_incident", Target: "nope"}, "no incident"},
		{"unknown document", ApplyChange{ChangeType: "update_context_document", Target: "nope"}, "no context document"},
		{"remove unknown document", ApplyChange{ChangeType: "remove_context_document", Target: "nope"}, "no context document"},
		{"unknown column", ApplyChange{ChangeType: "update_description", Target: "column:missing", Detail: "x"}, "holds no entity"},
		{"documentation without url", ApplyChange{ChangeType: "add_documentation", Detail: "x"}, "link url"},
		{"query without name", ApplyChange{ChangeType: "add_curated_query", QuerySQL: "SELECT 1"}, "query name"},
		{"incident without title", ApplyChange{ChangeType: "raise_incident", Detail: "x"}, "incident title"},
		{"property without name", ApplyChange{ChangeType: "set_structured_property", Detail: "1"}, "property name"},
		{"bad property values", ApplyChange{ChangeType: "set_structured_property", Target: "p", Detail: "[1,"}, "parsing property values"},
		{"tag without name", ApplyChange{ChangeType: "add_tag", Detail: " "}, "name the tag"},
		{"term without name", ApplyChange{ChangeType: "add_glossary_term"}, "name the glossary term"},
		{"unknown term reference", ApplyChange{ChangeType: "add_glossary_term", Detail: "mcp:glossary_term:nope"}, "holds no entity"},
		{"delete_tag on a dataset", ApplyChange{ChangeType: "delete_tag"}, "needs a tag entity_urn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			csStore := &spyChangesetStore{}
			cat := testCatalog()
			tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
			res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
				Action: "apply", EntityURN: testEntityURN,
				Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "first"}, tc.change},
			})
			require.NoError(t, err)
			require.True(t, res.IsError)
			assert.Contains(t, resultText(res), tc.want)
			assert.Contains(t, resultText(res), "nothing was written")
			assert.Empty(t, cat.Aspects(testDataset).Tags)
			assert.Empty(t, csStore.Changesets)
		})
	}
}

func TestApplyToCatalog_StoreFailureWritesNothing(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	cat.FailWrite = errors.New("disk full")
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "disk full")
	assert.Empty(t, csStore.Changesets)
}

// An entity the catalog does not hold, a reference that does not parse and a
// target in no form the catalog reads are refused by name.
func TestApplyToCatalog_Targets(t *testing.T) {
	cases := map[string]string{
		"urn:li:dataset:(urn:li:dataPlatform:trino,nowhere.t,PROD)": "holds no entity",
		"mcp:dataset:(trino,acme)":                                  "entity_urn",
		"orders":                                                    "is not a catalog reference",
		"urn:li:dataset:broken":                                     "entity_urn",
	}
	for target, want := range cases {
		t.Run(target, func(t *testing.T) {
			tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, testCatalog())
			res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
				Action: "apply", EntityURN: target, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
			})
			require.NoError(t, err)
			require.True(t, res.IsError)
			assert.Contains(t, resultText(res), want)
		})
	}
}

// A definition is curated through its mcp: reference or the DataHub URN it
// was imported from; a definition carries no tags.
func TestApplyToCatalog_Definitions(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	term, err := cat.PutDefinition(context.Background(), catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Net Revenue"})
	require.NoError(t, err)
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)

	for _, target := range []string{term.Reference, "urn:li:glossaryTerm:Net Revenue"} {
		res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
			Action: "apply", EntityURN: target,
			Changes: []ApplyChange{{ChangeType: "update_description", Detail: "Revenue after refunds"}},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, resultText(res))
	}
	got, err := cat.GetDefinition(context.Background(), catalog.KeyRef(catalog.KindGlossaryTerm, term.ID))
	require.NoError(t, err)
	assert.Equal(t, "Revenue after refunds", got.Aspects.Description)

	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: term.Reference, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not allowed")
}

// add_glossary_term names an existing term by name or reference and creates a
// term the catalog does not hold; a second apply reuses it.
func TestApplyToCatalog_GlossaryTermCreatedOnce(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	resp, _ := applyOK(t, tk, csStore, ApplyChange{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:Revenue"})
	created, ok := resp["created_ids"].([]any)
	require.True(t, ok)
	require.Len(t, created, 1)

	resp, _ = applyOK(t, tk, csStore, ApplyChange{ChangeType: "add_glossary_term", Target: "column:amount", Detail: text(t, created[0])})
	assert.Nil(t, resp["created_ids"], "an existing term is not created again")
	assert.Equal(t, []string{text(t, created[0])}, cat.Aspects(catalog.ColumnRef(testDataset, "amount")).GlossaryTerms)
}

// flag_quality_issue raises one incident per distinct open issue.
func TestApplyToCatalog_QualityIssueDeduplicated(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "flag_quality_issue", Detail: "nulls"})
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "flag_quality_issue", Detail: "nulls"})
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "flag_quality_issue"})
	a := cat.Aspects(testDataset)
	assert.Len(t, a.Incidents, 1)
	assert.Equal(t, []string{qualityIssueTag}, a.Tags)
}

// delete_tag takes the tag off every dataset and column carrying it, and the
// rollback puts it back on all of them.
func TestApplyToCatalog_DeleteTag(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	other := cat.AddDataset("acme", "test.other")
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "legacy"},
		ApplyChange{ChangeType: "add_tag", Target: "column:amount", Detail: "legacy"})
	_, err := cat.WriteAspect(context.Background(), other, catalog.AspectTags, []byte(`["legacy","keep"]`), "seed")
	require.NoError(t, err)

	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: "urn:li:tag:legacy", Changes: []ApplyChange{{ChangeType: "delete_tag"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	m := parseJSONResult(t, res)
	assert.Equal(t, "mcp:tag:legacy", m["entity_urn"])
	assert.Empty(t, cat.Aspects(testDataset).Tags)
	assert.Empty(t, cat.Aspects(catalog.ColumnRef(testDataset, "amount")).Tags)
	assert.Equal(t, []string{"keep"}, cat.Aspects(other).Tags)

	rollbackOK(t, tk, text(t, m["changeset_id"]))
	assert.Equal(t, []string{"legacy"}, cat.Aspects(testDataset).Tags)
	assert.Equal(t, []string{"legacy", "keep"}, cat.Aspects(other).Tags)
}

func TestApplyToCatalog_NoCatalog(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, nil)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no platform catalog")

	res, _, err = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "x"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Nil(t, tk.currentMetadata(context.Background(), testEntityURN))
}

func TestApplyToCatalog_UnknownSink(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, testCatalog())
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "apply", Sink: "datahub"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "valid: catalog, knowledge_page, agent_instructions")
}

// The changeset records the entity's state before the apply and the response
// reports it after, so a caller sees what persisted without another call.
func TestApplyToCatalog_RecordsBeforeAndAfter(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	cat.SetDescription(testDatasetRef, "old")
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", Status: StatusApproved}}}
	tk := newApplyToolkit(t, store, csStore, cat)
	res, _, err := tk.handleApplyKnowledge(ctxWithUser("u", "s", "admin"), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, InsightIDs: []string{"i1"},
		Changes: []ApplyChange{{ChangeType: "update_description", Detail: "new"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	m := parseJSONResult(t, res)
	cs := csStore.Changesets[0]
	assert.Equal(t, "old", cs.PreviousValue["description"])
	assert.Len(t, cs.NewValue[fieldCatalogChanges], 1)
	state, ok := m["resulting_state"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "new", state["description"])
	assert.Equal(t, StatusApplied, store.Insights[0].Status)
	assert.Contains(t, m["message"], "action=rollback changeset_id="+cs.ID)
}

// A changeset recording failure after the write is reported as an error.
func TestApplyToCatalog_ChangesetStoreError(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{InsertErr: errStub}, testCatalog())
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "failed to record changeset")
}

// The confirmation round trip happens before anything is read or written.
func TestApplyToCatalog_RequiresConfirmation(t *testing.T) {
	cat := testCatalog()
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, cat)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}},
	})
	require.NoError(t, err)
	assert.Equal(t, true, parseJSONResult(t, res)["confirmation_required"])
	assert.Empty(t, cat.Aspects(testDataset).Tags)
}

// bulk_untag clears a tag everywhere in one changeset that a rollback undoes.
func TestBulkUntag_RemovesEverywhereAndRollsBack(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	other := cat.AddDataset("acme", "test.other")
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "old"},
		ApplyChange{ChangeType: "add_tag", Target: "column:amount", Detail: "old"})
	_, err := cat.WriteAspect(context.Background(), other, catalog.AspectTags, []byte(`["old"]`), "seed")
	require.NoError(t, err)

	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "urn:li:tag:old"})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	m := parseJSONResult(t, res)
	assert.EqualValues(t, 3, m["entities_untagged"])
	assert.Equal(t, true, m["revertible"])
	assert.Nil(t, m["truncated"])
	assert.Empty(t, cat.Aspects(other).Tags)

	rollbackOK(t, tk, text(t, m["changeset_id"]))
	assert.Equal(t, []string{"old"}, cat.Aspects(other).Tags)
	assert.Equal(t, []string{"old"}, cat.Aspects(catalog.ColumnRef(testDataset, "amount")).Tags)
}

func TestBulkUntag_NothingTaggedAndConfirmation(t *testing.T) {
	cat := testCatalog()
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, cat)

	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "none"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, parseJSONResult(t, res)["entities_untagged"])

	_, err = cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags, []byte(`["x"]`), "seed")
	require.NoError(t, err)
	res, _, err = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "x"})
	require.NoError(t, err)
	m := parseJSONResult(t, res)
	assert.Equal(t, true, m["confirmation_required"])
	assert.EqualValues(t, 1, m["entities_found"])
	assert.Equal(t, []string{"x"}, cat.Aspects(testDataset).Tags, "nothing removed before confirmation")

	res, _, err = tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestBulkUntag_ChangesetInsertFailureIsError(t *testing.T) {
	cat := testCatalog()
	_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags, []byte(`["x"]`), "seed")
	require.NoError(t, err)
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{InsertErr: errStub}, cat)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "bulk_untag", TagURN: "x"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "FAILED to record the changeset")
}

func TestSampleURNs(t *testing.T) {
	many := make([]string, bulkUntagURNSample+5)
	assert.Len(t, sampleURNs(many), bulkUntagURNSample)
	assert.Len(t, sampleURNs(many[:3]), 3)
}

// A promoted statement becomes a curated query on each dataset it reads that
// the catalog holds.
func TestCatalogQueryWriter(t *testing.T) {
	assert.Nil(t, NewCatalogQueryWriter(nil))
	cat := testCatalog()
	other := cat.AddDataset("acme", "test.other")
	w := NewCatalogQueryWriter(cat)
	ref, err := w.CreateCuratedQuery(context.Background(),
		[]string{testDatasetRef, other.String(), "mcp:dataset:(trino,acme,not.held)", "graphql:x:y"},
		"Join", "SELECT 1", "why")
	require.NoError(t, err)
	assert.Equal(t, testDatasetRef, ref)
	want := []catalog.CuratedQuery{{Name: "Join", SQL: "SELECT 1", Description: "why"}}
	assert.Equal(t, want, cat.Aspects(testDataset).CuratedQueries)
	assert.Equal(t, want, cat.Aspects(other).CuratedQueries)

	_, err = w.CreateCuratedQuery(context.Background(), []string{"mcp:dataset:(trino,acme,not.held)"}, "n", "s", "")
	assert.ErrorIs(t, err, ErrNoQueryTarget)
}

// An apply of prompts alone touches no catalog entity and needs no catalog.
func TestApplyToCatalog_PromptOnly(t *testing.T) {
	pc := &spyPromptCreator{}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, nil)
	tk.SetPromptCreator(pc)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: "anything",
		Changes: []ApplyChange{{ChangeType: "add_prompt", Target: "p", Detail: "body"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	m := parseJSONResult(t, res)
	assert.Equal(t, false, m["revertible"])
	require.Len(t, csStore.Changesets, 1)
	revertible, blocking := changesetRevertibility(&csStore.Changesets[0])
	assert.False(t, revertible)
	assert.Equal(t, []string{"add_prompt"}, blocking)
}

// spyPromptCreator records the prompts an apply creates.
type spyPromptCreator struct {
	created []*prompt.Prompt
	err     error
}

func (s *spyPromptCreator) Create(_ context.Context, p *prompt.Prompt) error {
	if s.err != nil {
		return s.err
	}
	p.ID = "prompt-1"
	s.created = append(s.created, p)
	return nil
}

func (*spyPromptCreator) RegisterRuntimePrompt(*prompt.Prompt) {}

// A prompt that cannot be created after the catalog was written says so and
// names the changeset path back.
func TestApplyToCatalog_PromptFailureAfterCatalogWrite(t *testing.T) {
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, cat)
	tk.SetPromptCreator(&spyPromptCreator{err: errStub})
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN,
		Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "x"}, {ChangeType: "add_prompt", Target: "p", Detail: "b"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "catalog changes were written")
}

// text asserts a decoded JSON value is a string.
func text(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "%v is not a string", v)
	return s
}

// A write that lands between an apply's read and its write is kept: the
// store refuses the stale write and the apply is planned again on what is
// there now.
func TestApplyToCatalog_ConcurrentWriteIsNotLost(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	cat.SetBeforeWrite(func() {
		_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags, []byte(`["theirs"]`), "someone")
		require.NoError(t, err)
	})
	applyOK(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "mine"})
	assert.Equal(t, []string{"theirs", "mine"}, cat.Aspects(testDataset).Tags)
}

// An apply that keeps finding its read stale gives up and writes nothing.
func TestApplyToCatalog_StaleEveryTimeGivesUp(t *testing.T) {
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, cat)
	var interfere func()
	n := 0
	interfere = func() {
		n++
		_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags,
			fmt.Appendf(nil, `["t%d"]`, n), "someone")
		require.NoError(t, err)
		cat.SetBeforeWrite(interfere)
	}
	cat.SetBeforeWrite(interfere)
	res, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "mine"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "written since it was read")
	assert.Equal(t, 3, n)
	cat.SetBeforeWrite(nil)
}

// bulk_untag and a promoted query plan again on a stale read too.
func TestUntagAndPromotionRetryStaleReads(t *testing.T) {
	cat := testCatalog()
	_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags, []byte(`["old","keep"]`), "seed")
	require.NoError(t, err)
	cat.SetBeforeWrite(func() {
		_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectTags, []byte(`["old","keep","new"]`), "someone")
		require.NoError(t, err)
	})
	refs, _, err := curator{store: cat}.untag(context.Background(), "old", 10, "ana")
	require.NoError(t, err)
	assert.Equal(t, []string{testDatasetRef}, refs)
	assert.Equal(t, []string{"keep", "new"}, cat.Aspects(testDataset).Tags)

	cat.SetBeforeWrite(func() {
		_, err := cat.WriteAspect(context.Background(), testDataset, catalog.AspectCuratedQueries,
			[]byte(`[{"name":"theirs","sql":"SELECT 2"}]`), "someone")
		require.NoError(t, err)
	})
	_, err = NewCatalogQueryWriter(cat).CreateCuratedQuery(context.Background(), []string{testDatasetRef}, "mine", "SELECT 1", "")
	require.NoError(t, err)
	assert.Len(t, cat.Aspects(testDataset).CuratedQueries, 2)
}
