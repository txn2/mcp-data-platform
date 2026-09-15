package catalogapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// graphqlSDL is a schema with one namespaced query, so the operations the
// descent produces have a dotted id rather than a bare field name.
const graphqlSDL = `
type Query {
  catalog: CatalogQueries
}
type CatalogQueries {
  "Find one product by id."
  product(id: ID!): Product
}
type Product {
  id: ID!
  name: String
}
`

// seedGraphQLSpec puts one GraphQL spec in a catalog and returns the
// mounted routes.
func seedGraphQLSpec(t *testing.T, specName, content string) *http.ServeMux {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: specName, Content: content, SourceKind: apicatalog.SourceInline,
		SpecFormat: apicatalog.FormatGraphQL,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	return testMux(Config{Catalogs: store, Mutable: true})
}

// A GraphQL spec entry is SDL, and prepareSpec must not hand it to the
// OpenAPI parser: it is served to a graphql connection, not to the HTTP
// gateway.
func TestPrepareSpecCountsAGraphQLSchemasOperations(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName:   "schema",
		Content:    graphqlSDL,
		SourceKind: apicatalog.SourceInline,
		SpecFormat: apicatalog.FormatGraphQL,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	// Nothing is rendered: the SDL is already what the connection serves.
	if entry.OpenAPIContent != "" {
		t.Errorf("a GraphQL spec rendered an OpenAPI document:\n%s", entry.OpenAPIContent)
	}
	if entry.Effective() != graphqlSDL {
		t.Error("Effective() did not resolve to the SDL the operator supplied")
	}
	if entry.OperationCount != 1 {
		t.Errorf("operation_count = %d, want the one query the schema exposes", entry.OperationCount)
	}
}

// Changing a spec's format from wsdl to graphql has to stop the stale
// render from being what Effective returns.
func TestChangingASpecToGraphQLClearsTheRenderItReplaces(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName:       "schema",
		Content:        graphqlSDL,
		SourceKind:     apicatalog.SourceInline,
		SpecFormat:     apicatalog.FormatGraphQL,
		OpenAPIContent: `{"openapi":"3.0.3"}`,
	}
	if err := prepareSpec(&entry); err != nil {
		t.Fatalf("prepareSpec: %v", err)
	}
	if entry.Effective() != graphqlSDL {
		t.Errorf("Effective() still serves the render the change replaced: %q", entry.Effective())
	}
}

func TestASpecMarkedGraphQLThatIsNotASchemaIsRefusedWithSomewhereToGo(t *testing.T) {
	t.Parallel()
	entry := apicatalog.SpecEntry{
		SpecName:   "schema",
		Content:    `{"__schema":{"queryType":{"name":"Query"}}}`,
		SourceKind: apicatalog.SourceInline,
		SpecFormat: apicatalog.FormatGraphQL,
	}
	err := prepareSpec(&entry)
	if err == nil {
		t.Fatal("an introspection result was accepted as a GraphQL spec")
	}
	// What a catalog stores is a document an operator can read back and
	// diff, so the refusal names where SDL comes from.
	if !strings.Contains(err.Error(), "SDL") {
		t.Errorf("the refusal does not say what the content must be: %v", err)
	}
}

// The operations route renders either format from one shape, so one page
// lists a REST catalog and a GraphQL one.
func TestGraphQLSpecOperationsListWithoutTheDocument(t *testing.T) {
	t.Parallel()
	res := doJSON(t, seedGraphQLSpec(t, "schema", graphqlSDL), http.MethodGet, opsPath("schema"), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	var out operationListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Operations) != 1 {
		t.Fatalf("got %d operations, want 1: %+v", len(out.Operations), out.Operations)
	}
	op := out.Operations[0]
	if op.OperationID != "query:catalog.product" {
		t.Errorf("operation_id = %q; want the dotted id a connection invokes by", op.OperationID)
	}
	if op.Method != "QUERY" {
		t.Errorf("method = %q; want the GraphQL operation kind", op.Method)
	}
	if op.Path != "/catalog/product" {
		t.Errorf("path = %q; want the route a persona rule names", op.Path)
	}
	if op.Summary != "Find one product by id." {
		t.Errorf("summary = %q", op.Summary)
	}
	// A GraphQL endpoint is one address, so there is no base path.
	if out.BasePath != "" {
		t.Errorf("base_path = %q for a GraphQL spec", out.BasePath)
	}
	if strings.Contains(res.Body.String(), "type Query") {
		t.Errorf("the operation list returned the schema document: %s", res.Body.String())
	}
}

func TestAGraphQLSpecThatDoesNotLoadIsUnprocessable(t *testing.T) {
	t.Parallel()
	// Written straight into the store, because the write path refuses it.
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "schema", Content: "this is not a schema",
		SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatGraphQL,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	mux := testMux(Config{Catalogs: store, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, opsPath("schema"), nil)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, mux, http.MethodGet, opsPath("schema")+"/query:catalog.product", nil)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("detail: %d %s", res.Code, res.Body.String())
	}
}

// The detail route answers with what graphql_discover returns at its
// operation level, so a page and a tool call describe one operation
// identically.
func TestOneGraphQLOperationCarriesADocumentThatCallsIt(t *testing.T) {
	t.Parallel()
	mux := seedGraphQLSpec(t, "schema", graphqlSDL)

	res := doJSON(t, mux, http.MethodGet, opsPath("schema")+"/query:catalog.product", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", res.Code, res.Body.String())
	}
	var out struct {
		OperationID string `json:"operation_id"`
		Skeleton    string `json:"skeleton"`
		Variables   string `json:"variables"`
		ReturnType  string `json:"return_type"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.OperationID != "query:catalog.product" {
		t.Errorf("the detail describes %q", out.OperationID)
	}
	if !strings.Contains(out.Skeleton, "product") {
		t.Errorf("the skeleton does not call the operation: %q", out.Skeleton)
	}
	if out.ReturnType != "Product" {
		t.Errorf("return_type = %q", out.ReturnType)
	}
}

func TestAGraphQLOperationTheSchemaDoesNotExposeIsNotFound(t *testing.T) {
	t.Parallel()
	mux := seedGraphQLSpec(t, "schema", graphqlSDL)

	res := doJSON(t, mux, http.MethodGet, opsPath("schema")+"/query:nothing.like.this", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("detail: %d %s", res.Code, res.Body.String())
	}
}
