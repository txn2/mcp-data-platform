package datahub

import (
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
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

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		URL:             "http://localhost:8080",
		Token:           "test-token",
		Timeout:         60 * time.Second,
		DefaultLimit:    20,
		MaxLimit:        200,
		MaxLineageDepth: 10,
		ConnectionName:  "prod-datahub",
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
			found := false
			for _, tool := range tools {
				if tool == expected {
					found = true
					break
				}
			}
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

	t.Run("SetMiddleware", func(t *testing.T) {
		chain := middleware.NewChain()
		toolkit.SetMiddleware(chain)
		if toolkit.middlewareChain != chain {
			t.Error("middlewareChain not set")
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
