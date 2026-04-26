package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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

// mockMultiConnectionToolkit is a mockToolkit that also implements toolkit.ConnectionLister,
// simulating aggregate toolkits like Trino multi-connection mode.
type mockMultiConnectionToolkit struct {
	mockToolkit
	connections []toolkit.ConnectionDetail
}

func (m mockMultiConnectionToolkit) ListConnections() []toolkit.ConnectionDetail {
	return m.connections
}

// Verify interface compliance.
var (
	_ registry.Toolkit         = mockMultiConnectionToolkit{}
	_ toolkit.ConnectionLister = mockMultiConnectionToolkit{}
)

type mockToolkitRegistry struct {
	allResult []mockToolkit
	// rawToolkits allows injecting toolkits of any type (e.g. mockMultiConnectionToolkit).
	// When set, All() returns these instead of allResult.
	rawToolkits []registry.Toolkit
}

func (m *mockToolkitRegistry) All() []registry.Toolkit {
	if m.rawToolkits != nil {
		return m.rawToolkits
	}
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

func (m *mockToolkitRegistry) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	for _, tk := range m.allResult {
		if slices.Contains(tk.tools, toolName) {
			return registry.ToolkitMatch{
				Kind:       tk.kind,
				Name:       tk.name,
				Connection: tk.connection,
				Found:      true,
			}
		}
	}
	return registry.ToolkitMatch{}
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

// --- Mock APIKeyStore ---

type mockAPIKeyStore struct {
	setErr      error
	deleteErr   error
	setCalls    []platform.APIKeyDefinition
	deleteCalls []string
}

func (*mockAPIKeyStore) List(_ context.Context) ([]platform.APIKeyDefinition, error) {
	return nil, nil
}

func (m *mockAPIKeyStore) Set(_ context.Context, def platform.APIKeyDefinition) error {
	m.setCalls = append(m.setCalls, def)
	return m.setErr
}

func (m *mockAPIKeyStore) Delete(_ context.Context, name string) error {
	m.deleteCalls = append(m.deleteCalls, name)
	return m.deleteErr
}

// Verify interface compliance.
var _ platform.APIKeyStore = (*mockAPIKeyStore)(nil)

// --- Mock AuditQuerier ---

type mockAuditQuerier struct {
	queryResult         []audit.Event
	queryErr            error
	countResult         int
	countErr            error
	distinctResult      []string
	distinctErr         error
	distinctPairsResult map[string]string
	distinctPairsErr    error
}

func (m *mockAuditQuerier) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return m.queryResult, m.queryErr
}

func (m *mockAuditQuerier) Count(_ context.Context, _ audit.QueryFilter) (int, error) {
	return m.countResult, m.countErr
}

func (m *mockAuditQuerier) Distinct(_ context.Context, _ string, _, _ *time.Time) ([]string, error) {
	return m.distinctResult, m.distinctErr
}

func (m *mockAuditQuerier) DistinctPairs(_ context.Context, _, _ string, _, _ *time.Time) (map[string]string, error) {
	return m.distinctPairsResult, m.distinctPairsErr
}

// Verify interface compliance.
var _ AuditQuerier = (*mockAuditQuerier)(nil)

// --- Mock APIKeyManager ---

type mockAPIKeyManager struct {
	keys       []auth.APIKeySummary
	generateFn func(def auth.APIKey) (string, error)
	removeFn   func(name string) bool
}

func (m *mockAPIKeyManager) ListKeys() []auth.APIKeySummary {
	return m.keys
}

func (m *mockAPIKeyManager) GenerateKey(def auth.APIKey) (string, error) {
	if m.generateFn != nil {
		return m.generateFn(def)
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
	mode         string
	entries      map[string]*configstore.Entry
	changelog    []configstore.ChangelogEntry
	setErr       error
	setCalls     int
	deleteErr    error
	listErr      error
	getErr       error
	changelogErr error
}

func (m *mockConfigStore) Get(_ context.Context, key string) (*configstore.Entry, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.entries != nil {
		if e, ok := m.entries[key]; ok {
			return e, nil
		}
	}
	return nil, configstore.ErrNotFound
}

func (m *mockConfigStore) Set(_ context.Context, key, value, _ string) error {
	m.setCalls++
	if m.setErr != nil {
		return m.setErr
	}
	if m.entries == nil {
		m.entries = make(map[string]*configstore.Entry)
	}
	m.entries[key] = &configstore.Entry{Key: key, Value: value}
	return nil
}

func (m *mockConfigStore) Delete(_ context.Context, key, _ string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if m.entries != nil {
		if _, ok := m.entries[key]; ok {
			delete(m.entries, key)
			return nil
		}
	}
	return configstore.ErrNotFound
}

func (m *mockConfigStore) List(_ context.Context) ([]configstore.Entry, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []configstore.Entry
	for _, e := range m.entries {
		result = append(result, *e)
	}
	return result, nil
}

func (m *mockConfigStore) Changelog(_ context.Context, _ int) ([]configstore.ChangelogEntry, error) {
	if m.changelogErr != nil {
		return nil, m.changelogErr
	}
	return m.changelog, nil
}

func (m *mockConfigStore) Mode() string {
	if m.mode == "" {
		return "database"
	}
	return m.mode
}

// Verify interface compliance.
var _ ConfigStore = (*mockConfigStore)(nil)

// --- Mock PersonaStore ---

type mockPersonaStore struct {
	listResult  []platform.PersonaDefinition
	listErr     error
	setErr      error
	deleteErr   error
	setCalls    []platform.PersonaDefinition
	deleteCalls []string
}

func (m *mockPersonaStore) List(_ context.Context) ([]platform.PersonaDefinition, error) {
	return m.listResult, m.listErr
}

func (*mockPersonaStore) Get(_ context.Context, _ string) (*platform.PersonaDefinition, error) {
	return nil, platform.ErrPersonaNotFound
}

func (m *mockPersonaStore) Set(_ context.Context, def platform.PersonaDefinition) error {
	m.setCalls = append(m.setCalls, def)
	return m.setErr
}

func (m *mockPersonaStore) Delete(_ context.Context, name string) error {
	m.deleteCalls = append(m.deleteCalls, name)
	return m.deleteErr
}

// Verify interface compliance.
var _ platform.PersonaStore = (*mockPersonaStore)(nil)

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
