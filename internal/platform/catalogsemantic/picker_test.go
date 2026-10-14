package catalogsemantic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// listingReader is a fakeReader that also lists definitions, recording each
// query.
type listingReader struct {
	fakeReader
	defs    []catalog.DefinitionSummary
	queries []catalog.DefinitionQuery
	listErr error
}

func (l *listingReader) ListDefinitions(_ context.Context, q catalog.DefinitionQuery) ([]catalog.DefinitionSummary, error) {
	l.queries = append(l.queries, q)
	return l.defs, l.listErr
}

func TestPickerListsOneKindByText(t *testing.T) {
	r := &listingReader{defs: []catalog.DefinitionSummary{
		{Reference: "mcp:glossary_term:t1", Name: "Revenue", Description: "Money in."},
	}}
	p := New(r, nil)
	ctx := context.Background()

	terms, err := p.SearchGlossaryTerms(ctx, "rev", 5)
	require.NoError(t, err)
	assert.Equal(t, []semantic.EntityRef{{URN: "mcp:glossary_term:t1", Name: "Revenue", Description: "Money in."}}, terms)
	assert.Equal(t, catalog.DefinitionQuery{Kinds: []catalog.Kind{catalog.KindGlossaryTerm}, Text: "rev", Limit: 5}, r.queries[0])

	_, err = p.SearchDataProducts(ctx, "*", 3)
	require.NoError(t, err)
	assert.Equal(t, catalog.DefinitionQuery{Kinds: []catalog.Kind{catalog.KindDataProduct}, Limit: 3}, r.queries[1],
		"* lists every data product")

	_, err = p.ListDomains(ctx)
	require.NoError(t, err)
	assert.Equal(t, catalog.DefinitionQuery{Kinds: []catalog.Kind{catalog.KindDomain}, Limit: domainListLimit}, r.queries[2])
}

func TestPickerReportsAListingFailure(t *testing.T) {
	p := New(&listingReader{listErr: errBoom}, nil)
	_, err := p.SearchGlossaryTerms(context.Background(), "x", 5)
	require.ErrorIs(t, err, errBoom)
	_, total, err := p.SearchGlossaryTermsCounted(context.Background(), "x", 5)
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, semantic.TotalUnknown, total)
}

func TestPickerWithAStoreThatCannotListPicksNothing(t *testing.T) {
	refs, err := New(&fakeReader{}, nil).ListDomains(context.Background())
	require.NoError(t, err)
	assert.Empty(t, refs)
	assert.NotNil(t, refs, "an empty pick is [], never null")
}

func TestCountedSearchesKnowTheTotalOnlyFromAShortPage(t *testing.T) {
	r := &listingReader{defs: []catalog.DefinitionSummary{{Reference: "mcp:glossary_term:a"}, {Reference: "mcp:glossary_term:b"}}}
	p := New(r, nil)
	_, total, err := p.SearchGlossaryTermsCounted(context.Background(), "", 5)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	_, total, err = p.SearchGlossaryTermsCounted(context.Background(), "", 2)
	require.NoError(t, err)
	assert.Equal(t, semantic.TotalUnknown, total)

	r.search = []catalog.DatasetSummary{{Reference: "mcp:dataset:(trino,acme,a.b.c)", Name: "c"}}
	tables, total, err := p.SearchTablesCounted(context.Background(), semantic.SearchFilter{Query: "c", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, tables, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, semantic.TotalUnknown, pageTotal(3, 0), "no limit proves no total")

	r.err = errBoom
	_, total, err = p.SearchTablesCounted(context.Background(), semantic.SearchFilter{Query: "c", Limit: 10})
	require.Error(t, err)
	assert.Equal(t, semantic.TotalUnknown, total)
}
