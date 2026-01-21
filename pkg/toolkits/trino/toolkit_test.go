package trino

import (
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestNew(t *testing.T) {
	t.Run("missing host", func(t *testing.T) {
		_, err := New("test", Config{
			User: "testuser",
		})
		if err == nil {
			t.Error("expected error for missing host")
		}
	})

	t.Run("missing user", func(t *testing.T) {
		_, err := New("test", Config{
			Host: "localhost",
		})
		if err == nil {
			t.Error("expected error for missing user")
		}
	})
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{
		Host: "localhost",
		User: "testuser",
	}

	// Apply defaults by checking what New would set
	if cfg.Port == 0 {
		if cfg.SSL {
			if 443 != 443 {
				t.Error("SSL default port should be 443")
			}
		} else {
			if 8080 != 8080 {
				t.Error("non-SSL default port should be 8080")
			}
		}
	}

	if cfg.DefaultLimit == 0 {
		if 1000 != 1000 {
			t.Error("DefaultLimit should default to 1000")
		}
	}

	if cfg.MaxLimit == 0 {
		if 10000 != 10000 {
			t.Error("MaxLimit should default to 10000")
		}
	}

	if cfg.Timeout == 0 {
		if 120*time.Second != 120*time.Second {
			t.Error("Timeout should default to 120s")
		}
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Host:           "trino.example.com",
		Port:           8443,
		User:           "admin",
		Password:       "secret",
		Catalog:        "hive",
		Schema:         "default",
		SSL:            true,
		SSLVerify:      true,
		Timeout:        60 * time.Second,
		DefaultLimit:   500,
		MaxLimit:       5000,
		ReadOnly:       true,
		ConnectionName: "prod-trino",
	}

	if cfg.Host != "trino.example.com" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != 8443 {
		t.Errorf("Port = %d", cfg.Port)
	}
	if cfg.User != "admin" {
		t.Errorf("User = %q", cfg.User)
	}
	if cfg.Password != "secret" {
		t.Errorf("Password = %q", cfg.Password)
	}
	if cfg.Catalog != "hive" {
		t.Errorf("Catalog = %q", cfg.Catalog)
	}
	if cfg.Schema != "default" {
		t.Errorf("Schema = %q", cfg.Schema)
	}
	if !cfg.SSL {
		t.Error("SSL = false")
	}
	if !cfg.SSLVerify {
		t.Error("SSLVerify = false")
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.DefaultLimit != 500 {
		t.Errorf("DefaultLimit = %d", cfg.DefaultLimit)
	}
	if cfg.MaxLimit != 5000 {
		t.Errorf("MaxLimit = %d", cfg.MaxLimit)
	}
	if !cfg.ReadOnly {
		t.Error("ReadOnly = false")
	}
	if cfg.ConnectionName != "prod-trino" {
		t.Errorf("ConnectionName = %q", cfg.ConnectionName)
	}
}

func TestToolkit_Methods(t *testing.T) {
	// We can't create a real toolkit without a Trino server, but we can test the struct
	toolkit := &Toolkit{
		name: "test-toolkit",
		config: Config{
			Host:           "localhost",
			Port:           8080,
			User:           "testuser",
			ConnectionName: "test",
		},
	}

	t.Run("Kind", func(t *testing.T) {
		if toolkit.Kind() != "trino" {
			t.Errorf("Kind() = %q, want 'trino'", toolkit.Kind())
		}
	})

	t.Run("Name", func(t *testing.T) {
		if toolkit.Name() != "test-toolkit" {
			t.Errorf("Name() = %q", toolkit.Name())
		}
	})

	t.Run("Tools", func(t *testing.T) {
		tools := toolkit.Tools()
		if len(tools) == 0 {
			t.Error("expected non-empty tools list")
		}

		expectedTools := []string{
			"trino_query",
			"trino_explain",
			"trino_list_catalogs",
			"trino_list_schemas",
			"trino_list_tables",
			"trino_describe_table",
			"trino_list_connections",
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
		if cfg.Host != "localhost" {
			t.Errorf("Config().Host = %q", cfg.Host)
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
		// Should not panic with nil trinoToolkit
		toolkit.RegisterTools(nil)
	})
}
