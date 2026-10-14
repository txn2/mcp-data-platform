package searchfed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// personaScope admits the analyst persona to one bound connection name.
type personaScope struct{ allowed string }

func (s personaScope) AllowConnection(persona, connection string) bool {
	return persona == "admin" || connection == s.allowed
}

func (personaScope) ConnectionsForURN(string) []string { return nil }

// namedToolkit is a connection whose configured connection_name differs from
// its instance key, so the name persona rules match is not the one a catalog
// reference carries.
type namedToolkit struct {
	connToolkit
	connection string
}

func (n namedToolkit) Connection() string { return n.connection }

func TestNew_NativeCatalogFetchThroughTheAssembledRouter(t *testing.T) {
	store := catalogfake.New()
	ref := store.AddDataset("warehouse", "hive.sales.orders")
	other := store.AddDataset("payroll", "hive.hr.salaries")
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(namedToolkit{connToolkit{kind: "trino", name: "warehouse"}, "Warehouse"}))
	require.NoError(t, reg.Register(namedToolkit{connToolkit{kind: "trino", name: "payroll"}, "Payroll"}))
	h := New(Config{
		ToolkitName:     "default",
		Catalog:         store,
		ConnectionScope: personaScope{allowed: "Warehouse"},
		Registry:        reg,
	})
	require.NotNil(t, h)
	ctx := context.Background()

	doc, err := h.Router().Fetch(ctx, ref.String(), knowledge.Caller{Email: "a@example.com", Persona: "analyst"})
	require.NoError(t, err)
	assert.Equal(t, ref.String(), doc.Reference)

	_, err = h.Router().Fetch(ctx, other.String(), knowledge.Caller{Email: "a@example.com", Persona: "analyst"})
	assert.ErrorIs(t, err, knowledge.ErrNotFound, "a dataset on a connection the persona cannot reach is not fetchable")

	doc, err = h.Router().Fetch(ctx, other.String(), knowledge.Caller{Email: "b@example.com", Persona: "admin"})
	require.NoError(t, err)
	assert.Equal(t, "salaries", doc.Title)
}

func TestNew_DefinitionsAreSearchedAndFetchedThroughTheAssembledRouter(t *testing.T) {
	store := catalogfake.New()
	term, err := store.PutDefinition(context.Background(), catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Net Revenue"})
	require.NoError(t, err)
	store.SetDescription(term.Reference, "Revenue after refunds.")
	h := New(Config{ToolkitName: "default", Catalog: store, Registry: registry.NewRegistry()})
	require.NotNil(t, h)
	ctx := context.Background()
	caller := knowledge.Caller{Email: "a@example.com", Persona: "analyst"}

	res, err := h.Router().Search(ctx, knowledge.Query{Intent: "what is net revenue", Limit: 5, Caller: caller})
	require.NoError(t, err)
	var found string
	for _, g := range res.Groups {
		if g.Source == knowledge.SourceGovernance && len(g.Hits) > 0 {
			found = g.Hits[0].Reference
		}
	}
	assert.Equal(t, term.Reference, found, "a glossary term is a search hit of its own")

	doc, err := h.Router().Fetch(ctx, "mcp:glossary_term:net-revenue", caller)
	require.NoError(t, err, "a term is fetched by its slug as well as its id")
	assert.Equal(t, "Revenue after refunds.", doc.Body)
	assert.Equal(t, knowledge.SourceGovernance, doc.Source)
}

func TestNew_NoCatalogLeavesTheSourceOut(t *testing.T) {
	h := New(Config{ToolkitName: "default", ThreadStore: stubThreadStore{}, Registry: registry.NewRegistry()})
	_, err := h.Router().Fetch(context.Background(), "mcp:dataset:(trino,w,a.b.c)", knowledge.Caller{Email: "a@example.com"})
	assert.ErrorIs(t, err, knowledge.ErrNotFound)
}

func TestCatalogBoundWithNoRegistryUsesTheInstance(t *testing.T) {
	assert.Nil(t, catalogBound(nil))
}
