package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// toolSchemaResponse wraps tool schemas keyed by name.
type toolSchemaResponse struct {
	Schemas map[string]toolSchema `json:"schemas"`
}

// toolSchema describes a single tool's schema for the admin UI.
type toolSchema struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// getToolSchemas handles GET /api/v1/admin/tools/schemas.
//
// @Summary      Get tool schemas
// @Description  Returns JSON schemas for all registered tools including parameter definitions.
// @Tags         Tools
// @Produce      json
// @Success      200  {object}  toolSchemaResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /tools/schemas [get]
func (h *Handler) getToolSchemas(w http.ResponseWriter, r *http.Request) {
	if h.deps.MCPServer == nil {
		writeJSON(w, http.StatusOK, toolSchemaResponse{Schemas: map[string]toolSchema{}})
		return
	}

	session, cleanup, err := h.connectInternalSession(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to MCP server")
		return
	}
	defer cleanup()

	listResult, err := session.ListTools(r.Context(), &mcp.ListToolsParams{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tools")
		return
	}

	schemas := make(map[string]toolSchema, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		kind := ""
		if h.deps.ToolkitRegistry != nil {
			match := h.deps.ToolkitRegistry.GetToolkitForTool(tool.Name)
			if match.Found {
				kind = match.Kind
			}
		}
		schemas[tool.Name] = toolSchema{
			Name:        tool.Name,
			Kind:        kind,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
		}
	}

	writeJSON(w, http.StatusOK, toolSchemaResponse{Schemas: schemas})
}

// toolCallRequest is the request body for POST /tools/call.
type toolCallRequest struct {
	ToolName   string         `json:"tool_name"`
	Connection string         `json:"connection"`
	Parameters map[string]any `json:"parameters"`
}

// toolCallResponse is the response from POST /tools/call.
type toolCallResponse struct {
	Content    []toolContentBlock `json:"content"`
	IsError    bool               `json:"is_error"`
	DurationMs int64              `json:"duration_ms"`
}

// toolContentBlock represents a content block in the MCP response.
type toolContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callTool handles POST /api/v1/admin/tools/call.
//
// @Summary      Call a tool
// @Description  Executes a tool via the MCP server and returns the result.
// @Tags         Tools
// @Accept       json
// @Produce      json
// @Param        body  body      toolCallRequest  true  "Tool call request"
// @Success      200   {object}  toolCallResponse
// @Failure      400   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /tools/call [post]
func (h *Handler) callTool(w http.ResponseWriter, r *http.Request) {
	if h.deps.MCPServer == nil {
		writeError(w, http.StatusServiceUnavailable, "MCP server not available")
		return
	}

	req, err := decodeToolCallRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	session, cleanup, err := h.connectInternalSession(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to MCP server")
		return
	}
	defer cleanup()

	start := time.Now()
	result, err := session.CallTool(r.Context(), &mcp.CallToolParams{
		Name:      req.ToolName,
		Arguments: req.arguments(),
	})
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, toolCallResponse{
			Content:    []toolContentBlock{{Type: "text", Text: "error: " + err.Error()}},
			IsError:    true,
			DurationMs: durationMs,
		})
		return
	}

	blocks := extractContentBlocks(result.Content)
	if len(blocks) == 0 && result.IsError {
		blocks = []toolContentBlock{{Type: "text", Text: "Tool call returned an error with no details."}}
	}

	writeJSON(w, http.StatusOK, toolCallResponse{
		Content:    blocks,
		IsError:    result.IsError,
		DurationMs: durationMs,
	})
}

// decodeToolCallRequest reads and validates a toolCallRequest from an HTTP request.
func decodeToolCallRequest(r *http.Request) (*toolCallRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("failed to read request body")
	}

	var req toolCallRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if req.ToolName == "" {
		return nil, fmt.Errorf("tool_name is required")
	}

	return &req, nil
}

// arguments returns the merged arguments map with connection injected if present.
func (r *toolCallRequest) arguments() map[string]any {
	args := r.Parameters
	if args == nil {
		args = make(map[string]any)
	}
	if r.Connection != "" {
		args["connection"] = r.Connection
	}
	return args
}

// extractContentBlocks converts MCP content to serializable content blocks.
func extractContentBlocks(mcpContent []mcp.Content) []toolContentBlock {
	content := make([]toolContentBlock, 0, len(mcpContent))
	for _, c := range mcpContent {
		switch tc := c.(type) {
		case *mcp.TextContent:
			content = append(content, toolContentBlock{Type: "text", Text: tc.Text})
		case *mcp.EmbeddedResource:
			if tc.Resource != nil && tc.Resource.Text != "" {
				content = append(content, toolContentBlock{Type: "text", Text: tc.Resource.Text})
			}
		}
	}
	if len(content) == 0 {
		return []toolContentBlock{}
	}
	return content
}

// connectInternalSession creates an in-memory MCP client session for internal use.
// The returned cleanup function must be called to release resources.
func (h *Handler) connectInternalSession(r *http.Request) (*mcp.ClientSession, func(), error) {
	t1, t2 := mcp.NewInMemoryTransports()

	// Inject the admin's auth token into the server connection context so
	// the MCP auth middleware can authenticate the internal call.
	ctx := r.Context()
	if token := extractToken(r); token != "" {
		ctx = middleware.WithToken(ctx, token)
	}

	serverSession, err := h.deps.MCPServer.Connect(ctx, t1, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("server connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "admin-internal", Version: "v1"}, nil)
	session, err := client.Connect(r.Context(), t2, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, nil, fmt.Errorf("client connect: %w", err)
	}

	cleanup := func() {
		_ = session.Close()
		_ = serverSession.Close()
	}
	return session, cleanup, nil
}
