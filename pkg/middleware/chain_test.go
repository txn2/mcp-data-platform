package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTextResult creates a simple text result for testing.
func newTextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

// newErrorResult creates an error result for testing.
func newErrorResult(errMsg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: errMsg},
		},
	}
}

func TestChain(t *testing.T) {
	t.Run("empty chain", func(t *testing.T) {
		chain := NewChain()

		handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return newTextResult("success"), nil
		}

		wrapped := chain.Wrap(handler)
		result, err := wrapped(context.Background(), mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("handler error = %v", err)
		}
		if result == nil {
			t.Fatal("result is nil")
		}
	})

	t.Run("before middleware", func(t *testing.T) {
		chain := NewChain()

		var callOrder []string

		chain.UseBefore(func(next Handler) Handler {
			return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				callOrder = append(callOrder, "before1")
				return next(ctx, req)
			}
		})

		chain.UseBefore(func(next Handler) Handler {
			return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				callOrder = append(callOrder, "before2")
				return next(ctx, req)
			}
		})

		handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			callOrder = append(callOrder, "handler")
			return newTextResult("success"), nil
		}

		wrapped := chain.Wrap(handler)
		_, _ = wrapped(context.Background(), mcp.CallToolRequest{})

		// Before middleware should run in order added
		expected := []string{"before1", "before2", "handler"}
		if len(callOrder) != len(expected) {
			t.Fatalf("call order length = %d, want %d", len(callOrder), len(expected))
		}
		for i, v := range expected {
			if callOrder[i] != v {
				t.Errorf("callOrder[%d] = %q, want %q", i, callOrder[i], v)
			}
		}
	})

	t.Run("after middleware", func(t *testing.T) {
		chain := NewChain()

		var callOrder []string

		chain.UseAfter(func(next Handler) Handler {
			return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				result, err := next(ctx, req)
				callOrder = append(callOrder, "after1")
				return result, err
			}
		})

		chain.UseAfter(func(next Handler) Handler {
			return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				result, err := next(ctx, req)
				callOrder = append(callOrder, "after2")
				return result, err
			}
		})

		handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			callOrder = append(callOrder, "handler")
			return newTextResult("success"), nil
		}

		wrapped := chain.Wrap(handler)
		_, _ = wrapped(context.Background(), mcp.CallToolRequest{})

		// After middleware should run in reverse order (first added runs last)
		expected := []string{"handler", "after2", "after1"}
		if len(callOrder) != len(expected) {
			t.Fatalf("call order length = %d, want %d", len(callOrder), len(expected))
		}
		for i, v := range expected {
			if callOrder[i] != v {
				t.Errorf("callOrder[%d] = %q, want %q", i, callOrder[i], v)
			}
		}
	})

	t.Run("WrapWithContext", func(t *testing.T) {
		chain := NewChain()

		handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return newErrorResult("no platform context"), nil
			}
			return newTextResult(pc.ToolName), nil
		}

		wrapped := chain.WrapWithContext(handler, "test_tool", "test_kind", "test_name")
		result, err := wrapped(context.Background(), mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("handler error = %v", err)
		}

		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("expected TextContent")
		}
		if textContent.Text != "test_tool" {
			t.Errorf("text = %q, want %q", textContent.Text, "test_tool")
		}
	})
}
