package apigateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// petstoreSpec is a small Petstore-shaped spec used by the
// schema-lookup tests. Includes a path parameter, a request body,
// and explicit response shapes so the flattener has something to
// chew on. Security and servers are present at the doc level so we
// can confirm they DO NOT leak into the response.
const petstoreSpec = `
openapi: 3.0.3
info:
  title: Petstore
  version: "1.0"
servers:
  - url: https://petstore.example.com/v1
security:
  - apiKey: []
components:
  securitySchemes:
    apiKey:
      type: apiKey
      in: header
      name: X-API-Key
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      description: Return paginated pets
      parameters:
        - name: limit
          in: query
          required: false
          description: How many items
          schema:
            type: integer
            default: 50
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id:
                      type: integer
                    name:
                      type: string
                  required: [id, name]
    post:
      operationId: createPet
      summary: Create a pet
      requestBody:
        required: true
        description: Pet to create
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
                age:
                  type: integer
              required: [name]
      responses:
        '201':
          description: Created
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:
                    type: integer
                  name:
                    type: string
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        '200':
          description: OK
`

func setupSchemaLookupTk(t *testing.T) *Toolkit {
	t.Helper()
	tk := New("api")
	setupCatalogWithSpec(t, tk, "petstore", "default", petstoreSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://petstore.example.com",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

func parseSchemaResult(t *testing.T, r *mcp.CallToolResult) EndpointSchemaOutput {
	t.Helper()
	text := textContent(r)
	var out EndpointSchemaOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v\npayload: %s", err, text)
	}
	return out
}

func TestGetEndpointSchema_ListPets(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, err := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "listPets",
	})
	if err != nil || r.IsError {
		t.Fatalf("listPets: err=%v isError=%v body=%s", err, r.IsError, textContent(r))
	}
	out := parseSchemaResult(t, r)
	if out.OperationID != "listPets" || out.Method != "GET" || out.Path != "/v1/pets" {
		t.Fatalf("unexpected op: %+v", out)
	}
	if len(out.Parameters) != 1 || out.Parameters[0].Name != "limit" {
		t.Fatalf("parameters: %+v", out.Parameters)
	}
	if out.RequestBody != nil {
		t.Errorf("GET should have no request body")
	}
	if len(out.Responses) != 1 || out.Responses[0].Status != "200" {
		t.Fatalf("responses: %+v", out.Responses)
	}
	if !strings.Contains(textContent(r), `"type"`) {
		t.Errorf("response should contain a type field from the schema")
	}
}

func TestGetEndpointSchema_StripsSecurityAndServers(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "listPets",
	})
	body := textContent(r)
	if strings.Contains(body, "security") {
		t.Errorf("response leaked security fields: %s", body)
	}
	if strings.Contains(body, "servers") {
		t.Errorf("response leaked servers fields: %s", body)
	}
	if strings.Contains(body, "X-API-Key") {
		t.Errorf("response leaked auth header name from securitySchemes: %s", body)
	}
}

func TestGetEndpointSchema_CreatePetHasRequestBody(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "createPet",
	})
	out := parseSchemaResult(t, r)
	if out.RequestBody == nil || !out.RequestBody.Required {
		t.Fatalf("expected required request body: %+v", out.RequestBody)
	}
	if len(out.RequestBody.ContentTypes) != 1 || out.RequestBody.ContentTypes[0] != "application/json" {
		t.Errorf("content types: %+v", out.RequestBody.ContentTypes)
	}
}

func TestGetEndpointSchema_PathParameter(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "getPet",
	})
	out := parseSchemaResult(t, r)
	if len(out.Parameters) != 1 {
		t.Fatalf("expected 1 path param: %+v", out.Parameters)
	}
	if out.Parameters[0].In != "path" || !out.Parameters[0].Required {
		t.Errorf("unexpected param: %+v", out.Parameters[0])
	}
}

func TestGetEndpointSchema_OperationNotFound(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "ghost",
	})
	if !r.IsError {
		t.Fatal("expected error result for missing op")
	}
	if !strings.Contains(textContent(r), "not found") {
		t.Errorf("error body: %s", textContent(r))
	}
}

func TestGetEndpointSchema_RequiresConnection(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		OperationID: "listPets",
	})
	if !r.IsError {
		t.Fatal("expected error for missing connection")
	}
}

func TestGetEndpointSchema_RequiresOperationID(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c",
	})
	if !r.IsError {
		t.Fatal("expected error for missing operation_id")
	}
}

func TestGetEndpointSchema_ConnectionMissing(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "ghost", OperationID: "listPets",
	})
	if !r.IsError {
		t.Fatal("expected error for missing connection")
	}
}

func TestGetEndpointSchema_NoSpecConfigured(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c", map[string]any{
		"base_url": "https://x",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "anything",
	})
	if !r.IsError {
		t.Fatal("expected error for spec-less connection")
	}
}

func TestGetEndpointSchema_AmbiguousAcrossSpecs(t *testing.T) {
	tk := New("api")
	store := setupCatalogWithSpec(t, tk, "vendor", "users",
		minimalSpecWith(`/v1/things:
    `+pathOpYAML("get", "list", "Users-side list")))
	if err := store.UpsertSpec(context.Background(), "vendor",
		newSpecEntry("orders", minimalSpecWith(`/v1/things:
    `+pathOpYAML("get", "list", "Orders-side list")))); err != nil {
		t.Fatalf("UpsertSpec orders: %v", err)
	}
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "list",
	})
	if !r.IsError {
		t.Fatal("expected ambiguity error")
	}
	var amb ambiguousSchemaError
	if err := json.Unmarshal([]byte(textContent(r)), &amb); err != nil {
		t.Fatalf("unmarshal ambiguity: %v\nbody=%s", err, textContent(r))
	}
	if len(amb.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d (%+v)", len(amb.Candidates), amb.Candidates)
	}
}

func TestGetEndpointSchema_SpecDisambiguates(t *testing.T) {
	tk := New("api")
	store := setupCatalogWithSpec(t, tk, "vendor", "users",
		minimalSpecWith(`/v1/users:
    `+pathOpYAML("get", "list", "Users-side list")))
	if err := store.UpsertSpec(context.Background(), "vendor",
		newSpecEntry("orders", minimalSpecWith(`/v1/orders:
    `+pathOpYAML("get", "list", "Orders-side list")))); err != nil {
		t.Fatalf("UpsertSpec orders: %v", err)
	}
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://x",
		"catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	r, _, _ := tk.handleGetEndpointSchema(context.Background(), nil, GetEndpointSchemaInput{
		Connection: "c", OperationID: "list", Spec: "orders",
	})
	if r.IsError {
		t.Fatalf("expected success with spec filter: %s", textContent(r))
	}
	out := parseSchemaResult(t, r)
	if out.Spec != "orders" || out.Path != "/v1/orders" {
		t.Errorf("disambiguated to wrong op: %+v", out)
	}
}

func TestSchemaToValue_RespectsDepthCap(t *testing.T) {
	// nil ref short-circuits to nil regardless of depth.
	if got := schemaToValue(nil, maxSchemaDepth+1); got != nil {
		t.Errorf("nil ref should produce nil, got %v", got)
	}

	// Non-nil ref at the depth cap must return the truncation
	// marker rather than walking the schema further. Without this
	// branch a recursive schema would expand forever — the
	// regression we're guarding against.
	ref := &openapi3.SchemaRef{Value: &openapi3.Schema{}}
	got := schemaToValue(ref, maxSchemaDepth)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any at depth cap, got %T", got)
	}
	truncated, _ := m["truncated"].(bool)
	if !truncated {
		t.Errorf("expected truncated=true at depth cap, got %v", m)
	}

	// One level below the cap must NOT truncate — the same input
	// returns the populated schema map.
	got = schemaToValue(ref, maxSchemaDepth-1)
	m, ok = got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any below cap, got %T", got)
	}
	if _, truncated := m["truncated"]; truncated {
		t.Errorf("did not expect truncation below cap, got %v", m)
	}
}

func TestCappedJSONResult_LargeSchemaTruncates(t *testing.T) {
	big := make([]ParameterDetail, 0, 200)
	for range 200 {
		big = append(big, ParameterDetail{
			Name:        "p_" + strings.Repeat("x", 80),
			In:          "query",
			Description: strings.Repeat("verbose ", 30),
			Schema:      map[string]any{"type": "string"},
		})
	}
	out := EndpointSchemaOutput{
		OperationID: "huge",
		Method:      "GET",
		Path:        "/huge",
		Parameters:  big,
	}
	r := cappedJSONResult(out)
	body := textContent(r)
	if len(body) > maxResponseChars+200 {
		t.Errorf("capped body=%d chars; exceeds cap %d significantly", len(body), maxResponseChars)
	}
	if !strings.Contains(body, "schema details elided") {
		t.Errorf("expected truncation note in body: %s", body[:300])
	}
}

func TestCatalogPackageContractExported(_ *testing.T) {
	// Smoke: ensure the catalog package still satisfies the Store
	// interface check from this side of the import boundary. Without
	// this, a downstream Store refactor that breaks the contract
	// would only surface when a non-test caller is compiled.
	var _ catalog.Store = catalog.NewMemoryStore()
}

func TestReloadConnectionsByCatalog_AcrossMultipleConns(t *testing.T) {
	tk := New("api")
	store := setupCatalogWithSpec(t, tk, "petstore", "default",
		minimalSpecWith(`/v1/a:
    `+pathOpYAML("get", "a", "A")))
	for _, name := range []string{"prod", "staging", "sandbox"} {
		if err := tk.AddConnection(name, map[string]any{
			"base_url":   "https://x",
			"catalog_id": "petstore",
		}); err != nil {
			t.Fatalf("AddConnection %s: %v", name, err)
		}
	}
	if err := store.UpsertSpec(context.Background(), "petstore",
		newSpecEntry("default", minimalSpecWith(`/v1/b:
    `+pathOpYAML("get", "b", "B")))); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	tk.ReloadConnectionsByCatalog("petstore")
	for _, name := range []string{"prod", "staging", "sandbox"} {
		tk.mu.RLock()
		c := tk.connections[name]
		tk.mu.RUnlock()
		if c == nil || len(c.operations) != 1 || c.operations[0].OperationID != "b" {
			t.Errorf("%s did not pick up new spec: %+v", name, c.operations)
		}
	}
}

func TestReloadConnectionsByCatalog_NoOpEmpty(_ *testing.T) {
	tk := New("api")
	// Should not panic with no connections.
	tk.ReloadConnectionsByCatalog("")
	tk.ReloadConnectionsByCatalog("nonexistent")
}

func TestReloadConnection_NotFound(t *testing.T) {
	tk := New("api")
	err := tk.ReloadConnection("ghost")
	if err == nil {
		t.Fatal("expected error for missing connection")
	}
}

func TestPreferredContentType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"only application/json", []string{"application/json"}, "application/json"},
		{"json wins over xml", []string{"application/json", "application/xml"}, "application/json"},
		{"json wins over xml regardless of order", []string{"application/xml", "application/json"}, "application/json"},
		{"no json falls to first sorted", []string{"application/xml", "text/plain"}, "application/xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := preferredContentType(c.in)
			if got != c.want {
				t.Errorf("preferredContentType(%v) = %q want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFlattenRequestBody_DeterministicSchemaPick(t *testing.T) {
	t.Parallel()
	rb := &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
		Content: openapi3.Content{
			"application/xml": &openapi3.MediaType{
				Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Description: "xml-shape"}},
			},
			"application/json": &openapi3.MediaType{
				Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Description: "json-shape"}},
			},
		},
	}}
	// Run many times to defeat random map order — the deterministic
	// picker should always return the json schema.
	for i := range 50 {
		out := flattenRequestBody(rb)
		m, ok := out.Schema.(map[string]any)
		if !ok {
			t.Fatalf("iter %d: schema not map", i)
		}
		if m["description"] != "json-shape" {
			t.Fatalf("iter %d: got %v want json-shape", i, m["description"])
		}
	}
}

func TestFlattenExamples_Empty(t *testing.T) {
	got := flattenExamples(nil)
	if len(got) != 0 {
		t.Errorf("nil examples should flatten to empty map, got %+v", got)
	}
}
