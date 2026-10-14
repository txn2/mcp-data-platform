//go:build integration

package catalogstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var ordersRef = catalog.DatasetRef("trino", "warehouse", "hive", "sales", "orders")

func putOrders(t *testing.T, s *Store, cols ...string) *catalog.Dataset {
	t.Helper()
	in := catalog.DatasetInput{Ref: ordersRef, Type: catalog.DatasetTable, SchemaResourceID: "res-1"}
	for _, c := range cols {
		in.Columns = append(in.Columns, catalog.ColumnInput{Name: c, DataType: "varchar", Nullable: c != "id"})
	}
	d, err := s.PutDataset(context.Background(), in)
	require.NoError(t, err)
	return d
}

func TestStore_DatasetAndEveryAspect_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))

	d := putOrders(t, s, "id", "amount")
	assert.Equal(t, "orders", d.Name)
	assert.Equal(t, "mcp:dataset:(trino,warehouse,hive.sales.orders)", d.Reference)
	require.Len(t, d.Columns, 2)
	assert.Equal(t, "id", d.Columns[0].Name)
	assert.False(t, d.Columns[0].Nullable)

	written := map[catalog.AspectName]string{
		catalog.AspectDescription:          `"Orders placed on the storefront."`,
		catalog.AspectTags:                 `["pii","finance"]`,
		catalog.AspectGlossaryTerms:        `["mcp:glossary_term:revenue"]`,
		catalog.AspectOwners:               `[{"owner":"ana@example.com","type":"technical_owner"}]`,
		catalog.AspectDomain:               `"mcp:domain:sales"`,
		catalog.AspectCustomProperties:     `{"refresh":"hourly"}`,
		catalog.AspectStructuredProperties: `{"retention_days":[90]}`,
		catalog.AspectDocumentationLinks:   `[{"url":"https://example.com/orders"}]`,
		catalog.AspectCuratedQueries:       `[{"name":"daily","sql":"SELECT 1"}]`,
		catalog.AspectIncidents:            `[{"id":"i1","title":"late","state":"active"}]`,
		catalog.AspectContextDocuments:     `[{"id":"d1","title":"Loading","content":"at 02:00"}]`,
	}
	for name, v := range written {
		_, err := s.WriteAspect(ctx, ordersRef, name, json.RawMessage(v), "ana@example.com")
		require.NoError(t, err, name)
	}
	amount := catalog.ColumnRef(ordersRef, "amount")
	_, err := s.WriteAspect(ctx, amount, catalog.AspectDescription, json.RawMessage(`"Order total in USD."`), "ana@example.com")
	require.NoError(t, err)

	got, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	for name, v := range written {
		var want any
		require.NoError(t, json.Unmarshal([]byte(v), &want))
		field, err := json.Marshal(got.Aspects)
		require.NoError(t, err)
		var all map[string]any
		require.NoError(t, json.Unmarshal(field, &all))
		assert.Equal(t, want, all[string(name)], name)
	}
	assert.Equal(t, "Order total in USD.", got.Columns[1].Aspects.Description)
	assert.Empty(t, got.Columns[0].Aspects.Description)
	assert.Equal(t, "res-1", got.SchemaResourceID)
}

func TestStore_RedeclaringKeepsCurationOfKeptColumns_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id", "amount", "legacy")
	_, err := s.WriteAspect(ctx, catalog.ColumnRef(ordersRef, "amount"), catalog.AspectDescription,
		json.RawMessage(`"kept"`), "a")
	require.NoError(t, err)

	d := putOrders(t, s, "amount", "id", "added")
	require.Len(t, d.Columns, 3)
	assert.Equal(t, []string{"amount", "id", "added"}, []string{d.Columns[0].Name, d.Columns[1].Name, d.Columns[2].Name})
	assert.Equal(t, "kept", d.Columns[0].Aspects.Description)

	_, err = s.WriteAspect(ctx, catalog.ColumnRef(ordersRef, "legacy"), catalog.AspectDescription,
		json.RawMessage(`"x"`), "a")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestStore_HistoryAndRollback_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")

	first, err := s.WriteAspect(ctx, ordersRef, catalog.AspectDescription, json.RawMessage(`"first"`), "ana")
	require.NoError(t, err)
	assert.Equal(t, 1, first.Version)
	assert.Nil(t, first.Previous)

	second, err := s.WriteAspect(ctx, ordersRef, catalog.AspectDescription, json.RawMessage(`"second"`), "ben")
	require.NoError(t, err)
	assert.Equal(t, 2, second.Version)
	assert.JSONEq(t, `"first"`, string(second.Previous))

	hist, err := s.History(ctx, ordersRef, catalog.AspectDescription)
	require.NoError(t, err)
	require.Len(t, hist, 2)
	assert.Equal(t, second.ID, hist[0].ID)
	assert.Equal(t, "ben", hist[0].WrittenBy)
	assert.JSONEq(t, `"second"`, string(hist[0].Value))
	assert.Equal(t, first.ID, hist[1].ID)

	back, err := s.Rollback(ctx, second.ID, "admin")
	require.NoError(t, err)
	assert.Equal(t, 3, back.Version)
	assert.Equal(t, second.ID, back.Reverts)
	assert.JSONEq(t, `"first"`, string(back.Value))

	d, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Equal(t, "first", d.Aspects.Description)

	// Rolling back the first write restores the absence it replaced.
	_, err = s.Rollback(ctx, first.ID, "admin")
	require.NoError(t, err)
	d, err = s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Empty(t, d.Aspects.Description)

	hist, err = s.History(ctx, ordersRef, catalog.AspectDescription)
	require.NoError(t, err)
	require.Len(t, hist, 4)
	assert.Nil(t, hist[0].Value)

	_, err = s.Rollback(ctx, "00000000-0000-0000-0000-000000000000", "admin")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
	_, err = s.Rollback(ctx, "not-a-uuid", "admin")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestStore_ConcurrentWritesTakeDistinctVersions_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.WriteAspect(ctx, ordersRef, catalog.AspectTags, json.RawMessage(`["t"]`), "w")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	hist, err := s.History(ctx, ordersRef, catalog.AspectTags)
	require.NoError(t, err)
	require.Len(t, hist, writers)
	for i, c := range hist {
		assert.Equal(t, writers-i, c.Version)
	}
}

func TestStore_Definitions_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))

	node, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Finance"})
	require.NoError(t, err)
	assert.Equal(t, "finance", node.Slug)
	assert.Equal(t, "mcp:glossary_node:"+node.ID, node.Reference)

	term, err := s.PutDefinition(ctx, catalog.DefinitionInput{
		Kind: catalog.KindGlossaryTerm, Name: "Net Revenue", Parent: node.Reference,
	})
	require.NoError(t, err)
	assert.Equal(t, "net-revenue", term.Slug)
	assert.Equal(t, node.Reference, term.Parent)

	// By slug and by id, the same definition.
	bySlug, err := s.GetDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, "net-revenue"))
	require.NoError(t, err)
	byID, err := s.GetDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, term.ID))
	require.NoError(t, err)
	assert.Equal(t, term.ID, bySlug.ID)
	assert.Equal(t, term.ID, byID.ID)

	// A rename keeps the id and the reference.
	renamed, err := s.PutDefinition(ctx, catalog.DefinitionInput{
		Kind: catalog.KindGlossaryTerm, Slug: "net-revenue", Name: "Net revenue (USD)",
	})
	require.NoError(t, err)
	assert.Equal(t, term.ID, renamed.ID)
	assert.Equal(t, term.Reference, renamed.Reference)
	assert.Equal(t, "Net revenue (USD)", renamed.Name)
	assert.Empty(t, renamed.Parent)

	_, err = s.WriteAspect(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, "net-revenue"), catalog.AspectDescription,
		json.RawMessage(`"Revenue after refunds."`), "ana")
	require.NoError(t, err)
	byID, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, term.ID))
	require.NoError(t, err)
	assert.Equal(t, "Revenue after refunds.", byID.Aspects.Description)

	_, err = s.WriteAspect(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, term.ID), catalog.AspectTags, json.RawMessage(`["x"]`), "ana")
	assert.ErrorIs(t, err, catalog.ErrAspectNotAllowed)

	tag, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindTag, Name: "pii"})
	require.NoError(t, err)
	assert.Equal(t, "mcp:tag:pii", tag.Reference)

	_, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindDomain, "absent"))
	assert.ErrorIs(t, err, catalog.ErrNotFound)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindDomain, Name: "Sales", Parent: node.Reference})
	assert.Error(t, err)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "x", Parent: "mcp:glossary_node:absent"})
	assert.True(t, errors.Is(err, catalog.ErrNotFound))
}

func TestStore_RemovedAndSeenAgain_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")
	_, err := s.WriteAspect(ctx, ordersRef, catalog.AspectDescription, json.RawMessage(`"kept"`), "a")
	require.NoError(t, err)

	n, err := s.MarkRemoved(ctx, catalog.Removal{ConnectionKind: "trino", Connection: "warehouse", Types: []catalog.DatasetType{catalog.DatasetTable}})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	d, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	require.NotNil(t, d.RemovedAt, "a dataset the scan did not see is marked removed")
	assert.Equal(t, "kept", d.Aspects.Description, "and its curation is still readable")

	n, err = s.MarkRemoved(ctx, catalog.Removal{ConnectionKind: "trino", Connection: "warehouse", Types: []catalog.DatasetType{catalog.DatasetTable}})
	require.NoError(t, err)
	assert.Zero(t, n, "an already-removed dataset is not marked again")

	d = putOrders(t, s, "id")
	assert.Nil(t, d.RemovedAt, "seeing it again clears the mark")
}

func TestStore_UnreadPrefixKeepsItsDatasets_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	put := func(path ...string) catalog.Ref {
		ref := catalog.DatasetRef("trino", "warehouse", path...)
		_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: ref, Type: catalog.DatasetTable})
		require.NoError(t, err)
		return ref
	}
	inDown := put("my_cat", "s", "t")
	lookalike := put("myxcat", "s", "t")
	whole := put("down")
	gone := put("hive", "s", "gone")

	n, err := s.MarkRemoved(ctx, catalog.Removal{
		ConnectionKind: "trino", Connection: "warehouse", Types: []catalog.DatasetType{catalog.DatasetTable},
		Unread: []string{"my_cat", "down"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, n, "only what was read and not seen is marked")
	for _, c := range []struct {
		ref     catalog.Ref
		removed bool
	}{{inDown, false}, {whole, false}, {lookalike, true}, {gone, true}} {
		d, err := s.GetDataset(ctx, c.ref)
		require.NoError(t, err)
		assert.Equal(t, c.removed, d.RemovedAt != nil, c.ref.String())
	}
}

func TestStore_SearchMatchesWildcardCharactersAsWritten_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	for _, name := range []string{"order_lines", "orderxlines"} {
		_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: catalog.DatasetRef("trino", "warehouse", "hive", "s", name), Type: catalog.DatasetTable})
		require.NoError(t, err)
	}
	got, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "order_lines"})
	require.NoError(t, err)
	require.Len(t, got, 1, "an underscore is a character, not a wildcard")
	assert.Equal(t, "order_lines", got[0].Name)
	got, err = s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "%"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStore_ScanDoesNotDowngradeARegisteredTable_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: ordersRef, Type: catalog.DatasetRegisteredTable, SchemaResourceID: "res-9"})
	require.NoError(t, err)
	d := putOrders(t, s, "id")
	assert.Equal(t, catalog.DatasetRegisteredTable, d.Type)
	assert.Equal(t, "res-1", d.SchemaResourceID)

	d, err = s.PutDataset(ctx, catalog.DatasetInput{Ref: ordersRef, Type: catalog.DatasetView})
	require.NoError(t, err)
	assert.Equal(t, "res-1", d.SchemaResourceID, "a declaration naming no schema document keeps the one recorded")
}

func TestStore_Lineage_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	edges := []catalog.Edge{
		{Source: "a", Target: "b", WriterKind: catalog.WriterSQL, WriterRef: "call-1"},
		{Source: "b", Target: "c", WriterKind: catalog.WriterScript, WriterRef: "mcp:script:1"},
		{Source: "c", Target: "a", WriterKind: catalog.WriterSQL, WriterRef: "call-2"},
		{Source: "x", Target: "c", WriterKind: catalog.WriterRegistration},
	}
	require.NoError(t, s.AddEdges(ctx, edges))
	require.NoError(t, s.AddEdges(ctx, edges[:1]), "recording an edge again is not a duplicate")

	up, err := s.Lineage(ctx, "c", catalog.Upstream, 5)
	require.NoError(t, err)
	depth := map[string]int{}
	for _, h := range up {
		depth[h.Source+">"+h.Target] = h.Depth
	}
	assert.Equal(t, map[string]int{"b>c": 1, "x>c": 1, "a>b": 2}, depth, "the walk stops at the cycle back to c")

	down, err := s.Lineage(ctx, "a", catalog.Downstream, 1)
	require.NoError(t, err)
	require.Len(t, down, 1)
	assert.Equal(t, "b", down[0].Target)
}

func TestStore_SearchAndByPath_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")
	other := catalog.DatasetRef("trino", "archive", "hive", "sales", "orders")
	_, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: other, Type: catalog.DatasetTable})
	require.NoError(t, err)
	gone := catalog.DatasetRef("trino", "warehouse", "hive", "sales", "gone")
	_, err = s.PutDataset(ctx, catalog.DatasetInput{Ref: gone, Type: catalog.DatasetTable})
	require.NoError(t, err)
	_, err = s.MarkRemoved(ctx, catalog.Removal{ConnectionKind: "trino", Connection: "warehouse", Types: []catalog.DatasetType{catalog.DatasetTable}, Keep: []string{ordersRef.String()}})
	require.NoError(t, err)
	_, err = s.WriteAspect(ctx, ordersRef, catalog.AspectDescription, json.RawMessage(`"Storefront revenue"`), "a")
	require.NoError(t, err)
	_, err = s.WriteAspect(ctx, ordersRef, catalog.AspectTags, json.RawMessage(`["finance","pii"]`), "a")
	require.NoError(t, err)

	refs, err := s.DatasetsByPath(ctx, "trino", "hive.sales.orders")
	require.NoError(t, err)
	assert.Equal(t, []string{other.String(), ordersRef.String()}, refs, "one per connection, ordered by connection")

	all, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "*"})
	require.NoError(t, err)
	assert.Len(t, all, 2, "a removed dataset is not found")

	hits, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "REVENUE"})
	require.NoError(t, err)
	require.Len(t, hits, 1, "the description matches, ignoring case")
	assert.Equal(t, "Storefront revenue", hits[0].Description)
	assert.Equal(t, []string{"finance", "pii"}, hits[0].Tags)
	assert.Equal(t, "hive.sales.orders", hits[0].Path)

	tagged, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Tags: []string{"pii"}})
	require.NoError(t, err)
	require.Len(t, tagged, 1)

	page, err := s.SearchDatasets(ctx, catalog.DatasetQuery{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, ordersRef.String(), page[0].Reference)

	none, err := s.SearchDatasets(ctx, catalog.DatasetQuery{ConnectionKind: "api"})
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

func TestStore_MarkDatasetRemoved_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")
	require.NoError(t, s.MarkDatasetRemoved(ctx, ordersRef))
	d, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	require.NotNil(t, d.RemovedAt)
	require.NoError(t, s.MarkDatasetRemoved(ctx, catalog.DatasetRef("trino", "w", "a", "b", "absent")),
		"a dataset the store does not hold is not an error")
}

// WriteAspects is all-or-nothing: an edit the store refuses leaves every
// earlier edit in the call unwritten.
func TestStore_WriteAspectsIsAtomic_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id", "amount")
	amount := catalog.ColumnRef(ordersRef, "amount")

	changes, err := s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"Orders"`)},
		{Ref: amount, Aspect: catalog.AspectTags, Value: json.RawMessage(`["money"]`)},
	}, "ana@example.com")
	require.NoError(t, err)
	require.Len(t, changes, 2)
	assert.Equal(t, amount.String(), changes[1].Reference)

	_, err = s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"Changed"`)},
		{Ref: amount, Aspect: catalog.AspectIncidents, Value: json.RawMessage(`[]`)},
	}, "ana@example.com")
	require.ErrorIs(t, err, catalog.ErrAspectNotAllowed)
	_, err = s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"Changed"`)},
		{Ref: catalog.DatasetRef("trino", "warehouse", "hive", "sales", "missing"), Aspect: catalog.AspectTags, Value: json.RawMessage(`["x"]`)},
	}, "ana@example.com")
	require.ErrorIs(t, err, catalog.ErrNotFound)

	got, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Equal(t, "Orders", got.Aspects.Description)
}

// RevertChanges undoes a set of changes in one transaction, and refuses all
// of them, naming the newer write, when one's aspect was written since.
func TestStore_RevertChanges_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")

	_, err := s.WriteAspect(ctx, ordersRef, catalog.AspectDescription, json.RawMessage(`"v1"`), "ana")
	require.NoError(t, err)
	applied, err := s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"v2"`)},
		{Ref: ordersRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`)},
	}, "agent")
	require.NoError(t, err)
	ids := []string{applied[1].ID, applied[0].ID}

	newer, err := s.WriteAspect(ctx, ordersRef, catalog.AspectTags, json.RawMessage(`["pii","finance"]`), "bo")
	require.NoError(t, err)
	_, err = s.RevertChanges(ctx, ids, "admin")
	var conflict *catalog.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, newer.ID, conflict.Newer.ID)
	assert.Equal(t, "bo", conflict.Newer.WrittenBy)
	assert.Equal(t, catalog.AspectTags, conflict.Aspect)
	got, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Aspects.Description, "nothing reverted")

	_, err = s.Rollback(ctx, newer.ID, "bo")
	require.NoError(t, err)
	_, err = s.RevertChanges(ctx, ids, "admin")
	require.ErrorAs(t, err, &conflict, "the rollback of the newer write is itself newer")

	fresh, err := s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"v3"`)},
		{Ref: ordersRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["x"]`)},
	}, "agent")
	require.NoError(t, err)
	reverted, err := s.RevertChanges(ctx, []string{fresh[1].ID, fresh[0].ID}, "admin")
	require.NoError(t, err)
	require.Len(t, reverted, 2)
	assert.Equal(t, fresh[1].ID, reverted[0].Reverts)
	got, err = s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Aspects.Description)
	assert.Equal(t, []string{"pii"}, got.Aspects.Tags)

	_, err = s.RevertChanges(ctx, []string{"not-a-uuid"}, "admin")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
	_, err = s.RevertChanges(ctx, []string{"00000000-0000-0000-0000-000000000000"}, "admin")
	assert.ErrorIs(t, err, catalog.ErrNotFound)
}

func TestStore_TaggedEntities_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id", "amount")
	amount := catalog.ColumnRef(ordersRef, "amount")
	_, err := s.WriteAspects(ctx, []catalog.AspectEdit{
		{Ref: ordersRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii","finance"]`)},
		{Ref: amount, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`)},
	}, "ana")
	require.NoError(t, err)
	term, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "pii"})
	require.NoError(t, err)
	require.NotNil(t, term)

	refs, err := s.TaggedEntities(ctx, "pii", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{amount.String(), ordersRef.String()}, refs)
	refs, err = s.TaggedEntities(ctx, "pii", 1)
	require.NoError(t, err)
	assert.Len(t, refs, 1)
	refs, err = s.TaggedEntities(ctx, "nobody", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{}, refs)
}

// A checked write lands when the aspect still holds what the writer read,
// however PostgreSQL re-spelled it, and is refused with ErrStale, writing
// nothing, when it was written since.
func TestStore_CheckedWrite_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	putOrders(t, s, "id")
	_, err := s.WriteAspect(ctx, ordersRef, catalog.AspectCustomProperties, json.RawMessage(`{"b":"2","a":"1"}`), "ana")
	require.NoError(t, err)

	_, err = s.WriteAspects(ctx, []catalog.AspectEdit{{
		Ref: ordersRef, Aspect: catalog.AspectCustomProperties, Value: json.RawMessage(`{"a":"1","b":"2","c":"3"}`),
		Expect: json.RawMessage(`{"a": "1", "b": "2"}`), Checked: true,
	}, {
		Ref: ordersRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`), Checked: true,
	}}, "agent")
	require.NoError(t, err, "the same JSON spelled differently, and an absent aspect expected absent")

	_, err = s.WriteAspects(ctx, []catalog.AspectEdit{{
		Ref: ordersRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"x"`), Checked: true,
	}, {
		Ref: ordersRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii","finance"]`),
		Expect: json.RawMessage(`["other"]`), Checked: true,
	}}, "agent")
	require.ErrorIs(t, err, catalog.ErrStale)
	got, err := s.GetDataset(ctx, ordersRef)
	require.NoError(t, err)
	assert.Empty(t, got.Aspects.Description, "nothing in a refused write lands")
	assert.Equal(t, []string{"pii"}, got.Aspects.Tags)
}
