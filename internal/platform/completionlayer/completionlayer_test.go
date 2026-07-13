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
	got, more := h.promptArgument(context.Background(), analystPC(), "dataset", "ord")
	assert.ElementsMatch(t, []string{"orders", "order_items"}, got)
	assert.False(t, more)
}

func TestPromptTopicCompletions(t *testing.T) {
	h := New(testDeps(t))
	got, _ := h.promptArgument(context.Background(), analystPC(), "topic", "")
	assert.ElementsMatch(t, []string{"Finance", "Revenue Product", "Gross Revenue"}, got)
}

func TestPromptConnectionCompletionsGated(t *testing.T) {
	h := New(testDeps(t))
	got, more := h.promptArgument(context.Background(), analystPC(), "connection", "")
	assert.Equal(t, []string{"prod-trino"}, got)
	assert.False(t, more)

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
		vals, more := h.values(ctx, caller, ref, arg, nil)
		assert.Nil(t, vals)
		assert.False(t, more)
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
	names, more := h.datasetNames(ctx, "x")
	assert.Nil(t, names)
	assert.False(t, more)
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
	assert.Equal(t, 2, res.Completion.Total)
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

func TestHandlerProviderSignalsHasMore(t *testing.T) {
	// A page-bounded search that returns exactly fetchLimit rows but dedups to
	// fewer than the cap still reports HasMore.
	sem := &fakeSemantic{}
	for range fetchLimit {
		sem.tables = append(sem.tables, semantic.TableSearchResult{Name: "dup"}) // all identical → dedups to 1
	}
	sem.Provider = semantic.NewNoopProvider()
	h := New(Deps{
		Authenticator: stubAuth{info: &middleware.UserInfo{UserID: "u1"}},
		Semantic:      sem,
	})
	res, err := h.Handler()(context.Background(), promptReq(""))
	require.NoError(t, err)
	assert.Equal(t, []string{"dup"}, res.Completion.Values)
	assert.True(t, res.Completion.HasMore, "provider signals more even though dedup shrank the set")
	assert.Equal(t, 0, res.Completion.Total)
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
