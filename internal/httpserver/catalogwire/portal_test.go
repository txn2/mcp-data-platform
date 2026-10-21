package catalogwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/internal/httpserver/catalogapi"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

func TestCatalogRegistrarMountsTheCatalogRoutes(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	reg := PortalRegistrar(p, catalogfake.New(), nil, func([]string) bool { return true })
	require.NotNil(t, reg)
	mux := http.NewServeMux()
	reg(mux)
	for _, path := range []string{"/api/v1/portal/catalog/connections", "/api/v1/portal/catalog/datasets"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		_, pattern := mux.Handler(req)
		assert.NotEmpty(t, pattern, "no handler for %s", path)
	}
}

func TestCatalogConnectionsListsTheCatalogedKinds(t *testing.T) {
	assert.Equal(t, []catalogapi.Connection{}, Connections(nil))

	multi, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "primary",
		Instances:         map[string]trinokit.Config{"primary": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = multi.Close() })
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(multi))
	require.NoError(t, reg.Register(graphqlToolkit(t)))
	got := Connections(reg)
	assert.Contains(t, got, catalogapi.Connection{Kind: "trino", Name: "primary"})
	assert.Contains(t, got, catalogapi.Connection{Kind: "graphql", Name: "gh"})
	assert.Empty(t, Connections(registry.NewRegistry()))
}

func TestCatalogLabelerNamesDefinitions(t *testing.T) {
	store := catalogfake.New()
	term, err := store.PutDefinition(context.Background(), catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Net Revenue"})
	require.NoError(t, err)

	got := Labeler{Store: store}.Labels(context.Background(), []string{
		term.Reference, "mcp:glossary_term:absent", "not a reference",
	})
	assert.Equal(t, map[string]string{term.Reference: "Net Revenue"}, got)
}
