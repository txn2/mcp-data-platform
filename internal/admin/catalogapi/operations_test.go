package catalogapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// browseSpec is a small spec with a path parameter, a request body and two
// response statuses, so the pane the operator opens has something to render.
const browseSpec = `
openapi: 3.0.3
info:
  title: Petstore
  version: "1.0"
servers:
  - url: https://petstore.example.com/v1
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      tags: [pets]
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        '200':
          description: OK
    post:
      operationId: createPet
      summary: Create a pet
      tags: [pets]
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        '201':
          description: Created
        '400':
          description: Bad request
  /pets/{id}:
    get:
      operationId: getPet
      tags: [pets]
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

// seedSpec puts one spec in a catalog and returns the mounted routes.
func seedSpec(t *testing.T, specName, content, basePath string) *http.ServeMux {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: specName, Content: content, SourceKind: apicatalog.SourceInline, BasePath: basePath,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	return testMux(Config{Catalogs: store, Mutable: true})
}

func opsPath(spec string) string {
	return "/api/v1/admin/api-catalogs/petstore/specs/" + spec + "/operations"
}

// TestSpecOperations_ListsWithoutTheDocument is the point of the route: the
// operator sees what a catalog exposes without reading the spec by eye.
func TestSpecOperations_ListsWithoutTheDocument(t *testing.T) {
	t.Parallel()
	res := doJSON(t, seedSpec(t, "default", browseSpec, ""), http.MethodGet, opsPath("default"), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	var out operationListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Operations) != 3 {
		t.Fatalf("got %d operations, want 3: %+v", len(out.Operations), out.Operations)
	}
	if out.BasePath != "/v1" {
		t.Errorf("base_path = %q, want the spec's declared server path", out.BasePath)
	}
	if out.Operations[0].Path != "/v1/pets" || out.Operations[0].Method != "GET" {
		t.Errorf("first row = %+v", out.Operations[0])
	}
	if len(out.Operations[0].Tags) != 1 || out.Operations[0].Tags[0] != "pets" {
		t.Errorf("tags are what the index groups by: %+v", out.Operations[0].Tags)
	}
	// The acceptance criterion this route exists for.
	body := res.Body.String()
	if strings.Contains(body, "openapi") || strings.Contains(body, `"content"`) {
		t.Errorf("the operation list returned the spec document: %s", body)
	}
}

// TestSpecOperations_EmptySpecRendersAsAnEmptyList is one of the three empty
// states: a spec that parses to nothing is not a spec that failed.
func TestSpecOperations_EmptySpecRendersAsAnEmptyList(t *testing.T) {
	t.Parallel()
	const empty = "openapi: 3.0.3\ninfo:\n  title: Empty\n  version: \"1\"\npaths: {}\n"
	res := doJSON(t, seedSpec(t, "empty", empty, ""), http.MethodGet, opsPath("empty"), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	var out operationListResponse
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Operations == nil {
		t.Fatal("operations must be [] rather than null")
	}
	if len(out.Operations) != 0 {
		t.Errorf("got %d operations, want 0", len(out.Operations))
	}
}

func TestSpecOperations_UnknownSpecIsNotFound(t *testing.T) {
	t.Parallel()
	res := doJSON(t, seedSpec(t, "default", browseSpec, ""), http.MethodGet, opsPath("nope"), nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// TestSpecOperations_UnparseableSpecSaysSo keeps a stored-but-broken spec from
// reading as a spec that exposes nothing.
func TestSpecOperations_UnparseableSpecSaysSo(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	// Written straight to the store: the write route parses, so this state is
	// only reachable for content that was valid when it was saved.
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "broken", Content: "not a spec at all", SourceKind: apicatalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	mux := testMux(Config{Catalogs: store, Mutable: true})

	res := doJSON(t, mux, http.MethodGet, opsPath("broken"), nil)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", res.Code, res.Body.String())
	}
	res = doJSON(t, mux, http.MethodGet, opsPath("broken")+"/listPets", nil)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("detail status = %d, want 422: %s", res.Code, res.Body.String())
	}
}

// TestSpecOperation_ShowsParametersBodyAndResponses is the acceptance criterion
// for the pane the operator opens.
func TestSpecOperation_ShowsParametersBodyAndResponses(t *testing.T) {
	t.Parallel()
	mux := seedSpec(t, "default", browseSpec, "")

	res := doJSON(t, mux, http.MethodGet, opsPath("default")+"/createPet", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", res.Code, res.Body.String())
	}
	var out apigatewaykit.EndpointSchemaOutput
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Method != "POST" || out.Path != "/v1/pets" {
		t.Fatalf("unexpected operation: %+v", out)
	}
	if out.RequestBody == nil || !out.RequestBody.Required {
		t.Fatalf("request body: %+v", out.RequestBody)
	}
	if len(out.Responses) != 2 {
		t.Errorf("responses = %d, want the spec's 2: %+v", len(out.Responses), out.Responses)
	}

	res = doJSON(t, mux, http.MethodGet, opsPath("default")+"/getPet", nil)
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if len(out.Parameters) != 1 || !out.Parameters[0].Required {
		t.Errorf("a required path parameter must be marked required: %+v", out.Parameters)
	}
}

// TestSpecOperation_SynthesizedIDSurvivesTheURL is the encoding case: an
// operation with no declared operationId carries a method and a path in its id.
func TestSpecOperation_SynthesizedIDSurvivesTheURL(t *testing.T) {
	t.Parallel()
	const noIDSpec = `
openapi: 3.0.3
info:
  title: Anonymous
  version: "1.0"
paths:
  /things/{id}:
    get:
      summary: Get a thing
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: OK
`
	mux := seedSpec(t, "anon", noIDSpec, "")
	res := doJSON(t, mux, http.MethodGet, opsPath("anon"), nil)
	var list operationListResponse
	_ = json.Unmarshal(res.Body.Bytes(), &list)
	if len(list.Operations) != 1 {
		t.Fatalf("operations: %+v", list.Operations)
	}
	id := list.Operations[0].OperationID
	if id != "GET /things/{id}" {
		t.Fatalf("synthesized id = %q", id)
	}

	res = doJSON(t, mux, http.MethodGet, opsPath("anon")+"/"+url.PathEscape(id), nil)
	if res.Code != http.StatusOK {
		t.Fatalf("detail on a synthesized id: %d %s", res.Code, res.Body.String())
	}
	var out apigatewaykit.EndpointSchemaOutput
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Summary != "Get a thing" {
		t.Errorf("resolved the wrong operation: %+v", out)
	}
}

func TestSpecOperation_UnknownOperationIsNotFound(t *testing.T) {
	t.Parallel()
	res := doJSON(t, seedSpec(t, "default", browseSpec, ""), http.MethodGet, opsPath("default")+"/noSuchOp", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// TestSpecOperations_ServedInReadOnlyMode keeps the browse routes available to
// a file-config deployment, which cannot write catalogs but can be asked what
// its catalogs expose.
func TestSpecOperations_ServedInReadOnlyMode(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "default", Content: browseSpec, SourceKind: apicatalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	mux := testMux(Config{Catalogs: store})

	if res := doJSON(t, mux, http.MethodGet, opsPath("default"), nil); res.Code != http.StatusOK {
		t.Fatalf("list in read-only mode: %d %s", res.Code, res.Body.String())
	}
	if res := doJSON(t, mux, http.MethodGet, opsPath("default")+"/listPets", nil); res.Code != http.StatusOK {
		t.Fatalf("detail in read-only mode: %d %s", res.Code, res.Body.String())
	}
}

// TestSpecOperations_OperatorBasePathOverrideIsApplied proves the listing
// reports the paths a call would actually use, not the spec-relative ones.
func TestSpecOperations_OperatorBasePathOverrideIsApplied(t *testing.T) {
	t.Parallel()
	res := doJSON(t, seedSpec(t, "default", browseSpec, "/api/v2"), http.MethodGet, opsPath("default"), nil)
	var out operationListResponse
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.BasePath != "/api/v2" {
		t.Errorf("base_path = %q, want the override", out.BasePath)
	}
	if out.Operations[0].Path != "/api/v2/pets" {
		t.Errorf("path = %q, want the override applied", out.Operations[0].Path)
	}
}

// unreadableStore fails every spec read, which is what a database outage looks
// like from these routes.
type unreadableStore struct {
	CatalogStore
}

func (unreadableStore) GetSpec(context.Context, string, string) (*apicatalog.SpecEntry, error) {
	return nil, errors.New("connection refused")
}

// TestSpecOperations_UnreadableStoreIsAFault separates "the spec is not there"
// from "we could not look", which a reader acts on differently.
func TestSpecOperations_UnreadableStoreIsAFault(t *testing.T) {
	t.Parallel()
	mux := testMux(Config{Catalogs: unreadableStore{}, Mutable: true})

	for _, path := range []string{opsPath("default"), opsPath("default") + "/listPets"} {
		res := doJSON(t, mux, http.MethodGet, path, nil)
		if res.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500: %s", path, res.Code, res.Body.String())
		}
	}
}
