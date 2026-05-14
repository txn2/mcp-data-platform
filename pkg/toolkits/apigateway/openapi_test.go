package apigateway

import (
	"context"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

const validMinimalSpec = `
openapi: 3.0.3
info:
  title: Test
  version: "1.0"
paths:
  /v1/users:
    get:
      operationId: listUsers
      summary: List users
      tags: [users]
      responses:
        "200": {description: ok}
    post:
      operationId: createUser
      summary: Create a user
      tags: [users]
      responses:
        "201": {description: created}
  /v1/users/{id}:
    get:
      operationId: getUser
      summary: Get a single user by id
      tags: [users]
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string}
      responses:
        "200": {description: ok}
    delete:
      operationId: deleteUser
      summary: Delete a user
      tags: [users, admin]
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string}
      responses:
        "204": {description: deleted}
  /v1/orders:
    get:
      operationId: listOrders
      summary: List orders
      tags: [orders]
      responses:
        "200": {description: ok}
`

// Spec missing operationId on the GET to confirm the synthesized
// "METHOD path" id is produced.
const specWithoutOperationID = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /raw:
    get:
      summary: no opId
      responses:
        "200": {description: ok}
`

func TestParseOpenAPISpec_Valid(t *testing.T) {
	doc, err := parseOpenAPISpec(validMinimalSpec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	if doc == nil {
		t.Fatal("nil doc")
	}
}

func TestParseOpenAPISpec_RejectsEmpty(t *testing.T) {
	_, err := parseOpenAPISpec("")
	if err == nil {
		t.Error("empty spec accepted")
	}
	_, err = parseOpenAPISpec("   \n\t  ")
	if err == nil {
		t.Error("whitespace-only spec accepted")
	}
}

func TestParseOpenAPISpec_RejectsInvalid(t *testing.T) {
	_, err := parseOpenAPISpec("not a spec")
	if err == nil {
		t.Error("invalid spec accepted")
	}
	// Valid YAML but invalid OpenAPI (missing required fields).
	_, err = parseOpenAPISpec("openapi: 3.0.3\n")
	if err == nil {
		t.Error("OpenAPI doc with no info/paths accepted")
	}
}

// TestParseOpenAPISpec_RejectsExternalRefs is the security guard:
// a spec containing an external $ref must not trigger an outbound
// HTTP fetch at parse time. We assert by spec inspection — the
// loader is configured with IsExternalRefsAllowed: false.
func TestParseOpenAPISpec_RejectsExternalRefs(t *testing.T) {
	spec := `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: x
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                $ref: "https://evil.example/schema.json"
`
	_, err := parseOpenAPISpec(spec)
	if err == nil {
		t.Error("spec with external $ref accepted; loader should refuse")
	}
}

func TestBuildOperationIndex_AllMethods(t *testing.T) {
	doc, err := parseOpenAPISpec(validMinimalSpec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	ops, _ := buildOperationIndex(doc, "")
	if len(ops) != 5 {
		t.Fatalf("expected 5 operations, got %d: %+v", len(ops), ops)
	}

	seen := make([]string, 0, len(ops))
	for _, op := range ops {
		seen = append(seen, op.Method+" "+op.Path)
	}
	want := []string{
		"GET /v1/orders",
		"GET /v1/users",
		"POST /v1/users",
		"DELETE /v1/users/{id}",
		"GET /v1/users/{id}",
	}
	for _, w := range want {
		if !slices.Contains(seen, w) {
			t.Errorf("expected %q in operation index; got %v", w, seen)
		}
	}
}

func TestBuildOperationIndex_SynthesizesMissingOperationID(t *testing.T) {
	doc, err := parseOpenAPISpec(specWithoutOperationID)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	ops, _ := buildOperationIndex(doc, "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %+v", ops)
	}
	if ops[0].OperationID != "GET /raw" {
		t.Errorf("OperationID = %q; want %q", ops[0].OperationID, "GET /raw")
	}
}

func TestBuildOperationIndex_NilDoc(t *testing.T) {
	if ops, texts := buildOperationIndex(nil, ""); ops != nil || texts != nil {
		t.Errorf("buildOperationIndex(nil) = (%v, %v); want (nil, nil)", ops, texts)
	}
}

func TestRankOperations_EmptyQueryReturnsAllUpToLimit(t *testing.T) {
	doc, _ := parseOpenAPISpec(validMinimalSpec)
	ops, _ := buildOperationIndex(doc, "")

	all := rankOperations(ops, "", 0)
	if len(all) != len(ops) {
		t.Errorf("empty query, no limit: got %d; want %d", len(all), len(ops))
	}
	capped := rankOperations(ops, "", 2)
	if len(capped) != 2 {
		t.Errorf("limit=2: got %d", len(capped))
	}
}

func TestRankOperations_SubstringMatchesIDPathSummaryTags(t *testing.T) {
	doc, _ := parseOpenAPISpec(validMinimalSpec)
	ops, _ := buildOperationIndex(doc, "")

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"matches operation_id", "deleteUser", 1},
		{"matches path", "/orders", 1},
		{"matches summary", "Create", 1}, // "Create a user"
		{"matches tag", "admin", 1},      // only deleteUser has admin tag
		{"no match", "nonexistent-thing", 0},
		{"case-insensitive", "USERS", 4}, // /v1/users + /v1/users/{id} (4 ops)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rankOperations(ops, tc.query, 0)
			if len(got) != tc.want {
				t.Errorf("query %q: got %d match(es); want %d (%+v)", tc.query, len(got), tc.want, got)
			}
		})
	}
}

func TestParseConfig_AcceptsCatalogID(t *testing.T) {
	c, err := ParseConfig(map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "petstore-2024",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.CatalogID != "petstore-2024" {
		t.Errorf("CatalogID = %q, want %q", c.CatalogID, "petstore-2024")
	}
}

// AddConnection logs a warning and proceeds with zero ops when the
// referenced catalog contains an unparseable spec — see
// addParsedConnection's buildConnSpecs path. The connection still
// registers so the operator can fix the catalog content from the
// portal.
func TestAddConnection_UnparseableCatalogSpec_RegistersWithZeroOps(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "badspecs", "default", "this is not openapi")
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "badspecs",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if c == nil {
		t.Fatal("connection not registered")
	}
	if len(c.operations) != 0 {
		t.Errorf("expected 0 ops from unparseable spec, got %d", len(c.operations))
	}
}

func TestAddConnection_BuildsOperationIndexFromCatalog(t *testing.T) {
	tk := New("test")
	setupCatalogWithSpec(t, tk, "petstore", "default", validMinimalSpec)
	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c1"]
	tk.mu.RUnlock()
	if c == nil || len(c.operations) != 5 {
		t.Errorf("expected 5 operations on connection, got %v", c)
	}
	for _, op := range c.operations {
		if op.Spec != "default" {
			t.Errorf("op %s missing Spec tag (got %q)", op.OperationID, op.Spec)
		}
	}
}

func TestAddConnection_MultiSpecCatalog(t *testing.T) {
	tk := New("test")
	store := setupCatalogWithSpec(t, tk, "vendor", "users",
		minimalSpecWith(`/v1/users:
    `+pathOpYAML("get", "listUsers", "List users")))
	if err := store.UpsertSpec(context.Background(), "vendor",
		newSpecEntry("orders", minimalSpecWith(`/v1/orders:
    `+pathOpYAML("get", "listOrders", "List orders")))); err != nil {
		t.Fatalf("UpsertSpec orders: %v", err)
	}
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if c == nil || len(c.operations) != 2 {
		t.Fatalf("expected 2 ops across both specs, got %d", len(c.operations))
	}
	specs := map[string]struct{}{}
	for _, op := range c.operations {
		specs[op.Spec] = struct{}{}
	}
	if len(specs) != 2 {
		t.Errorf("expected ops tagged with both spec names, got %v", specs)
	}
}

func TestAddConnection_WithoutCatalogStore_ZeroOps(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "nope",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if len(c.operations) != 0 {
		t.Errorf("expected 0 ops without a catalog store, got %d", len(c.operations))
	}
}

func TestAddConnection_CatalogMissing_ZeroOps(t *testing.T) {
	tk := New("test")
	tk.SetCatalogStore(catalog.NewMemoryStore())
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "missing",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if len(c.operations) != 0 {
		t.Errorf("expected 0 ops when catalog missing, got %d", len(c.operations))
	}
}
