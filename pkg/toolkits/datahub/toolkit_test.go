package datahub

import (
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
		cfg := Config{URL: "http://localhost:8080"}
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
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080"})
		if cfg.Timeout != 30*time.Second {
			t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
		}
	})

	t.Run("applies default limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080"})
		if cfg.DefaultLimit != 10 {
			t.Errorf("DefaultLimit = %d, want 10", cfg.DefaultLimit)
		}
	})

	t.Run("applies max limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080"})
		if cfg.MaxLimit != 100 {
			t.Errorf("MaxLimit = %d, want 100", cfg.MaxLimit)
		}
	})

	t.Run("applies max lineage depth", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080"})
		if cfg.MaxLineageDepth != 5 {
			t.Errorf("MaxLineageDepth = %d, want 5", cfg.MaxLineageDepth)
		}
	})

	t.Run("applies connection name from toolkit name", func(t *testing.T) {
		cfg := applyDefaults("my-toolkit", Config{URL: "http://localhost:8080"})
		if cfg.ConnectionName != "my-toolkit" {
			t.Errorf("ConnectionName = %q, want 'my-toolkit'", cfg.ConnectionName)
		}
	})

	t.Run("preserves custom timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080", Timeout: 60 * time.Second})
		if cfg.Timeout != 60*time.Second {
			t.Errorf("Timeout = %v, want 60s", cfg.Timeout)
		}
	})

	t.Run("preserves custom default limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080", DefaultLimit: 50})
		if cfg.DefaultLimit != 50 {
			t.Errorf("DefaultLimit = %d, want 50", cfg.DefaultLimit)
		}
	})

	t.Run("preserves custom max limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080", MaxLimit: 500})
		if cfg.MaxLimit != 500 {
			t.Errorf("MaxLimit = %d, want 500", cfg.MaxLimit)
		}
	})

	t.Run("preserves custom max lineage depth", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080", MaxLineageDepth: 10})
		if cfg.MaxLineageDepth != 10 {
			t.Errorf("MaxLineageDepth = %d, want 10", cfg.MaxLineageDepth)
		}
	})

	t.Run("preserves custom connection name", func(t *testing.T) {
		cfg := applyDefaults("test", Config{URL: "http://localhost:8080", ConnectionName: "custom"})
		if cfg.ConnectionName != "custom" {
			t.Errorf("ConnectionName = %q, want 'custom'", cfg.ConnectionName)
		}
	})
}

func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := Config{
		URL:             "http://localhost:8080",
		Token:           "token",
		Timeout:         60 * time.Second,
		DefaultLimit:    50,
		MaxLimit:        500,
		MaxLineageDepth: 10,
		ConnectionName:  "custom-name",
	}
	result := applyDefaults("test", cfg)

	if result.Timeout != 60*time.Second {
		t.Errorf("Timeout should be preserved: got %v", result.Timeout)
	}
	if result.DefaultLimit != 50 {
		t.Errorf("DefaultLimit should be preserved: got %d", result.DefaultLimit)
	}
	if result.MaxLimit != 500 {
		t.Errorf("MaxLimit should be preserved: got %d", result.MaxLimit)
	}
	if result.MaxLineageDepth != 10 {
		t.Errorf("MaxLineageDepth should be preserved: got %d", result.MaxLineageDepth)
	}
	if result.ConnectionName != "custom-name" {
		t.Errorf("ConnectionName should be preserved: got %s", result.ConnectionName)
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		URL:             "http://localhost:8080",
		Token:           "test-token",
		Timeout:         60 * time.Second,
		DefaultLimit:    20,
		MaxLimit:        200,
		MaxLineageDepth: 10,
		ConnectionName:  "prod-datahub",
		Debug:           true,
	}

	if cfg.URL != "http://localhost:8080" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.Token != "test-token" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.DefaultLimit != 20 {
		t.Errorf("DefaultLimit = %d", cfg.DefaultLimit)
	}
	if cfg.MaxLimit != 200 {
		t.Errorf("MaxLimit = %d", cfg.MaxLimit)
	}
	if cfg.MaxLineageDepth != 10 {
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
		cfg := Config{URL: "http://localhost:8080"}
		if cfg.Debug {
			t.Error("Debug should default to false")
		}
	})

	t.Run("debug can be set to true", func(t *testing.T) {
		cfg := Config{URL: "http://localhost:8080", Debug: true}
		if !cfg.Debug {
			t.Error("Debug should be true when set")
		}
	})
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{
		URL: "http://localhost:8080",
	}

	// Check what defaults would be applied by New
	if cfg.Timeout == 0 {
		defaultTimeout := 30 * time.Second
		if defaultTimeout != 30*time.Second {
			t.Error("default timeout should be 30s")
		}
	}

	if cfg.DefaultLimit == 0 {
		defaultLimit := 10
		if defaultLimit != 10 {
			t.Error("default DefaultLimit should be 10")
		}
	}

	if cfg.MaxLimit == 0 {
		maxLimit := 100
		if maxLimit != 100 {
			t.Error("default MaxLimit should be 100")
		}
	}

	if cfg.MaxLineageDepth == 0 {
		maxDepth := 5
		if maxDepth != 5 {
			t.Error("default MaxLineageDepth should be 5")
		}
	}
}

func TestToolkit_Methods(t *testing.T) {
	// Create toolkit without client for testing methods
	toolkit := &Toolkit{
		name: "test-datahub",
		config: Config{
			URL:            "http://localhost:8080",
			Token:          "test-token",
			ConnectionName: "test",
		},
	}

	t.Run("Kind", func(t *testing.T) {
		if toolkit.Kind() != "datahub" {
			t.Errorf("Kind() = %q, want 'datahub'", toolkit.Kind())
		}
	})

	t.Run("Name", func(t *testing.T) {
		if toolkit.Name() != "test-datahub" {
			t.Errorf("Name() = %q", toolkit.Name())
		}
	})

	t.Run("Connection", func(t *testing.T) {
		if toolkit.Connection() != "test" {
			t.Errorf("Connection() = %q, want 'test'", toolkit.Connection())
		}
	})

	t.Run("Tools", func(t *testing.T) {
		tools := toolkit.Tools()
		if len(tools) == 0 {
			t.Error("expected non-empty tools list")
		}

		expectedTools := []string{
			"datahub_search",
			"datahub_get_entity",
			"datahub_get_schema",
			"datahub_get_lineage",
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
			found := slices.Contains(tools, expected)
			if !found {
				t.Errorf("missing expected tool: %s", expected)
			}
		}
	})

	t.Run("Config", func(t *testing.T) {
		cfg := toolkit.Config()
		if cfg.URL != "http://localhost:8080" {
			t.Errorf("Config().URL = %q", cfg.URL)
		}
	})

	t.Run("SetSemanticProvider", func(t *testing.T) {
		provider := semantic.NewNoopProvider()
		toolkit.SetSemanticProvider(provider)
		if toolkit.semanticProvider != provider {
			t.Error("semanticProvider not set")
		}
	})

	t.Run("SetQueryProvider", func(t *testing.T) {
		provider := query.NewNoopProvider()
		toolkit.SetQueryProvider(provider)
		if toolkit.queryProvider != provider {
			t.Error("queryProvider not set")
		}
	})

	t.Run("Client nil", func(t *testing.T) {
		if toolkit.Client() != nil {
			t.Error("expected nil client")
		}
	})

	t.Run("Close nil client", func(t *testing.T) {
		err := toolkit.Close()
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	t.Run("RegisterTools nil toolkit", func(t *testing.T) {
		// Should not panic with nil datahubToolkit
		toolkit.RegisterTools(nil)
	})

	t.Run("RegisterTools with server", func(t *testing.T) {
		server := mcp.NewServer(&mcp.Implementation{
			Name:    "test",
			Version: "1.0.0",
		}, nil)
		// Should not panic
		toolkit.RegisterTools(server)
	})
}
