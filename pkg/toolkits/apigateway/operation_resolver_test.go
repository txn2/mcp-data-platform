package apigateway

import (
	"context"
	"testing"
)

const resolverTestSpec = `
openapi: 3.0.0
info:
  title: users
  version: "1.0"
paths:
  /users:
    get:
      operationId: listUsers
      responses:
        "200": { description: ok }
    post:
      operationId: createUser
      responses:
        "201": { description: created }
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
      responses:
        "200": { description: ok }
  /widgets:
    get:
      responses:
        "200": { description: ok }
    options:
      responses:
        "204": { description: no content }
`

// newResolverTestToolkit builds a toolkit with one connection whose
// single spec is rebased under effectiveBasePath. Constructed directly
// (no catalog store) so the test exercises buildOperationRouter +
// FindRoute in isolation.
func newResolverTestToolkit(t *testing.T, connName, basePath string) *Toolkit {
	t.Helper()
	doc, err := parseOpenAPISpec(resolverTestSpec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	tk := New("test")
	tk.connections[connName] = &conn{
		specs: map[string]*specState{
			"users": {doc: doc, effectiveBasePath: basePath},
		},
	}
	return tk
}

func TestResolveOperationID(t *testing.T) {
	tk := newResolverTestToolkit(t, "acme", "/v1")

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"templated path", "GET", "/v1/users/123", "getUser"},
		{"exact collection", "GET", "/v1/users", "listUsers"},
		{"method discriminates", "POST", "/v1/users", "createUser"},
		{"lowercase method normalized", "get", "/v1/users/abc", "getUser"},
		{"no match returns empty", "GET", "/v1/orders", ""},
		{"wrong method returns empty", "DELETE", "/v1/users/1", ""},
		{"missing base path returns empty", "GET", "/users/1", ""},
		{"empty path returns empty", "GET", "", ""},
		{"relative path normalized to leading slash", "GET", "v1/users/9", "getUser"},
		// #519: a collection endpoint invoked with a query string must
		// still resolve. Before the fix the "?..." stayed in the path
		// component and the collection route ("^/v1/users$") missed,
		// collapsing list/bulk traffic into "unknown".
		{"collection with query string", "GET", "/v1/users?limit=100&offset=0", "listUsers"},
		{"item with query string", "GET", "/v1/users/123?expand=lines", "getUser"},
		{"collection with fragment", "GET", "/v1/users#frag", "listUsers"},
		// #519: an operation with no declared operationId must resolve
		// to the same synthesized "<METHOD> <rawPath>" id that
		// api_list_endpoints advertises, not "unknown".
		{"synthesized id for missing operationId", "GET", "/v1/widgets", "GET /widgets"},
		{"synthesized id with query string", "GET", "/v1/widgets?page=2", "GET /widgets"},
		{"synthesized id lowercase method normalized", "get", "/v1/widgets", "GET /widgets"},
		// #519: the router matches OPTIONS (openapi3 PathItem.Operations
		// includes it) but api_list_endpoints does not list it, so the
		// resolver must NOT synthesize a metric label for it.
		{"unlisted method not synthesized", "OPTIONS", "/v1/widgets", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tk.ResolveOperationID(context.Background(), "acme", tc.method, tc.path)
			if got != tc.want {
				t.Errorf("ResolveOperationID(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestResolveOperationID_UnknownConnection(t *testing.T) {
	tk := newResolverTestToolkit(t, "acme", "/v1")
	if got := tk.ResolveOperationID(context.Background(), "nope", "GET", "/v1/users"); got != "" {
		t.Errorf("unknown connection = %q, want empty", got)
	}
}

func TestResolveOperationID_NoCatalog(t *testing.T) {
	tk := New("test")
	tk.connections["bare"] = &conn{} // no specs (no catalog configured)
	if got := tk.ResolveOperationID(context.Background(), "bare", "GET", "/anything"); got != "" {
		t.Errorf("no-catalog connection = %q, want empty", got)
	}
}

func TestResolveOperationID_NoBasePath(t *testing.T) {
	tk := newResolverTestToolkit(t, "acme", "")
	if got := tk.ResolveOperationID(context.Background(), "acme", "GET", "/users/42"); got != "getUser" {
		t.Errorf("no-base-path getUser = %q, want getUser", got)
	}
}
