package federation

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
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
