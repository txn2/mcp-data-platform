package registry

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// mockToolkit is a simple mock for testing
type mockToolkit struct {
	kind       string
	name       string
	tools      []string
	closeCalls int
}

func (m *mockToolkit) Kind() string                            { return m.kind }
func (m *mockToolkit) Name() string                            { return m.name }
func (m *mockToolkit) RegisterTools(_ *server.MCPServer)       {}
func (m *mockToolkit) Tools() []string                         { return m.tools }
func (m *mockToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (m *mockToolkit) SetQueryProvider(_ query.Provider)       {}
func (m *mockToolkit) SetMiddleware(_ *middleware.Chain)       {}
func (m *mockToolkit) Close() error                            { m.closeCalls++; return nil }

func TestRegistry(t *testing.T) {
	t.Run("Register and Get", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}

		if err := reg.Register(toolkit); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		got, ok := reg.Get("trino", "prod")
		if !ok {
			t.Fatal("Get() returned false")
		}
		if got.Kind() != "trino" {
			t.Errorf("Kind() = %q, want %q", got.Kind(), "trino")
		}
	})

	t.Run("Register duplicate", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}

		_ = reg.Register(toolkit)
		err := reg.Register(toolkit)
		if err == nil {
			t.Error("Register() expected error for duplicate")
		}
	})

	t.Run("Get not found", func(t *testing.T) {
		reg := NewRegistry()
		_, ok := reg.Get("nonexistent", "name")
		if ok {
			t.Error("Get() returned true for nonexistent toolkit")
		}
	})

	t.Run("GetByKind", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "trino", name: "prod"})
		_ = reg.Register(&mockToolkit{kind: "trino", name: "staging"})
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "main"})

		trinoToolkits := reg.GetByKind("trino")
		if len(trinoToolkits) != 2 {
			t.Errorf("GetByKind(trino) returned %d toolkits, want 2", len(trinoToolkits))
		}
	})

	t.Run("All", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "trino", name: "prod"})
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "main"})

		all := reg.All()
		if len(all) != 2 {
			t.Errorf("All() returned %d toolkits, want 2", len(all))
		}
	})

	t.Run("AllTools", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "trino", name: "prod", tools: []string{"trino_query", "trino_describe"}})
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "main", tools: []string{"datahub_search"}})

		tools := reg.AllTools()
		if len(tools) != 3 {
			t.Errorf("AllTools() returned %d tools, want 3", len(tools))
		}
	})

	t.Run("Close", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}
		_ = reg.Register(toolkit)

		if err := reg.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if toolkit.closeCalls != 1 {
			t.Errorf("closeCalls = %d, want 1", toolkit.closeCalls)
		}
	})

	t.Run("SetSemanticProvider", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}
		_ = reg.Register(toolkit)

		provider := semantic.NewNoopProvider()
		reg.SetSemanticProvider(provider)
		// Just verify it doesn't panic
	})

	t.Run("SetQueryProvider", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}
		_ = reg.Register(toolkit)

		provider := query.NewNoopProvider()
		reg.SetQueryProvider(provider)
		// Just verify it doesn't panic
	})

	t.Run("SetMiddleware", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkit{kind: "trino", name: "prod"}
		_ = reg.Register(toolkit)

		chain := middleware.NewChain()
		reg.SetMiddleware(chain)
		// Just verify it doesn't panic
	})
}
