package registry

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
func (m *mockToolkit) RegisterTools(_ *mcp.Server)             {}
func (m *mockToolkit) Tools() []string                         { return m.tools }
func (m *mockToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (m *mockToolkit) SetQueryProvider(_ query.Provider)       {}
func (m *mockToolkit) SetMiddleware(_ *middleware.Chain)       {}
func (m *mockToolkit) Close() error                            { m.closeCalls++; return nil }

// mockToolkitWithCloseError is a toolkit that returns an error on Close.
type mockToolkitWithCloseError struct {
	mockToolkit
}

func (m *mockToolkitWithCloseError) Close() error {
	return fmt.Errorf("close error")
}

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

	t.Run("RegisterAllTools", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&mockToolkit{kind: "trino", name: "prod", tools: []string{"trino_query"}})
		_ = reg.Register(&mockToolkit{kind: "datahub", name: "main", tools: []string{"datahub_search"}})

		server := mcp.NewServer(&mcp.Implementation{
			Name:    "test",
			Version: "1.0.0",
		}, nil)
		// Should not panic
		reg.RegisterAllTools(server)
	})

	t.Run("Register with providers pre-set", func(t *testing.T) {
		reg := NewRegistry()
		// Set providers before registering
		reg.SetSemanticProvider(semantic.NewNoopProvider())
		reg.SetQueryProvider(query.NewNoopProvider())
		reg.SetMiddleware(middleware.NewChain())

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

	t.Run("Close with toolkit error", func(t *testing.T) {
		reg := NewRegistry()
		toolkit := &mockToolkitWithCloseError{mockToolkit: mockToolkit{kind: "trino", name: "prod"}}
		_ = reg.Register(toolkit)

		err := reg.Close()
		if err == nil {
			t.Error("Close() expected error when toolkit fails")
		}
	})

	t.Run("RegisterFactory", func(t *testing.T) {
		reg := NewRegistry()
		factory := func(name string, config map[string]any) (Toolkit, error) {
			return &mockToolkit{kind: "custom", name: name}, nil
		}
		reg.RegisterFactory("custom", factory)

		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   "custom",
			Name:   "test",
			Config: map[string]any{},
		})
		if err != nil {
			t.Fatalf("CreateAndRegister() error = %v", err)
		}

		_, ok := reg.Get("custom", "test")
		if !ok {
			t.Error("Get() returned false after CreateAndRegister")
		}
	})

	t.Run("CreateAndRegister factory error", func(t *testing.T) {
		reg := NewRegistry()
		factory := func(name string, config map[string]any) (Toolkit, error) {
			return nil, fmt.Errorf("factory error")
		}
		reg.RegisterFactory("failing", factory)

		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   "failing",
			Name:   "test",
			Config: map[string]any{},
		})
		if err == nil {
			t.Error("CreateAndRegister() expected error when factory fails")
		}
	})

	t.Run("CreateAndRegister unknown kind", func(t *testing.T) {
		reg := NewRegistry()

		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   "unknown",
			Name:   "test",
			Config: map[string]any{},
		})
		if err == nil {
			t.Error("CreateAndRegister() expected error for unknown kind")
		}
	})
}

func TestRegisterBuiltinFactories(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltinFactories(reg)

	// Verify all three factories are registered by trying to create with invalid config
	t.Run("trino factory registered", func(t *testing.T) {
		// Should fail with invalid config (missing host)
		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   "trino",
			Name:   "test",
			Config: map[string]any{},
		})
		if err == nil {
			t.Error("expected error for missing trino config")
		}
	})

	t.Run("datahub factory registered", func(t *testing.T) {
		// Should fail with invalid config (missing url)
		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   "datahub",
			Name:   "test",
			Config: map[string]any{},
		})
		if err == nil {
			t.Error("expected error for missing datahub config")
		}
	})

	t.Run("s3 factory registered", func(t *testing.T) {
		// S3 factory is registered, try to create (may succeed with AWS defaults)
		_ = reg.CreateAndRegister(ToolkitConfig{
			Kind:   "s3",
			Name:   "test",
			Config: map[string]any{},
		})
		// Just verify the factory is called - actual creation depends on AWS SDK defaults
	})
}

func TestTrinoFactory(t *testing.T) {
	// Test with invalid config
	_, err := TrinoFactory("test", map[string]any{})
	if err == nil {
		t.Error("TrinoFactory() expected error for missing host")
	}
}

func TestDataHubFactory(t *testing.T) {
	// Test with invalid config
	_, err := DataHubFactory("test", map[string]any{})
	if err == nil {
		t.Error("DataHubFactory() expected error for missing url")
	}
}

func TestS3Factory(t *testing.T) {
	// S3Factory may succeed with AWS SDK defaults (env vars, IAM role, etc.)
	// Just verify it can be called
	_, _ = S3Factory("test", map[string]any{})
}
