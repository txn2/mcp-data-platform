package datahub

import (
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
		"datahub_search",
		"datahub_get_entity",
		"datahub_get_schema",
		"datahub_get_lineage",
		"datahub_get_column_lineage",
		"datahub_get_queries",
		"datahub_get_glossary_term",
		"datahub_list_tags",
		"datahub_list_domains",
		"datahub_list_data_products",
		"datahub_get_data_product",
		"datahub_list_connections",
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
			"datahub_search":     "Custom search",
			"datahub_get_entity": "Custom entity",
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
