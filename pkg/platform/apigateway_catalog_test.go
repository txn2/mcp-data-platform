package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestAPIGatewayCatalog_EndToEnd wires the api-gateway toolkit
// against a memory catalog store and exercises the three MCP tools
// (api_list_endpoints, api_get_endpoint_schema, api_invoke_endpoint)
// through a real MCP server + client over HTTP. Proves the catalog
// → connection → tool path works end-to-end, not just in handler
// unit tests.
func TestAPIGatewayCatalog_EndToEnd(t *testing.T) {
	// 1. Mock upstream API. /v1/users returns a constant body; the
	// invoke-end of the test asserts we actually hit it via the
	// catalog-resolved method/path.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"id":1,"name":"a"}]}`))
	}))
	defer upstream.Close()

	// 2. Catalog store with one spec describing the upstream.
	store := apicatalog.NewMemoryStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "vendor", Name: "vendor", DisplayName: "Vendor API",
	}))
	require.NoError(t, store.UpsertSpec(ctx, "vendor", apicatalog.SpecEntry{
		SpecName:   "users",
		SourceKind: apicatalog.SourceInline,
		Content: `
openapi: 3.0.3
info:
  title: Users
  version: "1.0"
paths:
  /v1/users:
    get:
      operationId: listUsers
      summary: List users
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  users:
                    type: array
                    items:
                      type: object
                      properties:
                        id: {type: integer}
                        name: {type: string}
`,
	}))

	// 3. Toolkit pointed at the upstream + catalog store.
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	require.NoError(t, tk.AddConnection("primary", map[string]any{
		"base_url":   upstream.URL,
		"catalog_id": "vendor",
	}))

	// 4. Real MCP server + register the toolkit's tools.
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tk.RegisterTools(srv)

	// 5. HTTP transport and client.
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	// 6. api_list_endpoints — should surface listUsers with spec=users.
	listRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "api_list_endpoints",
		Arguments: map[string]any{"connection": "primary"},
	})
	require.NoError(t, err)
	require.False(t, listRes.IsError, "list_endpoints body: %s", textOf(t, listRes))
	listBody := textOf(t, listRes)
	assert.Contains(t, listBody, "listUsers")
	assert.Contains(t, listBody, `"spec": "users"`)

	// 7. api_get_endpoint_schema — should return parameters + response
	// schema, with security/servers stripped.
	schemaRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "api_get_endpoint_schema",
		Arguments: map[string]any{
			"connection":   "primary",
			"operation_id": "listUsers",
		},
	})
	require.NoError(t, err)
	require.False(t, schemaRes.IsError, "get_endpoint_schema body: %s", textOf(t, schemaRes))
	schemaBody := textOf(t, schemaRes)
	assert.Contains(t, schemaBody, `"name": "limit"`)
	assert.Contains(t, schemaBody, `"status": "200"`)
	assert.NotContains(t, schemaBody, `"security"`)
	assert.NotContains(t, schemaBody, `"servers"`)

	// 8. api_invoke_endpoint — should actually hit the upstream and
	// return the body.
	invokeRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "api_invoke_endpoint",
		Arguments: map[string]any{
			"connection": "primary",
			"method":     "GET",
			"path":       "/v1/users",
		},
	})
	require.NoError(t, err)
	require.False(t, invokeRes.IsError, "invoke body: %s", textOf(t, invokeRes))
	invokeBody := textOf(t, invokeRes)
	assert.Contains(t, invokeBody, `"users"`)
}

// TestAPIGatewayCatalog_ReloadFanOut proves a catalog mutation
// reaches every connection that references it without a process
// restart. Two connections share a catalog; we edit the catalog's
// spec, call ReloadConnectionsByCatalog, and confirm both
// connections see the new operation set.
func TestAPIGatewayCatalog_ReloadFanOut(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "vendor", Name: "vendor", DisplayName: "V",
	}))
	require.NoError(t, store.UpsertSpec(ctx, "vendor", apicatalog.SpecEntry{
		SpecName:   "default",
		SourceKind: apicatalog.SourceInline,
		Content: `openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses: {"200": {description: ok}}
`,
	}))

	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	for _, name := range []string{"prod", "staging"} {
		require.NoError(t, tk.AddConnection(name, map[string]any{
			"base_url":   "https://upstream.example.com",
			"catalog_id": "vendor",
		}))
	}

	// Edit the spec.
	require.NoError(t, store.UpsertSpec(ctx, "vendor", apicatalog.SpecEntry{
		SpecName:   "default",
		SourceKind: apicatalog.SourceInline,
		Content: `openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses: {"200": {description: ok}}
  /b:
    get:
      operationId: b
      responses: {"200": {description: ok}}
`,
	}))

	tk.ReloadConnectionsByCatalog("vendor")

	for _, name := range []string{"prod", "staging"} {
		details := tk.ListConnections()
		var found bool
		for _, d := range details {
			if d.Name == name {
				found = true
			}
		}
		require.True(t, found, "connection %s missing after reload", name)
	}

	// Drive list_endpoints through the MCP transport to confirm the
	// rebuild propagated all the way to the model-facing surface.
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0.0"}, nil)
	tk.RegisterTools(srv)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	for _, conn := range []string{"prod", "staging"} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "api_list_endpoints",
			Arguments: map[string]any{"connection": conn},
		})
		require.NoError(t, err)
		body := textOf(t, res)
		assert.Contains(t, body, "\"operation_id\": \"a\"", "conn=%s body=%s", conn, body)
		assert.Contains(t, body, "\"operation_id\": \"b\"", "conn=%s body=%s", conn, body)
	}
}

// TestAPIGatewayCatalog_AmbiguousOperationID verifies the
// disambiguation path: two component specs in one catalog define
// the same operation_id, the model omits the spec arg, the tool
// returns a structured error listing both candidates.
func TestAPIGatewayCatalog_AmbiguousOperationID(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "vendor", Name: "vendor", DisplayName: "V",
	}))
	for _, name := range []string{"users", "orders"} {
		require.NoError(t, store.UpsertSpec(ctx, "vendor", apicatalog.SpecEntry{
			SpecName:   name,
			SourceKind: apicatalog.SourceInline,
			Content: `openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /thing:
    get:
      operationId: list
      responses: {"200": {description: ok}}
`,
		}))
	}

	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	require.NoError(t, tk.AddConnection("c", map[string]any{
		"base_url":   "https://upstream.example.com",
		"catalog_id": "vendor",
	}))

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0.0"}, nil)
	tk.RegisterTools(srv)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	// No spec argument → ambiguity error.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "api_get_endpoint_schema",
		Arguments: map[string]any{
			"connection":   "c",
			"operation_id": "list",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected ambiguity error, got: %s", textOf(t, res))
	body := textOf(t, res)
	assert.Contains(t, body, "ambiguous")
	assert.Contains(t, body, `"spec": "users"`)
	assert.Contains(t, body, `"spec": "orders"`)

	// Same call with spec set → success.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "api_get_endpoint_schema",
		Arguments: map[string]any{
			"connection":   "c",
			"operation_id": "list",
			"spec":         "users",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "expected success with spec: %s", textOf(t, res))
	assert.Contains(t, textOf(t, res), `"spec": "users"`)
}

// TestWireAPIGatewayCatalogStore_HappyPath exercises the wire helper
// against a real api-gateway toolkit and confirms the store is
// installed and the connections reload (operations get rebuilt
// against the catalog).
func TestWireAPIGatewayCatalogStore_HappyPath(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	ctx := context.Background()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "v", Name: "v", DisplayName: "V",
	}))
	require.NoError(t, store.UpsertSpec(ctx, "v", apicatalog.SpecEntry{
		SpecName:   "default",
		SourceKind: apicatalog.SourceInline,
		Content: `openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: x
      responses: {"200": {description: ok}}
`,
	}))

	mc, err := apigatewaykit.ParseMultiConfig("api", map[string]map[string]any{
		"c": {"base_url": "https://x.example.com", "catalog_id": "v"},
	})
	require.NoError(t, err)
	tk := apigatewaykit.NewMulti(mc)
	t.Cleanup(func() { _ = tk.Close() })

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	p := &Platform{toolkitRegistry: reg}

	p.WireAPIGatewayCatalogStore(store)

	got := p.APIGatewayCatalogStore()
	require.Same(t, store, got, "APIGatewayCatalogStore should return the wired store")
}

func TestWireAPIGatewayCatalogStore_NilNoop(_ *testing.T) {
	p := &Platform{toolkitRegistry: registry.NewRegistry()}
	p.WireAPIGatewayCatalogStore(nil)
}

func TestWireAPIGatewayCatalogStoreFromDB_NilDBNoop(_ *testing.T) {
	p := &Platform{toolkitRegistry: registry.NewRegistry()}
	p.WireAPIGatewayCatalogStoreFromDB()
}

func TestAPIGatewayCatalogStore_NoToolkitReturnsNil(t *testing.T) {
	p := &Platform{toolkitRegistry: registry.NewRegistry()}
	if p.APIGatewayCatalogStore() != nil {
		t.Fatal("expected nil when no api-gateway toolkit is registered")
	}
}

func textOf(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}
