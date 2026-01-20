package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolkit_RegisterTools(t *testing.T) {
	toolkit := NewToolkit()
	defer toolkit.Close()

	// Create a test server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "1.0.0",
	}, nil)

	// Should not panic
	toolkit.RegisterTools(server)
}

func TestToolkit_Close(t *testing.T) {
	toolkit := NewToolkit()

	// Should not error
	if err := toolkit.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
