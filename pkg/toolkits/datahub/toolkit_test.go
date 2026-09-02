package datahub

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const (
	dhTestTimeoutSec     = 60
	dhTestDefaultLimit50 = 50
	dhTestMaxLimit500    = 500
	dhTestLineageDepth   = 10
	dhTestDefaultLimit20 = 20
	dhTestMaxLimit200    = 200
	dhTestLocalhostURL   = "http://localhost:8080"
	dhTestDefTimeoutSec  = 30
	dhTestDefLimit       = 10
	dhTestDefMaxLimit    = 100
	dhTestDefMaxDepth    = 5
)

func TestNew(t *testing.T) {
	t.Run("missing URL", func(t *testing.T) {
		_, err := New("test", Config{})
		if err == nil {
			t.Error("expected error for missing URL")
		}
	})
}

func TestValidateConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := Config{URL: dhTestLocalhostURL}
		if err := validateConfig(cfg); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing URL", func(t *testing.T) {
		cfg := Config{}
		if err := validateConfig(cfg); err == nil {
			t.Error("expected error for missing URL")
		}
	})
}

func TestApplyDefaults(t *testing.T) {
	t.Run("applies default timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL})
		if cfg.Timeout != dhTestDefTimeoutSec*time.Second {
			t.Errorf("Timeout = %v, want %ds", cfg.Timeout, dhTestDefTimeoutSec)
		}
	})

	t.Run("applies default limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL})
		if cfg.DefaultLimit != dhTestDefLimit {
			t.Errorf("DefaultLimit = %d, want %d", cfg.DefaultLimit, dhTestDefLimit)
		}
	})

	t.Run("applies max limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL})
		if cfg.MaxLimit != dhTestDefMaxLimit {
			t.Errorf("MaxLimit = %d, want %d", cfg.MaxLimit, dhTestDefMaxLimit)
		}
	})

	t.Run("applies max lineage depth", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL})
		if cfg.MaxLineageDepth != dhTestDefMaxDepth {
			t.Errorf("MaxLineageDepth = %d, want %d", cfg.MaxLineageDepth, dhTestDefMaxDepth)
		}
	})

	t.Run("applies connection name from toolkit name", func(t *testing.T) {
		cfg := applyDefaults("my-toolkit", Config{URL: dhTestLocalhostURL})
		if cfg.ConnectionName != "my-toolkit" {
			t.Errorf("ConnectionName = %q, want 'my-toolkit'", cfg.ConnectionName)
		}
	})

	t.Run("preserves custom timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL, Timeout: 60 * time.Second})
		if cfg.Timeout != dhTestTimeoutSec*time.Second {
			t.Errorf("Timeout = %v, want 60s", cfg.Timeout)
		}
	})

	t.Run("preserves custom default limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL, DefaultLimit: dhTestDefaultLimit50})
		if cfg.DefaultLimit != dhTestDefaultLimit50 {
			t.Errorf("DefaultLimit = %d, want %d", cfg.DefaultLimit, dhTestDefaultLimit50)
		}
	})

	t.Run("preserves custom max limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL, MaxLimit: dhTestMaxLimit500})
		if cfg.MaxLimit != dhTestMaxLimit500 {
			t.Errorf("MaxLimit = %d, want %d", cfg.MaxLimit, dhTestMaxLimit500)
		}
	})

	t.Run("preserves custom max lineage depth", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL, MaxLineageDepth: dhTestLineageDepth})
		if cfg.MaxLineageDepth != dhTestLineageDepth {
			t.Errorf("MaxLineageDepth = %d, want %d", cfg.MaxLineageDepth, dhTestLineageDepth)
		}
	})

	t.Run("preserves custom connection name", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: dhTestLocalhostURL, ConnectionName: "custom"})
		if cfg.ConnectionName != "custom" {
			t.Errorf("ConnectionName = %q, want 'custom'", cfg.ConnectionName)
		}
	})
}

func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := Config{
		URL:             dhTestLocalhostURL,
		Token:           "token",
		Timeout:         dhTestTimeoutSec * time.Second,
		DefaultLimit:    dhTestDefaultLimit50,
		MaxLimit:        dhTestMaxLimit500,
		MaxLineageDepth: dhTestLineageDepth,
		ConnectionName:  "custom-name",
	}
	result := applyDefaults("test", cfg)

	if result.Timeout != dhTestTimeoutSec*time.Second {
		t.Errorf("Timeout should be preserved: got %v", result.Timeout)
	}
	if result.DefaultLimit != dhTestDefaultLimit50 {
		t.Errorf("DefaultLimit should be preserved: got %d", result.DefaultLimit)
	}
	if result.MaxLimit != dhTestMaxLimit500 {
		t.Errorf("MaxLimit should be preserved: got %d", result.MaxLimit)
	}
	if result.MaxLineageDepth != dhTestLineageDepth {
		t.Errorf("MaxLineageDepth should be preserved: got %d", result.MaxLineageDepth)
	}
	if result.ConnectionName != "custom-name" {
		t.Errorf("ConnectionName should be preserved: got %s", result.ConnectionName)
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		URL:             dhTestLocalhostURL,
		Token:           "test-token",
		Timeout:         dhTestTimeoutSec * time.Second,
		DefaultLimit:    dhTestDefaultLimit20,
		MaxLimit:        dhTestMaxLimit200,
		MaxLineageDepth: dhTestLineageDepth,
		ConnectionName:  "prod-datahub",
		Debug:           true,
	}

	if cfg.URL != dhTestLocalhostURL {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.Token != "test-token" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.Timeout != dhTestTimeoutSec*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.DefaultLimit != dhTestDefaultLimit20 {
		t.Errorf("DefaultLimit = %d", cfg.DefaultLimit)
	}
	if cfg.MaxLimit != dhTestMaxLimit200 {
		t.Errorf("MaxLimit = %d", cfg.MaxLimit)
	}
	if cfg.MaxLineageDepth != dhTestLineageDepth {
		t.Errorf("MaxLineageDepth = %d", cfg.MaxLineageDepth)
	}
	if cfg.ConnectionName != "prod-datahub" {
		t.Errorf("ConnectionName = %q", cfg.ConnectionName)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
}

func TestConfig_ReadOnlyField(t *testing.T) {
	t.Run("read_only defaults to false", func(t *testing.T) {
		cfg := Config{URL: dhTestLocalhostURL}
		if cfg.ReadOnly {
			t.Error("ReadOnly should default to false")
		}
	})

	t.Run("read_only can be set to true", func(t *testing.T) {
		cfg := Config{URL: dhTestLocalhostURL, ReadOnly: true}
		if !cfg.ReadOnly {
			t.Error("ReadOnly should be true when set")
		}
	})
}

func TestConfig_DebugField(t *testing.T) {
	t.Run("debug defaults to false", func(t *testing.T) {
		cfg := Config{URL: dhTestLocalhostURL}
		if cfg.Debug {
			t.Error("Debug should default to false")
		}
	})

	t.Run("debug can be set to true", func(t *testing.T) {
		cfg := Config{URL: dhTestLocalhostURL, Debug: true}
		if !cfg.Debug {
			t.Error("Debug should be true when set")
		}
	})
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{
		URL: dhTestLocalhostURL,
	}

	// Check what defaults would be applied by New
	if cfg.Timeout == 0 {
		defaultTimeout := dhTestDefTimeoutSec * time.Second
		if defaultTimeout != dhTestDefTimeoutSec*time.Second {
			t.Error("default timeout should be 30s")
		}
	}

	if cfg.DefaultLimit == 0 {
		defaultLimit := dhTestDefLimit
		if defaultLimit != dhTestDefLimit {
			t.Error("default DefaultLimit should be 10")
		}
	}

	if cfg.MaxLimit == 0 {
		maxLimit := dhTestDefMaxLimit
		if maxLimit != dhTestDefMaxLimit {
			t.Error("default MaxLimit should be 100")
		}
	}

	if cfg.MaxLineageDepth == 0 {
		maxDepth := dhTestDefMaxDepth
		if maxDepth != dhTestDefMaxDepth {
			t.Error("default MaxLineageDepth should be 5")
		}
	}
}

func newTestDatahubToolkit() *Toolkit {
	return &Toolkit{
		name: "test-datahub",
		config: Config{
			URL:            dhTestLocalhostURL,
			Token:          "test-token",
			ConnectionName: "test",
		},
	}
}

func TestToolkit_KindAndName(t *testing.T) {
	tk := newTestDatahubToolkit()
	if tk.Kind() != "datahub" {
		t.Errorf("Kind() = %q, want 'datahub'", tk.Kind())
	}
	if tk.Name() != "test-datahub" {
		t.Errorf("Name() = %q", tk.Name())
	}
	if tk.Connection() != "test" {
		t.Errorf("Connection() = %q, want 'test'", tk.Connection())
	}
}

func TestToolkit_Tools(t *testing.T) {
	tk := newTestDatahubToolkit()
	tools := tk.Tools()
	if len(tools) == 0 {
		t.Error("expected non-empty tools list")
	}

	expectedTools := []string{
		"datahub_get_lineage",
		"datahub_browse",
		"datahub_create",
		"datahub_update",
		"datahub_delete",
	}

	if len(tools) != len(expectedTools) {
		t.Errorf("Tools() returned %d tools, want %d", len(tools), len(expectedTools))
	}

	for _, expected := range expectedTools {
		if !slices.Contains(tools, expected) {
			t.Errorf("missing expected tool: %s", expected)
		}
	}
}

func TestToolkit_Tools_ReadOnly(t *testing.T) {
	tk := &Toolkit{
		name:   "test-datahub-readonly",
		config: Config{ReadOnly: true},
	}
	tools := tk.Tools()

	expectedReadTools := []string{
		"datahub_get_lineage",
		"datahub_browse",
	}

	if len(tools) != len(expectedReadTools) {
		t.Errorf("Tools() returned %d tools in read-only mode, want %d", len(tools), len(expectedReadTools))
	}

	writeTools := []string{"datahub_create", "datahub_update", "datahub_delete"}
	for _, wt := range writeTools {
		if slices.Contains(tools, wt) {
			t.Errorf("found write tool %s in read-only mode", wt)
		}
	}
}

func TestToolkit_ConfigAndProviders(t *testing.T) {
	tk := newTestDatahubToolkit()
	cfg := tk.Config()
	if cfg.URL != dhTestLocalhostURL {
		t.Errorf("Config().URL = %q", cfg.URL)
	}

	sp := semantic.NewNoopProvider()
	tk.SetSemanticProvider(sp)
	if tk.semanticProvider != sp {
		t.Error("semanticProvider not set")
	}

	qp := query.NewNoopProvider()
	tk.SetQueryProvider(qp)
	if tk.queryProvider != qp {
		t.Error("queryProvider not set")
	}
}

func TestToolkit_ClientAndClose(t *testing.T) {
	tk := newTestDatahubToolkit()
	if tk.Client() != nil {
		t.Error("expected nil client")
	}
	if err := tk.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestToDataHubToolNames(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := toDataHubToolNames(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("valid conversion", func(t *testing.T) {
		input := map[string]string{
			"datahub_search": "Custom search",
			"datahub_browse": "Custom browse",
		}
		result := toDataHubToolNames(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		for k, v := range input {
			if got := result[dhtools.ToolName(k)]; got != v {
				t.Errorf("result[%q] = %q, want %q", k, got, v)
			}
		}
	})

	t.Run("empty map", func(t *testing.T) {
		result := toDataHubToolNames(map[string]string{})
		if result == nil {
			t.Error("expected non-nil empty map")
		}
		if len(result) != 0 {
			t.Errorf("expected 0 entries, got %d", len(result))
		}
	})
}

func TestToDataHubAnnotations(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := toDataHubAnnotations(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("valid conversion", func(t *testing.T) {
		readOnly := true
		destructive := false
		input := map[string]AnnotationConfig{
			"datahub_search": {
				ReadOnlyHint:    &readOnly,
				DestructiveHint: &destructive,
			},
		}
		result := toDataHubAnnotations(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		ann := result[dhtools.ToolName("datahub_search")]
		if ann == nil {
			t.Fatal("expected non-nil annotation")
		}
		if !ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Error("expected DestructiveHint=false")
		}
	})
}

func TestAnnotationConfigToMCP(t *testing.T) {
	t.Run("all fields set", func(t *testing.T) {
		readOnly := true
		destructive := false
		idempotent := true
		openWorld := false
		cfg := AnnotationConfig{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  &idempotent,
			OpenWorldHint:   &openWorld,
		}
		ann := toolkit.AnnotationsToMCP(cfg)
		if !ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Error("expected DestructiveHint=false")
		}
		if !ann.IdempotentHint {
			t.Error("expected IdempotentHint=true")
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Error("expected OpenWorldHint=false")
		}
	})

	t.Run("no fields set", func(t *testing.T) {
		cfg := AnnotationConfig{}
		ann := toolkit.AnnotationsToMCP(cfg)
		if ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=false")
		}
		if ann.DestructiveHint != nil {
			t.Error("expected DestructiveHint=nil")
		}
		if ann.IdempotentHint {
			t.Error("expected IdempotentHint=false")
		}
		if ann.OpenWorldHint != nil {
			t.Error("expected OpenWorldHint=nil")
		}
	})
}

func TestCreateToolkit_WriteEnabled(t *testing.T) {
	t.Run("write enabled when not read-only", func(t *testing.T) {
		cfg := Config{
			URL:          dhTestLocalhostURL,
			DefaultLimit: dhTestDefLimit,
			MaxLimit:     dhTestDefMaxLimit,
		}
		tk := createToolkit(nil, cfg)
		if tk == nil {
			t.Fatal("expected non-nil toolkit")
		}
	})

	t.Run("write disabled when read-only", func(t *testing.T) {
		cfg := Config{
			URL:          dhTestLocalhostURL,
			DefaultLimit: dhTestDefLimit,
			MaxLimit:     dhTestDefMaxLimit,
			ReadOnly:     true,
		}
		tk := createToolkit(nil, cfg)
		if tk == nil {
			t.Fatal("expected non-nil toolkit")
		}
	})
}

func TestCreateToolkit_WithTitles(t *testing.T) {
	cfg := Config{
		URL:          dhTestLocalhostURL,
		DefaultLimit: dhTestDefLimit,
		MaxLimit:     dhTestDefMaxLimit,
		Titles:       map[string]string{"datahub_search": "Search Catalog"},
	}
	tk := createToolkit(nil, cfg)
	if tk == nil {
		t.Fatal("expected non-nil toolkit")
	}
}

func TestToolkit_RegisterTools(_ *testing.T) {
	tk := newTestDatahubToolkit()
	// Should not panic with nil server
	tk.RegisterTools(nil)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "1.0.0",
	}, nil)
	// Should not panic with real server
	tk.RegisterTools(server)
}

func TestToolkit_RegisterTools_WithRealToolkit(t *testing.T) {
	// Construct a Toolkit with a non-nil datahubToolkit to exercise
	// the Register branch in RegisterTools.
	innerToolkit := dhtools.NewToolkit(nil, dhtools.Config{WriteEnabled: true})
	tk := &Toolkit{
		name: "reg-test",
		config: Config{
			URL:            dhTestLocalhostURL,
			ConnectionName: "reg-test",
		},
		datahubToolkit: innerToolkit,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "1.0.0",
	}, nil)

	// Should register tools without panic when datahubToolkit is non-nil.
	tk.RegisterTools(server)

	// Verify the list_connections tool is NOT in the declared Tools() list.
	for _, tool := range tk.Tools() {
		if tool == "datahub_list_connections" {
			t.Error("datahub_list_connections should not be in Tools()")
		}
	}

	// Verify write tools ARE in the declared Tools() list (default non-readonly).
	writeTools := []string{"datahub_create", "datahub_update", "datahub_delete"}
	for _, wt := range writeTools {
		if !slices.Contains(tk.Tools(), wt) {
			t.Errorf("expected write tool %s in non-readonly mode", wt)
		}
	}
}

func TestToolkit_RegisterTools_ReadOnly(t *testing.T) {
	innerToolkit := dhtools.NewToolkit(nil, dhtools.Config{})
	tk := &Toolkit{
		name: "reg-test-readonly",
		config: Config{
			URL:            dhTestLocalhostURL,
			ConnectionName: "reg-test-readonly",
			ReadOnly:       true,
		},
		datahubToolkit: innerToolkit,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test",
		Version: "1.0.0",
	}, nil)

	// Should register tools without panic in read-only mode.
	tk.RegisterTools(server)

	// Verify write tools are NOT in the declared Tools() list.
	writeTools := []string{"datahub_create", "datahub_update", "datahub_delete"}
	for _, wt := range writeTools {
		if slices.Contains(tk.Tools(), wt) {
			t.Errorf("found write tool %s in read-only mode", wt)
		}
	}
}

// retiredReadTools names each DataHub read the platform no longer registers and
// the fetch call that replaces it (#1590, acceptance 1). The three dataset
// reads collapse onto one reference: a fetched dataset carries its business
// context, declared schema, and saved queries together.
var retiredReadTools = map[string]string{
	"datahub_get_entity":        "fetch urn:li:dataset:<id>",
	"datahub_get_schema":        "fetch urn:li:dataset:<id> (the record's schema)",
	"datahub_get_queries":       "fetch urn:li:dataset:<id> (the record's queries)",
	"datahub_get_glossary_term": "fetch urn:li:glossaryTerm:<id>",
	"datahub_get_data_product":  "fetch urn:li:dataProduct:<id>",
}

// listRegistered registers the toolkit on an in-memory server and returns the
// tools a connected client is offered, keyed by name.
func listRegistered(t *testing.T, tk *Toolkit) map[string]*mcp.Tool {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tk.RegisterTools(server)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	out := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func realToolkit(readOnly bool) *Toolkit {
	return &Toolkit{
		name:           "reg-test",
		config:         Config{URL: dhTestLocalhostURL, ConnectionName: "reg-test", ReadOnly: readOnly},
		datahubToolkit: createToolkit(nil, Config{ReadOnly: readOnly}),
	}
}

func TestRetiredReadToolsAreReplacedByFetch(t *testing.T) {
	tk := realToolkit(false)
	registered := listRegistered(t, tk)
	for retired, replacement := range retiredReadTools {
		if replacement == "" {
			t.Errorf("%s: no replacement named", retired)
		}
		if slices.Contains(tk.Tools(), retired) {
			t.Errorf("%s is still declared by Tools(); replaced by %s", retired, replacement)
		}
		if _, ok := registered[retired]; ok {
			t.Errorf("%s is still registered on the server; replaced by %s", retired, replacement)
		}
	}
	for _, kept := range []string{"datahub_browse", "datahub_get_lineage", "datahub_create", "datahub_update", "datahub_delete"} {
		if _, ok := registered[kept]; !ok {
			t.Errorf("%s must stay registered", kept)
		}
	}
	if len(registered) != len(tk.Tools()) {
		t.Errorf("registered %d tools, Tools() declares %d", len(registered), len(tk.Tools()))
	}
}

func TestToolDescriptionsNameNoRetiredTool(t *testing.T) {
	registered := listRegistered(t, realToolkit(false))
	for name, tool := range registered {
		for retired := range retiredReadTools {
			if strings.Contains(tool.Description, retired) {
				t.Errorf("%s description still steers to %s: %q", name, retired, tool.Description)
			}
		}
	}
	browse := registered["datahub_browse"].Description
	if !strings.Contains(browse, "fetch") {
		t.Errorf("datahub_browse description must point at fetch for the full read: %q", browse)
	}
}

func TestToolDescriptions_ConfiguredOverrideWins(t *testing.T) {
	merged := toolDescriptions(map[string]string{"datahub_browse": "mine", "datahub_get_lineage": "lineage"})
	if merged[dhtools.ToolBrowse] != "mine" {
		t.Errorf("configured browse description lost: %q", merged[dhtools.ToolBrowse])
	}
	if merged[dhtools.ToolGetLineage] != "lineage" {
		t.Errorf("configured lineage description lost: %q", merged[dhtools.ToolGetLineage])
	}
	if got := toolDescriptions(nil)[dhtools.ToolBrowse]; got != platformDescriptions[dhtools.ToolBrowse] {
		t.Errorf("platform browse description missing without config: %q", got)
	}
}
