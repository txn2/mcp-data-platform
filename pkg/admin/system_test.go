package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // register swagger docs
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestGetSystemInfo(t *testing.T) {
	t.Run("returns runtime feature availability", func(t *testing.T) {
		cfg := testConfig()
		cfg.Server.Name = "test-platform"
		cfg.Server.Description = "Test description"
		cfg.Server.Transport = "http"
		cfg.Audit.Enabled = true
		cfg.OAuth.Enabled = true
		cfg.Knowledge.Enabled = true
		cfg.Admin.Enabled = true

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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body systemInfoResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "test-platform", body.Name)
		assert.Equal(t, "Test description", body.Description)
		assert.Equal(t, "http", body.Transport)
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
		cfg.Audit.Enabled = true
		cfg.Knowledge.Enabled = true

		h := NewHandler(Deps{
			Config:            cfg,
			DatabaseAvailable: false,
			// No AuditQuerier, no Knowledge handler — not available at runtime
		}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
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
		cfg.Admin.PortalTitle = "ACME Admin"

		h := NewHandler(Deps{Config: cfg}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body publicBrandingResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "acme-platform", body.Name)
		assert.Equal(t, "ACME Admin", body.PortalTitle)
	})

	t.Run("returns empty when no config", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body publicBrandingResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Empty(t, body.Name)
		assert.Empty(t, body.PortalTitle)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public/branding", http.NoBody)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/docs/index.html", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolListResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 1, body.Total)
		assert.Equal(t, "platform_info", body.Tools[0].Name)
	})
}

func TestListConnections(t *testing.T) {
	t.Run("returns connections from registry", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
				{kind: "datahub", name: "primary", connection: "primary-datahub", tools: []string{"datahub_search"}},
			},
		}
		h := NewHandler(Deps{ToolkitRegistry: reg}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/connections", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/connections", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/connections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
	})
}
