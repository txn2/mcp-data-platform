package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewToolkit(t *testing.T) {
	toolkit := NewToolkit()
	if toolkit == nil {
		t.Error("NewToolkit() returned nil")
	}
}

func TestToolkit_RegisterTools(_ *testing.T) {
	toolkit := NewToolkit()
	defer func() { _ = toolkit.Close() }()

	// Create a test server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "1.0.0",
	}, nil)

	// Should not panic
	toolkit.RegisterTools(server)
}

func TestToolkit_handleExampleTool(t *testing.T) {
	toolkit := NewToolkit()
	defer func() { _ = toolkit.Close() }()

	args := ExampleToolArgs{
		Message: "Hello, World!",
	}

	result, _, err := toolkit.handleExampleTool(context.Background(), nil, args)
	if err != nil {
		t.Fatalf("handleExampleTool() error = %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content item, got %d", len(result.Content))
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}

	if textContent.Text != "Echo: Hello, World!" {
		t.Errorf("expected 'Echo: Hello, World!', got %q", textContent.Text)
	}
}

func TestToolkit_Close(t *testing.T) {
	toolkit := NewToolkit()

	// Should not error
	if err := toolkit.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
