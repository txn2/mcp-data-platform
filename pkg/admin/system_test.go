package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // register swagger docs
	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

func TestGetSystemInfo(t *testing.T) {
	t.Run("returns runtime feature availability", func(t *testing.T) {
		origCommit, origDate := mcpserver.Commit, mcpserver.Date
		mcpserver.Commit = "abc1234"
		mcpserver.Date = "2025-01-15T10:30:00Z"
		t.Cleanup(func() {
			mcpserver.Commit = origCommit
			mcpserver.Date = origDate
		})

		cfg := testConfig()
		cfg.Server.Name = "test-platform"
		cfg.Server.Description = "Test description"
		cfg.Server.Transport = "http"
		cfg.Audit.Enabled = new(true)
		cfg.OAuth.Enabled = true
		cfg.Knowledge.Enabled = new(true)
		cfg.Admin.Enabled = true
		cfg.Portal.Logo = "https://cdn.example.com/logo.svg"
		cfg.Portal.LogoLight = "https://cdn.example.com/logo-light.svg"
		cfg.Portal.LogoDark = "https://cdn.example.com/logo-dark.svg"

		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
			},
		}
		pReg := &mockPersonaRegistry{
			allResult: testPersonas("analyst", "admin"),
		}
		kh := NewKnowledgeHandler(nil, nil, nil)
		aq := &mockAuditQuerier{}

		h := NewHandler(Deps{
			Config:            cfg,
			ToolkitRegistry:   reg,
			PersonaRegistry:   pReg,
			Knowledge:         kh,
			AuditQuerier:      aq,
			DatabaseAvailable: true,
			ConfigStore:       &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body systemInfoResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "test-platform", body.Name)
		assert.Equal(t, "abc1234", body.Commit)
		assert.Equal(t, "2025-01-15T10:30:00Z", body.BuildDate)
		assert.Equal(t, "Test description", body.Description)
		assert.Equal(t, "http", body.Transport)
		assert.Equal(t, "https://cdn.example.com/logo.svg", body.PortalLogo)
		assert.Equal(t, "https://cdn.example.com/logo-light.svg", body.PortalLogoLight)
		assert.Equal(t, "https://cdn.example.com/logo-dark.svg", body.PortalLogoDark)
		assert.Equal(t, "database", body.ConfigMode)
		assert.True(t, body.Features.Audit, "audit should be true when AuditQuerier is set")
		assert.True(t, body.Features.OAuth)
		assert.True(t, body.Features.Knowledge, "knowledge should be true when Knowledge handler is set")
		assert.True(t, body.Features.Admin)
		assert.True(t, body.Features.Database, "database should be true when DatabaseAvailable is set")
		assert.Equal(t, 1, body.ToolkitCount)
		assert.Equal(t, 2, body.PersonaCount)
	})

	t.Run("features reflect no-DB mode", func(t *testing.T) {
		cfg := testConfig()
		cfg.Audit.Enabled = new(true)
		cfg.Knowledge.Enabled = new(true)

		h := NewHandler(Deps{
			Config:            cfg,
			DatabaseAvailable: false,
			// No AuditQuerier, no Knowledge handler — not available at runtime
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body systemInfoResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.False(t, body.Features.Audit, "audit should be false when no AuditQuerier")
		assert.False(t, body.Features.Knowledge, "knowledge should be false when no Knowledge handler")
		assert.False(t, body.Features.Database, "database should be false")
		assert.Equal(t, "file", body.ConfigMode, "config mode defaults to file")
	})

	t.Run("returns info without config", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body systemInfoResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Empty(t, body.Name)
		assert.Equal(t, 0, body.ToolkitCount)
		assert.Equal(t, "file", body.ConfigMode)
	})
}

func TestGetPublicBranding(t *testing.T) {
	t.Run("returns platform name from config", func(t *testing.T) {
		cfg := testConfig()
		cfg.Server.Name = "acme-platform"
		cfg.Portal.Title = "ACME Admin"
		cfg.Portal.OIDCButtonLabel = "Sign in with ACME Keycloak"
		cfg.Portal.Logo = "https://cdn.example.com/acme-logo.svg"
		cfg.Portal.LogoLight = "https://cdn.example.com/acme-light.svg"
		cfg.Portal.LogoDark = "https://cdn.example.com/acme-dark.svg"

		h := NewHandler(Deps{Config: cfg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body publicBrandingResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "acme-platform", body.Name)
		assert.NotEmpty(t, body.Version)
		assert.Equal(t, "ACME Admin", body.PortalTitle)
		assert.Equal(t, "Sign in with ACME Keycloak", body.OIDCButtonLabel)
		assert.Equal(t, "https://cdn.example.com/acme-logo.svg", body.PortalLogo)
		assert.Equal(t, "https://cdn.example.com/acme-light.svg", body.PortalLogoLight)
		assert.Equal(t, "https://cdn.example.com/acme-dark.svg", body.PortalLogoDark)
	})

	t.Run("returns version even without config", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body publicBrandingResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Empty(t, body.Name)
		assert.Empty(t, body.PortalTitle)
		assert.NotEmpty(t, body.Version)
	})

	t.Run("bypasses auth middleware", func(t *testing.T) {
		cfg := testConfig()
		cfg.Server.Name = "test-platform"

		authCalled := false
		authMiddle := func(_ http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				authCalled = true
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
			})
		}

		h := NewHandler(Deps{Config: cfg}, authMiddle)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.False(t, authCalled, "auth middleware should not be called for public endpoints")
		assert.Equal(t, http.StatusOK, w.Code)
		var body publicBrandingResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "test-platform", body.Name)
	})
}

func TestSwaggerEndpoint(t *testing.T) {
	h := NewHandler(Deps{Config: testConfig()}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/docs/index.html", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestListTools(t *testing.T) {
	t.Run("returns tools from registry", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query", "trino_describe_table"}},
				{kind: "datahub", name: "primary", connection: "primary-datahub", tools: []string{"datahub_search"}},
			},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(3), body["total"])
		tools, ok := body["tools"].([]any)
		require.True(t, ok, "tools should be a slice")
		assert.Len(t, tools, 3)
	})

	t.Run("includes platform tools", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
			},
		}
		h := NewHandler(Deps{
			ToolkitRegistry: reg,
			PlatformTools: []platform.ToolInfo{
				{Name: "platform_info", Kind: "platform"},
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 2, body.Total)
		require.Len(t, body.Tools, 2)

		// Tools are sorted alphabetically — platform_info before trino_query.
		assert.Equal(t, "platform_info", body.Tools[0].Name)
		assert.Equal(t, "platform", body.Tools[0].Kind)
		assert.Empty(t, body.Tools[0].Toolkit)
		assert.Equal(t, "trino_query", body.Tools[1].Name)
		assert.Equal(t, "trino", body.Tools[1].Kind)
	})

	t.Run("returns empty list when no registry", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
	})

	t.Run("returns only platform tools when no registry", func(t *testing.T) {
		h := NewHandler(Deps{
			PlatformTools: []platform.ToolInfo{
				{Name: "platform_info", Kind: "platform"},
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 1, body.Total)
		assert.Equal(t, "platform_info", body.Tools[0].Name)
	})

	t.Run("uses ConnectionResolver per tool when toolkit fans out across upstreams", func(t *testing.T) {
		// Mirrors the gateway toolkit's behavior: one toolkit instance,
		// many upstream connections, each tool maps to a specific
		// upstream via ConnectionForTool. Without resolver use, every
		// tool would inherit the toolkit's instance-level Connection()
		// (or empty string) and the admin Tools page would group them
		// all under "platform" / the toolkit's default name.
		gw := gatewayLikeToolkit{
			mockToolkit: mockToolkit{
				kind:       "mcp",
				name:       "primary",
				connection: "primary", // would be the bucket key without per-tool resolution
				tools:      []string{"vendor_a__list", "vendor_b__list", "unrouted_tool"},
			},
			perTool: map[string]string{
				"vendor_a__list": "vendor-a",
				"vendor_b__list": "vendor-b",
				// "unrouted_tool" deliberately absent to exercise the
				// empty-string fallback to tk.Connection().
			},
		}
		reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{gw}}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var body toolListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body.Tools, 3)

		byName := make(map[string]toolInfo, len(body.Tools))
		for _, ti := range body.Tools {
			byName[ti.Name] = ti
		}
		assert.Equal(t, "vendor-a", byName["vendor_a__list"].Connection,
			"per-tool resolver must override toolkit default for tools that ConnectionForTool resolves")
		assert.Equal(t, "vendor-b", byName["vendor_b__list"].Connection)
		assert.Equal(t, "primary", byName["unrouted_tool"].Connection,
			"empty ConnectionForTool result must fall back to tk.Connection() so tools aren't dropped into the platform bucket")
	})
}

// gatewayLikeToolkit embeds mockToolkit and adds ConnectionForTool so it
// satisfies registry.ConnectionResolver — modeling the gateway
// toolkit's 1:many fan-out behavior in a unit test without pulling in
// the gateway package.
type gatewayLikeToolkit struct {
	mockToolkit
	perTool map[string]string
}

func (g gatewayLikeToolkit) ConnectionForTool(toolName string) string {
	return g.perTool[toolName]
}

// Verify the test mock implements both interfaces.
var (
	_ registry.Toolkit            = gatewayLikeToolkit{}
	_ registry.ConnectionResolver = gatewayLikeToolkit{}
)

func TestListConnections(t *testing.T) {
	t.Run("returns connections from registry", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
				{kind: "datahub", name: "primary", connection: "primary-datahub", tools: []string{"datahub_search"}},
			},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body connectionListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 2, body.Total)
		// With no visibility config, hidden_tools should be empty.
		for _, c := range body.Connections {
			assert.Empty(t, c.HiddenTools, "hidden_tools should be empty with no visibility config for %s", c.Name)
		}
	})

	t.Run("returns hidden_tools based on visibility config", func(t *testing.T) {
		cfg := testConfig()
		cfg.Tools.Allow = []string{"trino_*"}

		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query", "trino_describe_table"}},
				{kind: "datahub", name: "primary", connection: "primary-datahub", tools: []string{"datahub_search", "datahub_get_entity"}},
			},
		}
		h := NewHandler(Deps{Config: cfg, ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body connectionListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 2, body.Total)

		// Find connections by name (sorted alphabetically).
		var trinoConn, datahubConn connectionInfo
		for _, c := range body.Connections {
			switch c.Kind {
			case "trino":
				trinoConn = c
			case "datahub":
				datahubConn = c
			}
		}

		// Trino tools match allow pattern — nothing hidden.
		assert.Empty(t, trinoConn.HiddenTools)
		// DataHub tools do NOT match "trino_*" — all hidden.
		assert.ElementsMatch(t, []string{"datahub_search", "datahub_get_entity"}, datahubConn.HiddenTools)
	})

	t.Run("returns empty list when no registry", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
	})

	// Built-in single-instance toolkits (knowledge, memory, portal) report an
	// empty Connection(). The persona filter always allows an empty connection
	// name, so they are not gateable connections and must not appear here —
	// listing them produced phantom "denied" rows in the persona editor that
	// no allow pattern could clear.
	t.Run("excludes toolkits with an empty connection name", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
				{kind: "knowledge", name: "knowledge", connection: "", tools: []string{"capture_insight"}},
				{kind: "memory", name: "memory", connection: "", tools: []string{"memory_recall"}},
			},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body connectionListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body.Connections, 1, "only the real (non-empty) connection should be listed")
		assert.Equal(t, "prod-trino", body.Connections[0].Connection)
		for _, c := range body.Connections {
			assert.NotEmpty(t, c.Connection, "no empty-connection rows may be returned")
		}
	})

	// A multi-connection toolkit (apigateway with N upstream connections,
	// trino with multiple catalogs) must expand to one entry per real
	// connection because the persona filter authorizes against the
	// connection name resolved at call time.
	t.Run("multi-connection toolkit expands to one entry per connection", func(t *testing.T) {
		mt := mockMultiConnectionToolkit{
			mockToolkit: mockToolkit{
				kind: "api", name: "apigateway", connection: "apigateway",
				tools: []string{"api_invoke_endpoint", "api_list_endpoints"},
			},
			connections: []toolkit.ConnectionDetail{
				{Name: "vendor-a", Description: "Vendor A REST API"},
				{Name: "vendor-b", Description: "Vendor B REST API"},
				{Name: "vendor-c", Description: "Vendor C REST API"},
			},
		}
		single := mockToolkit{
			kind: "knowledge", name: "knowledge", connection: "knowledge",
			tools: []string{"capture_insight", "apply_knowledge"},
		}
		reg := &mockToolkitRegistry{
			rawToolkits: []registry.Toolkit{mt, single},
			allResult:   []mockToolkit{single},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body connectionListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 4, body.Total, "3 expanded API connections + 1 single-connection knowledge toolkit")

		byName := make(map[string]connectionInfo, len(body.Connections))
		for _, c := range body.Connections {
			byName[c.Name] = c
		}
		for _, name := range []string{"vendor-a", "vendor-b", "vendor-c"} {
			c, ok := byName[name]
			require.True(t, ok, "expected expanded connection %q in response", name)
			assert.Equal(t, "api", c.Kind, "%s should inherit kind from parent toolkit", name)
			assert.Equal(t, name, c.Connection, "%s authorization identifier must equal connection name", name)
			assert.ElementsMatch(t, []string{"api_invoke_endpoint", "api_list_endpoints"}, c.Tools)
		}
		// Single-connection toolkit unchanged.
		kn, ok := byName["knowledge"]
		require.True(t, ok)
		assert.Equal(t, "knowledge", kn.Kind)
		assert.Equal(t, "knowledge", kn.Connection)
	})

	// A toolkit whose Tools() returns nil (e.g. gateway with no live
	// upstream connections) must serialize tools AND hidden_tools as [],
	// not null. The persona editor's connections scope reads
	// c.tools.length / c.hidden_tools.length on the wire value, and a
	// null crashes the render. connectionInfo.MarshalJSON enforces the
	// invariant for both fields no matter how the struct is constructed.
	t.Run("nil Tools() serializes both tools and hidden_tools as empty arrays", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "gateway", name: "vendor", connection: "vendor", tools: nil},
			},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		raw := w.Body.String()
		for _, field := range []string{"tools", "hidden_tools"} {
			assert.Contains(t, raw, fmt.Sprintf("%q:[]", field), "%s must be []; got: %s", field, raw)
			assert.NotContains(t, raw, fmt.Sprintf("%q:null", field), "%s must never be null on the wire", field)
		}
	})

	// A gateway upstream's reachability must reach the /admin/connections
	// surface, identically to list_connections, so operators see the same
	// health in the admin UI and the MCP tool. A connection without health
	// tracking (no live session) omits the field.
	t.Run("gateway connection health is surfaced", func(t *testing.T) {
		mt := mockMultiConnectionToolkit{
			mockToolkit: mockToolkit{
				kind: "mcp", name: "gateway", connection: "gateway",
				tools: []string{"vendor_echo"},
			},
			connections: []toolkit.ConnectionDetail{
				{
					Name: "up",
					Health: &toolkit.ConnectionHealth{
						Reachable: true, LastSuccessUnix: 1_700_000_000,
					},
				},
				{
					Name: "down",
					Health: &toolkit.ConnectionHealth{
						Reachable: false, LastError: "connection refused",
					},
				},
				{Name: "untracked"},
			},
		}
		reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{mt}}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body connectionListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))

		byName := make(map[string]connectionInfo, len(body.Connections))
		for _, c := range body.Connections {
			byName[c.Name] = c
		}

		up := byName["up"]
		require.NotNil(t, up.Health)
		assert.True(t, up.Health.Reachable)
		assert.Equal(t, "2023-11-14T22:13:20Z", up.Health.LastSuccess)

		down := byName["down"]
		require.NotNil(t, down.Health)
		assert.False(t, down.Health.Reachable)
		assert.Equal(t, "connection refused", down.Health.LastError)

		assert.Nil(t, byName["untracked"].Health, "no health struct means no health on the wire")
	})
}
