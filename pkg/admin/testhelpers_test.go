package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
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
	// lastRegistered is the persona most recently registered, so a test can
	// assert on what a revert or an update actually put into force rather than
	// only on how many times Register was called.
	lastRegistered *persona.Persona
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
	m.lastRegistered = p
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
	// lastQueryFilter records the filter passed to the most recent
	// Query call so handler tests can assert query-param extraction.
	lastQueryFilter audit.QueryFilter
}

func (m *mockAuditQuerier) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	m.lastQueryFilter = filter
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

// recordingAuditQuerier captures the most recent filter passed to Query so
// tests can assert on the parameters the handler builds.
type recordingAuditQuerier struct {
	lastQueryFilter    audit.QueryFilter
	lastDistinctColumn string
	distinctResults    map[string][]string
}

func (r *recordingAuditQuerier) Query(_ context.Context, f audit.QueryFilter) ([]audit.Event, error) {
	r.lastQueryFilter = f
	return []audit.Event{}, nil
}

func (*recordingAuditQuerier) Count(_ context.Context, _ audit.QueryFilter) (int, error) {
	return 0, nil
}

func (r *recordingAuditQuerier) Distinct(_ context.Context, column string, _, _ *time.Time) ([]string, error) {
	r.lastDistinctColumn = column
	if r.distinctResults != nil {
		if v, ok := r.distinctResults[column]; ok {
			return v, nil
		}
	}
	return []string{}, nil
}

func (*recordingAuditQuerier) DistinctPairs(_ context.Context, _, _ string, _, _ *time.Time) (map[string]string, error) {
	return map[string]string{}, nil
}

var _ AuditQuerier = (*recordingAuditQuerier)(nil)

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
	// mu guards entries, changelog, and setCalls so the mock can be
	// safely shared across goroutines in concurrency tests
	// (e.g. the visibility-lock test for #343 bug 1). Single-goroutine
	// tests pay no measurable cost — Mutex.Lock on uncontended use is
	// a single CAS.
	mu              sync.Mutex
	mode            string
	entries         map[string]*configstore.Entry
	changelog       []configstore.ChangelogEntry
	changelogTotal  int
	changelogLimit  int
	changelogOffset int
	setErr          error
	setCalls        int
	deleteErr       error
	listErr         error
	getErr          error
	changelogErr    error
}

func (m *mockConfigStore) Get(_ context.Context, key string) (*configstore.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []configstore.Entry
	for _, e := range m.entries {
		result = append(result, *e)
	}
	return result, nil
}

func (m *mockConfigStore) Changelog(_ context.Context, limit, offset int) ([]configstore.ChangelogEntry, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changelogLimit = limit
	m.changelogOffset = offset
	if m.changelogErr != nil {
		return nil, 0, m.changelogErr
	}
	total := m.changelogTotal
	if total == 0 {
		total = len(m.changelog)
	}
	return m.changelog, total, nil
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
	listResult  []personastore.Definition
	listErr     error
	setErr      error
	deleteErr   error
	setCalls    []personastore.Definition
	deleteCalls []string
}

func (m *mockPersonaStore) List(_ context.Context) ([]personastore.Definition, error) {
	return m.listResult, m.listErr
}

func (*mockPersonaStore) Get(_ context.Context, _ string) (*personastore.Definition, error) {
	return nil, personastore.ErrNotFound
}

func (m *mockPersonaStore) Set(_ context.Context, def personastore.Definition) error {
	m.setCalls = append(m.setCalls, def)
	return m.setErr
}

func (m *mockPersonaStore) Delete(_ context.Context, name string) error {
	m.deleteCalls = append(m.deleteCalls, name)
	return m.deleteErr
}

// Verify interface compliance.
var _ personastore.Store = (*mockPersonaStore)(nil)

// --- Test helpers ---

func testConfig() *platform.Config {
	cfg := &platform.Config{
		Server: platform.ServerConfig{
			Name:      "test-platform",
			Version:   "1.0.0",
			Transport: "http",
		},
		Admin: platform.AdminConfig{
			Enabled: new(true),
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

// doJSON drives a request through the assembled admin handler and returns the
// recorded response. Shared by the route tests that stayed in this package;
// the catalog seam carries its own copy against its own mux.
func doJSON(t *testing.T, h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rc io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rc = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rc)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// mockAuditMetricsQuerier is the double for the aggregate audit rollups. The
// audit routes moved to internal/admin/auditapi, which carries its own copy;
// this one serves the tools-detail surface that stayed behind.
type mockAuditMetricsQuerier struct {
	timeseriesResult  []audit.TimeseriesBucket
	timeseriesErr     error
	breakdownResult   []audit.BreakdownEntry
	breakdownErr      error
	overviewResult    *audit.Overview
	overviewErr       error
	performanceResult *audit.PerformanceStats
	performanceErr    error
	enrichmentResult  *audit.EnrichmentStats
	enrichmentErr     error
	discoveryResult   *audit.DiscoveryStats
	discoveryErr      error
}

func (m *mockAuditMetricsQuerier) Timeseries(_ context.Context, _ audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error) {
	return m.timeseriesResult, m.timeseriesErr
}

func (m *mockAuditMetricsQuerier) Breakdown(_ context.Context, _ audit.BreakdownFilter) ([]audit.BreakdownEntry, error) {
	return m.breakdownResult, m.breakdownErr
}

func (m *mockAuditMetricsQuerier) Overview(_ context.Context, _ audit.MetricsFilter) (*audit.Overview, error) {
	return m.overviewResult, m.overviewErr
}

func (m *mockAuditMetricsQuerier) Performance(_ context.Context, _ audit.MetricsFilter) (*audit.PerformanceStats, error) {
	return m.performanceResult, m.performanceErr
}

func (m *mockAuditMetricsQuerier) Enrichment(_ context.Context, _ audit.MetricsFilter) (*audit.EnrichmentStats, error) {
	return m.enrichmentResult, m.enrichmentErr
}

func (m *mockAuditMetricsQuerier) Discovery(_ context.Context, _ audit.MetricsFilter) (*audit.DiscoveryStats, error) {
	return m.discoveryResult, m.discoveryErr
}

// Verify interface compliance.
var _ AuditMetricsQuerier = (*mockAuditMetricsQuerier)(nil)
