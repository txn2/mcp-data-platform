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
	ops, _ := buildOperationIndex(doc, "", "")
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
	ops, _ := buildOperationIndex(doc, "", "")
	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %+v", ops)
	}
	if ops[0].OperationID != "GET /raw" {
		t.Errorf("OperationID = %q; want %q", ops[0].OperationID, "GET /raw")
	}
}

func TestBuildOperationIndex_NilDoc(t *testing.T) {
	if ops, texts := buildOperationIndex(nil, "", ""); ops != nil || texts != nil {
		t.Errorf("buildOperationIndex(nil) = (%v, %v); want (nil, nil)", ops, texts)
	}
}

func TestRankOperations_EmptyQueryReturnsAllUpToLimit(t *testing.T) {
	doc, _ := parseOpenAPISpec(validMinimalSpec)
	ops, _ := buildOperationIndex(doc, "", "")

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
	ops, _ := buildOperationIndex(doc, "", "")

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

// TestRankOperations_MultiTokenAndMatch covers finding #4 (round 2):
// a multi-token query previously failed because the substring check
// treated the whole query as a single phrase. "gift list" should
// match an operation whose summary is "List all gifts", since both
// tokens appear in searchable fields (path/summary respectively).
func TestRankOperations_MultiTokenAndMatch(t *testing.T) {
	ops := []OperationSummary{
		{OperationID: "listGifts", Method: "GET", Path: "/gifts", Summary: "List all gifts"},
		{OperationID: "createGift", Method: "POST", Path: "/gifts", Summary: "Create a gift"},
		{OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "List all users"},
	}
	got := rankOperations(ops, "gift list", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 match (listGifts), got %d: %+v", len(got), got)
	}
	if got[0].OperationID != "listGifts" {
		t.Errorf("expected listGifts, got %s", got[0].OperationID)
	}
}

// TestRankOperations_SpecNameSearchable covers finding #4: the spec
// name must be a searchable field so an operator can navigate a
// multi-spec catalog by vendor-supplied section.
func TestRankOperations_SpecNameSearchable(t *testing.T) {
	ops := []OperationSummary{
		{OperationID: "list", Method: "GET", Path: "/things", Summary: "List things", Spec: "orders"},
		{OperationID: "list2", Method: "GET", Path: "/things", Summary: "List things", Spec: "users"},
	}
	got := rankOperations(ops, "orders", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 match in orders spec, got %d", len(got))
	}
	if got[0].Spec != "orders" {
		t.Errorf("expected orders spec, got %s", got[0].Spec)
	}
}

// TestRankOperations_ZeroMatchReturnsEmptySlice covers finding #6:
// a no-match query must produce "operations": [] in the JSON
// response, not "operations": null. Returning nil would force every
// client to handle two empty shapes.
func TestRankOperations_ZeroMatchReturnsEmptySlice(t *testing.T) {
	ops := []OperationSummary{
		{OperationID: "a", Method: "GET", Path: "/a"},
	}
	got := rankOperations(ops, "no-such-thing-anywhere", 10)
	if got == nil {
		t.Fatal("got nil; want empty []OperationSummary")
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d", len(got))
	}
}

// TestSpecBasePath covers finding #5: the path component of the
// first declared servers[].url is extracted (with a trailing slash
// stripped) and the scheme + host are ignored so the operator's
// connection.base_url remains the authoritative host.
func TestSpecBasePath(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
		want      string
	}{
		{"empty spec", "", ""},
		{"host only", "https://api.example.com", ""},
		{"host + path", "https://api.example.com/v1", "/v1"},
		{"host + path + trailing slash", "https://api.example.com/v1/", "/v1"},
		{"host + nested path", "https://api.example.com/foo/v2", "/foo/v2"},
		{"path only", "/v1", "/v1"},
		{"root only", "/", ""},
		{"relative path no leading slash", "v1", "/v1"},
		{"relative path nested no leading slash", "api/v2", "/api/v2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specYAML := minimalSpecWith(`/widgets:
    get:
      operationId: listWidgets
      responses:
        "200": {description: ok}`)
			if tc.serverURL != "" {
				specYAML = "openapi: 3.0.3\ninfo:\n  title: T\n  version: \"1.0\"\nservers:\n  - url: " + tc.serverURL + "\npaths:\n  /widgets:\n    get:\n      operationId: listWidgets\n      responses:\n        \"200\": {description: ok}\n"
			}
			doc, err := parseOpenAPISpec(specYAML)
			if err != nil {
				t.Fatalf("parseOpenAPISpec: %v", err)
			}
			if got := specBasePath(doc); got != tc.want {
				t.Errorf("specBasePath(%q) = %q; want %q", tc.serverURL, got, tc.want)
			}
		})
	}
}

// TestBuildOperationIndex_AppliesBasePath proves the base path is
// prepended to every operation's path so the model using
// api_list_endpoints output as input to api_invoke_endpoint
// receives the full upstream path, not the spec-relative path that
// 404s when the segment is missing from the connection's base_url.
func TestBuildOperationIndex_AppliesBasePath(t *testing.T) {
	spec := `
openapi: 3.0.3
info:
  title: Users
  version: "1.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200": {description: ok}
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string}
      responses:
        "200": {description: ok}
`
	doc, err := parseOpenAPISpec(spec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	ops, _ := buildOperationIndex(doc, "users", "/v3")
	wantPaths := map[string]bool{
		"/v3/users":      true,
		"/v3/users/{id}": true,
	}
	for _, op := range ops {
		if !wantPaths[op.Path] {
			t.Errorf("unexpected op path %q (want one of %v)", op.Path, wantPaths)
		}
		delete(wantPaths, op.Path)
	}
	if len(wantPaths) != 0 {
		t.Errorf("missing expected paths: %v", wantPaths)
	}
}

// TestComputeEffectiveBasePath covers finding #1: when an operator's
// connection.base_url already contains the spec's servers[0] path
// segment as a suffix, the toolkit must NOT prepend the segment to
// the operation paths or the invoke-time URL join will double it.
// Drives the dedupe rule from a tabulated set of operator inputs.
func TestComputeEffectiveBasePath(t *testing.T) {
	cases := []struct {
		name     string
		connURL  string
		specBase string
		want     string
	}{
		{"empty spec base", "https://api.example.com/v1", "", ""},
		{"host-only conn", "https://api.example.com", "/v1", "/v1"},
		{"trailing slash on conn", "https://api.example.com/", "/v1", "/v1"},
		{"conn already includes spec base", "https://api.example.com/v1", "/v1", ""},
		{"conn includes deeper spec base", "https://api.example.com/api/v2", "/api/v2", ""},
		{"conn includes spec base as suffix", "https://api.example.com/foo/api/v2", "/api/v2", ""},
		{"conn path is unrelated to spec base", "https://api.example.com/legacy", "/v1", "/v1"},
		{"unparseable conn falls back to spec base", "://not-a-url", "/v1", "/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeEffectiveBasePath(tc.connURL, tc.specBase); got != tc.want {
				t.Errorf("computeEffectiveBasePath(%q, %q) = %q; want %q",
					tc.connURL, tc.specBase, got, tc.want)
			}
		})
	}
}

// TestFilterBySpec covers finding #4: the new spec input filters
// operations to the matching component spec; empty spec is a
// passthrough.
func TestFilterBySpec(t *testing.T) {
	ops := []OperationSummary{
		{OperationID: "a", Spec: "users"},
		{OperationID: "b", Spec: "orders"},
		{OperationID: "c", Spec: "users"},
	}
	if got := filterBySpec(ops, ""); len(got) != 3 {
		t.Errorf("empty filter must passthrough, got %d ops", len(got))
	}
	if got := filterBySpec(ops, "users"); len(got) != 2 {
		t.Errorf("users filter must return 2 ops, got %d", len(got))
	}
	if got := filterBySpec(ops, "nonexistent"); len(got) != 0 {
		t.Errorf("no-match must return empty slice, got %d", len(got))
	}
}
