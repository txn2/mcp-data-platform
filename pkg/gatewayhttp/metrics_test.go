package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// --- stubs ---

type stubResolver struct {
	id    string
	known map[string]bool
}

func (s stubResolver) ResolveOperationID(_ context.Context, _, _, _ string) string { return s.id }

func (s stubResolver) HasConnection(connection string) bool { return s.known[connection] }

type stubIdentity struct{ id string }

func (s stubIdentity) ResolveIdentity(_ context.Context) string { return s.id }

// --- unit: statusRecorder ---

func TestStatusRecorder_DefaultsTo200(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	assert.Equal(t, http.StatusOK, rec.statusCode(), "unwritten status must default to 200")

	rec.WriteHeader(http.StatusBadGateway)
	assert.Equal(t, http.StatusBadGateway, rec.statusCode())

	// First write wins; a second WriteHeader does not overwrite.
	rec.WriteHeader(http.StatusOK)
	assert.Equal(t, http.StatusBadGateway, rec.statusCode())
}

// --- unit: nil Metrics passthrough ---

func TestWithMetrics_NilMetricsPassthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	wrapped := withMetrics(next, Deps{}) // no Metrics

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", http.NoBody)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)
	assert.True(t, called, "handler must still run when metrics disabled")
}

// --- unit: label resolution helpers ---

func TestResolveOperation(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/gateway/acme/invoke", http.NoBody)
	r.SetPathValue("connection", "acme")

	t.Run("nil resolver yields unknown", func(t *testing.T) {
		got := resolveOperation(r, Deps{}, &invokeMeta{method: "GET", path: "/v1/x"})
		assert.Equal(t, "unknown", got)
	})
	t.Run("empty path yields unknown", func(t *testing.T) {
		got := resolveOperation(r, Deps{Resolver: stubResolver{id: "getX"}}, &invokeMeta{})
		assert.Equal(t, "unknown", got)
	})
	t.Run("resolver empty result yields unknown", func(t *testing.T) {
		got := resolveOperation(r, Deps{Resolver: stubResolver{id: ""}}, &invokeMeta{method: "GET", path: "/v1/x"})
		assert.Equal(t, "unknown", got)
	})
	t.Run("resolved operation id", func(t *testing.T) {
		got := resolveOperation(r, Deps{Resolver: stubResolver{id: "getX"}}, &invokeMeta{method: "GET", path: "/v1/x"})
		assert.Equal(t, "getX", got)
	})
}

func TestResolveIdentity(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", http.NoBody)

	assert.Equal(t, "unknown", resolveIdentity(r, Deps{}), "nil identity resolver yields unknown")
	assert.Equal(t, "unknown", resolveIdentity(r, Deps{Identity: stubIdentity{id: ""}}), "empty identity yields unknown")
	assert.Equal(t, "nifi", resolveIdentity(r, Deps{Identity: stubIdentity{id: "nifi"}}))
}

func TestMethodLabel(t *testing.T) {
	// Empty (failed-decode) and unsupported methods clamp to "unknown";
	// supported methods normalize to uppercase. This keeps the method
	// label bounded against an arbitrary request-body value.
	assert.Equal(t, "unknown", methodLabel(&invokeMeta{}))
	assert.Equal(t, "GET", methodLabel(&invokeMeta{method: "GET"}))
	assert.Equal(t, "GET", methodLabel(&invokeMeta{method: "get"}))
	assert.Equal(t, "unknown", methodLabel(&invokeMeta{method: "garbage-method"}))
}

func TestConnectionLabel(t *testing.T) {
	newReq := func(conn string) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", http.NoBody)
		r.SetPathValue("connection", conn)
		return r
	}
	resolver := stubResolver{known: map[string]bool{"acme": true}}

	t.Run("registered connection passes through", func(t *testing.T) {
		assert.Equal(t, "acme", connectionLabel(newReq("acme"), Deps{Resolver: resolver}))
	})
	t.Run("unregistered connection clamps to unknown", func(t *testing.T) {
		assert.Equal(t, "unknown", connectionLabel(newReq("attacker-random-xyz"), Deps{Resolver: resolver}))
	})
	t.Run("nil resolver clamps to unknown", func(t *testing.T) {
		assert.Equal(t, "unknown", connectionLabel(newReq("acme"), Deps{}))
	})
}

// --- integration: real handler + real metrics + real toolkit ---

// newInboundMetricsServer builds a real apigateway toolkit (optionally
// catalog-backed so operationId resolves), a real metrics recorder, and
// the gateway handler wired with both plus a stub identity. Returns the
// httptest server and the metrics recorder so the test can scrape.
func newInboundMetricsServer(t *testing.T, upstreamURL, connName string, withCatalog bool) (*httptest.Server, *observability.Metrics) {
	t.Helper()

	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	require.NotNil(t, m)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	tk := apigatewaykit.New("apigateway")
	tk.SetMetrics(m)

	config := map[string]any{
		"base_url":        upstreamURL,
		"auth_mode":       apigatewaykit.AuthModeNone,
		"call_timeout":    "5s",
		"connect_timeout": "2s",
	}
	if withCatalog {
		store := catalog.NewMemoryStore()
		require.NoError(t, store.CreateCatalog(context.Background(), catalog.Catalog{ID: "cat1", Name: "cat1"}))
		require.NoError(t, store.UpsertSpec(context.Background(), "cat1", catalog.SpecEntry{
			SpecName:   "items",
			SourceKind: "inline",
			BasePath:   "/v1",
			Content: `openapi: 3.0.0
info: {title: items, version: "1.0"}
paths:
  /items:
    get:
      operationId: listItems
      responses: {"200": {description: ok}}
`,
		}))
		tk.SetCatalogStore(store)
		config["catalog_id"] = "cat1"
	}
	require.NoError(t, tk.AddConnection(connName, config))

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	tk.RegisterTools(mcpServer)

	handler, err := NewHandler(Deps{
		MCPServer: mcpServer,
		Metrics:   m,
		Resolver:  tk,
		Identity:  stubIdentity{id: "nifi-etl"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, m
}

func TestIntegration_InboundMetricsRecorded_ResolvedOperation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gateway, m := newInboundMetricsServer(t, upstream.URL, "acme", true)

	status, _ := postJSON(t, gateway.URL+"/api/v1/gateway/acme/invoke", `{"method":"GET","path":"/v1/items"}`)
	require.Equal(t, http.StatusOK, status)

	body := scrapeInbound(t, m)
	for _, want := range []string{
		"apigateway_inbound_requests_total",
		`connection="acme"`,
		`operation_id="listItems"`,
		`method="GET"`,
		`status_class="2xx"`,
		`identity="nifi-etl"`,
	} {
		assert.Contains(t, body, want, "scrape:\n%s", body)
	}
	// Duration histogram present and identity-free.
	assert.Contains(t, body, "apigateway_inbound_duration_seconds")
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "apigateway_inbound_duration_seconds") {
			assert.NotContains(t, line, "identity=", "duration must not carry identity")
		}
	}
}

func TestIntegration_InboundMetrics_NoCatalogUnknownOperation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gateway, m := newInboundMetricsServer(t, upstream.URL, "bare", false)

	status, _ := postJSON(t, gateway.URL+"/api/v1/gateway/bare/invoke", `{"method":"GET","path":"/anything"}`)
	require.Equal(t, http.StatusOK, status)

	body := scrapeInbound(t, m)
	assert.Contains(t, body, `operation_id="unknown"`, "no-catalog connection must yield unknown operation_id; scrape:\n%s", body)
	assert.Contains(t, body, `connection="bare"`)
}

// scrapeInbound returns the /metrics scrape body, failing if no inbound
// series is present yet (the async-free recording path is synchronous,
// so one scrape suffices).
func scrapeInbound(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	m.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}
