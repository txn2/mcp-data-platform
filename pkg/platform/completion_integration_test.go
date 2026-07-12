package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/completionlayer"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// These tests prove the completion/complete wiring end-to-end: the platform
// advertises the capability and, over the real server transport, routes a
// completion request through the completionlayer seam to the value providers.
// The seam's own logic (per-provider values, persona gating, cap/timeout) is
// covered by internal/platform/completionlayer.

var errUnauthenticated = errors.New("unauthenticated")

type completionFakeSemantic struct {
	semantic.Provider
	tables   []semantic.TableSearchResult
	products []semantic.TableSearchResult
	domains  []semantic.EntityRef
	terms    []semantic.EntityRef
}

func (f *completionFakeSemantic) SearchTables(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	if slices.Contains(filter.EntityTypes, "DATA_PRODUCT") {
		return f.products, nil
	}
	return f.tables, nil
}

func (f *completionFakeSemantic) ListDomains(context.Context) ([]semantic.EntityRef, error) {
	return f.domains, nil
}

func (f *completionFakeSemantic) SearchGlossaryTerms(context.Context, string, int) ([]semantic.EntityRef, error) {
	return f.terms, nil
}

type completionRolesMapper struct{ reg *persona.Registry }

func (completionRolesMapper) MapToRoles(map[string]any) ([]string, error) { return nil, nil }
func (m completionRolesMapper) MapToPersona(_ context.Context, roles []string) (*persona.Persona, error) {
	if per, ok := m.reg.GetForRoles(roles); ok {
		return per, nil
	}
	return nil, errUnauthenticated
}

func completionTestPlatform(t *testing.T, roles []string) *Platform {
	t.Helper()
	preg := persona.NewRegistry()
	require.NoError(t, preg.Register(&persona.Persona{
		Name:     "analyst",
		Roles:    []string{"analyst"},
		Tools:    persona.ToolRules{Allow: []string{"search", "trino_browse"}},
		Priority: 10,
	}))
	require.NoError(t, preg.Register(&persona.Persona{
		Name:     "viewer",
		Roles:    []string{"viewer"},
		Tools:    persona.ToolRules{Allow: []string{"platform_info"}},
		Priority: 5,
	}))

	sem := &completionFakeSemantic{
		tables:  []semantic.TableSearchResult{{Name: "orders"}, {Name: "order_items"}},
		domains: []semantic.EntityRef{{Name: "Finance"}},
	}
	sem.Provider = semantic.NewNoopProvider()

	return &Platform{
		config:           &Config{},
		authenticator:    stubAuthenticator{info: &middleware.UserInfo{UserID: "u1", Email: "u@example.com", Roles: roles}},
		authorizer:       persona.NewAuthorizer(preg, completionRolesMapper{reg: preg}),
		personaRegistry:  preg,
		toolkitRegistry:  registry.NewRegistry(),
		semanticProvider: sem,
		queryProvider:    query.NewNoopProvider(),
	}
}

// completionHandler mirrors the CompletionHandler wiring in New so the test
// exercises the same construction path.
func (p *Platform) completionHandler() func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	if p.buildServerCapabilities().Completions == nil {
		return nil
	}
	deps := completionlayer.Deps{
		Authenticator:   p.authenticator,
		Authorizer:      p.authorizer,
		AdminPersona:    p.config.Admin.Persona,
		Semantic:        p.semanticProvider,
		Query:           p.queryProvider,
		Registry:        p.toolkitRegistry,
		PersonaRegistry: p.personaRegistry,
	}
	if p.personaRegistry != nil {
		deps.PersonasForRoles = personasForRolesFunc(p.personaRegistry)
	}
	return completionlayer.New(deps).Handler()
}

func completionTestSession(t *testing.T, p *Platform) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, &mcp.ServerOptions{
		Capabilities:      p.buildServerCapabilities(),
		CompletionHandler: p.completionHandler(),
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestCompletionCapabilityAdvertised(t *testing.T) {
	p := completionTestPlatform(t, []string{"analyst"})
	require.NotNil(t, p.buildServerCapabilities().Completions,
		"completions capability must be advertised when prompts/resources exist")
	require.NotNil(t, p.completionHandler(), "handler must be wired when completions are enabled")

	// With no prompts and no resources, neither is present.
	bare := &Platform{config: &Config{
		Knowledge: KnowledgeConfig{Enabled: new(bool)}, // explicitly disabled
		Resources: ResourcesConfig{Enabled: new(bool)}, // explicitly disabled
	}}
	assert.Nil(t, bare.buildServerCapabilities().Completions)
	assert.Nil(t, bare.completionHandler())
}

func TestCompletionRoundTripThroughServer(t *testing.T) {
	p := completionTestPlatform(t, []string{"analyst"})
	session := completionTestSession(t, p)

	// Prompt dataset argument.
	res, err := session.Complete(context.Background(), &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "trace-data-lineage"},
		Argument: mcp.CompleteParamsArgument{Name: "dataset", Value: "ord"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"orders", "order_items"}, res.Completion.Values)

	// Resource schema-template variable.
	cats, err := session.Complete(context.Background(), &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/resource", URI: schemaTemplateURI},
		Argument: mcp.CompleteParamsArgument{Name: "schema_name"},
		Context:  &mcp.CompleteContext{Arguments: map[string]string{"catalog": "hive"}},
	})
	require.NoError(t, err)
	// The noop query provider lists no schemas, so the completion is empty but
	// the round-trip proves the resource route is wired.
	assert.Empty(t, cats.Completion.Values)
}

func TestCompletionPersonaFilteredThroughServer(t *testing.T) {
	// The viewer persona is denied search, so it completes no dataset names.
	p := completionTestPlatform(t, []string{"viewer"})
	session := completionTestSession(t, p)

	res, err := session.Complete(context.Background(), &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "trace-data-lineage"},
		Argument: mcp.CompleteParamsArgument{Name: "dataset", Value: "ord"},
	})
	require.NoError(t, err)
	assert.Empty(t, res.Completion.Values)
}

func TestCompletionUnauthenticatedThroughServer(t *testing.T) {
	p := completionTestPlatform(t, []string{"analyst"})
	p.authenticator = stubAuthenticator{err: errUnauthenticated}
	session := completionTestSession(t, p)

	res, err := session.Complete(context.Background(), &mcp.CompleteParams{
		Ref:      &mcp.CompleteReference{Type: "ref/prompt", Name: "trace-data-lineage"},
		Argument: mcp.CompleteParamsArgument{Name: "dataset", Value: "ord"},
	})
	require.NoError(t, err)
	assert.Empty(t, res.Completion.Values)
}
