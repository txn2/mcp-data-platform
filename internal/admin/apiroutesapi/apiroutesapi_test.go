package apiroutesapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/admin/apiroutesapi"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

const spec = `
openapi: 3.0.3
info: {title: Orders, version: "1.0"}
paths:
  /v1/orders:
    get:
      operationId: listOrders
      summary: List orders
      tags: [orders]
      responses: {"200": {description: ok}}
  /v1/orders/{id}:
    delete:
      operationId: deleteOrder
      summary: Delete an order
      tags: [orders]
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: deleted}}
`

// stubRegistry serves a fixed toolkit list.
type stubRegistry struct{ toolkits []registry.Toolkit }

func (s stubRegistry) All() []registry.Toolkit { return s.toolkits }

// denyAll refuses every route, standing in for the editing operator's own
// persona having rules of its own.
type denyAll struct{}

func (denyAll) Allow(_ context.Context, _, _, _, _ string) (allowed bool, reason string) {
	return false, "denied"
}

// apiToolkit builds an api-gateway toolkit with one cataloged connection.
func apiToolkit(t *testing.T) *apigatewaykit.Toolkit {
	t.Helper()
	tk := apigatewaykit.New("api")
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	if err := store.CreateCatalog(t.Context(), catalog.Catalog{
		ID: "orders", Name: "orders", DisplayName: "Orders",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(t.Context(), "orders", catalog.SpecEntry{
		SpecName: "default", Content: spec, SourceKind: "inline",
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	if err := tk.AddConnection("crm", map[string]any{
		"base_url": "https://api.example.com", "catalog_id": "orders",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

// listing serves the route and decodes its body.
func listing(t *testing.T, cfg apiroutesapi.Config) (status int, body map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	apiroutesapi.Register(mux, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/admin/api-route-connections", http.NoBody))
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func TestListConnections_ReturnsEveryOperationTheCatalogDeclares(t *testing.T) {
	code, body := listing(t, apiroutesapi.Config{
		Toolkits: stubRegistry{toolkits: []registry.Toolkit{apiToolkit(t)}},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	conns, _ := body["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	conn, _ := conns[0].(map[string]any)
	if conn["name"] != "crm" {
		t.Errorf("name = %v, want crm", conn["name"])
	}
	if conn["catalog_id"] != "orders" {
		t.Errorf("catalog_id = %v, want orders", conn["catalog_id"])
	}
	ops, _ := conn["operations"].([]any)
	if len(ops) != 2 {
		t.Fatalf("operations = %d, want 2", len(ops))
	}
	// The path is the one the catalog declares, placeholders and all: that is
	// the form a rule is written in and the form a listing surface matches on.
	paths := map[string]bool{}
	for _, raw := range ops {
		op, _ := raw.(map[string]any)
		path, _ := op["path"].(string)
		paths[path] = true
	}
	if !paths["/v1/orders/{id}"] {
		t.Errorf("the templated path is absent from %v", paths)
	}
}

// The reason this surface exists rather than reusing the caller-scoped browse
// routes: an operator writing rules for one persona is not that persona, and a
// listing narrowed by the operator's own rules would hide exactly the
// operations they are trying to grant back.
func TestListConnections_IsNotNarrowedByTheRoutePolicy(t *testing.T) {
	tk := apiToolkit(t)
	tk.SetRoutePolicy(denyAll{})

	_, body := listing(t, apiroutesapi.Config{
		Toolkits: stubRegistry{toolkits: []registry.Toolkit{tk}},
	})
	conns, _ := body["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("a deny-all policy removed the connection: %v", body)
	}
	conn, _ := conns[0].(map[string]any)
	ops, _ := conn["operations"].([]any)
	if len(ops) != 2 {
		t.Errorf("operations = %d under a deny-all policy, want the catalog's 2", len(ops))
	}
}

func TestListConnections_SkipsToolkitsOfOtherKinds(t *testing.T) {
	_, body := listing(t, apiroutesapi.Config{
		Toolkits: stubRegistry{toolkits: nil},
	})
	conns, _ := body["connections"].([]any)
	if len(conns) != 0 {
		t.Errorf("connections = %d with no api toolkit, want 0", len(conns))
	}
	if body["total"] != float64(0) {
		t.Errorf("total = %v, want 0", body["total"])
	}
}

// A deployment that cannot enumerate its toolkits registers nothing rather
// than answering "there are no API connections", which the editor would render
// as a persona having nothing to be granted.
func TestRegister_MountsNothingWithoutARegistry(t *testing.T) {
	mux := http.NewServeMux()
	apiroutesapi.Register(mux, apiroutesapi.Config{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/admin/api-route-connections", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
