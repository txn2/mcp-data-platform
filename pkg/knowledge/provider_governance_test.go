package knowledge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

func governanceFixture() *fakeCatalog {
	return &fakeCatalog{
		definitions: map[string]*catalog.Definition{
			"mcp:glossary_term:revenue": {
				Reference: "mcp:glossary_term:t1", Kind: catalog.KindGlossaryTerm, Name: "Revenue",
				Parent: "mcp:glossary_node:n1",
				Aspects: catalog.Aspects{
					Description:      "Money booked from sales.",
					Owners:           []catalog.Owner{{Owner: "ana@example.com", Type: "business_owner"}},
					CustomProperties: map[string]string{"source": "finance"},
				},
			},
			"mcp:glossary_node:finance": {
				Reference: "mcp:glossary_node:n1", Kind: catalog.KindGlossaryNode, Name: "Finance",
			},
		},
		summaries: []catalog.DefinitionSummary{
			{Reference: "mcp:glossary_node:n1", Kind: catalog.KindGlossaryNode, Slug: "finance", Name: "Finance", Children: 1},
			{
				Reference: "mcp:glossary_term:t1", Kind: catalog.KindGlossaryTerm, Slug: "revenue", Name: "Revenue",
				Description: "Money booked from sales.", Parent: "mcp:glossary_node:n1",
			},
			{Reference: "mcp:tag:pii", Kind: catalog.KindTag, Slug: "pii", Name: "pii"},
		},
		uses: map[string][]catalog.Use{
			"mcp:glossary_term:revenue": {
				{
					Reference: "mcp:column:(trino,warehouse,hive.sales.orders,amount)", Dataset: "mcp:dataset:(trino,warehouse,hive.sales.orders)",
					Name: "orders", Column: "amount", Connection: "warehouse",
				},
				{
					Reference: "mcp:dataset:(trino,payroll,hr.staff.pay)", Dataset: "mcp:dataset:(trino,payroll,hr.staff.pay)",
					Name: "pay", Connection: "payroll",
				},
			},
		},
	}
}

func TestGovernanceProvider_Metadata(t *testing.T) {
	p := NewGovernanceProvider(governanceFixture(), nil)
	assert.Equal(t, SourceGovernance, p.Name())
	assert.Equal(t, ScopeShared, p.Scope())
}

func TestGovernanceProvider_SearchRanksByTheWordsAQuestionShares(t *testing.T) {
	store := governanceFixture()
	p := NewGovernanceProvider(store, nil)
	hits, err := p.Search(context.Background(), Query{Intent: "what does revenue mean", Limit: 5})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, Hit{
		Text: "glossary term: Revenue\nMoney booked from sales.", Source: SourceGovernance,
		Ref: "mcp:glossary_term:t1", Score: hits[0].Score, Reference: "mcp:glossary_term:t1",
	}, hits[0])
	assert.Equal(t, vocabularyPageLimit, store.gotQuery.Limit, "the whole vocabulary is ranked, not a page of it")

	hits, err = p.Search(context.Background(), Query{Intent: "finance pii", Limit: 1})
	require.NoError(t, err)
	require.Len(t, hits, 1, "the limit caps the hits")

	hits, err = p.Search(context.Background(), Query{Intent: "pii"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "tag: pii", hits[0].Text, "a definition with no description is its heading")

	hits, err = p.Search(context.Background(), Query{EntityURNs: []string{"mcp:tag:pii"}})
	require.NoError(t, err)
	assert.Nil(t, hits, "a definition is reached by fetch, not the entity path")

	store.listErr = errors.New("boom")
	_, err = p.Search(context.Background(), Query{Intent: "revenue"})
	require.Error(t, err)
}

func TestGovernanceProvider_FetchATermListsWhatCarriesIt(t *testing.T) {
	p := NewGovernanceProvider(governanceFixture(), nil)
	caller, gate := scopedCaller(stubScope{allowed: map[string]bool{"warehouse": true}})
	doc, owned, err := p.Fetch(context.Background(), "mcp:glossary_term:revenue", caller)
	require.True(t, owned)
	require.NoError(t, err)
	assert.Equal(t, "mcp:glossary_term:t1", doc.Reference)
	assert.Equal(t, "Money booked from sales.", doc.Body)
	e, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok)
	assert.Equal(t, "mcp:glossary_node:n1", e.Parent)
	assert.Equal(t, "ana@example.com", e.Owners[0].Owner)
	assert.Equal(t, "finance", e.CustomProperties["source"])
	require.Len(t, e.Carriers, 1, "the carrier on a denied connection is withheld")
	assert.Equal(t, "amount", e.Carriers[0].Column)
	assert.Equal(t, 1, e.CarriersWithheld)
	assert.Contains(t, e.Notice, "1 result is hidden")
	assert.Zero(t, gate.withheld, "a withheld carrier is reported inside the record, not as a withheld hit")
	assert.False(t, e.MoreCarriers)
}

func TestGovernanceProvider_FetchANodeListsItsChildren(t *testing.T) {
	p := NewGovernanceProvider(governanceFixture(), nil)
	doc, _, err := p.Fetch(context.Background(), "mcp:glossary_node:finance", Caller{})
	require.NoError(t, err)
	e, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok, "content is %T", doc.Content)
	require.Len(t, e.Children, 1)
	assert.Equal(t, "Revenue", e.Children[0].Name)
	assert.Empty(t, e.Carriers)
}

func TestGovernanceProvider_FetchBoundsTheCarriers(t *testing.T) {
	store := governanceFixture()
	many := make([]catalog.Use, carrierLimit+3)
	for i := range many {
		many[i] = catalog.Use{Dataset: "mcp:dataset:(trino,warehouse,a.b.c)", Connection: "warehouse"}
	}
	store.uses["mcp:glossary_term:revenue"] = many
	var asked [2]string
	p := NewGovernanceProvider(store, func(kind, instance string) string {
		asked = [2]string{kind, instance}
		return instance
	})
	doc, _, err := p.Fetch(context.Background(), "mcp:glossary_term:revenue", Caller{})
	require.NoError(t, err)
	e, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok, "content is %T", doc.Content)
	assert.Len(t, e.Carriers, carrierLimit)
	assert.True(t, e.MoreCarriers)
	assert.Equal(t, [2]string{"trino", "warehouse"}, asked)
}

func TestGovernanceProvider_FetchDeclinesAndMisses(t *testing.T) {
	store := governanceFixture()
	p := NewGovernanceProvider(store, nil)
	for _, ref := range []string{"mcp:dataset:(trino,w,a.b.c)", "mcp:asset:1", "not a reference"} {
		_, owned, err := p.Fetch(context.Background(), ref, Caller{})
		assert.False(t, owned, ref)
		assert.NoError(t, err, ref)
	}
	_, owned, err := p.Fetch(context.Background(), "mcp:domain:absent", Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)

	store.listErr = errors.New("list down")
	_, _, err = p.Fetch(context.Background(), "mcp:glossary_node:finance", Caller{})
	assert.ErrorIs(t, err, store.listErr)

	store.listErr = nil
	store.err = errors.New("down")
	_, _, err = p.Fetch(context.Background(), "mcp:glossary_term:revenue", Caller{})
	assert.ErrorIs(t, err, store.err)
}

func TestGovernanceBoundName(t *testing.T) {
	p := NewGovernanceProvider(nil, nil)
	assert.Empty(t, p.boundName("not a reference"))
	assert.Equal(t, "warehouse", p.boundName("mcp:dataset:(trino,warehouse,a.b.c)"))
}

// A definition is found by a word of the question wherever it falls in name
// order, not only within the first page of every definition.
func TestGovernanceSearchFindsADefinitionPastTheFirstPage(t *testing.T) {
	f := &fakeCatalog{}
	for i := range vocabularyPageLimit + 5 {
		f.summaries = append(f.summaries, catalog.DefinitionSummary{
			Reference: fmt.Sprintf("mcp:tag:a%04d", i), Kind: catalog.KindTag, Slug: fmt.Sprintf("a%04d", i), Name: fmt.Sprintf("a%04d", i),
		})
	}
	f.summaries = append(f.summaries, catalog.DefinitionSummary{
		Reference: "mcp:glossary_term:z", Kind: catalog.KindGlossaryTerm, Slug: "zebra-churn", Name: "Zebra churn",
	})
	hits, err := NewGovernanceProvider(f, nil).Search(context.Background(), Query{Intent: "zebra churn", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "mcp:glossary_term:z", hits[0].Ref)
}
