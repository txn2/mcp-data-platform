package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPIconMiddleware_ToolsList(t *testing.T) {
	cfg := IconsMiddlewareConfig{
		Tools: map[string]IconConfig{
			"trino_query": {Source: "https://example.com/trino.svg", MIMEType: "image/svg+xml"},
		},
	}
	mw := MCPIconMiddleware(cfg)

	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{
			Tools: []*mcp.Tool{
				{Name: "trino_query", Description: "Run SQL"},
				{Name: "datahub_search", Description: "Search"},
			},
		}, nil
	})

	result, err := handler(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok {
		t.Fatal("result is not *mcp.ListToolsResult")
	}

	// trino_query should have an icon
	if len(listResult.Tools[0].Icons) != 1 {
		t.Fatalf("trino_query icons: got %d, want 1", len(listResult.Tools[0].Icons))
	}
	if listResult.Tools[0].Icons[0].Source != "https://example.com/trino.svg" {
		t.Errorf("icon source = %q, want %q", listResult.Tools[0].Icons[0].Source, "https://example.com/trino.svg")
	}
	if listResult.Tools[0].Icons[0].MIMEType != "image/svg+xml" {
		t.Errorf("icon mime = %q, want %q", listResult.Tools[0].Icons[0].MIMEType, "image/svg+xml")
	}

	// datahub_search should have no icons
	if len(listResult.Tools[1].Icons) != 0 {
		t.Errorf("datahub_search icons: got %d, want 0", len(listResult.Tools[1].Icons))
	}
}

func TestMCPIconMiddleware_ResourceTemplatesList(t *testing.T) {
	cfg := IconsMiddlewareConfig{
		Resources: map[string]IconConfig{
			"schema://{catalog}.{schema_name}/{table}": {Source: "https://example.com/schema.png"},
		},
	}
	mw := MCPIconMiddleware(cfg)

	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourceTemplatesResult{
			ResourceTemplates: []*mcp.ResourceTemplate{
				{URITemplate: "schema://{catalog}.{schema_name}/{table}", Name: "Table Schema"},
				{URITemplate: "glossary://{term}", Name: "Glossary Term"},
			},
		}, nil
	})

	result, err := handler(context.Background(), "resources/templates/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListResourceTemplatesResult)
	if !ok {
		t.Fatal("result is not *mcp.ListResourceTemplatesResult")
	}

	if len(listResult.ResourceTemplates[0].Icons) != 1 {
		t.Fatalf("schema template icons: got %d, want 1", len(listResult.ResourceTemplates[0].Icons))
	}
	if len(listResult.ResourceTemplates[1].Icons) != 0 {
		t.Errorf("glossary template icons: got %d, want 0", len(listResult.ResourceTemplates[1].Icons))
	}
}

func TestMCPIconMiddleware_PromptsList(t *testing.T) {
	cfg := IconsMiddlewareConfig{
		Prompts: map[string]IconConfig{
			"knowledge_capture": {Source: "https://example.com/knowledge.png"},
		},
	}
	mw := MCPIconMiddleware(cfg)

	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListPromptsResult{
			Prompts: []*mcp.Prompt{
				{Name: "knowledge_capture", Description: "Capture knowledge"},
			},
		}, nil
	})

	result, err := handler(context.Background(), "prompts/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListPromptsResult)
	if !ok {
		t.Fatal("result is not *mcp.ListPromptsResult")
	}

	if len(listResult.Prompts[0].Icons) != 1 {
		t.Fatalf("knowledge_capture icons: got %d, want 1", len(listResult.Prompts[0].Icons))
	}
}

func TestMCPIconMiddleware_PassthroughNonListMethods(t *testing.T) {
	cfg := IconsMiddlewareConfig{
		Tools: map[string]IconConfig{
			"trino_query": {Source: "https://example.com/icon.png"},
		},
	}
	mw := MCPIconMiddleware(cfg)

	called := false
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	})

	result, err := handler(context.Background(), "tools/call", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}

	// tools/call results should not be modified
	if _, ok := result.(*mcp.CallToolResult); !ok {
		t.Error("result type changed unexpectedly")
	}
}

func TestMCPIconMiddleware_EmptyConfig(t *testing.T) {
	cfg := IconsMiddlewareConfig{}
	mw := MCPIconMiddleware(cfg)

	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{
			Tools: []*mcp.Tool{{Name: "test_tool"}},
		}, nil
	})

	result, err := handler(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok {
		t.Fatal("result is not *mcp.ListToolsResult")
	}
	if len(listResult.Tools[0].Icons) != 0 {
		t.Errorf("expected no icons with empty config, got %d", len(listResult.Tools[0].Icons))
	}
}

func TestMCPIconMiddleware_ErrorPassthrough(t *testing.T) {
	cfg := IconsMiddlewareConfig{
		Tools: map[string]IconConfig{
			"test": {Source: "https://example.com/icon.png"},
		},
	}
	mw := MCPIconMiddleware(cfg)

	expectedErr := &PlatformError{Category: "test", Message: "fail"}
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, expectedErr
	})

	result, err := handler(context.Background(), "tools/list", nil)
	if !errors.Is(err, expectedErr) {
		t.Errorf("error = %v, want %v", err, expectedErr)
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}
