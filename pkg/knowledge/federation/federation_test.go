package federation

import (
	"context"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// stubToolkit is a minimal registry.Toolkit for registry-walk tests. It
// optionally implements toolkit.ConnectionLister.
type stubToolkit struct {
	kind  string
	name  string
	conns []toolkit.ConnectionDetail
}

func (s *stubToolkit) Kind() string                        { return s.kind }
func (s *stubToolkit) Name() string                        { return s.name }
func (s *stubToolkit) Connection() string                  { return s.name }
func (*stubToolkit) RegisterTools(*mcp.Server)             {}
func (*stubToolkit) Tools() []string                       { return nil }
func (*stubToolkit) SetSemanticProvider(semantic.Provider) {}
func (*stubToolkit) SetQueryProvider(query.Provider)       {}
func (*stubToolkit) Close() error                          { return nil }

// listerToolkit embeds stubToolkit and implements ConnectionLister.
type listerToolkit struct{ stubToolkit }

func (l *listerToolkit) ListConnections() []toolkit.ConnectionDetail { return l.conns }

func TestEndpointSearchers_AdaptsAPIGateways(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register(apigatewaykit.New("api")); err != nil {
		t.Fatalf("register api toolkit: %v", err)
	}
	// A non-api toolkit must not produce an endpoint searcher.
	if err := reg.Register(&stubToolkit{kind: "trino", name: "warehouse"}); err != nil {
		t.Fatalf("register trino toolkit: %v", err)
	}

	searchers := EndpointSearchers(reg)
	if len(searchers) != 1 {
		t.Fatalf("expected 1 endpoint searcher for one api toolkit, got %d", len(searchers))
	}
	// An empty gateway ranks nothing but must not error.
	got, err := searchers[0].SearchEndpoints(context.Background(), "anything", 10)
	if err != nil {
		t.Fatalf("SearchEndpoints error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty gateway should return no candidates, got %+v", got)
	}
}

func TestEndpointSearchers_NoneWhenNoGateways(t *testing.T) {
	reg := registry.NewRegistry()
	if got := EndpointSearchers(reg); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestConnectionLister_WalksRegistry(t *testing.T) {
	reg := registry.NewRegistry()
	// A multi-connection lister toolkit.
	if err := reg.Register(&listerToolkit{
		stubToolkit: stubToolkit{kind: "api", name: "gw"},
	}); err != nil {
		t.Fatalf("register lister toolkit: %v", err)
	}
	// A fallback single-connection data toolkit.
	if err := reg.Register(&stubToolkit{kind: "trino", name: "warehouse"}); err != nil {
		t.Fatalf("register trino toolkit: %v", err)
	}
	// A non-data toolkit (e.g. the search tool itself) must be skipped.
	if err := reg.Register(&stubToolkit{kind: "search", name: "default"}); err != nil {
		t.Fatalf("register search toolkit: %v", err)
	}

	infos := NewConnectionLister(reg).Connections()
	got := map[string]string{} // name -> kind
	for _, c := range infos {
		got[c.Name] = c.Kind
	}
	if got["warehouse"] != "trino" {
		t.Errorf("expected fallback trino connection 'warehouse', got %+v", infos)
	}
	if _, ok := got["default"]; ok {
		t.Errorf("non-data 'search' toolkit must not appear as a connection: %+v", infos)
	}
}

func TestConnectionLister_IncludesListedConnections(t *testing.T) {
	reg := registry.NewRegistry()
	lt := &listerToolkit{stubToolkit: stubToolkit{kind: "api", name: "gw"}}
	lt.conns = []toolkit.ConnectionDetail{
		{Name: "stripe", Description: "payments"},
		{Name: "shopify", Description: "commerce"},
	}
	if err := reg.Register(lt); err != nil {
		t.Fatalf("register: %v", err)
	}
	infos := NewConnectionLister(reg).Connections()
	if len(infos) != 2 {
		t.Fatalf("expected 2 listed connections, got %+v", infos)
	}
	for _, c := range infos {
		if c.Kind != "api" {
			t.Errorf("expected kind api, got %q", c.Kind)
		}
		if c.Name == "stripe" && c.Description != "payments" {
			t.Errorf("description not carried: %+v", c)
		}
	}
}

// TestEndpointSearchers_AdaptsGraphQLToolkits proves the second kind of
// remote operation joins the same group. Both answer one question —
// which remote operation serves this intent — so a caller narrowing a
// search to "endpoints" must not have to know which kind their
// connection is.
func TestEndpointSearchers_AdaptsGraphQLToolkits(t *testing.T) {
	reg := registry.NewRegistry()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": "https://unreached.invalid/graphql"})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "erp"
	gql := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "erp", Instances: map[string]graphqlkit.Config{"erp": cfg},
	})
	sdl, err := os.ReadFile("../../../internal/gqlschema/testdata/namespaced.graphql")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if err := gql.SetSchema(context.Background(), "erp", sdl); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := reg.Register(gql); err != nil {
		t.Fatalf("register graphql toolkit: %v", err)
	}
	if err := reg.Register(apigatewaykit.New("api")); err != nil {
		t.Fatalf("register api toolkit: %v", err)
	}

	searchers := EndpointSearchers(reg)
	if len(searchers) != 2 {
		t.Fatalf("expected one searcher per operation-serving kind, got %d", len(searchers))
	}

	var candidates []knowledge.EndpointCandidate
	for _, s := range searchers {
		got, err := s.SearchEndpoints(context.Background(), "product query", 10)
		if err != nil {
			t.Fatalf("SearchEndpoints error: %v", err)
		}
		candidates = append(candidates, got...)
	}
	var found *knowledge.EndpointCandidate
	for i, c := range candidates {
		if c.OperationID == "query:masterData.product.query" {
			found = &candidates[i]
		}
	}
	if found == nil {
		t.Fatalf("the GraphQL operation was not federated: %+v", candidates)
	}
	// A GraphQL operation carries the same three coordinates an OpenAPI
	// one does, because that is the space its persona rules are in.
	if found.Connection != "erp" || found.Method != "QUERY" || found.Path != "/masterData/product/query" {
		t.Errorf("candidate = %+v", found)
	}
	if found.Score <= 0 {
		t.Errorf("candidate carries no relevance signal: %+v", found)
	}
}
