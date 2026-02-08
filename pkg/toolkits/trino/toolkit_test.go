package trino

import (
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	trinoTestPort8080       = 8080
	trinoTestPort8443       = 8443
	trinoTestPort443        = 443
	trinoTestPort9999       = 9999
	trinoTestDefaultLimit   = 500
	trinoTestMaxLimit       = 5000
	trinoTestTimeoutSec     = 60
	trinoTestHost           = "localhost"
	trinoTestConnectionName = "test"
	trinoTestDefLimit       = 1000
	trinoTestDefMaxLimit    = 10000
	trinoTestDefTimeoutSec  = 120
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
			Host: trinoTestHost,
		})
		if err == nil {
			t.Error("expected error for missing user")
		}
	})
}

func TestValidateConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := Config{Host: trinoTestHost, User: "testuser"}
		if err := validateConfig(cfg); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing host", func(t *testing.T) {
		cfg := Config{User: "testuser"}
		if err := validateConfig(cfg); err == nil {
			t.Error("expected error for missing host")
		}
	})

	t.Run("missing user", func(t *testing.T) {
		cfg := Config{Host: trinoTestHost}
		if err := validateConfig(cfg); err == nil {
			t.Error("expected error for missing user")
		}
	})
}

func TestApplyDefaults(t *testing.T) {
	t.Run("applies default port for non-SSL", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user"})
		if cfg.Port != trinoTestPort8080 {
			t.Errorf("Port = %d, want 8080", cfg.Port)
		}
	})

	t.Run("applies default port for SSL", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user", SSL: true})
		if cfg.Port != trinoTestPort443 {
			t.Errorf("Port = %d, want 443", cfg.Port)
		}
	})

	t.Run("preserves custom port", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user", Port: 9090})
		if cfg.Port != 9090 {
			t.Errorf("Port = %d, want 9090", cfg.Port)
		}
	})

	t.Run("applies default limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user"})
		if cfg.DefaultLimit != trinoTestDefLimit {
			t.Errorf("DefaultLimit = %d, want 1000", cfg.DefaultLimit)
		}
	})

	t.Run("applies max limit", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user"})
		if cfg.MaxLimit != trinoTestDefMaxLimit {
			t.Errorf("MaxLimit = %d, want 10000", cfg.MaxLimit)
		}
	})

	t.Run("applies timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user"})
		if cfg.Timeout != trinoTestDefTimeoutSec*time.Second {
			t.Errorf("Timeout = %v, want 120s", cfg.Timeout)
		}
	})

	t.Run("applies connection name from toolkit name", func(t *testing.T) {
		cfg := applyDefaults("my-toolkit", Config{Host: trinoTestHost, User: "user"})
		if cfg.ConnectionName != "my-toolkit" {
			t.Errorf("ConnectionName = %q, want 'my-toolkit'", cfg.ConnectionName)
		}
	})

	t.Run("preserves custom connection name", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Host: trinoTestHost, User: "user", ConnectionName: "custom"})
		if cfg.ConnectionName != "custom" {
			t.Errorf("ConnectionName = %q, want 'custom'", cfg.ConnectionName)
		}
	})
}

func TestDefaultPort(t *testing.T) {
	t.Run("SSL port", func(t *testing.T) {
		if port := defaultPort(true); port != trinoTestPort443 {
			t.Errorf("defaultPort(true) = %d, want 443", port)
		}
	})

	t.Run("non-SSL port", func(t *testing.T) {
		if port := defaultPort(false); port != trinoTestPort8080 {
			t.Errorf("defaultPort(false) = %d, want 8080", port)
		}
	})
}

func TestValidateConfig_BothMissing(t *testing.T) {
	cfg := Config{}
	err := validateConfig(cfg)
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := Config{
		Host:           trinoTestHost,
		User:           "user",
		Port:           trinoTestPort9999,
		DefaultLimit:   trinoTestDefaultLimit,
		MaxLimit:       trinoTestMaxLimit,
		Timeout:        trinoTestTimeoutSec * time.Second,
		ConnectionName: "custom-name",
	}
	result := applyDefaults("test", cfg)

	if result.Port != trinoTestPort9999 {
		t.Errorf("Port should be preserved: got %d", result.Port)
	}
	if result.DefaultLimit != trinoTestDefaultLimit {
		t.Errorf("DefaultLimit should be preserved: got %d", result.DefaultLimit)
	}
	if result.MaxLimit != trinoTestMaxLimit {
		t.Errorf("MaxLimit should be preserved: got %d", result.MaxLimit)
	}
	if result.Timeout != trinoTestTimeoutSec*time.Second {
		t.Errorf("Timeout should be preserved: got %v", result.Timeout)
	}
	if result.ConnectionName != "custom-name" {
		t.Errorf("ConnectionName should be preserved: got %s", result.ConnectionName)
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{
		Host: trinoTestHost,
		User: "testuser",
	}

	result := applyDefaults("test", cfg)

	if result.Port != trinoTestPort8080 {
		t.Errorf("non-SSL default port should be 8080, got %d", result.Port)
	}
	if result.DefaultLimit != trinoTestDefLimit {
		t.Errorf("DefaultLimit should default to 1000, got %d", result.DefaultLimit)
	}
	if result.MaxLimit != trinoTestDefMaxLimit {
		t.Errorf("MaxLimit should default to 10000, got %d", result.MaxLimit)
	}
	if result.Timeout != trinoTestDefTimeoutSec*time.Second {
		t.Errorf("Timeout should default to 120s, got %v", result.Timeout)
	}

	// Test SSL default port
	sslCfg := Config{
		Host: trinoTestHost,
		User: "testuser",
		SSL:  true,
	}
	sslResult := applyDefaults("test", sslCfg)
	if sslResult.Port != trinoTestPort443 {
		t.Errorf("SSL default port should be 443, got %d", sslResult.Port)
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Host:           "trino.example.com",
		Port:           trinoTestPort8443,
		User:           "admin",
		Password:       "secret",
		Catalog:        "hive",
		Schema:         "default",
		SSL:            true,
		SSLVerify:      true,
		Timeout:        trinoTestTimeoutSec * time.Second,
		DefaultLimit:   trinoTestDefaultLimit,
		MaxLimit:       trinoTestMaxLimit,
		ReadOnly:       true,
		ConnectionName: "prod-trino",
	}

	if cfg.Host != "trino.example.com" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != trinoTestPort8443 {
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
	if cfg.Timeout != trinoTestTimeoutSec*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.DefaultLimit != trinoTestDefaultLimit {
		t.Errorf("DefaultLimit = %d", cfg.DefaultLimit)
	}
	if cfg.MaxLimit != trinoTestMaxLimit {
		t.Errorf("MaxLimit = %d", cfg.MaxLimit)
	}
	if !cfg.ReadOnly {
		t.Error("ReadOnly = false")
	}
	if cfg.ConnectionName != "prod-trino" {
		t.Errorf("ConnectionName = %q", cfg.ConnectionName)
	}
}

func newTestTrinoToolkit() *Toolkit {
	return &Toolkit{
		name: "test-toolkit",
		config: Config{
			Host:           trinoTestHost,
			Port:           trinoTestPort8080,
			User:           "testuser",
			ConnectionName: trinoTestConnectionName,
		},
	}
}

func TestToolkit_KindAndName(t *testing.T) {
	tk := newTestTrinoToolkit()
	if tk.Kind() != "trino" {
		t.Errorf("Kind() = %q, want 'trino'", tk.Kind())
	}
	if tk.Name() != "test-toolkit" {
		t.Errorf("Name() = %q", tk.Name())
	}
	if tk.Connection() != "test" {
		t.Errorf("Connection() = %q, want 'test'", tk.Connection())
	}
}

func TestToolkit_Tools(t *testing.T) {
	tk := newTestTrinoToolkit()
	tools := tk.Tools()
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
		if !slices.Contains(tools, expected) {
			t.Errorf("missing expected tool: %s", expected)
		}
	}
}

func TestToolkit_ConfigAndProviders(t *testing.T) {
	tk := newTestTrinoToolkit()
	cfg := tk.Config()
	if cfg.Host != trinoTestHost {
		t.Errorf("Config().Host = %q", cfg.Host)
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
	tk := newTestTrinoToolkit()
	if tk.Client() != nil {
		t.Error("expected nil client")
	}
	if err := tk.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestToolkit_RegisterTools(t *testing.T) {
	tk := newTestTrinoToolkit()
	tk.RegisterTools(nil) // Should not panic

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tk.RegisterTools(server) // Should not panic
}
