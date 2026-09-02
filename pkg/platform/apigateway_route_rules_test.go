package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/routepolicy"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// The acceptance criterion of #1479, end to end: a rule stored the way the
// portal stores it denies api_invoke_endpoint for a caller holding that
// persona, on a real MCP server, against a real upstream.
//
// Each seam it crosses had its own regression:
//
//   - personastore dropped api_routes entirely, so a persona edited in the
//     portal was written back without the rules its file config gave it.
//   - the route policy was consulted with the path a call reaches, while the
//     rule and every listing surface speak the path the catalog declares, so a
//     rule naming an operation hid it from the listing and permitted the call.
//
// A unit test of either seam passes with the other broken, which is why this
// one starts from a stored Definition and ends at a refused tool call.

const routeRulesSpec = `
openapi: 3.0.3
info: {title: Orders, version: "1.0"}
paths:
  /v1/orders:
    get:
      operationId: listOrders
      responses: {"200": {description: OK}}
  /v1/orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: OK}}
    delete:
      operationId: deleteOrder
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: deleted}}
`

// routeRulesFixture wires a stored persona definition through the registry, the
// authorizer and the route policy onto a live api-gateway toolkit, and returns
// an MCP session that calls as the persona's role.
func routeRulesFixture(t *testing.T, def personastore.Definition) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	store := apicatalog.NewMemoryStore()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "orders", Name: "orders", DisplayName: "Orders",
	}))
	require.NoError(t, store.UpsertSpec(ctx, "orders", apicatalog.SpecEntry{
		SpecName: "default", SourceKind: apicatalog.SourceInline, Content: routeRulesSpec,
	}))

	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	require.NoError(t, tk.AddConnection("crm-prod", map[string]any{
		"base_url": upstream.URL, "catalog_id": "orders",
	}))

	// The persona reaches the toolkit the way a database-managed one does:
	// through the Definition the store round-trips, not through a struct the
	// test hand-builds.
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(def.ToPersona()))
	tk.SetRoutePolicy(routepolicy.New(routepolicy.Deps{
		Authorizer: persona.NewAuthorizer(reg, &persona.OIDCRoleMapper{Registry: reg}),
	}))

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tk.RegisterTools(srv)
	// The caller's roles reach the policy the way the middleware leaves them,
	// which is the context the policy resolves a persona from.
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithPreAuthenticatedUser(ctx, &middleware.UserInfo{
				UserID: "u1", Roles: def.Roles,
			}), method, req)
		}
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// deniedDefinition is a persona stored with one operation refused and the rest
// of the connection allowed — what the portal writes when an operator opens the
// API-endpoint scope and denies a single row.
func deniedDefinition() personastore.Definition {
	return personastore.Definition{
		Name:        "analyst",
		DisplayName: "Analyst",
		Roles:       []string{"analyst"},
		ToolsAllow:  []string{"*"},
		ConnsAllow:  []string{"*"},
		APIRoutes: []persona.APIRouteRule{
			{Connection: "crm-*", Action: persona.ActionAllow},
			{
				Connection: "crm-*",
				Methods:    []string{http.MethodDelete},
				Paths:      []string{"/v1/orders/{id}"},
				Action:     persona.ActionDeny,
			},
		},
	}
}

func TestAPIRouteRules_StoredRuleDeniesTheCallItNames(t *testing.T) {
	session := routeRulesFixture(t, deniedDefinition())
	ctx := context.Background()

	t.Run("the denied operation is refused with its parameters substituted", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "api_invoke_endpoint",
			Arguments: map[string]any{
				"connection": "crm-prod",
				"method":     http.MethodDelete,
				"path":       "/v1/orders/42",
			},
		})
		require.NoError(t, err)
		assert.True(t, res.IsError,
			"a rule naming /v1/orders/{id} let DELETE /v1/orders/42 through: %s", textOf(t, res))
		assert.Contains(t, textOf(t, res), "not authorized")
	})

	t.Run("addressing it by operation_id is refused too", func(t *testing.T) {
		// path_params are substituted before the gate runs, so this reaches the
		// policy in the same concrete form the raw call does.
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "api_invoke_endpoint",
			Arguments: map[string]any{
				"connection":   "crm-prod",
				"operation_id": "deleteOrder",
				"path_params":  map[string]any{"id": "42"},
			},
		})
		require.NoError(t, err)
		assert.True(t, res.IsError, "operation_id addressing bypassed the rule: %s", textOf(t, res))
	})

	t.Run("the denied operation is absent from api_discover", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "api_discover",
			Arguments: map[string]any{"connection": "crm-prod"},
		})
		require.NoError(t, err)
		require.False(t, res.IsError, "list body: %s", textOf(t, res))
		body := textOf(t, res)
		assert.NotContains(t, body, "deleteOrder")
		assert.Contains(t, body, "getOrder")
	})

	t.Run("the connection's other operations stay callable", func(t *testing.T) {
		for _, path := range []string{"/v1/orders", "/v1/orders/42"} {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "api_invoke_endpoint",
				Arguments: map[string]any{
					"connection": "crm-prod", "method": http.MethodGet, "path": path,
				},
			})
			require.NoError(t, err)
			assert.False(t, res.IsError, "GET %s was refused: %s", path, textOf(t, res))
		}
	})
}

func TestAPIRouteRules_APersonaWithNoRulesKeepsFullAccess(t *testing.T) {
	// The behavior the ticket declined to change: this axis narrows, it does
	// not gate. A connection no rule names is decided by the connection grant.
	def := deniedDefinition()
	def.APIRoutes = nil
	session := routeRulesFixture(t, def)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "api_invoke_endpoint",
		Arguments: map[string]any{
			"connection": "crm-prod", "method": http.MethodDelete, "path": "/v1/orders/42",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "an unruled persona was refused: %s", textOf(t, res))
}
