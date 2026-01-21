package platform

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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

		if p.MiddlewareChain() == nil {
			t.Error("MiddlewareChain() is nil")
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

func TestMiddlewareChainWithEnrichment(t *testing.T) {
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

	if p.MiddlewareChain() == nil {
		t.Error("MiddlewareChain() is nil")
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
