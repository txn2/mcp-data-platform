package mcpapps

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
		// Create a mock handler that returns a tools/list result
		handler := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "test_tool", Description: "A test tool"},
					{Name: "other_tool", Description: "Another tool"},
				},
			}, nil
		}

		// Wrap with middleware
		wrapped := middleware(handler)

		// Call with tools/list
		result, err := wrapped(context.Background(), "tools/list", nil)
		if err != nil {
			t.Fatalf("Middleware returned error: %v", err)
		}

		listResult, ok := result.(*mcp.ListToolsResult)
		if !ok {
			t.Fatalf("Result is not *mcp.ListToolsResult: %T", result)
		}

		// Check that test_tool has UI metadata
		var testTool, otherTool *mcp.Tool
		for _, tool := range listResult.Tools {
			if tool.Name == "test_tool" {
				testTool = tool
			}
			if tool.Name == "other_tool" {
				otherTool = tool
			}
		}

		if testTool == nil {
			t.Fatal("test_tool not found in result")
		}
		if otherTool == nil {
			t.Fatal("other_tool not found in result")
		}

		// Verify test_tool has UI metadata
		if testTool.Meta == nil {
			t.Fatal("test_tool.Meta is nil")
		}

		ui, ok := testTool.Meta["ui"]
		if !ok {
			t.Fatal("test_tool.Meta does not contain 'ui' key")
		}

		uiMap, ok := ui.(map[string]string)
		if !ok {
			t.Fatalf("ui metadata is not map[string]string: %T", ui)
		}

		if uiMap["resourceUri"] != "ui://test-app" {
			t.Errorf("resourceUri = %q, want %q", uiMap["resourceUri"], "ui://test-app")
		}

		// Verify other_tool does NOT have UI metadata
		if otherTool.Meta != nil {
			if _, hasUI := otherTool.Meta["ui"]; hasUI {
				t.Error("other_tool should not have UI metadata")
			}
		}
	})

	t.Run("passes through non-tools/list methods", func(t *testing.T) {
		callCount := 0
		handler := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
			callCount++
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
			}, nil
		}

		wrapped := middleware(handler)

		// Call with tools/call (not tools/list)
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
			return nil, nil
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
			return nil, expectedErr
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

	listResult := result.(*mcp.ListToolsResult)
	if len(listResult.Tools[0].Meta) > 0 {
		t.Error("Tool should not have metadata when no apps registered")
	}
}
