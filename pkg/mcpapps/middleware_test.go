package mcpapps

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// assertToolInList finds a tool by name in the list result and returns it.
func assertToolInList(t *testing.T, listResult *mcp.ListToolsResult, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range listResult.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in result", name)
	return nil
}

// assertToolHasUI checks that the given tool has a "ui" key in Meta with the expected resource URI.
func assertToolHasUI(t *testing.T, tool *mcp.Tool, expectedURI string) {
	t.Helper()
	if tool.Meta == nil {
		t.Fatalf("tool %q .Meta is nil", tool.Name)
	}
	ui, ok := tool.Meta["ui"]
	if !ok {
		t.Fatalf("tool %q .Meta does not contain 'ui' key", tool.Name)
	}
	uiMap, ok := ui.(map[string]string)
	if !ok {
		t.Fatalf("ui metadata is not map[string]string: %T", ui)
	}
	if uiMap["resourceUri"] != expectedURI {
		t.Errorf("resourceUri = %q, want %q", uiMap["resourceUri"], expectedURI)
	}
}

// assertToolNoUI checks that a tool does NOT have UI metadata.
func assertToolNoUI(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.Meta != nil {
		if _, hasUI := tool.Meta["ui"]; hasUI {
			t.Errorf("tool %q should not have UI metadata", tool.Name)
		}
	}
}

func TestToolMetadataMiddleware(t *testing.T) {
	testdata := testdataDir(t)

	// Setup registry with an app
	reg := NewRegistry()
	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: "ui://test-app",
		ToolNames:   []string{"test_tool"},
		AssetsPath:  testdata,
		EntryPoint:  "index.html",
	}
	if err := reg.Register(app); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	// Create middleware
	middleware := ToolMetadataMiddleware(reg)

	t.Run("injects UI metadata for matching tool", func(t *testing.T) {
		handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "test_tool", Description: "A test tool"},
					{Name: "other_tool", Description: "Another tool"},
				},
			}, nil
		}

		wrapped := middleware(handler)

		result, err := wrapped(context.Background(), "tools/list", nil)
		if err != nil {
			t.Fatalf("Middleware returned error: %v", err)
		}

		listResult, ok := result.(*mcp.ListToolsResult)
		if !ok {
			t.Fatalf("Result is not *mcp.ListToolsResult: %T", result)
		}

		testTool := assertToolInList(t, listResult, "test_tool")
		otherTool := assertToolInList(t, listResult, "other_tool")

		assertToolHasUI(t, testTool, "ui://test-app")
		assertToolNoUI(t, otherTool)
	})

	t.Run("passes through non-tools/list methods", func(t *testing.T) {
		callCount := 0
		handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			callCount++
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
			}, nil
		}

		wrapped := middleware(handler)

		_, err := wrapped(context.Background(), "tools/call", nil)
		if err != nil {
			t.Fatalf("Middleware returned error: %v", err)
		}

		if callCount != 1 {
			t.Errorf("Handler called %d times, want 1", callCount)
		}
	})

	t.Run("handles nil result gracefully", func(t *testing.T) {
		handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return nil, nil //nolint:nilnil // test mock: nil means no data
		}

		wrapped := middleware(handler)
		result, err := wrapped(context.Background(), "tools/list", nil)
		if err != nil {
			t.Fatalf("Middleware returned error: %v", err)
		}
		if result != nil {
			t.Errorf("Expected nil result, got %v", result)
		}
	})

	t.Run("handles error from handler", func(t *testing.T) {
		var dummy any
		expectedErr := json.Unmarshal([]byte("invalid"), &dummy)
		handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return nil, expectedErr //nolint:wrapcheck // test mock returning sentinel error
		}

		wrapped := middleware(handler)
		_, err := wrapped(context.Background(), "tools/list", nil)

		if !errors.Is(err, expectedErr) {
			t.Errorf("Expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestToolMetadataMiddleware_EmptyRegistry(t *testing.T) {
	reg := NewRegistry()
	middleware := ToolMetadataMiddleware(reg)

	handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{
			Tools: []*mcp.Tool{
				{Name: "test_tool", Description: "A test tool"},
			},
		}, nil
	}

	wrapped := middleware(handler)
	result, err := wrapped(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("Middleware returned error: %v", err)
	}

	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok {
		t.Fatalf("Result is not *mcp.ListToolsResult: %T", result)
	}
	if len(listResult.Tools[0].Meta) > 0 {
		t.Error("Tool should not have metadata when no apps registered")
	}
}
