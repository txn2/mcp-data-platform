package apigateway

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// specWithInfo builds a minimal valid OpenAPI document carrying an
// explicit info.title and info.description so derivation paths have
// something non-trivial to surface.
func specWithInfo(title, description, pathsBlock string) string {
	return `openapi: 3.0.0
info:
  title: "` + title + `"
  description: "` + description + `"
  version: "1"
paths:
  ` + pathsBlock + `
`
}

// twoSpecConn wires a connection backed by a two-spec catalog: an
// "orders" spec carrying operator overrides and a "users" spec that
// must derive its title/description from info.*. Returns the toolkit
// with connection "c" registered.
func twoSpecConn(t *testing.T) *Toolkit {
	t.Helper()
	tk := New("test")
	store := setupCatalogWithSpec(t, tk, "vendor", "users",
		specWithInfo("Users Service", "Manage users",
			"/users:\n    "+pathOpYAML("get", "listUsers", "List users")))
	orders := newSpecEntry("orders",
		specWithInfo("derived orders title", "derived orders desc",
			"/orders:\n    "+pathOpYAML("get", "listOrders", "List orders")))
	orders.Title = "Orders API"
	orders.Description = "Operator override"
	if err := store.UpsertSpec(context.Background(), "vendor", orders); err != nil {
		t.Fatalf("UpsertSpec orders: %v", err)
	}
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

func TestHandleListSpecs_RejectsMissingConnection(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleListSpecs(context.Background(), nil, ListSpecsInput{})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for missing connection")
	}
}

func TestHandleListSpecs_UnknownConnection(t *testing.T) {
	tk := New("test")
	res, _, err := tk.handleListSpecs(context.Background(), nil, ListSpecsInput{Connection: "nope"})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for unknown connection")
	}
}

func TestHandleListSpecs_NoCatalog(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c1", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, out, err := tk.handleListSpecs(context.Background(), nil, ListSpecsInput{Connection: "c1"})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if res.IsError {
		t.Errorf("connection without catalog should not be an error: %s", textContent(res))
	}
	o, ok := out.(ListSpecsOutput)
	if !ok {
		t.Fatalf("out type %T", out)
	}
	if len(o.Specs) != 0 {
		t.Errorf("expected empty specs, got %v", o.Specs)
	}
	if o.Note == "" {
		t.Error("expected a note for catalog-less connection")
	}
}

func TestHandleListSpecs_HappyPath(t *testing.T) {
	tk := twoSpecConn(t)
	res, out, err := tk.handleListSpecs(context.Background(), nil, ListSpecsInput{Connection: "c"})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", textContent(res))
	}
	o, ok := out.(ListSpecsOutput)
	if !ok {
		t.Fatalf("out type %T", out)
	}
	if len(o.Specs) != 2 {
		t.Fatalf("expected 2 specs, got %d (%+v)", len(o.Specs), o.Specs)
	}
	// Sorted by name: orders before users.
	if o.Specs[0].Name != "orders" || o.Specs[1].Name != "users" {
		t.Fatalf("expected sorted [orders users], got [%s %s]", o.Specs[0].Name, o.Specs[1].Name)
	}
	// Operator override wins on the orders spec.
	if o.Specs[0].Title != "Orders API" {
		t.Errorf("orders title: want override %q, got %q", "Orders API", o.Specs[0].Title)
	}
	if o.Specs[0].Description != "Operator override" {
		t.Errorf("orders description: want override, got %q", o.Specs[0].Description)
	}
	// Derived values on the users spec.
	if o.Specs[1].Title != "Users Service" {
		t.Errorf("users title: want derived %q, got %q", "Users Service", o.Specs[1].Title)
	}
	if o.Specs[1].Description != "Manage users" {
		t.Errorf("users description: want derived %q, got %q", "Manage users", o.Specs[1].Description)
	}
	if o.Specs[0].OperationCount != 1 || o.Specs[1].OperationCount != 1 {
		t.Errorf("expected operation_count 1 per spec, got orders=%d users=%d",
			o.Specs[0].OperationCount, o.Specs[1].OperationCount)
	}
}

func TestHandleListEndpoints_MultiSpecGatesOnEmptySpec(t *testing.T) {
	tk := twoSpecConn(t)
	res, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{Connection: "c"})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if res.IsError {
		t.Fatalf("gate response should not be an error: %s", textContent(res))
	}
	o, ok := out.(ListEndpointsOutput)
	if !ok {
		t.Fatalf("out type %T", out)
	}
	if len(o.Operations) != 0 {
		t.Errorf("gate should return no operations, got %d", len(o.Operations))
	}
	if len(o.Specs) != 2 {
		t.Errorf("gate should return 2 spec summaries, got %d", len(o.Specs))
	}
	if o.Note == "" {
		t.Error("gate should carry a note pointing at the spec filter")
	}
}

func TestHandleListEndpoints_MultiSpecExplicitSpecFallsThrough(t *testing.T) {
	tk := twoSpecConn(t)
	_, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{
		Connection: "c",
		Spec:       "orders",
	})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	o, _ := out.(ListEndpointsOutput)
	if len(o.Specs) != 0 {
		t.Errorf("explicit spec must not trigger the gate, got %d summaries", len(o.Specs))
	}
	if len(o.Operations) != 1 {
		t.Fatalf("expected 1 operation from orders spec, got %d", len(o.Operations))
	}
}

func TestHandleListEndpoints_SingleSpecPassthrough(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	_, out, err := tk.handleListEndpoints(context.Background(), nil, ListEndpointsInput{Connection: "c1"})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	o, _ := out.(ListEndpointsOutput)
	if len(o.Specs) != 0 {
		t.Errorf("single-spec catalog must not trigger the gate, got %d summaries", len(o.Specs))
	}
	if len(o.Operations) != 5 {
		t.Errorf("expected 5 operations unchanged, got %d", len(o.Operations))
	}
}

func TestSpecSummaryTitleDescription_Precedence(t *testing.T) {
	doc, err := catalog.ParseSpec(specWithInfo("Derived Title", "Derived Desc",
		"/x:\n    "+pathOpYAML("get", "getX", "Get X")))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if got := specSummaryTitle(catalog.SpecEntry{Title: "Override"}, doc); got != "Override" {
		t.Errorf("override title should win, got %q", got)
	}
	if got := specSummaryTitle(catalog.SpecEntry{}, doc); got != "Derived Title" {
		t.Errorf("empty override should derive info.title, got %q", got)
	}
	if got := specSummaryDescription(catalog.SpecEntry{Description: "OverrideD"}, doc); got != "OverrideD" {
		t.Errorf("override description should win, got %q", got)
	}
	if got := specSummaryDescription(catalog.SpecEntry{}, doc); got != "Derived Desc" {
		t.Errorf("empty override should derive info.description, got %q", got)
	}
	// A nil document must not panic and yields empty derived values.
	if got := specSummaryTitle(catalog.SpecEntry{}, nil); got != "" {
		t.Errorf("nil doc title should be empty, got %q", got)
	}
	if got := specSummaryDescription(catalog.SpecEntry{}, nil); got != "" {
		t.Errorf("nil doc description should be empty, got %q", got)
	}
}
