package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// --- Mock ToolkitRegistry ---

type mockToolkit struct {
	kind       string
	name       string
	connection string
	tools      []string
}

func (m mockToolkit) Kind() string                          { return m.kind }
func (m mockToolkit) Name() string                          { return m.name }
func (m mockToolkit) Connection() string                    { return m.connection }
func (m mockToolkit) Tools() []string                       { return m.tools }
func (mockToolkit) RegisterTools(_ *mcp.Server)             {}
func (mockToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (mockToolkit) SetQueryProvider(_ query.Provider)       {}
func (mockToolkit) Close() error                            { return nil }

// Verify interface compliance.
var _ registry.Toolkit = mockToolkit{}

type mockToolkitRegistry struct {
	allResult []mockToolkit
}

func (m *mockToolkitRegistry) All() []registry.Toolkit {
	result := make([]registry.Toolkit, len(m.allResult))
	for i, tk := range m.allResult {
		result[i] = tk
	}
	return result
}

func (m *mockToolkitRegistry) AllTools() []string {
	n := 0
	for _, tk := range m.allResult {
		n += len(tk.tools)
	}
	tools := make([]string, 0, n)
	for _, tk := range m.allResult {
		tools = append(tools, tk.tools...)
	}
	return tools
}

// Verify interface compliance.
var _ ToolkitRegistry = (*mockToolkitRegistry)(nil)

// --- Mock PersonaRegistry ---

type mockPersonaRegistry struct {
	allResult      []*persona.Persona
	getMap         map[string]*persona.Persona
	registerErr    error
	registerCalled int
	unregisterErr  error
	defaultName    string
}

func (m *mockPersonaRegistry) All() []*persona.Persona {
	return m.allResult
}

func (m *mockPersonaRegistry) Get(name string) (*persona.Persona, bool) {
	if m.getMap != nil {
		p, ok := m.getMap[name]
		return p, ok
	}
	for _, p := range m.allResult {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

func (m *mockPersonaRegistry) Register(p *persona.Persona) error {
	m.registerCalled++
	if m.registerErr != nil {
		return m.registerErr
	}
	// Update allResult in place for test visibility
	for i, existing := range m.allResult {
		if existing.Name == p.Name {
			m.allResult[i] = p
			return nil
		}
	}
	m.allResult = append(m.allResult, p)
	return nil
}

func (m *mockPersonaRegistry) Unregister(name string) error {
	if m.unregisterErr != nil {
		return m.unregisterErr
	}
	for i, p := range m.allResult {
		if p.Name == name {
			m.allResult = append(m.allResult[:i], m.allResult[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("persona %q not found", name)
}

func (m *mockPersonaRegistry) DefaultName() string {
	return m.defaultName
}

// Verify interface compliance.
var _ PersonaRegistry = (*mockPersonaRegistry)(nil)

// --- Mock AuditQuerier ---

type mockAuditQuerier struct {
	queryResult []audit.Event
	queryErr    error
	countResult int
	countErr    error
}

func (m *mockAuditQuerier) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return m.queryResult, m.queryErr
}

func (m *mockAuditQuerier) Count(_ context.Context, _ audit.QueryFilter) (int, error) {
	return m.countResult, m.countErr
}

// Verify interface compliance.
var _ AuditQuerier = (*mockAuditQuerier)(nil)

// --- Mock APIKeyManager ---

type mockAPIKeyManager struct {
	keys       []auth.APIKeySummary
	generateFn func(name string, roles []string) (string, error)
	removeFn   func(name string) bool
}

func (m *mockAPIKeyManager) ListKeys() []auth.APIKeySummary {
	return m.keys
}

func (m *mockAPIKeyManager) GenerateKey(name string, roles []string) (string, error) {
	if m.generateFn != nil {
		return m.generateFn(name, roles)
	}
	return "generated-key-value", nil
}

func (m *mockAPIKeyManager) RemoveByName(name string) bool {
	if m.removeFn != nil {
		return m.removeFn(name)
	}
	return false
}

// Verify interface compliance.
var _ APIKeyManager = (*mockAPIKeyManager)(nil)

// --- Mock ConfigStore ---

type mockConfigStore struct {
	mode       string
	saveErr    error
	saveCalls  int
	history    []configstore.Revision
	historyErr error
}

func (*mockConfigStore) Load(_ context.Context) ([]byte, error) {
	return nil, nil
}

func (m *mockConfigStore) Save(_ context.Context, _ []byte, _ configstore.SaveMeta) error {
	m.saveCalls++
	return m.saveErr
}

func (m *mockConfigStore) History(_ context.Context, _ int) ([]configstore.Revision, error) {
	return m.history, m.historyErr
}

func (m *mockConfigStore) Mode() string {
	if m.mode == "" {
		return "database"
	}
	return m.mode
}

// Verify interface compliance.
var _ ConfigStore = (*mockConfigStore)(nil)

// --- Test helpers ---

func testConfig() *platform.Config {
	cfg := &platform.Config{
		Server: platform.ServerConfig{
			Name:      "test-platform",
			Version:   "1.0.0",
			Transport: "http",
		},
		Admin: platform.AdminConfig{
			Enabled: true,
			Persona: "admin",
		},
	}
	return cfg
}

func testPersonas(names ...string) []*persona.Persona {
	personas := make([]*persona.Persona, len(names))
	for i, name := range names {
		personas[i] = &persona.Persona{
			Name:        name,
			DisplayName: name + " persona",
			Description: "Test " + name,
			Roles:       []string{name},
			Tools: persona.ToolRules{
				Allow: []string{"*"},
			},
		}
	}
	return personas
}

// decodeProblem parses a problem+json response body into a problemDetail.
func decodeProblem(body []byte) problemDetail {
	var pd problemDetail
	_ = json.Unmarshal(body, &pd)
	return pd
}
