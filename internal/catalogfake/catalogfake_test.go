package catalogfake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var errDown = errors.New("down")

func TestDatasetsReadBackWithTheirColumns(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t", "a", "b")
	f.SetDescription(catalog.ColumnRef(ref, "b").String(), "the b column")

	d, err := f.GetDataset(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "t", d.Name)
	require.Len(t, d.Columns, 2)
	assert.Equal(t, "the b column", d.Columns[1].Aspects.Description)
	assert.Equal(t, 1, d.Columns[1].Ordinal)

	_, err = f.GetDataset(ctx, catalog.ColumnRef(ref, "a"))
	require.ErrorIs(t, err, catalog.ErrNotFound, "a column is not a dataset")
	_, err = f.PutDataset(ctx, catalog.DatasetInput{Ref: ref})
	require.Error(t, err)

	paths, err := f.DatasetsByPath(ctx, "trino", "c.s.t")
	require.NoError(t, err)
	assert.Equal(t, []string{ref.String()}, paths)
	paths, err = f.DatasetsByPath(ctx, "trino", "c.s.missing")
	require.NoError(t, err)
	assert.NotNil(t, paths)
	assert.Empty(t, paths)
	assert.Equal(t, catalog.Aspects{}, f.Aspects(catalog.DatasetRef("trino", "w", "x")))
}

func TestDefinitionsAreFoundByIDOrSlug(t *testing.T) {
	ctx := context.Background()
	f := New()
	tag, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "PII"})
	require.NoError(t, err)
	assert.Equal(t, tag.Slug, tag.ID, "a tag's id is its name")

	term, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue"})
	require.NoError(t, err)
	assert.NotEqual(t, term.Slug, term.ID)

	node, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Finance"})
	require.NoError(t, err)
	again, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue", Parent: node.Reference})
	require.NoError(t, err)
	assert.Equal(t, term.ID, again.ID, "declaring a definition again updates it")
	assert.Equal(t, node.Reference, again.Parent)

	for _, bad := range []catalog.DefinitionInput{
		{Kind: catalog.KindGlossaryTerm, Name: "!!!"},
		{Kind: catalog.KindTag, Name: "x", Parent: node.Reference},
		{Kind: catalog.KindGlossaryTerm, Name: "x", Parent: "mcp:tag:pii"},
	} {
		_, err := f.PutDefinition(ctx, bad)
		require.ErrorIs(t, err, catalog.ErrInvalidDefinition, "%+v", bad)
	}
	_, err = f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "x", Parent: "mcp:glossary_node:none"})
	require.ErrorIs(t, err, catalog.ErrNotFound)

	bySlug, err := f.GetDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, term.Slug))
	require.NoError(t, err)
	assert.Equal(t, term.ID, bySlug.ID)
	_, err = f.GetDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, "missing"))
	require.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestWriteAspectsIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t", "a")
	f.SetDescription(ref.String(), "first")

	_, err := f.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ref, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`)},
		{
			Ref: ref, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"second"`),
			Expect: json.RawMessage(`"not what is stored"`), Checked: true,
		},
	}, "ana")
	require.ErrorIs(t, err, catalog.ErrStale)
	assert.Empty(t, f.Aspects(ref).Tags, "a refused write applies none of its edits")

	_, err = f.WriteAspects(ctx, []catalog.AspectEdit{{Ref: ref, Aspect: "lineage", Value: json.RawMessage(`[]`)}}, "ana")
	require.ErrorIs(t, err, catalog.ErrAspectNotAllowed)
	_, err = f.WriteAspects(ctx, []catalog.AspectEdit{{Ref: catalog.DatasetRef("trino", "w", "x"), Aspect: catalog.AspectTags}}, "ana")
	require.ErrorIs(t, err, catalog.ErrNotFound)

	out, err := f.WriteAspects(ctx, []catalog.AspectEdit{
		{
			Ref: ref, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"second"`),
			Expect: json.RawMessage(`"first"`), Checked: true,
		},
	}, "ana")
	require.NoError(t, err)
	assert.Equal(t, 2, out[0].Version)
	assert.JSONEq(t, `"first"`, string(out[0].Previous))

	f.FailWrite = errDown
	_, err = f.WriteAspect(ctx, ref, catalog.AspectDescription, json.RawMessage(`"x"`), "ana")
	require.ErrorIs(t, err, errDown)
	_, err = f.RevertChanges(ctx, []string{out[0].ID}, "ana")
	require.ErrorIs(t, err, errDown)
}

func TestBeforeWriteRunsOnceAheadOfTheChecks(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t")
	f.SetBeforeWrite(func() { f.SetDescription(ref.String(), "written between") })

	_, err := f.WriteAspects(ctx, []catalog.AspectEdit{{
		Ref: ref, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"mine"`), Checked: true,
	}}, "ana")
	require.ErrorIs(t, err, catalog.ErrStale, "the write lands after the read")

	_, err = f.WriteAspects(ctx, []catalog.AspectEdit{{
		Ref: ref, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"mine"`),
		Expect: json.RawMessage(`"written between"`), Checked: true,
	}}, "ana")
	require.NoError(t, err, "the hook ran once")
}

func TestRevertRestoresAndRefusesAChangeWrittenOver(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t")
	first, err := f.WriteAspect(ctx, ref, catalog.AspectTags, json.RawMessage(`["a"]`), "ana")
	require.NoError(t, err)
	desc, err := f.WriteAspect(ctx, ref, catalog.AspectDescription, json.RawMessage(`"d"`), "ana")
	require.NoError(t, err)
	second, err := f.WriteAspect(ctx, ref, catalog.AspectTags, json.RawMessage(`["b"]`), "ben")
	require.NoError(t, err)

	_, err = f.RevertChanges(ctx, []string{desc.ID, first.ID}, "admin")
	var conflict *catalog.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, second.ID, conflict.Newer.ID)
	assert.Equal(t, "d", f.Aspects(ref).Description, "a refused revert reverts nothing")

	_, err = f.Rollback(ctx, "missing", "admin")
	require.ErrorIs(t, err, catalog.ErrNotFound)

	back, err := f.Rollback(ctx, second.ID, "admin")
	require.NoError(t, err)
	assert.Equal(t, second.ID, back.Reverts)
	assert.Equal(t, []string{"a"}, f.Aspects(ref).Tags)

	cleared, err := f.Rollback(ctx, desc.ID, "admin")
	require.NoError(t, err)
	assert.Nil(t, cleared.Value)
	assert.Empty(t, f.Aspects(ref).Description, "reverting the first write clears the aspect")

	hist, err := f.History(ctx, ref, catalog.AspectTags)
	require.NoError(t, err)
	require.Len(t, hist, 3)
	assert.Equal(t, back.ID, hist[0].ID, "newest first")
}

func TestTaggedEntitiesListsDatasetsAndColumns(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t", "a", "b")
	col := catalog.ColumnRef(ref, "b")
	for _, r := range []catalog.Ref{ref, col} {
		_, err := f.WriteAspect(ctx, r, catalog.AspectTags, json.RawMessage(`["pii"]`), "ana")
		require.NoError(t, err)
	}
	_, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "pii"})
	require.NoError(t, err)

	refs, err := f.TaggedEntities(ctx, "pii", 10)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{ref.String(), col.String()}, refs)
	refs, err = f.TaggedEntities(ctx, "pii", 1)
	require.NoError(t, err)
	assert.Len(t, refs, 1)
	refs, err = f.TaggedEntities(ctx, "other", 10)
	require.NoError(t, err)
	assert.NotNil(t, refs)
}

func TestTheUnmodeledMethodsAnswerEmpty(t *testing.T) {
	ctx := context.Background()
	f := New()
	n, err := f.MarkRemoved(ctx, catalog.Removal{})
	require.NoError(t, err)
	assert.Zero(t, n)
	require.NoError(t, f.MarkDatasetRemoved(ctx, catalog.Ref{}))
	require.NoError(t, f.AddEdges(ctx, nil))
	hops, err := f.Lineage(ctx, "", catalog.Direction("upstream"), 1)
	require.NoError(t, err)
	assert.NotNil(t, hops)
	found, err := f.SearchDatasets(ctx, catalog.DatasetQuery{})
	require.NoError(t, err)
	assert.NotNil(t, found)
}

func TestSameJSON(t *testing.T) {
	assert.True(t, sameJSON(nil, nil))
	assert.True(t, sameJSON([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`)))
	assert.False(t, sameJSON(nil, []byte(`"x"`)))
	assert.False(t, sameJSON([]byte(`not json`), []byte(`"x"`)))
}

func TestSetDescriptionPanicsOnABadReference(t *testing.T) {
	assert.Panics(t, func() { New().SetDescription("not a reference", "x") })
	assert.Panics(t, func() { New().SetDescription(catalog.DatasetRef("trino", "w", "x").String(), "x") })
}

func TestDefinitionsListUsesAndDelete(t *testing.T) {
	ctx := context.Background()
	f := New()
	ref := f.AddDataset("w", "c.s.t", "amount")
	node, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Finance"})
	require.NoError(t, err)
	term, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue", Parent: node.Reference})
	require.NoError(t, err)
	domain, err := f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindDomain, Name: "Sales"})
	require.NoError(t, err)
	_, err = f.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "pii"})
	require.NoError(t, err)
	amount := catalog.ColumnRef(ref, "amount")
	_, err = f.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: amount, Aspect: catalog.AspectGlossaryTerms, Value: json.RawMessage(`["` + term.Reference + `"]`)},
		{Ref: amount, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`)},
		{Ref: ref, Aspect: catalog.AspectDomain, Value: json.RawMessage(`"` + domain.Reference + `"`)},
	}, "ana")
	require.NoError(t, err)

	roots, err := f.ListDefinitions(ctx, catalog.DefinitionQuery{
		Kinds: []catalog.Kind{catalog.KindGlossaryNode, catalog.KindGlossaryTerm}, Roots: true,
	})
	require.NoError(t, err)
	require.Len(t, roots, 1)
	assert.Equal(t, "Finance", roots[0].Name)
	children, err := f.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: node.Reference})
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, term.Reference, children[0].Reference)
	all, err := f.ListDefinitions(ctx, catalog.DefinitionQuery{})
	require.NoError(t, err)
	assert.Len(t, all, 4)

	for _, def := range []catalog.Ref{
		catalog.KeyRef(catalog.KindGlossaryTerm, "revenue"), catalog.KeyRef(catalog.KindTag, "pii"),
		catalog.KeyRef(catalog.KindDomain, "sales"),
	} {
		uses, err := f.Uses(ctx, def, 10)
		require.NoError(t, err)
		require.Len(t, uses, 1, def.String())
		require.ErrorIs(t, f.DeleteDefinition(ctx, def), catalog.ErrInUse, def.String())
	}
	uses, err := f.Uses(ctx, catalog.KeyRef(catalog.KindGlossaryNode, "finance"), 10)
	require.NoError(t, err)
	assert.Empty(t, uses)
	uses, err = f.Uses(ctx, catalog.KeyRef(catalog.KindTag, "pii"), 0)
	require.NoError(t, err)
	assert.Empty(t, uses, "the limit caps the uses")
	_, err = f.Uses(ctx, catalog.KeyRef(catalog.KindTag, "absent"), 1)
	require.ErrorIs(t, err, catalog.ErrNotFound)
	require.ErrorIs(t, f.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindTag, "absent")), catalog.ErrNotFound)

	_, err = f.WriteAspect(ctx, amount, catalog.AspectTags, nil, "ana")
	require.NoError(t, err)
	require.NoError(t, f.DeleteDefinition(ctx, catalog.KeyRef(catalog.KindTag, "pii")))
	_, err = f.GetDefinition(ctx, catalog.KeyRef(catalog.KindTag, "pii"))
	require.ErrorIs(t, err, catalog.ErrNotFound)

	levels, err := f.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "w"})
	require.NoError(t, err)
	assert.Empty(t, levels)
}
