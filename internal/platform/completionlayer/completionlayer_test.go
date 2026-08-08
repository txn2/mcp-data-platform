package completionlayer

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

var errUnauthenticated = errors.New("unauthenticated")

// --- test doubles ---------------------------------------------------------

// fakeSemantic is an uncounted provider: it answers searches but cannot report a
// total, standing in for any catalog backend that does not implement
// semantic.TableMatchCounter.
type fakeSemantic struct {
	semantic.Provider
	tables    []semantic.TableSearchResult
	products  []semantic.TableSearchResult
	domains   []semantic.EntityRef
	terms     []semantic.EntityRef
	searchErr error
}

func (f *fakeSemantic) SearchTables(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if slices.Contains(filter.EntityTypes, dataProductEntityType) {
		return f.products, nil
	}
	return f.tables, nil
}

func (f *fakeSemantic) ListDomains(context.Context) ([]semantic.EntityRef, error) {
	return f.domains, f.searchErr
}

func (f *fakeSemantic) SearchGlossaryTerms(context.Context, string, int) ([]semantic.EntityRef, error) {
	return f.terms, f.searchErr
}

// clampedSemantic mirrors the real DataHub client: it never returns more than
// clampAt rows however many were requested, and it reports the true match count
// separately. It is the shape that makes a "fetch one extra row" probe useless —
// the extra row is exactly what the clamp removes.
type clampedSemantic struct {
	semantic.Provider
	clampAt int
	// tableMatches/termMatches are how many entities match upstream; the page is
	// the clamped prefix of them.
	tableMatches int
	termMatches  int
	// domains is how many domains the listing returns, and domainsErr fails it.
	domains    int
	domainsErr error
	// queries records the query each table search was sent, so a test can assert
	// what an empty typed prefix becomes.
	queries []string
}

func (c *clampedSemantic) page(matches, requested int) int {
	return min(matches, min(requested, c.clampAt))
}

func (c *clampedSemantic) SearchTablesCounted(
	_ context.Context, filter semantic.SearchFilter,
) ([]semantic.TableSearchResult, int, error) {
	c.queries = append(c.queries, filter.Query)
	n := c.page(c.tableMatches, filter.Limit)
	out := make([]semantic.TableSearchResult, 0, n)
	for i := range n {
		out = append(out, semantic.TableSearchResult{Name: "d" + strconv.Itoa(i)})
	}
	return out, c.tableMatches, nil
}

func (c *clampedSemantic) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	results, _, err := c.SearchTablesCounted(ctx, filter)
	return results, err
}

func (c *clampedSemantic) SearchGlossaryTermsCounted(
	_ context.Context, _ string, limit int,
) ([]semantic.EntityRef, int, error) {
	n := c.page(c.termMatches, limit)
	out := make([]semantic.EntityRef, 0, n)
	for i := range n {
		out = append(out, semantic.EntityRef{Name: "t" + strconv.Itoa(i)})
	}
	return out, c.termMatches, nil
}

func (c *clampedSemantic) SearchGlossaryTerms(ctx context.Context, term string, limit int) ([]semantic.EntityRef, error) {
	refs, _, err := c.SearchGlossaryTermsCounted(ctx, term, limit)
	return refs, err
}

func (c *clampedSemantic) ListDomains(context.Context) ([]semantic.EntityRef, error) {
	if c.domainsErr != nil {
		return nil, c.domainsErr
	}
	out := make([]semantic.EntityRef, 0, c.domains)
	for i := range c.domains {
		out = append(out, semantic.EntityRef{Name: "dom" + strconv.Itoa(i)})
	}
	return out, nil
}

type fakeQuery struct {
	query.Provider
	catalogs []string
	schemas  map[string][]string
	tables   map[string][]string
	err      error
}

func (f *fakeQuery) ListCatalogs(context.Context) ([]string, error) { return f.catalogs, f.err }
func (f *fakeQuery) ListSchemas(_ context.Context, catalog string) ([]string, error) {
	return f.schemas[catalog], f.err
}

func (f *fakeQuery) ListTables(_ context.Context, catalog, schema string) ([]string, error) {
	return f.tables[catalog+"."+schema], f.err
}

type rolesMapper struct{ reg *persona.Registry }

func (rolesMapper) MapToRoles(map[string]any) ([]string, error) { return nil, nil }
func (m rolesMapper) MapToPersona(_ context.Context, roles []string) (*persona.Persona, error) {
	if per, ok := m.reg.GetForRoles(roles); ok {
		return per, nil
	}
	return nil, errUnauthenticated
}

type stubAuth struct {
	info *middleware.UserInfo
	err  error
}

func (s stubAuth) Authenticate(context.Context) (*middleware.UserInfo, error) { return s.info, s.err }

// connToolkit implements registry.Toolkit and toolkit.ConnectionLister.
type connToolkit struct {
	conns []toolkit.ConnectionDetail
}

func (connToolkit) Kind() string                          { return "trino" }
func (connToolkit) Name() string                          { return "trino" }
func (connToolkit) Connection() string                    { return "" }
func (connToolkit) RegisterTools(*mcp.Server)             {}
func (connToolkit) Tools() []string                       { return []string{"trino_query"} }
func (connToolkit) SetSemanticProvider(semantic.Provider) {}
func (connToolkit) SetQueryProvider(query.Provider)       {}
func (connToolkit) Close() error                          { return nil }
func (c connToolkit) ListConnections() []toolkit.ConnectionDetail {
	return c.conns
}

// --- harness --------------------------------------------------------------

func testDeps(t *testing.T) Deps {
	t.Helper()
	preg := persona.NewRegistry()
	require.NoError(t, preg.Register(&persona.Persona{
		Name:        "analyst",
		Roles:       []string{"analyst"},
		Tools:       persona.ToolRules{Allow: []string{"search", "trino_browse", "list_connections"}},
		Connections: persona.ConnectionRules{Allow: []string{"prod-trino"}},
		Priority:    10,
	}))
	require.NoError(t, preg.Register(&persona.Persona{
		Name:     "viewer",
		Roles:    []string{"viewer"},
		Tools:    persona.ToolRules{Allow: []string{"platform_info"}},
		Priority: 5,
	}))

	sem := &fakeSemantic{
		tables: []semantic.TableSearchResult{
			{Name: "orders", URN: "urn:li:dataset:orders"},
			{Name: "order_items", URN: "urn:li:dataset:order_items"},
		},
		products: []semantic.TableSearchResult{{Name: "Revenue Product"}},
		domains:  []semantic.EntityRef{{Name: "Finance"}},
		terms:    []semantic.EntityRef{{Name: "Gross Revenue"}},
	}
	sem.Provider = semantic.NewNoopProvider()

	qry := &fakeQuery{
		catalogs: []string{"hive", "iceberg"},
		schemas:  map[string][]string{"hive": {"sales", "ops"}},
		tables:   map[string][]string{"hive.sales": {"orders", "customers"}},
	}
	qry.Provider = query.NewNoopProvider()

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(connToolkit{conns: []toolkit.ConnectionDetail{{Name: "prod-trino"}, {Name: "staging-trino"}}}))

	return Deps{
		Authorizer:      persona.NewAuthorizer(preg, rolesMapper{reg: preg}),
		Semantic:        sem,
		Query:           qry,
		Registry:        reg,
		PersonaRegistry: preg,
	}
}

func analystPC() *middleware.PlatformContext {
	return &middleware.PlatformContext{UserID: "u1", Roles: []string{"analyst"}, PersonaName: "analyst"}
}

func viewerPC() *middleware.PlatformContext {
	return &middleware.PlatformContext{UserID: "u2", Roles: []string{"viewer"}, PersonaName: "viewer"}
}

// --- provider logic -------------------------------------------------------

func TestPromptDatasetCompletions(t *testing.T) {
	h := New(testDeps(t))
	got, cov := h.promptArgument(context.Background(), analystPC(), "dataset", "ord")
	assert.ElementsMatch(t, []string{"orders", "order_items"}, got)
	assert.Equal(t, coverageUnknown, cov, "an uncounted provider proves nothing about the rest of the matches")
}

func TestPromptTopicCompletions(t *testing.T) {
	h := New(testDeps(t))
	got, _ := h.promptArgument(context.Background(), analystPC(), "topic", "")
	assert.ElementsMatch(t, []string{"Finance", "Revenue Product", "Gross Revenue"}, got)
}

func TestPromptConnectionCompletionsGated(t *testing.T) {
	h := New(testDeps(t))
	got, cov := h.promptArgument(context.Background(), analystPC(), "connection", "")
	assert.Equal(t, []string{"prod-trino"}, got)
	assert.Equal(t, coverageComplete, cov, "the registry is fully enumerated")

	// The viewer is denied list_connections, so it completes no connections.
	denied, _ := h.promptArgument(context.Background(), viewerPC(), "connection", "")
	assert.Empty(t, denied)
}

func TestResourceSchemaCompletions(t *testing.T) {
	h := New(testDeps(t))
	ctx := context.Background()

	cats, _ := h.resourceTemplate(ctx, analystPC(), schemaTemplateURI, mcp.CompleteParamsArgument{Name: "catalog", Value: "h"}, nil)
	assert.Equal(t, []string{"hive"}, cats)

	schemas, _ := h.resourceTemplate(ctx, analystPC(), schemaTemplateURI, mcp.CompleteParamsArgument{Name: "schema_name"}, map[string]string{"catalog": "hive"})
	assert.ElementsMatch(t, []string{"sales", "ops"}, schemas)

	tables, _ := h.resourceTemplate(ctx, analystPC(), schemaTemplateURI, mcp.CompleteParamsArgument{Name: "table"}, map[string]string{"catalog": "hive", "schema_name": "sales"})
	assert.ElementsMatch(t, []string{"orders", "customers"}, tables)

	// schema_name without a resolved catalog cannot enumerate.
	none, _ := h.resourceTemplate(ctx, analystPC(), schemaTemplateURI, mcp.CompleteParamsArgument{Name: "schema_name"}, nil)
	assert.Empty(t, none)
}

func TestResourceGlossaryCompletions(t *testing.T) {
	h := New(testDeps(t))
	ctx := context.Background()

	got, _ := h.resourceTemplate(ctx, analystPC(), glossaryTemplateURI, mcp.CompleteParamsArgument{Name: "term", Value: "rev"}, nil)
	assert.Equal(t, []string{"Gross Revenue"}, got)

	// Non-term variable on the glossary template completes nothing.
	other, _ := h.resourceTemplate(ctx, analystPC(), glossaryTemplateURI, mcp.CompleteParamsArgument{Name: "not_term"}, nil)
	assert.Empty(t, other)
}

func TestPersonaFilteredNegative(t *testing.T) {
	h := New(testDeps(t))
	ctx := context.Background()

	// viewer is denied search → no dataset or topic completions.
	ds, _ := h.promptArgument(ctx, viewerPC(), "dataset", "ord")
	assert.Empty(t, ds)
	tp, _ := h.promptArgument(ctx, viewerPC(), "topic", "")
	assert.Empty(t, tp)
	// viewer is denied trino_browse → no schema completions.
	sc, _ := h.resourceTemplate(ctx, viewerPC(), schemaTemplateURI, mcp.CompleteParamsArgument{Name: "catalog"}, nil)
	assert.Empty(t, sc)
}

func TestValuesRouting(t *testing.T) {
	h := New(testDeps(t))
	ctx := context.Background()
	pc := analystPC()

	assertEmpty := func(ref *mcp.CompleteReference, arg mcp.CompleteParamsArgument, caller *middleware.PlatformContext) {
		vals, cov := h.values(ctx, caller, ref, arg, nil)
		assert.Nil(t, vals)
		assert.Equal(t, coverageComplete, cov)
	}
	assertEmpty(&mcp.CompleteReference{Type: "ref/unknown"}, mcp.CompleteParamsArgument{}, pc)
	assertEmpty(nil, mcp.CompleteParamsArgument{}, pc)
	assertEmpty(&mcp.CompleteReference{Type: "ref/prompt"}, mcp.CompleteParamsArgument{}, nil)
	assertEmpty(&mcp.CompleteReference{Type: "ref/prompt", Name: "x"}, mcp.CompleteParamsArgument{Name: "mystery"}, pc)
	assertEmpty(&mcp.CompleteReference{Type: "ref/resource", URI: "unknown://x"}, mcp.CompleteParamsArgument{Name: "catalog"}, pc)
}

func TestNilAuthorizerAllows(t *testing.T) {
	deps := testDeps(t)
	deps.Authorizer = nil // no persona filtering configured
	h := New(deps)
	got, _ := h.promptArgument(context.Background(), analystPC(), "dataset", "ord")
	assert.ElementsMatch(t, []string{"orders", "order_items"}, got)
}

func TestNilProvidersAndErrorsDegrade(t *testing.T) {
	// Nil providers.
	h := New(Deps{})
	ctx := context.Background()
	names, cov := h.datasetNames(ctx, "x")
	assert.Nil(t, names)
	assert.Equal(t, coverageComplete, cov)
	topics, _ := h.topics(ctx, "x")
	assert.Nil(t, topics)
	terms, _ := h.glossaryTerms(ctx, "x")
	assert.Nil(t, terms)
	assert.Nil(t, h.schemaVars(ctx, "catalog", "", nil))

	// Upstream errors degrade to empty.
	sem := &fakeSemantic{searchErr: errors.New("boom")}
	sem.Provider = semantic.NewNoopProvider()
	qry := &fakeQuery{err: errors.New("boom")}
	qry.Provider = query.NewNoopProvider()
	h = New(Deps{Semantic: sem, Query: qry})
	names, _ = h.datasetNames(ctx, "x")
	assert.Nil(t, names)
	topics, _ = h.topics(ctx, "x")
	assert.Empty(t, topics)
	terms, _ = h.glossaryTerms(ctx, "x")
	assert.Nil(t, terms)
	assert.Nil(t, h.schemaVars(ctx, "catalog", "", nil))
	assert.Nil(t, h.schemaVars(ctx, "schema_name", "", map[string]string{"catalog": "hive"}))
	assert.Nil(t, h.schemaVars(ctx, "table", "", map[string]string{"catalog": "hive", "schema_name": "s"}))
	assert.Nil(t, h.schemaVars(ctx, "mystery", "", nil))
}

func TestPersonaForContextRoleFallback(t *testing.T) {
	deps := testDeps(t)
	h := New(deps)
	// No explicit persona name resolves by role.
	per, ok := h.personaForContext(&middleware.PlatformContext{Roles: []string{"analyst"}})
	require.True(t, ok)
	assert.Equal(t, "analyst", per.Name)
	// Unknown explicit name falls back to a role match.
	per, ok = h.personaForContext(&middleware.PlatformContext{PersonaName: "ghost", Roles: []string{"analyst"}})
	require.True(t, ok)
	assert.Equal(t, "analyst", per.Name)
	// Nil registry → not resolvable.
	nilReg := New(Deps{})
	_, ok = nilReg.personaForContext(&middleware.PlatformContext{Roles: []string{"analyst"}})
	assert.False(t, ok)
}

// --- handler (auth, cap, timeout, shaping) --------------------------------

func promptReq(value string) *mcp.CompleteRequest {
	return &mcp.CompleteRequest{Params: &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "trace-data-lineage"},
		Argument: mcp.CompleteParamsArgument{Name: "dataset", Value: value},
	}}
}

func TestHandlerUnauthenticatedEmpty(t *testing.T) {
	deps := testDeps(t)
	deps.Authenticator = stubAuth{err: errUnauthenticated}
	h := New(deps)
	res, err := h.Handler()(context.Background(), promptReq("ord"))
	require.NoError(t, err)
	assert.Empty(t, res.Completion.Values)
}

func TestHandlerAuthenticatedRoundTrip(t *testing.T) {
	deps := testDeps(t)
	deps.Authenticator = stubAuth{info: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}}
	// The stub authenticator resolves roles=[analyst]; PersonasForRoles maps to the persona name.
	deps.PersonasForRoles = func(roles []string) []string {
		if len(roles) > 0 && roles[0] == "analyst" {
			return []string{"analyst"}
		}
		return nil
	}
	h := New(deps)
	res, err := h.Handler()(context.Background(), promptReq("ord"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"orders", "order_items"}, res.Completion.Values)
	assert.False(t, res.Completion.HasMore)
	assert.Equal(t, 0, res.Completion.Total,
		"the fake cannot count its matches, so completeness is unprovable and Total is omitted")
}

func TestHandlerResolvedArgumentsThreaded(t *testing.T) {
	deps := testDeps(t)
	deps.Authenticator = stubAuth{info: &middleware.UserInfo{UserID: "u1", Roles: []string{"analyst"}}}
	h := New(deps)
	// A schema_name completion carries a resolved catalog on the request context,
	// which the handler must thread through to the provider.
	req := &mcp.CompleteRequest{Params: &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/resource", URI: schemaTemplateURI},
		Argument: mcp.CompleteParamsArgument{Name: "schema_name"},
		Context:  &mcp.CompleteContext{Arguments: map[string]string{"catalog": "hive"}},
	}}
	res, err := h.Handler()(context.Background(), req)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sales", "ops"}, res.Completion.Values)
}

func TestHandlerNilRequestEmpty(t *testing.T) {
	h := New(testDeps(t))
	res, err := h.Handler()(context.Background(), &mcp.CompleteRequest{Params: &mcp.CompleteParams{}})
	require.NoError(t, err)
	assert.Empty(t, res.Completion.Values)
}

func TestHandlerCapAndTotalOmittedWhenTruncated(t *testing.T) {
	sem := &fakeSemantic{}
	for i := range MaxValues + 50 {
		sem.tables = append(sem.tables, semantic.TableSearchResult{Name: "d" + strconv.Itoa(i)})
	}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
		// No authorizer → no persona gating (dataset completions allowed).
	})
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, MaxValues)
	assert.True(t, res.Completion.HasMore)
	assert.Equal(t, 0, res.Completion.Total, "Total omitted when truncated")
}

// clampedHandler builds a handler over a provider that clamps every page to
// clampAt rows, whatever was requested, and counts its matches honestly.
func clampedHandler(clampAt, tableMatches, termMatches int) *Handle {
	sem := &clampedSemantic{clampAt: clampAt, tableMatches: tableMatches, termMatches: termMatches}
	sem.Provider = semantic.NewNoopProvider()
	return New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
		// No authorizer → no persona gating.
	})
}

func glossaryReq(value string) *mcp.CompleteRequest {
	return &mcp.CompleteRequest{Params: &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/resource", URI: glossaryTemplateURI},
		Argument: mcp.CompleteParamsArgument{Name: "term", Value: value},
	}}
}

// TestHandlerClampedPageReportsHasMore is the #1238 regression: the provider
// returns a page far shorter than the value cap because it clamped the request,
// while 50 datasets match. Nothing about the page's length reveals that — it is
// short, which is exactly what an exhausted search looks like — so a layer that
// reads "more exist" off a row count it requested reports the 10 names as the
// whole catalog with Total: 10.
func TestHandlerClampedPageReportsHasMore(t *testing.T) {
	h := clampedHandler(10, 50, 0)
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, 10)
	assert.True(t, res.Completion.HasMore, "50 datasets match; the clamped page holds 10")
	assert.Equal(t, 0, res.Completion.Total, "Total omitted: the returned set is not the whole set")
}

// TestHandlerCountedCompleteSetReportsTotal is the other side of the same signal:
// when the count says the page holds every match, the set is complete and Total
// is the number a client can trust.
func TestHandlerCountedCompleteSetReportsTotal(t *testing.T) {
	h := clampedHandler(10, 3, 0)
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, 3)
	assert.False(t, res.Completion.HasMore)
	assert.Equal(t, 3, res.Completion.Total)
}

// TestHandlerTopicArmReportsHasMore covers the merged topic completion, whose
// glossary and data-product arms are both clamped searches.
func TestHandlerTopicArmReportsHasMore(t *testing.T) {
	h := clampedHandler(5, 0, 40)
	req := promptReq("")
	req.Params.Argument.Name = "topic"
	res, err := h.Handler()(context.Background(), req)
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, 5)
	assert.True(t, res.Completion.HasMore, "40 glossary terms match; the clamped page holds 5")
	assert.Equal(t, 0, res.Completion.Total)
}

// TestHandlerGlossaryTemplateReportsHasMore covers the glossary:// resource
// template, whose only source is the doubly-clamped glossary search.
func TestHandlerGlossaryTemplateReportsHasMore(t *testing.T) {
	h := clampedHandler(5, 0, 40)
	res, err := h.Handler()(context.Background(), glossaryReq(""))
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, 5)
	assert.True(t, res.Completion.HasMore)
	assert.Equal(t, 0, res.Completion.Total)
}

// TestHandlerUncountedProviderClaimsNothing pins the fallback: a provider that
// cannot count leaves both fields off rather than declaring the page complete.
func TestHandlerUncountedProviderClaimsNothing(t *testing.T) {
	sem := &fakeSemantic{tables: []semantic.TableSearchResult{{Name: "orders"}}}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
	})
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Equal(t, []string{"orders"}, res.Completion.Values)
	assert.False(t, res.Completion.HasMore)
	assert.Equal(t, 0, res.Completion.Total)
}

// TestHandlerSearchFailureIsNotACompleteSet keeps a degraded lookup from reading
// as "no matches": an empty set from a failed search proves nothing.
func TestHandlerSearchFailureIsNotACompleteSet(t *testing.T) {
	sem := &fakeSemantic{searchErr: errors.New("boom")}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
	})
	names, cov := h.datasetNames(context.Background(), "x")
	assert.Empty(t, names)
	assert.Equal(t, coverageUnknown, cov)

	terms, cov := h.glossaryTerms(context.Background(), "x")
	assert.Empty(t, terms)
	assert.Equal(t, coverageUnknown, cov)
}

// TestTopicArmWithoutPickerCapability covers a catalog that can search datasets
// but has no domain/glossary picker: the two picker arms contribute nothing and
// claim nothing, so the merged coverage is the data-product arm's.
func TestTopicArmWithoutPickerCapability(t *testing.T) {
	sem := &noPickerSemantic{}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{Semantic: sem})

	values, cov := h.topics(context.Background(), "")
	assert.Equal(t, []string{"Revenue Product"}, values)
	assert.Equal(t, coverageUnknown, cov, "an uncounted product search proves nothing")

	terms, cov := h.glossaryTerms(context.Background(), "")
	assert.Empty(t, terms)
	assert.Equal(t, coverageComplete, cov, "no glossary source means no glossary matches")
}

// TestTopicArmDomainListingIsNeverProvable pins the domain arm: the picker
// returns rows and no count, and the listing behind it is bounded upstream, so
// neither a successful nor a failed listing can prove the topic set is whole.
func TestTopicArmDomainListingIsNeverProvable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		domainsErr error
		wantValues int
	}{
		{name: "listing succeeds", wantValues: 3},
		{name: "listing fails", domainsErr: errors.New("datahub down"), wantValues: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sem := &clampedSemantic{clampAt: 10, termMatches: 2, domains: 1, domainsErr: tc.domainsErr}
			sem.Provider = semantic.NewNoopProvider()
			h := New(Deps{Semantic: sem})

			values, cov := h.topics(context.Background(), "")
			assert.Len(t, values, tc.wantValues)
			assert.Equal(t, coverageUnknown, cov)
		})
	}
}

// TestArmsSubstituteTheListAllQuery pins the query a keystroke-free completion
// sends: the empty string reaches a relevance catalog as a query that matches
// nothing, so the first request of every session would return no values.
func TestArmsSubstituteTheListAllQuery(t *testing.T) {
	sem := &clampedSemantic{clampAt: 10, tableMatches: 4, termMatches: 1}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{Semantic: sem})
	ctx := context.Background()

	values, _ := h.datasetNames(ctx, "")
	assert.Len(t, values, 4)
	assert.Equal(t, []string{listAll}, sem.queries, "an empty prefix lists rather than matching nothing")

	sem.queries = nil
	_, _ = h.datasetNames(ctx, "  ord  ")
	assert.Equal(t, []string{"ord"}, sem.queries, "a typed prefix is sent as the query")

	sem.queries = nil
	_, _ = h.dataProductCandidates(ctx, "")
	assert.Equal(t, []string{listAll}, sem.queries)
}

// TestOverCapPageProvesMoreWithoutACount covers the fallback signal: a page
// holding more rows than a response may carry proves matches were left behind,
// whatever the backend reported, even after dedup shrinks the values below the cap.
func TestOverCapPageProvesMoreWithoutACount(t *testing.T) {
	sem := &uncountedPage{rows: fetchLimit, name: "dup"} // all identical → dedups to 1
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
	})
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Equal(t, []string{"dup"}, res.Completion.Values)
	assert.True(t, res.Completion.HasMore, "101 rows in hand outlive a missing count")
	assert.Equal(t, 0, res.Completion.Total)
}

// TestProvenTotalSurvivesTruncation covers the spec's reading of total: it is the
// number of options available, not the number sent, so a proven count is reported
// beside a truncated page.
func TestProvenTotalSurvivesTruncation(t *testing.T) {
	// Exactly fetchLimit datasets match, so the page holds every one of them: the
	// set is provably complete AND larger than a response may carry.
	const matches = fetchLimit
	sem := &clampedSemantic{clampAt: matches, tableMatches: matches}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
	})
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Len(t, res.Completion.Values, MaxValues)
	assert.True(t, res.Completion.HasMore)
	assert.Equal(t, matches, res.Completion.Total,
		"total is the number of options available, not the number sent")
}

// uncountedPage returns a fixed number of identical rows and cannot count.
type uncountedPage struct {
	semantic.Provider
	rows int
	name string
}

func (u *uncountedPage) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	out := make([]semantic.TableSearchResult, 0, u.rows)
	for range u.rows {
		out = append(out, semantic.TableSearchResult{Name: u.name})
	}
	return out, nil
}

// noPickerSemantic answers the dataset search but implements no CatalogPicker.
type noPickerSemantic struct{ semantic.Provider }

func (noPickerSemantic) SearchTables(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	if slices.Contains(filter.EntityTypes, dataProductEntityType) {
		return []semantic.TableSearchResult{{Name: "Revenue Product"}}, nil
	}
	return nil, nil
}

func TestCoverageMerge(t *testing.T) {
	// The weaker claim wins, in either argument order.
	assert.Equal(t, coverageComplete, coverageComplete.merge(coverageComplete))
	assert.Equal(t, coverageUnknown, coverageComplete.merge(coverageUnknown))
	assert.Equal(t, coverageUnknown, coverageUnknown.merge(coverageComplete))
	assert.Equal(t, coverageMore, coverageUnknown.merge(coverageMore))
	assert.Equal(t, coverageMore, coverageMore.merge(coverageComplete))
}

func TestCoverageOf(t *testing.T) {
	assert.Equal(t, coverageMore, coverageOf(10, 50))
	assert.Equal(t, coverageComplete, coverageOf(10, 10))
	// A total that cannot be the match count (unknown, or below the page the
	// caller is holding) proves nothing.
	assert.Equal(t, coverageUnknown, coverageOf(10, semantic.TotalUnknown))
	assert.Equal(t, coverageUnknown, coverageOf(10, 9))
}

func TestHandlerTimeoutDegradesToEmpty(t *testing.T) {
	sem := &blockingSemantic{}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
		Timeout:       20 * time.Millisecond,
	})
	start := time.Now()
	res, err := h.Handler()(context.Background(), promptReq("x"))
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Empty(t, res.Completion.Values)
	assert.Less(t, elapsed, time.Second, "handler must return promptly on timeout")
}

// blockingSemantic ignores ctx cancellation and blocks, exercising the deadline race.
type blockingSemantic struct{ semantic.Provider }

func (blockingSemantic) SearchTables(context.Context, semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	time.Sleep(2 * time.Second)
	return []semantic.TableSearchResult{{Name: "too-late"}}, nil
}
