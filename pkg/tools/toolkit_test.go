package tools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestToolkit_handleExampleTool(t *testing.T) {
	tests := []struct {
		name      string
		args      any
		wantText  string
		wantError bool
	}{
		{
			name:     "valid message",
			args:     map[string]any{"message": "hello"},
			wantText: "Echo: hello",
		},
		{
			name:      "missing message",
			args:      map[string]any{},
			wantError: true,
		},
		{
			name:      "invalid message type",
			args:      map[string]any{"message": 123},
			wantError: true,
		},
		{
			name:      "nil args",
			args:      nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolkit := NewToolkit()
			defer toolkit.Close()

			req := mcp.CallToolRequest{}
			req.Params.Arguments = tt.args

			result, err := toolkit.handleExampleTool(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantError {
				if !result.IsError {
					t.Error("expected error result")
				}
				return
			}

			if result.IsError {
				t.Errorf("unexpected error result: %v", result)
				return
			}

			if len(result.Content) == 0 {
				t.Fatal("expected content in result")
			}

			textContent, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("expected TextContent, got %T", result.Content[0])
			}

			if textContent.Text != tt.wantText {
				t.Errorf("got text %q, want %q", textContent.Text, tt.wantText)
			}
		})
	}
}
