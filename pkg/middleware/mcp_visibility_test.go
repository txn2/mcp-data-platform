package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIsToolVisible(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		allow   []string
		deny    []string
		visible bool
	}{
		{
			name:    "no rules - all visible",
			tool:    testAuditToolName,
			visible: true,
		},
		{
			name:    "allow only - matching",
			tool:    testAuditToolName,
			allow:   []string{"trino_*"},
			visible: true,
		},
		{
			name:    "allow only - not matching",
			tool:    "datahub_search",
			allow:   []string{"trino_*"},
			visible: false,
		},
		{
			name:    "deny only - matching",
			tool:    "s3_delete_object",
			deny:    []string{"s3_delete_*"},
			visible: false,
		},
		{
			name:    "deny only - not matching",
			tool:    "s3_list_objects",
			deny:    []string{"s3_delete_*"},
			visible: true,
		},
		{
			name:    "allow and deny - allowed then denied",
			tool:    "trino_delete_table",
			allow:   []string{"trino_*"},
			deny:    []string{"*_delete_*"},
			visible: false,
		},
		{
			name:    "allow and deny - allowed not denied",
			tool:    testAuditToolName,
			allow:   []string{"trino_*"},
			deny:    []string{"*_delete_*"},
			visible: true,
		},
		{
			name:    "allow and deny - not allowed",
			tool:    "datahub_search",
			allow:   []string{"trino_*"},
			deny:    []string{"*_delete_*"},
			visible: false,
		},
		{
			name:    "exact match allow",
			tool:    "platform_info",
			allow:   []string{"platform_info"},
			visible: true,
		},
		{
			name:    "exact match deny",
			tool:    "platform_info",
			deny:    []string{"platform_info"},
			visible: false,
		},
		{
			name:    "multiple allow patterns",
			tool:    "datahub_search",
			allow:   []string{"trino_*", "datahub_*"},
			visible: true,
		},
		{
			name:    "multiple deny patterns",
			tool:    "s3_delete_object",
			deny:    []string{"trino_delete_*", "s3_delete_*"},
			visible: false,
		},
		{
			name:    "invalid allow pattern treated as non-match",
			tool:    testAuditToolName,
			allow:   []string{"[invalid"},
			visible: false,
		},
		{
			name:    "invalid deny pattern treated as non-match",
			tool:    testAuditToolName,
			deny:    []string{"[invalid"},
			visible: true,
		},
		{
			name:    "wildcard star matches all",
			tool:    "anything",
			allow:   []string{"*"},
			visible: true,
		},
		{
			name:    "empty allow empty deny slices",
			tool:    testAuditToolName,
			allow:   []string{},
			deny:    []string{},
			visible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsToolVisible(tt.tool, tt.allow, tt.deny)
			if got != tt.visible {
				t.Errorf("IsToolVisible(%q, %v, %v) = %v, want %v",
					tt.tool, tt.allow, tt.deny, got, tt.visible)
			}
		})
	}
}

// asListToolsResult extracts a *mcp.ListToolsResult from a mcp.Result,
// failing the test if the type assertion fails.
func asListToolsResult(t *testing.T, result mcp.Result) *mcp.ListToolsResult {
	t.Helper()
	lr, ok := result.(*mcp.ListToolsResult)
	if !ok {
		t.Fatalf("expected *mcp.ListToolsResult, got %T", result)
	}
	return lr
}

func TestFilterToolVisibility(t *testing.T) {
	makeTools := func(names ...string) []*mcp.Tool {
		tools := make([]*mcp.Tool, len(names))
		for i, n := range names {
			tools[i] = &mcp.Tool{Name: n}
		}
		return tools
	}

	toolNames := func(tools []*mcp.Tool) []string {
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name
		}
		return names
	}

	t.Run("non tools/list passthrough", func(t *testing.T) {
		result := &mcp.CallToolResult{}
		got, err := filterToolVisibility([]string{"trino_*"}, nil, "tools/call", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != result {
			t.Error("expected same result object for non-tools/list method")
		}
	})

	t.Run("nil result passthrough", func(t *testing.T) {
		got, err := filterToolVisibility([]string{"trino_*"}, nil, "tools/list", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Error("expected nil result to pass through")
		}
	})

	t.Run("non ListToolsResult type passthrough", func(t *testing.T) {
		result := &mcp.CallToolResult{}
		got, err := filterToolVisibility([]string{"trino_*"}, nil, "tools/list", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != result {
			t.Error("expected non-ListToolsResult to pass through")
		}
	})

	t.Run("empty tools list", func(t *testing.T) {
		result := &mcp.ListToolsResult{Tools: []*mcp.Tool{}}
		got, err := filterToolVisibility([]string{"trino_*"}, nil, "tools/list", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listResult := asListToolsResult(t, got)
		if len(listResult.Tools) != 0 {
			t.Errorf("expected 0 tools, got %d", len(listResult.Tools))
		}
	})

	t.Run("allow filters correctly", func(t *testing.T) {
		result := &mcp.ListToolsResult{
			Tools: makeTools(testAuditToolName, "trino_describe_table", "datahub_search", "s3_list_objects"),
		}
		got, err := filterToolVisibility([]string{"trino_*"}, nil, "tools/list", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listResult := asListToolsResult(t, got)
		names := toolNames(listResult.Tools)
		if len(names) != 2 {
			t.Fatalf("expected 2 tools, got %d: %v", len(names), names)
		}
		if names[0] != testAuditToolName || names[1] != "trino_describe_table" {
			t.Errorf("unexpected tools: %v", names)
		}
	})

	t.Run("deny filters correctly", func(t *testing.T) {
		result := &mcp.ListToolsResult{
			Tools: makeTools(testAuditToolName, "s3_delete_object", "datahub_search"),
		}
		got, err := filterToolVisibility(nil, []string{"s3_delete_*"}, "tools/list", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listResult := asListToolsResult(t, got)
		names := toolNames(listResult.Tools)
		if len(names) != 2 {
			t.Fatalf("expected 2 tools, got %d: %v", len(names), names)
		}
		if names[0] != testAuditToolName || names[1] != "datahub_search" {
			t.Errorf("unexpected tools: %v", names)
		}
	})

	t.Run("allow and deny combined", func(t *testing.T) {
		result := &mcp.ListToolsResult{
			Tools: makeTools(testAuditToolName, "trino_delete_table", "datahub_search", "s3_list_objects"),
		}
		got, err := filterToolVisibility([]string{"trino_*"}, []string{"*_delete_*"}, "tools/list", result)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listResult := asListToolsResult(t, got)
		names := toolNames(listResult.Tools)
		if len(names) != 1 {
			t.Fatalf("expected 1 tool, got %d: %v", len(names), names)
		}
		if names[0] != testAuditToolName {
			t.Errorf("expected trino_query, got %v", names)
		}
	})
}

func TestMCPToolVisibilityMiddleware(t *testing.T) {
	tools := []*mcp.Tool{
		{Name: testAuditToolName},
		{Name: "trino_describe_table"},
		{Name: "datahub_search"},
		{Name: "s3_list_objects"},
	}

	baseHandler := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		if method == "tools/list" {
			return &mcp.ListToolsResult{Tools: tools}, nil
		}
		return &mcp.CallToolResult{}, nil
	}

	mw := MCPToolVisibilityMiddleware([]string{"trino_*"}, nil)
	handler := mw(baseHandler)

	t.Run("filters tools/list", func(t *testing.T) {
		result, err := handler(context.Background(), "tools/list", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		listResult := asListToolsResult(t, result)
		if len(listResult.Tools) != 2 {
			t.Errorf("expected 2 tools, got %d", len(listResult.Tools))
		}
	})

	t.Run("passes through non-tools/list", func(t *testing.T) {
		result, err := handler(context.Background(), "tools/call", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.(*mcp.CallToolResult); !ok {
			t.Error("expected CallToolResult for non-tools/list method")
		}
	})

	t.Run("propagates errors from next handler", func(t *testing.T) {
		errHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return nil, context.Canceled
		}
		errMW := MCPToolVisibilityMiddleware([]string{"trino_*"}, nil)
		h := errMW(errHandler)
		_, err := h(context.Background(), "tools/list", nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}
