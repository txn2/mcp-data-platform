package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

func TestNew(t *testing.T) {
	t.Run("requires config", func(t *testing.T) {
		_, err := New()
		if err == nil {
			t.Error("New() expected error without config")
		}
	})

	t.Run("minimal config with noop providers", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{
				Name: "test-platform",
			},
			Semantic: SemanticConfig{
				Provider: "noop",
			},
			Query: QueryConfig{
				Provider: "noop",
			},
			Storage: StorageConfig{
				Provider: "noop",
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if p == nil {
			t.Fatal("New() returned nil platform")
		}

		// Verify basic setup
		if p.Config() != cfg {
			t.Error("Config() did not return expected config")
		}
		if p.MCPServer() == nil {
			t.Error("MCPServer() is nil")
		}
	})

	t.Run("with injected providers", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: "test"},
		}
		semProv := semantic.NewNoopProvider()
		queryProv := query.NewNoopProvider()
		storageProv := storage.NewNoopProvider()

		p, err := New(
			WithConfig(cfg),
			WithSemanticProvider(semProv),
			WithQueryProvider(queryProv),
			WithStorageProvider(storageProv),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.SemanticProvider() != semProv {
			t.Error("SemanticProvider() did not return injected provider")
		}
		if p.QueryProvider() != queryProv {
			t.Error("QueryProvider() did not return injected provider")
		}
		if p.StorageProvider() != storageProv {
			t.Error("StorageProvider() did not return injected provider")
		}
	})

	t.Run("with injected registries", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}
		personaReg := persona.NewRegistry()
		toolkitReg := registry.NewRegistry()

		p, err := New(
			WithConfig(cfg),
			WithPersonaRegistry(personaReg),
			WithToolkitRegistry(toolkitReg),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.PersonaRegistry() != personaReg {
			t.Error("PersonaRegistry() did not return injected registry")
		}
		if p.ToolkitRegistry() != toolkitReg {
			t.Error("ToolkitRegistry() did not return injected registry")
		}
	})

	t.Run("with injected auth components", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}
		auth := &middleware.NoopAuthenticator{}
		authz := &middleware.NoopAuthorizer{}
		logger := &middleware.NoopAuditLogger{}

		p, err := New(
			WithConfig(cfg),
			WithAuthenticator(auth),
			WithAuthorizer(authz),
			WithAuditLogger(logger),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.MCPServer() == nil {
			t.Error("MCPServer() is nil")
		}
	})

	t.Run("with injected rule engine", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}
		engine := tuning.NewRuleEngine(&tuning.Rules{WarnOnDeprecated: true})

		p, err := New(
			WithConfig(cfg),
			WithRuleEngine(engine),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.RuleEngine() != engine {
			t.Error("RuleEngine() did not return injected engine")
		}
	})

	t.Run("unknown semantic provider", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: "test"},
			Semantic: SemanticConfig{
				Provider: "unknown",
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for unknown semantic provider")
		}
	})

	t.Run("unknown query provider", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query: QueryConfig{
				Provider: "unknown",
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for unknown query provider")
		}
	})

	t.Run("unknown storage provider", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage: StorageConfig{
				Provider: "unknown",
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for unknown storage provider")
		}
	})
}

func TestPlatformStartStop(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()

	// Start
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop
	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestPlatformClose(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := p.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLoadPersonas(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"analyst": {
					DisplayName: "Data Analyst",
					Roles:       []string{"analyst"},
					Tools: ToolRulesDef{
						Allow: []string{"trino_*"},
						Deny:  []string{"*_delete"},
					},
					Prompts: PromptsDef{
						SystemPrefix: "You are a data analyst.",
					},
					Hints: map[string]string{"key": "value"},
				},
				"admin": {
					DisplayName: "Administrator",
					Roles:       []string{"admin"},
					Tools: ToolRulesDef{
						Allow: []string{"*"},
					},
				},
			},
			DefaultPersona: "analyst",
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Check that personas were loaded
	pr := p.PersonaRegistry()

	analyst, ok := pr.Get("analyst")
	if !ok {
		t.Fatal("Get(analyst) returned false")
	}
	if analyst.DisplayName != "Data Analyst" {
		t.Errorf("analyst.DisplayName = %q", analyst.DisplayName)
	}

	admin, ok := pr.Get("admin")
	if !ok {
		t.Fatal("Get(admin) returned false")
	}
	if admin.DisplayName != "Administrator" {
		t.Errorf("admin.DisplayName = %q", admin.DisplayName)
	}
}

func TestMCPMiddlewareWithEnrichment(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Injection: InjectionConfig{
			TrinoSemanticEnrichment: true,
			DataHubQueryEnrichment:  true,
		},
		Audit: AuditConfig{
			Enabled: true,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Verify MCP server was created with middleware configured
	if p.MCPServer() == nil {
		t.Error("MCPServer() is nil")
	}
}

func TestCfgHelpers(t *testing.T) {
	cfg := map[string]any{
		"string_key":      "value",
		"int_key":         42,
		"float_key":       3.14,
		"bool_key":        true,
		"duration_string": "30s",
		"duration_int":    60,
		"duration_float":  90.0,
	}

	t.Run("cfgString", func(t *testing.T) {
		if v := cfgString(cfg, "string_key"); v != "value" {
			t.Errorf("cfgString(string_key) = %q", v)
		}
		if v := cfgString(cfg, "missing"); v != "" {
			t.Errorf("cfgString(missing) = %q", v)
		}
		if v := cfgString(cfg, "int_key"); v != "" {
			t.Errorf("cfgString(int_key) = %q (should be empty)", v)
		}
	})

	t.Run("cfgInt", func(t *testing.T) {
		if v := cfgInt(cfg, "int_key", 0); v != 42 {
			t.Errorf("cfgInt(int_key) = %d", v)
		}
		if v := cfgInt(cfg, "float_key", 0); v != 3 {
			t.Errorf("cfgInt(float_key) = %d", v)
		}
		if v := cfgInt(cfg, "missing", 100); v != 100 {
			t.Errorf("cfgInt(missing) = %d", v)
		}
	})

	t.Run("cfgBool", func(t *testing.T) {
		if v := cfgBool(cfg, "bool_key"); !v {
			t.Error("cfgBool(bool_key) = false")
		}
		if v := cfgBool(cfg, "missing"); v {
			t.Error("cfgBool(missing) = true")
		}
	})

	t.Run("cfgBoolDefault", func(t *testing.T) {
		if v := cfgBoolDefault(cfg, "bool_key", false); !v {
			t.Error("cfgBoolDefault(bool_key, false) = false")
		}
		if v := cfgBoolDefault(cfg, "missing", true); !v {
			t.Error("cfgBoolDefault(missing, true) = false")
		}
	})

	t.Run("cfgDuration", func(t *testing.T) {
		if v := cfgDuration(cfg, "duration_string", 0); v != 30*time.Second {
			t.Errorf("cfgDuration(duration_string) = %v", v)
		}
		if v := cfgDuration(cfg, "duration_int", 0); v != 60*time.Second {
			t.Errorf("cfgDuration(duration_int) = %v", v)
		}
		if v := cfgDuration(cfg, "duration_float", 0); v != 90*time.Second {
			t.Errorf("cfgDuration(duration_float) = %v", v)
		}
		if v := cfgDuration(cfg, "missing", 10*time.Second); v != 10*time.Second {
			t.Errorf("cfgDuration(missing) = %v", v)
		}
	})
}

func TestGetInstanceConfig(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Name: "test"},
		Toolkits: map[string]any{
			"trino": map[string]any{
				"default": "primary",
				"instances": map[string]any{
					"primary": map[string]any{
						"host": "localhost",
						"port": 8080,
					},
					"secondary": map[string]any{
						"host": "other.host",
						"port": 8081,
					},
				},
			},
		},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Run("get named instance", func(t *testing.T) {
		instanceCfg := p.getInstanceConfig("trino", "primary")
		if instanceCfg == nil {
			t.Fatal("getInstanceConfig(trino, primary) = nil")
		}
		if host := cfgString(instanceCfg, "host"); host != "localhost" {
			t.Errorf("host = %q", host)
		}
	})

	t.Run("get default instance", func(t *testing.T) {
		instanceCfg := p.getInstanceConfig("trino", "")
		if instanceCfg == nil {
			t.Fatal("getInstanceConfig(trino, '') = nil")
		}
		if host := cfgString(instanceCfg, "host"); host != "localhost" {
			t.Errorf("host = %q (should be primary/localhost)", host)
		}
	})

	t.Run("unknown toolkit kind", func(t *testing.T) {
		instanceCfg := p.getInstanceConfig("unknown", "any")
		if instanceCfg != nil {
			t.Error("getInstanceConfig(unknown, any) should be nil")
		}
	})

	t.Run("unknown instance", func(t *testing.T) {
		instanceCfg := p.getInstanceConfig("trino", "nonexistent")
		if instanceCfg != nil {
			t.Error("getInstanceConfig(trino, nonexistent) should be nil")
		}
	})
}

func TestResolveDefaultInstance(t *testing.T) {
	t.Run("with default key", func(t *testing.T) {
		kindCfg := map[string]any{"default": "primary"}
		instances := map[string]any{
			"primary":   map[string]any{},
			"secondary": map[string]any{},
		}
		result := resolveDefaultInstance(kindCfg, instances)
		if result != "primary" {
			t.Errorf("resolveDefaultInstance = %q", result)
		}
	})

	t.Run("without default key uses first", func(t *testing.T) {
		kindCfg := map[string]any{}
		instances := map[string]any{
			"only": map[string]any{},
		}
		result := resolveDefaultInstance(kindCfg, instances)
		if result != "only" {
			t.Errorf("resolveDefaultInstance = %q", result)
		}
	})

	t.Run("empty instances", func(t *testing.T) {
		kindCfg := map[string]any{}
		instances := map[string]any{}
		result := resolveDefaultInstance(kindCfg, instances)
		if result != "" {
			t.Errorf("resolveDefaultInstance = %q", result)
		}
	})
}

func TestCloseResource(t *testing.T) {
	t.Run("nil closer", func(t *testing.T) {
		var errs []error
		closeResource(&errs, nil)
		if len(errs) != 0 {
			t.Errorf("errs = %v", errs)
		}
	})

	t.Run("successful close", func(t *testing.T) {
		var errs []error
		closer := &testCloser{closeErr: nil}
		closeResource(&errs, closer)
		if len(errs) != 0 {
			t.Errorf("errs = %v", errs)
		}
	})

	t.Run("error on close", func(t *testing.T) {
		var errs []error
		closer := &testCloser{closeErr: context.DeadlineExceeded}
		closeResource(&errs, closer)
		if len(errs) != 1 {
			t.Errorf("errs = %v", errs)
		}
	})
}

// testCloser is a mock for testing closeResource.
type testCloser struct {
	closeErr error
}

func (m *testCloser) Close() error {
	return m.closeErr
}

func TestGetDataHubConfig(t *testing.T) {
	t.Run("no instance config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: nil,
			},
		}
		result := p.getDataHubConfig("default")
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid datahub config with url", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"url":     "http://datahub:8080",
								"token":   "test-token",
								"timeout": "30s",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.URL != "http://datahub:8080" {
			t.Errorf("expected URL 'http://datahub:8080', got %q", result.URL)
		}
		if result.Token != "test-token" {
			t.Errorf("expected Token 'test-token', got %q", result.Token)
		}
	})

	t.Run("valid datahub config with endpoint fallback", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"endpoint": "http://datahub:9080",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.URL != "http://datahub:9080" {
			t.Errorf("expected URL 'http://datahub:9080', got %q", result.URL)
		}
	})

	t.Run("valid datahub config with debug enabled", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"url":   "http://datahub:8080",
								"debug": true,
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.Debug {
			t.Error("expected Debug to be true")
		}
	})

	t.Run("valid datahub config with debug defaults to false", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"url": "http://datahub:8080",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Debug {
			t.Error("expected Debug to default to false")
		}
	})
}

func TestGetTrinoConfig(t *testing.T) {
	t.Run("no instance config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: nil,
			},
		}
		result := p.getTrinoConfig("default")
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid trino config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"trino": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"host":            "trino.example.com",
								"port":            8443,
								"user":            "admin",
								"password":        "secret",
								"catalog":         "hive",
								"schema":          "analytics",
								"ssl":             true,
								"ssl_verify":      true,
								"default_limit":   500,
								"max_limit":       5000,
								"read_only":       true,
								"connection_name": "prod",
							},
						},
					},
				},
			},
		}
		result := p.getTrinoConfig("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Host != "trino.example.com" {
			t.Errorf("expected Host 'trino.example.com', got %q", result.Host)
		}
		if result.Port != 8443 {
			t.Errorf("expected Port 8443, got %d", result.Port)
		}
		if !result.SSL {
			t.Error("expected SSL to be true")
		}
		if !result.SSLVerify {
			t.Error("expected SSLVerify to be true")
		}
		if result.DefaultLimit != 500 {
			t.Errorf("expected DefaultLimit 500, got %d", result.DefaultLimit)
		}
	})
}

func TestGetS3Config(t *testing.T) {
	t.Run("no instance config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: nil,
			},
		}
		result := p.getS3Config("default")
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid s3 config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"s3": map[string]any{
						"instances": map[string]any{
							"default": map[string]any{
								"region":            "us-west-2",
								"endpoint":          "http://minio:9000",
								"access_key_id":     "access-key",
								"secret_access_key": "secret-key",
								"bucket_prefix":     "prefix-",
								"connection_name":   "minio",
							},
						},
					},
				},
			},
		}
		result := p.getS3Config("default")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Region != "us-west-2" {
			t.Errorf("expected Region 'us-west-2', got %q", result.Region)
		}
		if result.Endpoint != "http://minio:9000" {
			t.Errorf("expected Endpoint 'http://minio:9000', got %q", result.Endpoint)
		}
		if result.ConnectionName != "minio" {
			t.Errorf("expected ConnectionName 'minio', got %q", result.ConnectionName)
		}
	})

	t.Run("s3 config with empty connection name uses instance name", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"s3": map[string]any{
						"instances": map[string]any{
							"myinstance": map[string]any{
								"region": "us-east-1",
							},
						},
					},
				},
			},
		}
		result := p.getS3Config("myinstance")
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.ConnectionName != "myinstance" {
			t.Errorf("expected ConnectionName 'myinstance', got %q", result.ConnectionName)
		}
	})
}

func TestProviderInstanceNotFound(t *testing.T) {
	t.Run("datahub instance not found", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: "test"},
			Semantic: SemanticConfig{
				Provider: "datahub",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"datahub": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{
							"url": "http://datahub:8080",
						},
					},
				},
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for nonexistent datahub instance")
		}
	})

	t.Run("trino instance not found", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query: QueryConfig{
				Provider: "trino",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{
							"host": "trino.example.com",
							"port": 8080,
						},
					},
				},
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for nonexistent trino instance")
		}
	})

	t.Run("s3 instance not found", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage: StorageConfig{
				Provider: "s3",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"s3": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{
							"region": "us-west-2",
						},
					},
				},
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for nonexistent s3 instance")
		}
	})
}

func TestCreateAuthenticatorWithAPIKeys(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Auth: AuthConfig{
			APIKeys: APIKeyAuthConfig{
				Enabled: true,
				Keys: []APIKeyDef{
					{Key: "test-key", Name: "test", Roles: []string{"admin"}},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p == nil {
		t.Fatal("New() returned nil")
	}
}

func TestPlatformStartError(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()

	// Start successfully
	if err := p.Start(ctx); err != nil {
		t.Fatalf("First Start() error = %v", err)
	}

	// Try to start again - should fail
	if err := p.Start(ctx); err == nil {
		t.Error("Second Start() expected error when already started")
	}

	// Clean up
	_ = p.Stop(ctx)
}

func TestCfgHelpersEdgeCases(t *testing.T) {
	cfg := map[string]any{
		"negative_int":    -42,
		"zero":            0,
		"empty_string":    "",
		"false_bool":      false,
		"negative_float":  -3.14,
		"zero_float":      0.0,
		"invalid_dur_str": "invalid",
	}

	t.Run("cfgInt negative", func(t *testing.T) {
		if v := cfgInt(cfg, "negative_int", 0); v != -42 {
			t.Errorf("cfgInt(negative_int) = %d", v)
		}
	})

	t.Run("cfgInt zero", func(t *testing.T) {
		if v := cfgInt(cfg, "zero", 100); v != 0 {
			t.Errorf("cfgInt(zero) = %d", v)
		}
	})

	t.Run("cfgInt negative float", func(t *testing.T) {
		if v := cfgInt(cfg, "negative_float", 0); v != -3 {
			t.Errorf("cfgInt(negative_float) = %d", v)
		}
	})

	t.Run("cfgString empty", func(t *testing.T) {
		if v := cfgString(cfg, "empty_string"); v != "" {
			t.Errorf("cfgString(empty_string) = %q", v)
		}
	})

	t.Run("cfgBool false value", func(t *testing.T) {
		if v := cfgBool(cfg, "false_bool"); v {
			t.Error("cfgBool(false_bool) = true")
		}
	})

	t.Run("cfgBoolDefault false overrides default", func(t *testing.T) {
		if v := cfgBoolDefault(cfg, "false_bool", true); v {
			t.Error("cfgBoolDefault(false_bool, true) = true")
		}
	})

	t.Run("cfgDuration invalid string returns default", func(t *testing.T) {
		if v := cfgDuration(cfg, "invalid_dur_str", 5*time.Second); v != 5*time.Second {
			t.Errorf("cfgDuration(invalid_dur_str) = %v", v)
		}
	})

	t.Run("cfgDuration zero", func(t *testing.T) {
		if v := cfgDuration(cfg, "zero", 10*time.Second); v != 0 {
			t.Errorf("cfgDuration(zero) = %v", v)
		}
	})

	t.Run("cfgDuration zero float", func(t *testing.T) {
		if v := cfgDuration(cfg, "zero_float", 10*time.Second); v != 0 {
			t.Errorf("cfgDuration(zero_float) = %v", v)
		}
	})
}

func TestInstanceConfigMapTypes(t *testing.T) {
	t.Run("instances as slice (wrong type)", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: "test"},
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": []string{"item1", "item2"}, // wrong type
				},
			},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		instanceCfg := p.getInstanceConfig("trino", "any")
		if instanceCfg != nil {
			t.Error("getInstanceConfig should return nil for wrong instances type")
		}
	})

	t.Run("kind config not a map", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: "test"},
			Toolkits: map[string]any{
				"trino": "not-a-map",
			},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		instanceCfg := p.getInstanceConfig("trino", "any")
		if instanceCfg != nil {
			t.Error("getInstanceConfig should return nil for non-map kind config")
		}
	})
}

func TestCreateAuthenticatorOIDCError(t *testing.T) {
	// Test OIDC authenticator error path
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Auth: AuthConfig{
			OIDC: OIDCAuthConfig{
				Enabled: true,
				// Missing required issuer - will cause error
				Issuer: "",
			},
		},
	}

	_, err := New(WithConfig(cfg))
	if err == nil {
		t.Error("New() expected error for invalid OIDC config")
	}
}

func TestPlatformCloseMultiple(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Close multiple times should be safe
	if err := p.Close(); err != nil {
		t.Errorf("First Close() error = %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

func TestPlatformStopIdempotent(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()

	// Start
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop multiple times should be safe
	if err := p.Stop(ctx); err != nil {
		t.Errorf("First Stop() error = %v", err)
	}
	// Second stop should not error (idempotent)
	_ = p.Stop(ctx)

	// Cleanup
	_ = p.Close()
}

func TestPlatformWithMultiplePersonas(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"viewer": {
					DisplayName: "Viewer",
					Roles:       []string{"viewer"},
					Tools: ToolRulesDef{
						Allow: []string{"*_list", "*_describe"},
						Deny:  []string{"*_execute", "*_delete"},
					},
				},
				"editor": {
					DisplayName: "Editor",
					Roles:       []string{"editor"},
					Tools: ToolRulesDef{
						Allow: []string{"*"},
						Deny:  []string{"*_delete"},
					},
				},
				"admin": {
					DisplayName: "Admin",
					Roles:       []string{"admin"},
					Tools: ToolRulesDef{
						Allow: []string{"*"},
					},
				},
			},
			DefaultPersona: "viewer",
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Verify all personas loaded
	pr := p.PersonaRegistry()

	viewer, ok := pr.Get("viewer")
	if !ok {
		t.Error("viewer persona not found")
	}
	if viewer.DisplayName != "Viewer" {
		t.Errorf("viewer.DisplayName = %q", viewer.DisplayName)
	}

	editor, ok := pr.Get("editor")
	if !ok {
		t.Error("editor persona not found")
	}
	if editor.DisplayName != "Editor" {
		t.Errorf("editor.DisplayName = %q", editor.DisplayName)
	}

	admin, ok := pr.Get("admin")
	if !ok {
		t.Error("admin persona not found")
	}
	if admin.DisplayName != "Admin" {
		t.Errorf("admin.DisplayName = %q", admin.DisplayName)
	}

	_ = p.Close()
}

func TestParseOrGenerateSigningKey(t *testing.T) {
	t.Run("valid base64 signing key", func(t *testing.T) {
		// Generate a valid 32-byte key encoded as base64
		validKey := "dGVzdC1zaWduaW5nLWtleS1hdC1sZWFzdC0zMi1ieXRlcw==" // "test-signing-key-at-least-32-bytes"
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: validKey,
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil")
		}
		_ = p.Close()
	})

	t.Run("invalid base64 signing key", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: "not-valid-base64!!!", // Invalid base64
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for invalid base64 signing key")
		}
	})

	t.Run("signing key too short", func(t *testing.T) {
		// "short" in base64 = "c2hvcnQ=" (5 bytes, less than 32)
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: "c2hvcnQ=", // "short" - only 5 bytes
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() expected error for signing key too short")
		}
	})

	t.Run("auto-generate signing key when not configured", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: "", // Empty - should auto-generate
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil with auto-generated key")
		}
		_ = p.Close()
	})
}

func TestInitOAuth(t *testing.T) {
	t.Run("OAuth disabled", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled: false,
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.OAuthServer() != nil {
			t.Error("OAuthServer() should be nil when OAuth is disabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with pre-registered clients", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled: true,
				Issuer:  "http://localhost:8080",
				Clients: []OAuthClientConfig{
					{
						ID:           "client-1",
						Secret:       "secret-1",
						RedirectURIs: []string{"http://localhost/callback"},
					},
					{
						ID:           "client-2",
						Secret:       "secret-2",
						RedirectURIs: []string{"http://localhost/callback2"},
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil when OAuth is enabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with upstream IdP", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled: true,
				Issuer:  "http://localhost:8080",
				Upstream: &UpstreamIDPConfig{
					Issuer:       "http://keycloak:8180/realms/test",
					ClientID:     "mcp-server",
					ClientSecret: "keycloak-secret",
					RedirectURI:  "http://localhost:8080/oauth/callback",
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil when OAuth is enabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with DCR", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			OAuth: OAuthConfig{
				Enabled: true,
				Issuer:  "http://localhost:8080",
				DCR: DCRConfig{
					Enabled:                 true,
					AllowedRedirectPatterns: []string{"^http://localhost.*"},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil when OAuth is enabled")
		}
		_ = p.Close()
	})
}

func TestDataHubSemanticProviderWithLineageConfig(t *testing.T) {
	// This test verifies that lineage configuration is properly wired
	// from platform config through to the DataHub semantic adapter.
	cfg := &Config{
		Server: ServerConfig{Name: "test"},
		Semantic: SemanticConfig{
			Provider: "datahub",
			Instance: "primary",
			Lineage: datahubsemantic.LineageConfig{
				Enabled:             true,
				MaxHops:             3,
				Inherit:             []string{"glossary_terms", "descriptions", "tags"},
				ConflictResolution:  "nearest",
				PreferColumnLineage: true,
				CacheTTL:            10 * time.Minute,
				Timeout:             5 * time.Second,
			},
		},
		Query:   QueryConfig{Provider: "noop"},
		Storage: StorageConfig{Provider: "noop"},
		Toolkits: map[string]any{
			"datahub": map[string]any{
				"instances": map[string]any{
					"primary": map[string]any{
						"url":   "http://datahub.example.com:8080/api/graphql",
						"token": "test-token",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Type assert to get the DataHub adapter
	// The semantic provider should be a datahub.Adapter (or wrapped in a cache)
	semProvider := p.SemanticProvider()
	if semProvider == nil {
		t.Fatal("SemanticProvider() returned nil")
	}

	// If caching is disabled, we can type assert directly
	adapter, ok := semProvider.(*datahubsemantic.Adapter)
	if !ok {
		// If caching was enabled, we'd need to unwrap
		t.Fatalf("SemanticProvider() is not a *datahub.Adapter, got %T", semProvider)
	}

	// Verify lineage config was wired through
	lineageCfg := adapter.LineageConfig()

	if !lineageCfg.Enabled {
		t.Error("LineageConfig().Enabled = false, want true - config was not wired through")
	}
	if lineageCfg.MaxHops != 3 {
		t.Errorf("LineageConfig().MaxHops = %d, want 3", lineageCfg.MaxHops)
	}
	if len(lineageCfg.Inherit) != 3 {
		t.Errorf("LineageConfig().Inherit len = %d, want 3", len(lineageCfg.Inherit))
	}
	expectedInherit := []string{"glossary_terms", "descriptions", "tags"}
	for i, want := range expectedInherit {
		if i >= len(lineageCfg.Inherit) {
			t.Errorf("LineageConfig().Inherit[%d] missing, want %q", i, want)
			continue
		}
		if lineageCfg.Inherit[i] != want {
			t.Errorf("LineageConfig().Inherit[%d] = %q, want %q", i, lineageCfg.Inherit[i], want)
		}
	}
	if lineageCfg.ConflictResolution != "nearest" {
		t.Errorf("LineageConfig().ConflictResolution = %q, want %q", lineageCfg.ConflictResolution, "nearest")
	}
	if !lineageCfg.PreferColumnLineage {
		t.Error("LineageConfig().PreferColumnLineage = false, want true")
	}
	if lineageCfg.CacheTTL != 10*time.Minute {
		t.Errorf("LineageConfig().CacheTTL = %v, want %v", lineageCfg.CacheTTL, 10*time.Minute)
	}
	if lineageCfg.Timeout != 5*time.Second {
		t.Errorf("LineageConfig().Timeout = %v, want %v", lineageCfg.Timeout, 5*time.Second)
	}
}

// createTestAppDir creates a temporary directory with test app files.
func createTestAppDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html><head></head><body>test</body></html>"), 0o600); err != nil {
		t.Fatalf("Failed to create test index.html: %v", err)
	}
	return dir
}

func TestConvertCSP(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := convertCSP(nil)
		if result != nil {
			t.Error("convertCSP(nil) should return nil")
		}
	})

	t.Run("converts resource domains", func(t *testing.T) {
		cfg := &CSPAppConfig{
			ResourceDomains: []string{"https://cdn.example.com", "https://fonts.googleapis.com"},
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if len(result.ResourceDomains) != 2 {
			t.Errorf("ResourceDomains len = %d, want 2", len(result.ResourceDomains))
		}
		if result.ResourceDomains[0] != "https://cdn.example.com" {
			t.Errorf("ResourceDomains[0] = %q", result.ResourceDomains[0])
		}
	})

	t.Run("converts connect domains", func(t *testing.T) {
		cfg := &CSPAppConfig{
			ConnectDomains: []string{"https://api.example.com"},
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if len(result.ConnectDomains) != 1 {
			t.Errorf("ConnectDomains len = %d, want 1", len(result.ConnectDomains))
		}
	})

	t.Run("converts frame domains", func(t *testing.T) {
		cfg := &CSPAppConfig{
			FrameDomains: []string{"https://embed.example.com"},
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if len(result.FrameDomains) != 1 {
			t.Errorf("FrameDomains len = %d, want 1", len(result.FrameDomains))
		}
	})

	t.Run("converts clipboard write permission", func(t *testing.T) {
		cfg := &CSPAppConfig{
			ClipboardWrite: true,
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if result.Permissions == nil {
			t.Fatal("Permissions should not be nil when ClipboardWrite is true")
		}
		if result.Permissions.ClipboardWrite == nil {
			t.Error("ClipboardWrite should not be nil")
		}
	})

	t.Run("no permissions when clipboard write false", func(t *testing.T) {
		cfg := &CSPAppConfig{
			ClipboardWrite: false,
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if result.Permissions != nil {
			t.Error("Permissions should be nil when ClipboardWrite is false")
		}
	})

	t.Run("full CSP config", func(t *testing.T) {
		cfg := &CSPAppConfig{
			ResourceDomains: []string{"https://cdn.example.com"},
			ConnectDomains:  []string{"https://api.example.com"},
			FrameDomains:    []string{"https://embed.example.com"},
			ClipboardWrite:  true,
		}
		result := convertCSP(cfg)
		if result == nil {
			t.Fatal("convertCSP returned nil")
		}
		if len(result.ResourceDomains) != 1 {
			t.Errorf("ResourceDomains len = %d", len(result.ResourceDomains))
		}
		if len(result.ConnectDomains) != 1 {
			t.Errorf("ConnectDomains len = %d", len(result.ConnectDomains))
		}
		if len(result.FrameDomains) != 1 {
			t.Errorf("FrameDomains len = %d", len(result.FrameDomains))
		}
		if result.Permissions == nil || result.Permissions.ClipboardWrite == nil {
			t.Error("ClipboardWrite permission not set")
		}
	})
}

func TestInitMCPApps(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		// mcpAppsRegistry should be nil when disabled
		if p.mcpAppsRegistry != nil {
			t.Error("mcpAppsRegistry should be nil when MCPApps disabled")
		}
	})

	t.Run("enabled with filesystem app", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"query_results": {
						Enabled:    true,
						Tools:      []string{"trino_query"},
						AssetsPath: testAppDir,
						EntryPoint: "index.html",
						Config: map[string]any{
							"chartCDN":         "https://cdn.example.com/chart.js",
							"defaultChartType": "bar",
							"maxTableRows":     500,
						},
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.mcpAppsRegistry == nil {
			t.Fatal("mcpAppsRegistry should not be nil when MCPApps enabled")
		}

		if !p.mcpAppsRegistry.HasApps() {
			t.Error("Registry should have apps")
		}

		app := p.mcpAppsRegistry.Get("query_results")
		if app == nil {
			t.Fatal("query_results app should be registered")
		}

		if len(app.ToolNames) != 1 || app.ToolNames[0] != "trino_query" {
			t.Errorf("ToolNames = %v, want [trino_query]", app.ToolNames)
		}
	})

	t.Run("app with missing assets fails", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"missing_app": {
						Enabled:    true,
						Tools:      []string{"test_tool"},
						AssetsPath: "/nonexistent/path",
						EntryPoint: "index.html",
					},
				},
			},
		}

		// Should error because assets don't exist
		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Fatal("New() should fail with missing assets")
		}
	})

	t.Run("disabled app not registered", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"query_results": {
						Enabled:    false,
						AssetsPath: testAppDir,
						EntryPoint: "index.html",
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.mcpAppsRegistry == nil {
			t.Fatal("mcpAppsRegistry should not be nil")
		}

		// Registry should exist but have no apps
		if p.mcpAppsRegistry.HasApps() {
			t.Error("Registry should have no apps when all disabled")
		}
	})

	t.Run("app with CSP config", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"test_app": {
						Enabled:    true,
						Tools:      []string{"test_tool"},
						AssetsPath: testAppDir,
						EntryPoint: "index.html",
						CSP: &CSPAppConfig{
							ResourceDomains: []string{"https://cdn.example.com"},
							ConnectDomains:  []string{"https://api.example.com"},
							FrameDomains:    []string{"https://embed.example.com"},
							ClipboardWrite:  true,
						},
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		if p.mcpAppsRegistry == nil {
			t.Fatal("mcpAppsRegistry should not be nil")
		}

		app := p.mcpAppsRegistry.Get("test_app")
		if app == nil {
			t.Fatal("test_app should be registered")
		}

		if app.CSP == nil {
			t.Fatal("CSP should not be nil")
		}

		if len(app.CSP.ResourceDomains) != 1 {
			t.Errorf("CSP.ResourceDomains len = %d, want 1", len(app.CSP.ResourceDomains))
		}
	})

	t.Run("app with custom resource URI", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"custom_app": {
						Enabled:     true,
						Tools:       []string{"test_tool"},
						AssetsPath:  testAppDir,
						EntryPoint:  "index.html",
						ResourceURI: "ui://custom-resource",
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		app := p.mcpAppsRegistry.Get("custom_app")
		if app == nil {
			t.Fatal("custom_app should be registered")
		}

		if app.ResourceURI != "ui://custom-resource" {
			t.Errorf("ResourceURI = %q, want ui://custom-resource", app.ResourceURI)
		}
	})

	t.Run("app with default entry point", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"default_entry": {
						Enabled:    true,
						Tools:      []string{"test_tool"},
						AssetsPath: testAppDir,
						// EntryPoint omitted - should default to index.html
					},
				},
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		app := p.mcpAppsRegistry.Get("default_entry")
		if app == nil {
			t.Fatal("default_entry should be registered")
		}

		if app.EntryPoint != "index.html" {
			t.Errorf("EntryPoint = %q, want index.html", app.EntryPoint)
		}
	})

	t.Run("app validation error", func(t *testing.T) {
		testAppDir := createTestAppDir(t)

		cfg := &Config{
			Server:   ServerConfig{Name: "test"},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
			MCPApps: MCPAppsConfig{
				Enabled: true,
				Apps: map[string]AppConfig{
					"invalid_app": {
						Enabled:    true,
						Tools:      []string{}, // Empty tools - validation should fail
						AssetsPath: testAppDir,
						EntryPoint: "index.html",
					},
				},
			},
		}

		_, err := New(WithConfig(cfg))
		if err == nil {
			t.Error("New() should fail with empty tools list")
		}
	})
}

func TestHintManager(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	hm := p.HintManager()
	if hm == nil {
		t.Fatal("HintManager() returned nil")
	}

	// Check that default hints were loaded
	hint, ok := hm.GetHint("datahub_search")
	if !ok {
		t.Error("Expected datahub_search hint to be loaded")
	}
	if hint == "" {
		t.Error("datahub_search hint should not be empty")
	}
}

func TestPersonaHintsLoadedToHintManager(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"analyst": {
					DisplayName: "Data Analyst",
					Roles:       []string{"analyst"},
					Hints: map[string]string{
						"custom_tool": "This is a custom hint from persona",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	hm := p.HintManager()

	// Check persona hint was loaded
	hint, ok := hm.GetHint("custom_tool")
	if !ok {
		t.Error("Expected custom_tool hint from persona to be loaded")
	}
	if hint != "This is a custom hint from persona" {
		t.Errorf("Unexpected hint value: %q", hint)
	}
}

func TestInitAuditNoopWhenDisabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Audit: AuditConfig{
			Enabled: false,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Platform should have been created without error
	if p.MCPServer() == nil {
		t.Error("MCPServer() should not be nil")
	}
}

func TestInitAuditNoopWithoutDatabase(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Audit: AuditConfig{
			Enabled:       true,
			LogToolCalls:  true,
			RetentionDays: 30,
		},
		// Database DSN intentionally left empty
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Should succeed with noop logger when DB not configured
	if p.MCPServer() == nil {
		t.Error("MCPServer() should not be nil")
	}
}

func TestLoadPersonasWithFullPromptConfig(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"analyst": {
					DisplayName: "Data Analyst",
					Description: "Analyzes data and runs queries",
					Roles:       []string{"analyst"},
					Tools: ToolRulesDef{
						Allow: []string{"trino_*"},
					},
					Prompts: PromptsDef{
						SystemPrefix: "You are a data analyst.",
						SystemSuffix: "Be concise.",
						Instructions: "Check DataHub first.",
					},
					Priority: 10,
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	pr := p.PersonaRegistry()
	analyst, ok := pr.Get("analyst")
	if !ok {
		t.Fatal("analyst persona not found")
	}

	if analyst.Description != "Analyzes data and runs queries" {
		t.Errorf("Description = %q", analyst.Description)
	}
	if analyst.Priority != 10 {
		t.Errorf("Priority = %d, want 10", analyst.Priority)
	}
	if analyst.Prompts.SystemPrefix != "You are a data analyst." {
		t.Errorf("SystemPrefix = %q", analyst.Prompts.SystemPrefix)
	}
	if analyst.Prompts.SystemSuffix != "Be concise." {
		t.Errorf("SystemSuffix = %q", analyst.Prompts.SystemSuffix)
	}
	if analyst.Prompts.Instructions != "Check DataHub first." {
		t.Errorf("Instructions = %q", analyst.Prompts.Instructions)
	}

	// Test GetFullSystemPrompt
	fullPrompt := analyst.GetFullSystemPrompt()
	if fullPrompt == "" {
		t.Error("GetFullSystemPrompt() returned empty string")
	}
	// Should contain all three parts
	if !contains(fullPrompt, "You are a data analyst.") {
		t.Error("fullPrompt missing SystemPrefix")
	}
	if !contains(fullPrompt, "Check DataHub first.") {
		t.Error("fullPrompt missing Instructions")
	}
	if !contains(fullPrompt, "Be concise.") {
		t.Error("fullPrompt missing SystemSuffix")
	}
}

func TestNew_NilToolkitsConfig(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		// Toolkits intentionally nil
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Should succeed with no toolkits loaded
	if p.ToolkitRegistry() == nil {
		t.Error("ToolkitRegistry() should not be nil")
	}
	if len(p.ToolkitRegistry().All()) != 0 {
		t.Errorf("expected 0 toolkits, got %d", len(p.ToolkitRegistry().All()))
	}
}

func TestNew_NoRulesEngine(t *testing.T) {
	// Verify that when no rule engine is provided and tuning rules are all
	// default, middleware setup still succeeds (nil ruleEngine path at L535).
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// The default RuleEngine is always created in initTuning, so it should not be nil
	if p.RuleEngine() == nil {
		t.Error("RuleEngine() should not be nil even without explicit config")
	}
}

func TestNew_NoAuthenticators_FallsBackToNoop(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		// Auth intentionally empty — no OIDC, no API keys, no OAuth
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Should fall back to NoopAuthenticator (L761)
	if p.MCPServer() == nil {
		t.Error("MCPServer() should not be nil")
	}
}

func TestNew_DefaultOAuthTTL(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		OAuth: OAuthConfig{
			Enabled: true,
			Issuer:  "http://localhost:8080",
			// No explicit TTLs — should use default 1h AccessTokenTTL (L332)
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	if p.OAuthServer() == nil {
		t.Fatal("OAuthServer() should not be nil")
	}
}

func TestNew_DefaultDataHubTimeout(t *testing.T) {
	p := &Platform{
		config: &Config{
			Toolkits: map[string]any{
				"datahub": map[string]any{
					"instances": map[string]any{
						"default": map[string]any{
							"url": "http://datahub:8080",
							// timeout not set — should default to 30s (L911)
						},
					},
				},
			},
		},
	}

	cfg := p.getDataHubConfig("default")
	if cfg == nil {
		t.Fatal("getDataHubConfig() returned nil")
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
}

func TestNew_DefaultTrinoTimeout(t *testing.T) {
	p := &Platform{
		config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"default": map[string]any{
							"host": "localhost",
							// timeout not set — should default to 120s (L939)
						},
					},
				},
			},
		},
	}

	cfg := p.getTrinoConfig("default")
	if cfg == nil {
		t.Fatal("getTrinoConfig() returned nil")
	}
	if cfg.Timeout != 120*time.Second {
		t.Errorf("Timeout = %v, want 120s", cfg.Timeout)
	}
}

func TestClose_NilAuditStore(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		// No audit configured — auditStore will be nil (L1084)
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Verify auditStore is nil
	if p.auditStore != nil {
		t.Error("auditStore should be nil without database")
	}

	// Close should succeed without panicking on nil auditStore
	if err := p.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestPlatformInfo_WithTags(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Name: "test-platform",
			Tags: []string{"fireworks", "retail", "pos"},
		},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	// Verify tags appear in the tool description (L59)
	desc := p.buildInfoToolDescription()
	if !containsHelper(desc, "fireworks") {
		t.Errorf("description %q does not contain tag 'fireworks'", desc)
	}
	if !containsHelper(desc, "retail") {
		t.Errorf("description %q does not contain tag 'retail'", desc)
	}
}

func TestNew_SigningKeyExactly32Bytes(t *testing.T) {
	// "aaaaaaaaaabbbbbbbbbbccccccccccdd" is exactly 32 bytes
	// base64 of 32 bytes of "a" repeated: use a known 32-byte string
	import32Bytes := "YWFhYWFhYWFhYWJiYmJiYmJiYmJjY2NjY2NjY2NjZGQ=" // "aaaaaaaaaabbbbbbbbbbccccccccccdd"
	cfg := &Config{
		Server:   ServerConfig{Name: "test"},
		Semantic: SemanticConfig{Provider: "noop"},
		Query:    QueryConfig{Provider: "noop"},
		Storage:  StorageConfig{Provider: "noop"},
		OAuth: OAuthConfig{
			Enabled:    true,
			Issuer:     "http://localhost:8080",
			SigningKey: import32Bytes,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v (exactly 32 bytes should be accepted)", err)
	}
	defer func() { _ = p.Close() }()

	if p.OAuthServer() == nil {
		t.Error("OAuthServer() should not be nil with exact 32-byte key")
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
