package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // register swagger docs
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
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(2), body["total"])
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
