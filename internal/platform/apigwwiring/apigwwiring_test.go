package apigwwiring

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// spec is a document with one operation, so a wired catalog store shows up
// as a connection that has operations rather than one that has none.
const spec = `
openapi: 3.0.3
info:
  title: Orders
  version: "1"
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        '200':
          description: OK
`

// otherToolkit is a registered toolkit of another kind, so the wiring is
// shown to pick out the api gateway ones rather than everything.
type otherToolkit struct{}

func (otherToolkit) Kind() string                          { return "other" }
func (otherToolkit) Name() string                          { return "other" }
func (otherToolkit) RegisterTools(*mcp.Server)             {}
func (otherToolkit) Tools() []string                       { return nil }
func (otherToolkit) SetSemanticProvider(semantic.Provider) {}
func (otherToolkit) SetQueryProvider(query.Provider)       {}
func (otherToolkit) Close() error                          { return nil }
func (otherToolkit) Connection() string                    { return "" }

// newRegistry returns a registry holding one api gateway toolkit whose one
// connection references catalogID, plus a toolkit of another kind.
func newRegistry(t *testing.T, catalogID string) *registry.Registry {
	t.Helper()
	cfg, err := apigatewaykit.ParseConfig(map[string]any{
		"base_url": "https://orders.example.com", "catalog_id": catalogID,
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "orders"
	tk := apigatewaykit.NewMulti(apigatewaykit.MultiConfig{
		DefaultName: "api",
		Instances:   map[string]apigatewaykit.Config{"orders": cfg},
	})
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register api gateway: %v", err)
	}
	if err := reg.Register(otherToolkit{}); err != nil {
		t.Fatalf("register other: %v", err)
	}
	return reg
}

// seededCatalog is a catalog store holding one spec under catalogID.
func seededCatalog(t *testing.T, catalogID string) apicatalog.Store {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: catalogID, Name: catalogID, DisplayName: catalogID,
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), catalogID, apicatalog.SpecEntry{
		SpecName: "orders", Content: spec, SourceKind: apicatalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	return store
}

func TestToolkitsPicksOutTheAPIGatewayOnes(t *testing.T) {
	found := Toolkits(newRegistry(t, ""))

	if len(found) != 1 {
		t.Fatalf("found %d api gateway toolkits in a registry holding one", len(found))
	}
	if Toolkits(nil) != nil {
		t.Error("a nil registry produced toolkits")
	}
}

// Wiring the catalog store reloads every connection, so one that registered
// before the store was available picks its operations up at once rather than
// staying empty until the next admin save.
func TestWiringACatalogStoreGivesAConnectionItsOperations(t *testing.T) {
	reg := newRegistry(t, "orders-v1")
	api := Toolkits(reg)[0]
	if got := api.ListConnections()[0].OperationCount; got != 0 {
		t.Fatalf("the connection had %d operations before the store was wired", got)
	}

	CatalogStore(reg, seededCatalog(t, "orders-v1"))

	if got := api.ListConnections()[0].OperationCount; got != 1 {
		t.Errorf("the connection has %d operations after the store was wired", got)
	}
}

func TestWiringNoCatalogStoreChangesNothing(t *testing.T) {
	reg := newRegistry(t, "orders-v1")

	CatalogStore(reg, nil)

	if got := Toolkits(reg)[0].ListConnections()[0].OperationCount; got != 0 {
		t.Errorf("a nil catalog store gave the connection %d operations", got)
	}
}

func TestTheWiredCatalogStoreIsTheOneTheAdminLayerShares(t *testing.T) {
	reg := newRegistry(t, "orders-v1")
	if CurrentCatalogStore(reg) != nil {
		t.Fatal("a catalog store was reported before one was wired")
	}
	store := seededCatalog(t, "orders-v1")

	CatalogStore(reg, store)

	if CurrentCatalogStore(reg) != store {
		t.Error("the admin layer would read a different store than the toolkit does")
	}
	if CurrentCatalogStore(registry.NewRegistry()) != nil {
		t.Error("a registry with no api gateway reported a catalog store")
	}
}

// connections is a connection store holding what another replica saved.
type connections map[string]map[string]any

// GetConnection returns one stored connection.
func (c connections) GetConnection(_ context.Context, name string) (map[string]any, error) {
	config, ok := c[name]
	if !ok {
		return nil, apigatewaykit.ErrConnectionNotFound
	}
	return config, nil
}

func TestWiringAConnectionStoreServesWhatAnotherReplicaSaved(t *testing.T) {
	reg := newRegistry(t, "")
	api := Toolkits(reg)[0]
	saved := connections{"elsewhere": {"base_url": "https://elsewhere.example.com"}}

	if api.ServesConnection(context.Background(), "elsewhere") {
		t.Fatal("a connection was served before the store was wired")
	}
	ConnectionStore(reg, saved)

	if !api.ServesConnection(context.Background(), "elsewhere") {
		t.Error("a connection another replica saved is not served after wiring")
	}
}

func TestWiringNoConnectionStoreLeavesTheToolkitServingWhatItWasHanded(t *testing.T) {
	reg := newRegistry(t, "")

	ConnectionStore(reg, nil)

	if Toolkits(reg)[0].ServesConnection(context.Background(), "elsewhere") {
		t.Error("a connection nobody handed over was served")
	}
}

func TestWiringNoRoutePolicyInstallsNone(t *testing.T) {
	reg := newRegistry(t, "")

	RoutePolicy(reg, nil)

	if Toolkits(reg)[0].RoutePolicy() != nil {
		t.Error("a nil route policy was installed")
	}
}

// allowAll is a route policy that permits everything, so the wiring is
// observed by the toolkit reporting one rather than by a denial.
type allowAll struct{}

// Allow permits every route.
func (allowAll) Allow(context.Context, string, string, string, string) (allowed bool, denial string) {
	return true, ""
}

func TestWiringARoutePolicyInstallsItOnEveryAPIGatewayToolkit(t *testing.T) {
	reg := newRegistry(t, "")

	RoutePolicy(reg, allowAll{})

	if Toolkits(reg)[0].RoutePolicy() == nil {
		t.Error("the route policy was not installed")
	}
}
