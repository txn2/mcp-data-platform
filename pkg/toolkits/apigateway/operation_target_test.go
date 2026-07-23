package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- unit: substitutePathParams ---

func TestSubstitutePathParams(t *testing.T) {
	cases := []struct {
		name     string
		template string
		params   map[string]string
		want     string
		wantErr  string
	}{
		{
			name:     "no placeholders returns unchanged",
			template: "/v1/things",
			params:   nil,
			want:     "/v1/things",
		},
		{
			name:     "single placeholder substituted",
			template: "/v1/users/{id}",
			params:   map[string]string{"id": "123"},
			want:     "/v1/users/123",
		},
		{
			name:     "multiple placeholders substituted",
			template: "/v1/orgs/{org}/users/{id}",
			params:   map[string]string{"org": "acme", "id": "42"},
			want:     "/v1/orgs/acme/users/42",
		},
		{
			name:     "value is URL-path-escaped",
			template: "/v1/files/{name}",
			params:   map[string]string{"name": "a b/c"},
			want:     "/v1/files/a%20b%2Fc",
		},
		{
			name:     "missing required parameter errors",
			template: "/v1/users/{id}",
			params:   nil,
			wantErr:  "missing required path parameter",
		},
		{
			name:     "empty value errors",
			template: "/v1/users/{id}",
			params:   map[string]string{"id": ""},
			wantErr:  "is empty",
		},
		{
			name:     "unknown parameter errors",
			template: "/v1/users/{id}",
			params:   map[string]string{"id": "1", "typo": "x"},
			wantErr:  "not present in the operation path template",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := substitutePathParams(c.template, c.params)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v; want substring %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}

// TestSubstitutePathParams_ReportsAllMissing proves the error names
// every unfilled placeholder in one shot so the caller can fix the
// call in a single retry rather than discovering them one at a time.
func TestSubstitutePathParams_ReportsAllMissing(t *testing.T) {
	_, err := substitutePathParams("/v1/orgs/{org}/users/{id}", nil)
	if err == nil {
		t.Fatal("expected error for missing params")
	}
	for _, want := range []string{"org", "id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name missing param %q", err.Error(), want)
		}
	}
}

// --- unit: operationAddressing.resolve XOR contract ---

func TestOperationAddressing_Resolve_PlainMode(t *testing.T) {
	method, path, err := operationAddressing{Method: "GET", Path: "/v1/x"}.resolve(&conn{})
	if err != nil {
		t.Fatalf("plain mode should not error: %v", err)
	}
	if method != "GET" || path != "/v1/x" {
		t.Errorf("plain mode should pass through; got %q %q", method, path)
	}
}

func TestOperationAddressing_Resolve_RejectsEmpty(t *testing.T) {
	_, _, err := operationAddressing{}.resolve(&conn{})
	if err == nil || !strings.Contains(err.Error(), "provide operation_id, or method and path") {
		t.Fatalf("expected empty-addressing rejection; got %v", err)
	}
}

func TestOperationAddressing_Resolve_RejectsBothForms(t *testing.T) {
	_, _, err := operationAddressing{
		Method: "GET", Path: "/v1/x", OperationID: "listThings",
	}.resolve(&conn{})
	if err == nil || !strings.Contains(err.Error(), "either operation_id or method+path") {
		t.Fatalf("expected both-forms rejection; got %v", err)
	}
}

func TestOperationAddressing_Resolve_PathParamsWithoutOperationID(t *testing.T) {
	_, _, err := operationAddressing{
		Method: "GET", Path: "/v1/x", PathParams: map[string]string{"id": "1"},
	}.resolve(&conn{})
	if err == nil || !strings.Contains(err.Error(), "path_params requires operation_id") {
		t.Fatalf("expected path_params-without-operation_id rejection; got %v", err)
	}
}

// --- unit: resolveOperationTarget against a catalog ---

// opTargetSpec is a two-operation spec (a collection and a templated
// item) with no servers block, so the effective base path is empty and
// each operation's full path equals its spec-relative path.
const opTargetSpec = `openapi: 3.0.0
info:
  title: things
  version: "1"
paths:
  /things:
    get:
      operationId: listThings
      summary: List things
      responses:
        "200":
          description: ok
  /things/{id}:
    get:
      operationId: getThing
      summary: Get one thing
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok`

func setupOpTargetToolkit(t *testing.T, baseURL string) *Toolkit {
	t.Helper()
	tk := New("api")
	setupCatalogWithSpec(t, tk, "things", "default", opTargetSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   baseURL,
		"catalog_id": "things",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

func TestResolveOperationTarget_NoSpecs(t *testing.T) {
	c := &conn{}
	_, _, err := resolveOperationTarget(c, operationAddressing{OperationID: "listThings"})
	if err == nil || !strings.Contains(err.Error(), "no catalog specs") {
		t.Fatalf("expected no-catalog error; got %v", err)
	}
}

func TestResolveOperationTarget_NotFound(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	c := tk.connections["c"]
	_, _, err := resolveOperationTarget(c, operationAddressing{OperationID: "noSuchOp"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error; got %v", err)
	}
}

func TestResolveOperationTarget_Collection(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	c := tk.connections["c"]
	method, path, err := resolveOperationTarget(c, operationAddressing{OperationID: "listThings"})
	if err != nil {
		t.Fatalf("resolve listThings: %v", err)
	}
	if method != "GET" || path != "/things" {
		t.Errorf("got %q %q; want GET /things", method, path)
	}
}

func TestResolveOperationTarget_TemplatedPath(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	c := tk.connections["c"]
	method, path, err := resolveOperationTarget(c, operationAddressing{
		OperationID: "getThing", PathParams: map[string]string{"id": "abc"},
	})
	if err != nil {
		t.Fatalf("resolve getThing: %v", err)
	}
	if method != "GET" || path != "/things/abc" {
		t.Errorf("got %q %q; want GET /things/abc", method, path)
	}
}

func TestResolveOperationTarget_TemplatedPathMissingParam(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	c := tk.connections["c"]
	_, _, err := resolveOperationTarget(c, operationAddressing{OperationID: "getThing"})
	if err == nil || !strings.Contains(err.Error(), "missing required path parameter") {
		t.Fatalf("expected missing-param error; got %v", err)
	}
}

func TestResolveOperationTarget_AmbiguousAcrossSpecs(t *testing.T) {
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
		"base_url": "https://x", "catalog_id": "vendor",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	c := tk.connections["c"]
	_, _, err := resolveOperationTarget(c, operationAddressing{OperationID: "list"})
	if err == nil || !strings.Contains(err.Error(), "multiple specs") {
		t.Fatalf("expected ambiguity error; got %v", err)
	}
	// The error must name both candidate specs so the model can retry.
	for _, spec := range []string{"users", "orders"} {
		if !strings.Contains(err.Error(), spec) {
			t.Errorf("ambiguity error %q does not name spec %q", err.Error(), spec)
		}
	}
	// A spec filter resolves the ambiguity.
	method, path, err := resolveOperationTarget(c, operationAddressing{OperationID: "list", Spec: "orders"})
	if err != nil {
		t.Fatalf("resolve with spec filter: %v", err)
	}
	if method != "GET" || path != "/v1/things" {
		t.Errorf("got %q %q; want GET /v1/things", method, path)
	}
}

// --- integration: handleInvoke by operation_id against a live upstream ---

// TestHandleInvoke_ByOperationID_TemplatedPath proves the full path:
// an operation_id + path_params resolves, through the real handler and
// HTTP client, into a concrete GET /things/abc reaching the upstream —
// closing the manual list-then-remember-method-and-path bookkeeping the
// issue describes (#1046).
func TestHandleInvoke_ByOperationID_TemplatedPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	tk := setupOpTargetToolkit(t, srv.URL)
	res, payload, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection:  "c",
		OperationID: "getThing",
		PathParams:  map[string]string{"id": "abc"},
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("unexpected error result: %s", textContent(res))
	}
	if gotMethod != "GET" || gotPath != "/things/abc" {
		t.Errorf("upstream saw %q %q; want GET /things/abc", gotMethod, gotPath)
	}
	out, ok := payload.(InvokeOutput)
	if !ok {
		t.Fatalf("payload is not InvokeOutput: %T", payload)
	}
	if out.Status != http.StatusOK {
		t.Errorf("Status = %d; want 200", out.Status)
	}
}

// TestHandleInvoke_ByOperationID_Collection proves a non-templated
// operation resolves without path_params.
func TestHandleInvoke_ByOperationID_Collection(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	tk := setupOpTargetToolkit(t, srv.URL)
	res, _, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection:  "c",
		OperationID: "listThings",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("unexpected error result: %s", textContent(res))
	}
	if gotPath != "/things" {
		t.Errorf("upstream saw %q; want /things", gotPath)
	}
}

// TestHandleInvoke_ByOperationID_RejectsBothForms proves the XOR guard
// fires at the handler boundary, returning a tool error rather than an
// ambiguous call.
func TestHandleInvoke_ByOperationID_RejectsBothForms(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	res, _, _ := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection:  "c",
		Method:      "GET",
		Path:        "/things",
		OperationID: "listThings",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result for both-forms input; got %+v", res)
	}
	if !strings.Contains(textContent(res), "either operation_id or method+path") {
		t.Errorf("error message = %q", textContent(res))
	}
}

// TestHandleInvoke_ByOperationID_NotFound proves an unknown id surfaces
// a caller-actionable error, not a silent empty call.
func TestHandleInvoke_ByOperationID_NotFound(t *testing.T) {
	tk := setupOpTargetToolkit(t, "https://x")
	res, _, _ := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection:  "c",
		OperationID: "noSuchOp",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result; got %+v", res)
	}
	if !strings.Contains(textContent(res), "not found") {
		t.Errorf("error message = %q", textContent(res))
	}
}

// --- integration: handleExport by operation_id ---

// TestHandleExport_ByOperationID proves api_export speaks the same
// operation_id shortcut: the resolved concrete path reaches the
// upstream and the exported asset is persisted.
func TestHandleExport_ByOperationID(t *testing.T) {
	var gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	t.Cleanup(upstream.Close)

	tk := New("api")
	setupCatalogWithSpec(t, tk, "things", "default", opTargetSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url": upstream.URL, "catalog_id": "things",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	store := &fakeExportAssetStore{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk.SetExportDeps(deps)

	res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection:  "c",
		OperationID: "getThing",
		PathParams:  map[string]string{"id": "abc"},
		Name:        "thing abc",
	})
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("unexpected error result: %s", textContent(res))
	}
	if gotMethod != "GET" || gotPath != "/things/abc" {
		t.Errorf("upstream saw %q %q; want GET /things/abc", gotMethod, gotPath)
	}
	out, ok := payload.(*exportOutput)
	if !ok {
		t.Fatalf("payload is not *exportOutput: %T", payload)
	}
	if out.AssetID == "" {
		t.Error("AssetID is empty")
	}
	if len(store.inserted) != 1 {
		t.Fatalf("AssetStore.Insert called %d times; want 1", len(store.inserted))
	}
}

// TestHandleExport_ByOperationID_RejectsBothForms mirrors the invoke
// XOR guard on the export surface.
func TestHandleExport_ByOperationID_RejectsBothForms(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	tk := New("api")
	setupCatalogWithSpec(t, tk, "things", "default", opTargetSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url": upstream.URL, "catalog_id": "things",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	tk.SetExportDeps(deps)

	res, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection:  "c",
		Method:      "GET",
		Path:        "/things",
		OperationID: "listThings",
		Name:        "x",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result for both-forms input; got %+v", res)
	}
	if !strings.Contains(textContent(res), "either operation_id or method+path") {
		t.Errorf("error message = %q", textContent(res))
	}
}
