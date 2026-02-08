package registry

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const regTestTrino = "trino"

// mockToolkit is a simple mock for testing.
type mockToolkit struct {
	kind       string
	name       string
	connection string
	tools      []string
	closeCalls int
}

func (m *mockToolkit) Kind() string                            { return m.kind }
func (m *mockToolkit) Name() string                            { return m.name }
func (m *mockToolkit) Connection() string                      { return m.connection }
func (m *mockToolkit) RegisterTools(_ *mcp.Server)             {} //nolint:revive // unused-receiver: mock
func (m *mockToolkit) Tools() []string                         { return m.tools }
func (m *mockToolkit) SetSemanticProvider(_ semantic.Provider) {} //nolint:revive // unused-receiver: mock
func (m *mockToolkit) SetQueryProvider(_ query.Provider)       {} //nolint:revive // unused-receiver: mock
func (m *mockToolkit) Close() error                            { m.closeCalls++; return nil }

// mockToolkitWithCloseError is a toolkit that returns an error on Close.
type mockToolkitWithCloseError struct {
	mockToolkit
}

func (m *mockToolkitWithCloseError) Close() error { //nolint:revive // unused-receiver: mock
	return fmt.Errorf("close error")
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	toolkit := &mockToolkit{kind: regTestTrino, name: "prod"}

	if err := reg.Register(toolkit); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := reg.Get(regTestTrino, "prod")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if got.Kind() != regTestTrino {
		t.Errorf("Kind() = %q, want %q", got.Kind(), regTestTrino)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	reg := NewRegistry()
	toolkit := &mockToolkit{kind: regTestTrino, name: "prod"}

	_ = reg.Register(toolkit)
	err := reg.Register(toolkit)
	if err == nil {
		t.Error("Register() expected error for duplicate")
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("nonexistent", "name")
	if ok {
		t.Error("Get() returned true for nonexistent toolkit")
	}
}

func TestRegistry_GetByKind(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{kind: regTestTrino, name: "prod"})
	_ = reg.Register(&mockToolkit{kind: regTestTrino, name: "staging"})
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "main"})

	trinoToolkits := reg.GetByKind(regTestTrino)
	if len(trinoToolkits) != 2 {
		t.Errorf("GetByKind(trino) returned %d toolkits, want 2", len(trinoToolkits))
	}
}

func TestRegistry_AllAndAllTools(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{kind: regTestTrino, name: "prod", tools: []string{"trino_query", "trino_describe"}})
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "main", tools: []string{"datahub_search"}})

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("All() returned %d toolkits, want 2", len(all))
	}

	tools := reg.AllTools()
	if len(tools) != 3 {
		t.Errorf("AllTools() returned %d tools, want 3", len(tools))
	}
}

func TestRegistry_Close(t *testing.T) {
	reg := NewRegistry()
	toolkit := &mockToolkit{kind: regTestTrino, name: "prod"}
	_ = reg.Register(toolkit)

	if err := reg.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if toolkit.closeCalls != 1 {
		t.Errorf("closeCalls = %d, want 1", toolkit.closeCalls)
	}
}

func TestRegistry_CloseWithError(t *testing.T) {
	reg := NewRegistry()
	toolkit := &mockToolkitWithCloseError{mockToolkit: mockToolkit{kind: regTestTrino, name: "prod"}}
	_ = reg.Register(toolkit)

	err := reg.Close()
	if err == nil {
		t.Error("Close() expected error when toolkit fails")
	}
}

func TestRegistry_Providers(t *testing.T) {
	reg := NewRegistry()
	toolkit := &mockToolkit{kind: regTestTrino, name: "prod"}
	_ = reg.Register(toolkit)

	reg.SetSemanticProvider(semantic.NewNoopProvider())
	reg.SetQueryProvider(query.NewNoopProvider())
	// Just verify it doesn't panic
}

func TestRegistry_RegisterAllTools(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{kind: regTestTrino, name: "prod", tools: []string{"trino_query"}})
	_ = reg.Register(&mockToolkit{kind: "datahub", name: "main", tools: []string{"datahub_search"}})

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	reg.RegisterAllTools(server) // Should not panic
}

func TestRegistry_RegisterWithPresetProviders(t *testing.T) {
	reg := NewRegistry()
	reg.SetSemanticProvider(semantic.NewNoopProvider())
	reg.SetQueryProvider(query.NewNoopProvider())

	toolkit := &mockToolkit{kind: regTestTrino, name: "prod"}
	if err := reg.Register(toolkit); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := reg.Get(regTestTrino, "prod")
	if !ok {
		t.Fatal("Get() returned false")
	}
	if got.Kind() != regTestTrino {
		t.Errorf("Kind() = %q, want %q", got.Kind(), regTestTrino)
	}
}

func TestRegistry_Factory(t *testing.T) {
	reg := NewRegistry()
	factory := func(name string, _ map[string]any) (Toolkit, error) {
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
}

func TestRegistry_FactoryError(t *testing.T) {
	reg := NewRegistry()
	factory := func(_ string, _ map[string]any) (Toolkit, error) {
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
}

func TestRegistry_UnknownKind(t *testing.T) {
	reg := NewRegistry()

	err := reg.CreateAndRegister(ToolkitConfig{
		Kind:   "unknown",
		Name:   "test",
		Config: map[string]any{},
	})
	if err == nil {
		t.Error("CreateAndRegister() expected error for unknown kind")
	}
}

func TestRegisterBuiltinFactories(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltinFactories(reg)

	// Verify all three factories are registered by trying to create with invalid config
	t.Run("trino factory registered", func(t *testing.T) {
		// Should fail with invalid config (missing host)
		err := reg.CreateAndRegister(ToolkitConfig{
			Kind:   regTestTrino,
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

func TestGetToolkitForTool_Found(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{
		kind:       regTestTrino,
		name:       "production",
		connection: "prod-trino",
		tools:      []string{"trino_query", "trino_describe"},
	})

	match := reg.GetToolkitForTool("trino_query")
	assertToolMatch(t, match, regTestTrino, "production", "prod-trino", true)
}

func TestGetToolkitForTool_NotFound(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{
		kind:       regTestTrino,
		name:       "production",
		connection: "prod-trino",
		tools:      []string{"trino_query"},
	})

	match := reg.GetToolkitForTool("unknown_tool")
	assertToolMatch(t, match, "", "", "", false)
}

func TestGetToolkitForTool_MultipleToolkits(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockToolkit{
		kind: regTestTrino, name: "production", connection: "prod-trino",
		tools: []string{"trino_query", "trino_describe"},
	})
	_ = reg.Register(&mockToolkit{
		kind: "datahub", name: "main", connection: "main-datahub",
		tools: []string{"datahub_search", "datahub_get_entity"},
	})
	_ = reg.Register(&mockToolkit{
		kind: "s3", name: "storage", connection: "s3-storage",
		tools: []string{"s3_list_buckets", "s3_get_object"},
	})

	tests := []struct {
		tool      string
		wantKind  string
		wantName  string
		wantConn  string
		wantFound bool
	}{
		{"trino_query", regTestTrino, "production", "prod-trino", true},
		{"datahub_search", "datahub", "main", "main-datahub", true},
		{"s3_list_buckets", "s3", "storage", "s3-storage", true},
		{"unknown", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			match := reg.GetToolkitForTool(tt.tool)
			assertToolMatch(t, match, tt.wantKind, tt.wantName, tt.wantConn, tt.wantFound)
		})
	}
}

func assertToolMatch(t *testing.T, match ToolkitMatch, wantKind, wantName, wantConn string, wantFound bool) {
	t.Helper()
	if match.Found != wantFound {
		t.Errorf("found = %v, want %v", match.Found, wantFound)
	}
	if match.Kind != wantKind {
		t.Errorf("kind = %q, want %q", match.Kind, wantKind)
	}
	if match.Name != wantName {
		t.Errorf("name = %q, want %q", match.Name, wantName)
	}
	if match.Connection != wantConn {
		t.Errorf("connection = %q, want %q", match.Connection, wantConn)
	}
}
