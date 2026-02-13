package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // register swagger docs
)

// newTestMCPServer creates an MCP server with test tools registered.
func newTestMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-platform",
		Version: "v0.0.1",
	}, nil)

	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Execute a SQL query",
		InputSchema: json.RawMessage(`{"type":"object","required":["sql"],"properties":{"sql":{"type":"string","description":"The SQL query"},"connection":{"type":"string","description":"Connection name"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "query result: 42 rows"}},
		}, nil
	})

	server.AddTool(&mcp.Tool{
		Name:        "datahub_search",
		Description: "Search DataHub catalog",
		InputSchema: json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string","description":"Search query"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "found 3 datasets"}},
		}, nil
	})

	return server
}

func TestGetToolSchemas(t *testing.T) {
	t.Run("returns schemas from MCP server", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
				{kind: "datahub", name: "primary", connection: "primary-datahub", tools: []string{"datahub_search"}},
			},
		}
		h := NewHandler(Deps{
			ToolkitRegistry: reg,
			MCPServer:       newTestMCPServer(),
		}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools/schemas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolSchemaResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body.Schemas, 2)

		trinoSchema, ok := body.Schemas["trino_query"]
		require.True(t, ok, "trino_query schema should be present")
		assert.Equal(t, "trino_query", trinoSchema.Name)
		assert.Equal(t, "trino", trinoSchema.Kind)
		assert.Equal(t, "Execute a SQL query", trinoSchema.Description)
		assert.NotNil(t, trinoSchema.Parameters)

		dhSchema, ok := body.Schemas["datahub_search"]
		require.True(t, ok, "datahub_search schema should be present")
		assert.Equal(t, "datahub", dhSchema.Kind)
	})

	t.Run("returns empty schemas when no MCP server", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools/schemas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolSchemaResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Empty(t, body.Schemas)
	})

	t.Run("returns empty kind when no registry", func(t *testing.T) {
		h := NewHandler(Deps{
			MCPServer: newTestMCPServer(),
		}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools/schemas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body toolSchemaResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body.Schemas, 2)

		trinoSchema := body.Schemas["trino_query"]
		assert.Equal(t, "", trinoSchema.Kind, "kind should be empty when no registry")
	})
}

func TestCallTool(t *testing.T) {
	t.Run("executes tool and returns result", func(t *testing.T) {
		h := NewHandler(Deps{
			MCPServer: newTestMCPServer(),
		}, nil)

		body, _ := json.Marshal(toolCallRequest{
			ToolName:   "trino_query",
			Parameters: map[string]any{"sql": "SELECT 1"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp toolCallResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.IsError)
		require.Len(t, resp.Content, 1)
		assert.Equal(t, "text", resp.Content[0].Type)
		assert.Equal(t, "query result: 42 rows", resp.Content[0].Text)
		assert.GreaterOrEqual(t, resp.DurationMs, int64(0))
	})

	t.Run("merges connection into arguments", func(t *testing.T) {
		var capturedArgs map[string]any
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
		server.AddTool(&mcp.Tool{
			Name:        "test_tool",
			Description: "Test",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_ = json.Unmarshal(req.Params.Arguments, &capturedArgs)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
			}, nil
		})

		h := NewHandler(Deps{MCPServer: server}, nil)
		body, _ := json.Marshal(toolCallRequest{
			ToolName:   "test_tool",
			Connection: "prod-trino",
			Parameters: map[string]any{"sql": "SELECT 1"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "prod-trino", capturedArgs["connection"])
		assert.Equal(t, "SELECT 1", capturedArgs["sql"])
	})

	t.Run("returns error for missing tool_name", func(t *testing.T) {
		h := NewHandler(Deps{MCPServer: newTestMCPServer()}, nil)
		body, _ := json.Marshal(toolCallRequest{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "tool_name is required")
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		h := NewHandler(Deps{MCPServer: newTestMCPServer()}, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader([]byte("not json")))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("returns error when no MCP server", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)
		body, _ := json.Marshal(toolCallRequest{ToolName: "trino_query"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("handles unknown tool gracefully", func(t *testing.T) {
		h := NewHandler(Deps{MCPServer: newTestMCPServer()}, nil)
		body, _ := json.Marshal(toolCallRequest{ToolName: "nonexistent_tool"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp toolCallResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content[0].Text, "error")
	})

	t.Run("handles nil parameters", func(t *testing.T) {
		h := NewHandler(Deps{MCPServer: newTestMCPServer()}, nil)
		body, _ := json.Marshal(toolCallRequest{ToolName: "datahub_search"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp toolCallResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.IsError)
	})

	t.Run("propagates auth token to internal session", func(t *testing.T) {
		h := NewHandler(Deps{MCPServer: newTestMCPServer()}, nil)
		body, _ := json.Marshal(toolCallRequest{
			ToolName:   "trino_query",
			Parameters: map[string]any{"sql": "SELECT 1"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools/call", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp toolCallResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.IsError)
	})
}
