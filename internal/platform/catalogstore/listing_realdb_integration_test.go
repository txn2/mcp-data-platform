//go:build integration

package catalogstore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

func TestStore_ListDefinitionsAndTheGlossaryTree_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	finance, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Finance"})
	require.NoError(t, err)
	revenue, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue", Parent: finance.Reference})
	require.NoError(t, err)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Churn"})
	require.NoError(t, err)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "pii"})
	require.NoError(t, err)
	_, err = s.WriteAspect(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, revenue.ID), catalog.AspectDescription,
		json.RawMessage(`"Money booked from sales."`), "ana@example.com")
	require.NoError(t, err)

	roots, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{
		Kinds: []catalog.Kind{catalog.KindGlossaryTerm, catalog.KindGlossaryNode}, Roots: true,
	})
	require.NoError(t, err)
	require.Len(t, roots, 2, "the root of the tree is the node and the term under no node")
	assert.Equal(t, "Churn", roots[0].Name)
	assert.Equal(t, "Finance", roots[1].Name)
	assert.Equal(t, 1, roots[1].Children)

	children, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: "mcp:glossary_node:finance"})
	require.NoError(t, err, "a parent named by its slug")
	require.Len(t, children, 1)
	assert.Equal(t, revenue.Reference, children[0].Reference)
	assert.Equal(t, finance.Reference, children[0].Parent)
	assert.Equal(t, "Money booked from sales.", children[0].Description)

	found, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{Text: "booked"})
	require.NoError(t, err)
	require.Len(t, found, 1, "text matches the description")
	assert.Equal(t, "Revenue", found[0].Name)

	tags, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{Kinds: []catalog.Kind{catalog.KindTag}})
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Equal(t, "mcp:tag:pii", tags[0].Reference)

	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: "mcp:glossary_node:nowhere"})
	require.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestStore_UsesAndDeleteRefusesWhatIsInUse_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id", "amount")
	term, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue"})
	require.NoError(t, err)
	domain, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindDomain, Name: "Sales"})
	require.NoError(t, err)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "pii"})
	require.NoError(t, err)
	amount := catalog.ColumnRef(ordersRef, "amount")
	_, err = s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: amount, Aspect: catalog.AspectGlossaryTerms, Value: json.RawMessage(`["` + term.Reference + `"]`)},
		{Ref: amount, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`)},
		{Ref: ordersRef, Aspect: catalog.AspectDomain, Value: json.RawMessage(`"` + domain.Reference + `"`)},
	}, "ana@example.com")
	require.NoError(t, err)

	uses, err := s.Uses(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, "revenue"), 10)
	require.NoError(t, err)
	require.Len(t, uses, 1)
	assert.Equal(t, catalog.Use{
		Reference: amount.String(), Dataset: ordersRef.String(), Name: "orders", Column: "amount", Connection: "warehouse",
	}, uses[0])
	uses, err = s.Uses(ctx, catalog.KeyRef(catalog.KindDomain, "sales"), 10)
	require.NoError(t, err)
	require.Len(t, uses, 1)
	assert.Equal(t, ordersRef.String(), uses[0].Reference)
	assert.Empty(t, uses[0].Column)

	for _, ref := range []catalog.Ref{
		catalog.KeyRef(catalog.KindGlossaryTerm, "revenue"),
		catalog.KeyRef(catalog.KindDomain, "sales"),
		catalog.KeyRef(catalog.KindTag, "pii"),
	} {
		err := s.DeleteDefinition(ctx, ref)
		var inUse *catalog.InUseError
		require.ErrorAs(t, err, &inUse, ref.String())
		assert.Equal(t, 1, inUse.Entities)
	}

	_, err = s.WriteAspect(ctx, amount, catalog.AspectTags, nil, "ana@example.com")
	require.NoError(t, err)
	require.NoError(t, s.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindTag, "pii")))
	_, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindTag, "pii"))
	require.ErrorIs(t, err, catalog.ErrNotFound)
	require.ErrorIs(t, s.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindTag, "pii")), catalog.ErrNotFound)

	node, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Finance"})
	require.NoError(t, err)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Margin", Parent: node.Reference})
	require.NoError(t, err)
	err = s.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryNode, "finance"))
	var inUse *catalog.InUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, 1, inUse.Children)
	require.NoError(t, s.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, "margin")))
	require.NoError(t, s.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryNode, "finance")))
}

func TestStore_BrowseOneLevelAtATime_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	for _, path := range [][]string{
		{"hive", "sales", "orders"}, {"hive", "sales", "order.lines"}, {"hive", "hr", "staff"}, {"iceberg", "ops", "runs"},
		{"hive", "sales.v2", "t"},
	} {
		_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: catalog.DatasetRef("trino", "warehouse", path...), Type: catalog.DatasetTable})
		require.NoError(t, err)
	}
	_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: catalog.DatasetRef("trino", "other", "hive", "x", "y"), Type: catalog.DatasetTable})
	require.NoError(t, err)

	top, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "warehouse"})
	require.NoError(t, err)
	require.Len(t, top, 2)
	assert.Equal(t, catalog.BrowseEntry{Name: "hive", Reference: "mcp:dataset:(trino,warehouse,hive)", Datasets: 4}, top[0])
	assert.Equal(t, "iceberg", top[1].Name)

	schema, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "warehouse", Prefix: []string{"hive", "sales"}})
	require.NoError(t, err)
	require.Len(t, schema, 2)
	assert.Equal(t, "order.lines", schema[0].Name, "a quoted segment is one segment")
	assert.Equal(t, `mcp:dataset:(trino,warehouse,hive.sales."order.lines")`, schema[0].Reference)
	assert.Equal(t, catalog.DatasetTable, schema[0].Type)
	assert.Zero(t, schema[0].Datasets)

	catalogs, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "warehouse", Prefix: []string{"hive"}})
	require.NoError(t, err)
	assert.Equal(t, []catalog.BrowseEntry{
		{Name: "hr", Reference: "mcp:dataset:(trino,warehouse,hive.hr)", Datasets: 1},
		{Name: "sales", Reference: "mcp:dataset:(trino,warehouse,hive.sales)", Datasets: 2},
		{Name: "sales.v2", Reference: `mcp:dataset:(trino,warehouse,hive."sales.v2")`, Datasets: 1},
	}, catalogs, "a quoted group is counted on its own")

	none, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "warehouse", Prefix: []string{"hive", "sales", "orders"}})
	require.NoError(t, err)
	assert.Equal(t, []catalog.BrowseEntry{}, none, "a dataset has nothing under it")

	scoped, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Scoped: true, Within: []string{"trino/other"}})
	require.NoError(t, err)
	require.Len(t, scoped, 1, "a scoped search sees only the connections it is scoped to")
	assert.Equal(t, "other", scoped[0].Connection)
	one, err := s.SearchDatasets(ctx, catalog.DatasetQuery{ConnectionKind: "trino", Connection: "warehouse"})
	require.NoError(t, err)
	assert.Len(t, one, 5)
}
