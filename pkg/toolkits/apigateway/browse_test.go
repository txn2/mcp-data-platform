package apigateway

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// denyPolicy refuses one (method, path) pair and permits everything else. It is
// the shape a persona's APIRoutes deny rule produces, reduced to what the
// browse surface has to observe.
type denyPolicy struct{ method, path string }

func (p denyPolicy) Allow(_ context.Context, _, method, path, _ string) (allowed bool, reason string) {
	if method == p.method && path == p.path {
		return false, "denied by test policy"
	}
	return true, ""
}

// TestBrowseOperations_ListsEveryOperationOfTheConnection is the plain case:
// the whole index, not the ranked page api_discover returns.
func TestBrowseOperations_ListsEveryOperationOfTheConnection(t *testing.T) {
	tk := setupSchemaLookupTk(t)

	ops, err := tk.BrowseOperations(context.Background(), "c")
	if err != nil {
		t.Fatalf("BrowseOperations: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("got %d operations, want the spec's 3: %+v", len(ops), ops)
	}
	// (spec, path, method) order, which is what a grouped index reads in.
	want := []string{"GET /v1/pets", "POST /v1/pets", "GET /v1/pets/{id}"}
	for i, op := range ops {
		if got := op.Method + " " + op.Path; got != want[i] {
			t.Errorf("operation %d = %q, want %q", i, got, want[i])
		}
	}
}

// TestBrowseOperations_OmitsWhatTheRoutePolicyDenies is the acceptance
// criterion: an operation a deny rule hides is absent from the list.
func TestBrowseOperations_OmitsWhatTheRoutePolicyDenies(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	tk.SetRoutePolicy(denyPolicy{method: "POST", path: "/v1/pets"})

	ops, err := tk.BrowseOperations(context.Background(), "c")
	if err != nil {
		t.Fatalf("BrowseOperations: %v", err)
	}
	for _, op := range ops {
		if op.OperationID == "createPet" {
			t.Fatalf("a denied operation is listed: %+v", op)
		}
	}
	if len(ops) != 2 {
		t.Errorf("got %d operations, want 2 after one denial", len(ops))
	}
}

// TestBrowseConnection_CountsOnlyWhatTheCallerReaches is the other half of that
// criterion: the denial has to move the count, or the page says one thing and
// the list shows another.
func TestBrowseConnection_CountsOnlyWhatTheCallerReaches(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	tk.SetRoutePolicy(denyPolicy{method: "POST", path: "/v1/pets"})

	detail, err := tk.BrowseConnection(context.Background(), "c")
	if err != nil {
		t.Fatalf("BrowseConnection: %v", err)
	}
	if detail.OperationCount != 2 {
		t.Errorf("operation_count = %d, want 2 after one denial", detail.OperationCount)
	}
	if len(detail.Specs) != 1 || detail.Specs[0].OperationCount != 2 {
		t.Errorf("per-spec count did not follow the denial: %+v", detail.Specs)
	}
}

// TestBrowseConnection_ReportsTheUpstreamRootAndAuthMode is what the page needs
// to say which call an operation produces, and never the credential.
func TestBrowseConnection_ReportsTheUpstreamRootAndAuthMode(t *testing.T) {
	tk := New("api")
	setupCatalogWithSpec(t, tk, "petstore", "default", petstoreSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://petstore.example.com",
		"catalog_id": "petstore",
		"auth_mode":  "bearer",
		"credential": "s3cr3t",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	detail, err := tk.BrowseConnection(context.Background(), "c")
	if err != nil {
		t.Fatalf("BrowseConnection: %v", err)
	}
	if detail.BaseURL != "https://petstore.example.com" {
		t.Errorf("base_url = %q", detail.BaseURL)
	}
	if detail.AuthMode != "bearer" {
		t.Errorf("auth_mode = %q, want bearer", detail.AuthMode)
	}
	if detail.CatalogID != "petstore" {
		t.Errorf("catalog_id = %q", detail.CatalogID)
	}
}

func TestBrowseConnection_UnknownConnection(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	if _, err := tk.BrowseConnection(context.Background(), "nope"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("err = %v, want ErrConnectionNotFound", err)
	}
	if _, err := tk.BrowseOperations(context.Background(), "nope"); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("err = %v, want ErrConnectionNotFound", err)
	}
	if _, err := tk.BrowseOperation(context.Background(), "nope", "listPets", ""); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("err = %v, want ErrConnectionNotFound", err)
	}
}

// TestBrowseOperation_MatchesTheToolsResolution is the reason the browse
// surface calls into the toolkit at all: a page and api_discover
// have to describe one operation identically.
func TestBrowseOperation_MatchesTheToolsResolution(t *testing.T) {
	tk := setupSchemaLookupTk(t)

	got, err := tk.BrowseOperation(context.Background(), "c", "createPet", "")
	if err != nil {
		t.Fatalf("BrowseOperation: %v", err)
	}
	r, _, toolErr := tk.handleDiscover(context.Background(), nil, DiscoverInput{
		Connection: "c", OperationID: "createPet",
	})
	if toolErr != nil || r.IsError {
		t.Fatalf("api_discover: err=%v isError=%v", toolErr, r.IsError)
	}
	want := parseSchemaResult(t, r)
	if got.Method != want.Method || got.Path != want.Path || got.OperationID != want.OperationID {
		t.Fatalf("browse %+v disagrees with the tool %+v", got, want)
	}
	if got.RequestBody == nil || !got.RequestBody.Required {
		t.Fatalf("request body lost in the browse path: %+v", got.RequestBody)
	}
	if len(got.Responses) != len(want.Responses) {
		t.Errorf("responses = %d, tool returned %d", len(got.Responses), len(want.Responses))
	}
}

// TestBrowseOperation_DeniedIsAbsentNotRefused keeps the browse surface
// consistent with the listing: what a caller cannot invoke does not exist here.
func TestBrowseOperation_DeniedIsAbsentNotRefused(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	tk.SetRoutePolicy(denyPolicy{method: "POST", path: "/v1/pets"})

	_, err := tk.BrowseOperation(context.Background(), "c", "createPet", "")
	if !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("err = %v, want ErrOperationNotFound", err)
	}
	// The permitted sibling still resolves, so the denial is the operation's
	// and not the connection's.
	if _, err := tk.BrowseOperation(context.Background(), "c", "listPets", ""); err != nil {
		t.Fatalf("a permitted operation was refused: %v", err)
	}
}

func TestBrowseOperation_UnknownOperation(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	if _, err := tk.BrowseOperation(context.Background(), "c", "noSuchOp", ""); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("err = %v, want ErrOperationNotFound", err)
	}
}

// secondSpec defines listPets a second time, which is what makes an id
// ambiguous across a multi-spec catalog.
const secondSpec = `
openapi: 3.0.3
info:
  title: Mirror
  version: "1.0"
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets from the mirror
      responses:
        '200':
          description: OK
`

func TestBrowseOperation_AmbiguousIDNamesTheSpecs(t *testing.T) {
	tk := New("api")
	store := setupCatalogWithSpec(t, tk, "petstore", "primary", petstoreSpec)
	if err := store.UpsertSpec(context.Background(), "petstore", newSpecEntry("mirror", secondSpec)); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	if err := tk.AddConnection("c", map[string]any{
		"base_url": "https://petstore.example.com", "catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	_, err := tk.BrowseOperation(context.Background(), "c", "listPets", "")
	if !errors.Is(err, ErrAmbiguousOperation) {
		t.Fatalf("err = %v, want ErrAmbiguousOperation", err)
	}
	if !strings.Contains(err.Error(), "mirror") || !strings.Contains(err.Error(), "primary") {
		t.Errorf("the error must name the specs to retry against: %v", err)
	}
	// Naming one resolves it, which is what makes the error actionable.
	got, err := tk.BrowseOperation(context.Background(), "c", "listPets", "mirror")
	if err != nil {
		t.Fatalf("disambiguated lookup: %v", err)
	}
	if got.Spec != "mirror" || got.Summary != "List pets from the mirror" {
		t.Errorf("resolved the wrong spec: %+v", got)
	}
}

// TestSpecOperations_ReadsACatalogSpecWithNoConnection is the operator's view:
// a spec is browsable before anything references it.
func TestSpecOperations_ReadsACatalogSpecWithNoConnection(t *testing.T) {
	ops, basePath, err := SpecOperations(petstoreSpec, "default", "")
	if err != nil {
		t.Fatalf("SpecOperations: %v", err)
	}
	if basePath != "/v1" {
		t.Errorf("base_path = %q, want the spec's declared server path /v1", basePath)
	}
	if len(ops) != 3 {
		t.Fatalf("got %d operations, want 3: %+v", len(ops), ops)
	}
	if ops[0].Path != "/v1/pets" {
		t.Errorf("path = %q, want the declared prefix applied", ops[0].Path)
	}
	if ops[0].Spec != "default" {
		t.Errorf("spec = %q, want the spec name carried onto each row", ops[0].Spec)
	}
}

// TestSpecOperations_OperatorOverrideWins covers the per-spec base_path an
// operator sets, which is authoritative over what the spec declares.
func TestSpecOperations_OperatorOverrideWins(t *testing.T) {
	ops, basePath, err := SpecOperations(petstoreSpec, "default", "/api/v2")
	if err != nil {
		t.Fatalf("SpecOperations: %v", err)
	}
	if basePath != "/api/v2" {
		t.Errorf("base_path = %q, want the override", basePath)
	}
	if ops[0].Path != "/api/v2/pets" {
		t.Errorf("path = %q, want the override applied", ops[0].Path)
	}
}

// TestSpecOperations_EmptySpecIsAListNotANull is what the empty state renders
// from: zero operations is a spec that parses, not a spec that failed.
func TestSpecOperations_EmptySpecIsAListNotANull(t *testing.T) {
	ops, _, err := SpecOperations("openapi: 3.0.3\ninfo:\n  title: Empty\n  version: \"1\"\npaths: {}\n", "empty", "")
	if err != nil {
		t.Fatalf("SpecOperations: %v", err)
	}
	if ops == nil {
		t.Fatal("a spec with no operations must yield an empty list, not nil")
	}
	if len(ops) != 0 {
		t.Errorf("got %d operations, want 0", len(ops))
	}
}

func TestSpecOperations_UnparseableContent(t *testing.T) {
	if _, _, err := SpecOperations("this is not a spec", "broken", ""); err == nil {
		t.Fatal("unparseable content must be an error, not an empty list")
	}
	if _, err := SpecOperation("this is not a spec", "broken", "", "listPets"); err == nil {
		t.Fatal("unparseable content must be an error")
	}
}

// TestSpecOperation_ResolvesTheSameDetailTheConnectionPathDoes keeps the two
// browse halves on one resolution.
func TestSpecOperation_ResolvesTheSameDetailTheConnectionPathDoes(t *testing.T) {
	got, err := SpecOperation(petstoreSpec, "default", "", "getPet")
	if err != nil {
		t.Fatalf("SpecOperation: %v", err)
	}
	if got.Method != "GET" || got.Path != "/v1/pets/{id}" {
		t.Fatalf("unexpected operation: %+v", got)
	}
	if len(got.Parameters) != 1 || got.Parameters[0].Name != "id" || !got.Parameters[0].Required {
		t.Errorf("path parameter lost: %+v", got.Parameters)
	}
	if got.SavedExamples != nil {
		t.Errorf("a catalog spec has no connection to promote examples on: %+v", got.SavedExamples)
	}
}

// TestSpecOperation_SynthesizedIDResolves covers an operation with no declared
// operationId, whose id carries a method and a path.
func TestSpecOperation_SynthesizedIDResolves(t *testing.T) {
	const noIDSpec = `
openapi: 3.0.3
info:
  title: Anonymous
  version: "1.0"
paths:
  /things:
    get:
      summary: List things
      responses:
        '200':
          description: OK
`
	ops, _, err := SpecOperations(noIDSpec, "anon", "")
	if err != nil {
		t.Fatalf("SpecOperations: %v", err)
	}
	if len(ops) != 1 || ops[0].OperationID != "GET /things" {
		t.Fatalf("synthesized id = %+v", ops)
	}
	got, err := SpecOperation(noIDSpec, "anon", "", ops[0].OperationID)
	if err != nil {
		t.Fatalf("SpecOperation on a synthesized id: %v", err)
	}
	if got.Summary != "List things" {
		t.Errorf("resolved the wrong operation: %+v", got)
	}
}

func TestSpecOperation_UnknownOperation(t *testing.T) {
	if _, err := SpecOperation(petstoreSpec, "default", "", "noSuchOp"); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("err = %v, want ErrOperationNotFound", err)
	}
}

// TestBrowseOperations_DoesNotReorderTheToolkitsOwnIndex is the defect the copy
// in BrowseOperations exists to prevent. With no route policy installed the
// filter returns the connection's OWN slice, so sorting it in place reorders
// the live index every other reader shares -- a write from a read path, holding
// no lock. The index is seeded out of order here rather than left to the
// per-spec map iteration that produced it, so the mutation is observable
// instead of a coin flip.
func TestBrowseOperations_DoesNotReorderTheToolkitsOwnIndex(t *testing.T) {
	tk := setupSchemaLookupTk(t)
	c := tk.connections["c"]
	c.operations = []OperationSummary{
		{OperationID: "getPet", Method: "GET", Path: "/v1/pets/{id}", Spec: "default"},
		{OperationID: "listPets", Method: "GET", Path: "/v1/pets", Spec: "default"},
	}
	before := append([]OperationSummary(nil), c.operations...)

	got, err := tk.BrowseOperations(context.Background(), "c")
	if err != nil {
		t.Fatalf("BrowseOperations: %v", err)
	}
	if len(got) != 2 || got[0].OperationID != "listPets" {
		t.Fatalf("the answer is not in path order: %+v", got)
	}
	if !reflect.DeepEqual(c.operations, before) {
		t.Fatalf("the toolkit's own index was reordered by a read: %+v", c.operations)
	}
}
