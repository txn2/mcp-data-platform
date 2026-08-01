package searchfed

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// The URNs the fake catalog returns. Each test connection is given its own
// DataHub platform name so a URN maps to exactly one connection; the third
// belongs to a platform no connection claims.
const (
	ordersURN  = "urn:li:dataset:(urn:li:dataPlatform:trino-a,db.analytics.orders,PROD)"
	payrollURN = "urn:li:dataset:(urn:li:dataPlatform:trino-b,db.analytics.payroll,PROD)"
	notesURN   = "urn:li:dataset:(urn:li:dataPlatform:mystery,db.analytics.notes,PROD)"
)

// connToolkit is a minimal registry.Toolkit standing for a data connection, so
// the federation's live connection lister sees a real registry.
type connToolkit struct{ kind, name string }

func (c connToolkit) Kind() string                        { return c.kind }
func (c connToolkit) Name() string                        { return c.name }
func (c connToolkit) Connection() string                  { return c.name }
func (connToolkit) RegisterTools(*mcp.Server)             {}
func (connToolkit) Tools() []string                       { return nil }
func (connToolkit) SetSemanticProvider(semantic.Provider) {}
func (connToolkit) SetQueryProvider(query.Provider)       {}
func (connToolkit) Close() error                          { return nil }

// fakeCatalog is a semantic.Provider whose search returns the three datasets
// above and whose entity lookup resolves them, so the catalog provider behaves
// as it does against DataHub without one being reachable.
type fakeCatalog struct{ *semantic.NoopProvider }

func (fakeCatalog) SearchTables(context.Context, semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return []semantic.TableSearchResult{
		{URN: ordersURN, Name: "db.analytics.orders", Description: "analytics orders"},
		{URN: payrollURN, Name: "db.analytics.payroll", Description: "analytics payroll"},
		{URN: notesURN, Name: "db.analytics.notes", Description: "analytics notes"},
	}, nil
}

func (fakeCatalog) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	switch table.Table {
	case "orders":
		return &semantic.TableContext{URN: ordersURN, Description: "analytics orders"}, nil
	case "payroll":
		return &semantic.TableContext{URN: payrollURN, Description: "analytics payroll"}, nil
	case "notes":
		return &semantic.TableContext{URN: notesURN, Description: "analytics notes"}, nil
	}
	// An unknown table resolves to an empty context, the way the noop provider
	// does; the catalog provider treats a URN-less context as no entity.
	return &semantic.TableContext{}, nil
}

// boundaryFederation assembles the real search federation the platform builds:
// the live toolkit registry behind the connections source, the catalog over a
// semantic provider, and the persona-backed connection scope over a real persona
// registry. Nothing here is hand-fed a Caller — the router builds one per arm.
func boundaryFederation(t *testing.T) *Handle {
	t.Helper()

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(connToolkit{kind: "trino", name: "warehouse-a"}))
	require.NoError(t, reg.Register(connToolkit{kind: "trino", name: "warehouse-b"}))

	personas := persona.NewRegistry()
	require.NoError(t, personas.Register(&persona.Persona{
		Name:        "analyst",
		Roles:       []string{"analyst"},
		Connections: persona.ConnectionRules{Allow: []string{"warehouse-a"}},
	}))
	require.NoError(t, personas.Register(persona.AdminPersona()))

	// The URN → connection mapping the platform derives from its connection
	// source map: each connection owns its own DataHub platform name.
	urnConns := func(urn string) []string {
		switch urn {
		case ordersURN:
			return []string{"warehouse-a"}
		case payrollURN:
			return []string{"warehouse-b"}
		default:
			return nil
		}
	}

	h := New(Config{
		ToolkitName:      "default",
		CatalogEnabled:   true,
		SemanticProvider: fakeCatalog{NoopProvider: semantic.NewNoopProvider()},
		Registry:         reg,
		ConnectionScope: connscope.New(connscope.Deps{
			Registry:       personas,
			URNConnections: urnConns,
		}),
	})
	require.NotNil(t, h)
	return h
}

// coverageBySource indexes a coverage summary for assertions.
func coverageBySource(cov []knowledge.SourceCoverage) map[string]knowledge.SourceCoverage {
	m := make(map[string]knowledge.SourceCoverage, len(cov))
	for _, c := range cov {
		m[c.Source] = c
	}
	return m
}

// refsBySource collects the hit references of the display set, grouped by source.
func refsBySource(groups []knowledge.SourceGroup) map[string][]string {
	m := make(map[string][]string, len(groups))
	for _, g := range groups {
		for _, h := range g.Hits {
			m[g.Source] = append(m[g.Source], h.Ref)
		}
	}
	return m
}

func TestSearch_PersonaConnectionBoundaryNarrowsDiscovery(t *testing.T) {
	h := boundaryFederation(t)
	ctx := context.Background()

	res, err := h.Router().Search(ctx, knowledge.Query{
		Intent: "analytics warehouse",
		Caller: knowledge.Caller{UserID: "u1", Email: "analyst@example.com", Persona: "analyst"},
		Limit:  20,
	})
	require.NoError(t, err)

	refs := refsBySource(res.Groups)
	assert.ElementsMatch(t, []string{ordersURN, notesURN}, refs[knowledge.SourceCatalog],
		"the denied connection's dataset is hidden; the one no connection claims stays visible")
	assert.Equal(t, []string{"warehouse-a"}, refs[knowledge.SourceConnections],
		"only the granted connection is discoverable")

	cov := coverageBySource(res.Coverage)
	assert.Equal(t, 1, cov[knowledge.SourceCatalog].Withheld)
	assert.Equal(t, 1, cov[knowledge.SourceConnections].Withheld)

	notice := knowledge.WithheldNotice(res.Coverage, "analyst")
	assert.Contains(t, notice, "2 results are hidden")
	assert.Contains(t, notice, "catalog and connections")
	assert.Contains(t, notice, "your persona (analyst)")
	assert.Contains(t, notice, "Ask an administrator")
}

func TestSearch_UnrestrictedPersonaSeesEverything(t *testing.T) {
	h := boundaryFederation(t)

	res, err := h.Router().Search(context.Background(), knowledge.Query{
		Intent: "analytics warehouse",
		Caller: knowledge.Caller{UserID: "u2", Email: "admin@example.com", Persona: "admin"},
		Limit:  20,
	})
	require.NoError(t, err)

	refs := refsBySource(res.Groups)
	assert.ElementsMatch(t, []string{ordersURN, payrollURN, notesURN}, refs[knowledge.SourceCatalog])
	assert.ElementsMatch(t, []string{"warehouse-a", "warehouse-b"}, refs[knowledge.SourceConnections])
	assert.Empty(t, knowledge.WithheldNotice(res.Coverage, "admin"),
		"nothing is withheld from a persona granted every connection")
}

func TestSearch_EntityLookupAppliesTheSameBoundary(t *testing.T) {
	h := boundaryFederation(t)

	res, err := h.Router().Search(context.Background(), knowledge.Query{
		EntityURNs: []string{ordersURN, payrollURN, notesURN},
		Caller:     knowledge.Caller{UserID: "u1", Email: "analyst@example.com", Persona: "analyst"},
		Limit:      20,
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{ordersURN, notesURN}, refsBySource(res.Groups)[knowledge.SourceCatalog],
		"handing search a denied URN directly must not surface it either")
	assert.Equal(t, 1, coverageBySource(res.Coverage)[knowledge.SourceCatalog].Withheld)
}

func TestFetch_DeniedReferencesAreNotFound(t *testing.T) {
	h := boundaryFederation(t)
	ctx := context.Background()
	analyst := knowledge.Caller{UserID: "u1", Email: "analyst@example.com", Persona: "analyst"}

	_, err := h.Router().Fetch(ctx, payrollURN, analyst)
	assert.ErrorIs(t, err, knowledge.ErrNotFound, "a dataset behind a denied connection is not fetchable")

	_, err = h.Router().Fetch(ctx, knowledgepage.ConnectionRef("trino", "warehouse-b"), analyst)
	assert.ErrorIs(t, err, knowledge.ErrNotFound, "a denied connection descriptor is not fetchable")

	// The permitted references still resolve for the same caller, so the boundary
	// narrows rather than breaks fetch.
	doc, err := h.Router().Fetch(ctx, ordersURN, analyst)
	require.NoError(t, err)
	assert.Equal(t, ordersURN, doc.Reference)

	doc, err = h.Router().Fetch(ctx, knowledgepage.ConnectionRef("trino", "warehouse-a"), analyst)
	require.NoError(t, err)
	assert.Equal(t, knowledge.SourceConnections, doc.Source)

	// And an admin reaches what the analyst could not.
	doc, err = h.Router().Fetch(ctx, payrollURN, knowledge.Caller{
		UserID: "u2", Email: "admin@example.com", Persona: "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, payrollURN, doc.Reference)
}

func TestSearch_NoConnectionScopeLeavesDiscoveryUnfiltered(t *testing.T) {
	// A deployment with no persona registry wires no scope; every source stays
	// visible, which is the behavior before the boundary existed.
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(connToolkit{kind: "trino", name: "warehouse-a"}))
	require.NoError(t, reg.Register(connToolkit{kind: "trino", name: "warehouse-b"}))

	h := New(Config{
		ToolkitName:      "default",
		CatalogEnabled:   true,
		SemanticProvider: fakeCatalog{NoopProvider: semantic.NewNoopProvider()},
		Registry:         reg,
	})
	require.NotNil(t, h)

	res, err := h.Router().Search(context.Background(), knowledge.Query{
		Intent: "analytics warehouse",
		Caller: knowledge.Caller{UserID: "u3", Email: "someone@example.com"},
		Limit:  20,
	})
	require.NoError(t, err)

	refs := refsBySource(res.Groups)
	assert.Len(t, refs[knowledge.SourceCatalog], 3)
	assert.Len(t, refs[knowledge.SourceConnections], 2)
	assert.Empty(t, knowledge.WithheldNotice(res.Coverage, ""))

	_, err = h.Router().Fetch(context.Background(), payrollURN, knowledge.Caller{Email: "someone@example.com"})
	assert.NoError(t, err, "fetch is unfiltered too when no boundary is wired")
	assert.False(t, errors.Is(err, knowledge.ErrNotFound))
}
