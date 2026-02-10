package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	"github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

const (
	testProviderNoop        = "noop"
	testServerName          = "test"
	testRoleAnalyst         = "analyst"
	testRoleAdmin           = "admin"
	testRoleViewer          = "viewer"
	testConflictNearest     = "nearest"
	testIntVal              = 42
	testFloatVal            = 3.14
	testDurationIntSec      = 60
	testDurationFloatSec    = 90.0
	testPort                = 8080
	testPortSecondary       = 8081
	testDefaultMaxOpenConns = 25
	testDefaultRetention    = 90
	testDefaultQuality      = 0.7
	testDisplayAnalyst      = "Data Analyst"
	testSystemPrefix        = "You are a data analyst."
	testInstancePrimary     = "primary"
	testInstanceDefault     = "default"
	testInstancesKey        = "instances"
	testToolName            = "test_tool"
	testEntryPoint          = "index.html"
	testCDNDomain           = "https://cdn.example.com"
	testCSPNilMsg           = "convertCSP returned nil"
	testFilePerms           = 0o600
	testLineageMaxHops      = 3
	testLineageCacheTTL     = 10 * time.Minute
	testLineageTimeout      = 5 * time.Second
	testDefaultDataHubTO    = 30 * time.Second
	testDefaultTrinoTO      = 120 * time.Second
	testMissingKey          = "missing"
	testDefaultFallback     = 100
	testDurationDefault     = 10 * time.Second
	testDuration30s         = 30 * time.Second
	testDuration90s         = 90 * time.Second
	testPriority            = 10
	testRetentionDays       = 30
	testMaxTableRows        = 500
	testNewErrFmt           = "New() error = %v"
	testFloatKeyExpected    = 3
	testNegFloatKeyExpected = -3
	testEdgeFallback5s      = 5 * time.Second
	testZeroFloat           = 0.0
	testMCPServerNilMsg     = "MCPServer() should not be nil"
	testToolkitKeyDatahub   = "datahub"
	testCfgKeyURL           = "url"
	testCfgKeyToken         = "token"
)

// newTestPlatform creates a Platform with noop providers for testing.
func newTestPlatform(t *testing.T, opts ...Option) *Platform {
	t.Helper()
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}
	allOpts := append([]Option{WithConfig(cfg)}, opts...)
	p, err := New(allOpts...)
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	return p
}

func TestNew_RequiresConfig(t *testing.T) {
	_, err := New()
	if err == nil {
		t.Error("New() expected error without config")
	}
}

func TestNew_MinimalConfigWithNoopProviders(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Name: "test-platform",
		},
		Semantic: SemanticConfig{
			Provider: testProviderNoop,
		},
		Query: QueryConfig{
			Provider: testProviderNoop,
		},
		Storage: StorageConfig{
			Provider: testProviderNoop,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
}

func TestNew_WithInjectedProviders(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Name: testServerName},
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
		t.Fatalf(testNewErrFmt, err)
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
}

func TestNew_WithInjectedRegistries(t *testing.T) {
	p := newTestPlatform(t,
		WithPersonaRegistry(persona.NewRegistry()),
		WithToolkitRegistry(registry.NewRegistry()),
	)

	if p.PersonaRegistry() == nil {
		t.Error("PersonaRegistry() should not be nil")
	}
	if p.ToolkitRegistry() == nil {
		t.Error("ToolkitRegistry() should not be nil")
	}
}

func TestNew_WithInjectedAuthComponents(t *testing.T) {
	auth := &middleware.NoopAuthenticator{}
	authz := &middleware.NoopAuthorizer{}
	logger := &middleware.NoopAuditLogger{}

	p := newTestPlatform(t,
		WithAuthenticator(auth),
		WithAuthorizer(authz),
		WithAuditLogger(logger),
	)

	if p.MCPServer() == nil {
		t.Error("MCPServer() is nil")
	}
}

func TestNew_WithInjectedRuleEngine(t *testing.T) {
	engine := tuning.NewRuleEngine(&tuning.Rules{WarnOnDeprecated: true})

	p := newTestPlatform(t, WithRuleEngine(engine))

	if p.RuleEngine() != engine {
		t.Error("RuleEngine() did not return injected engine")
	}
}

func TestNew_UnknownProviders(t *testing.T) {
	t.Run("unknown semantic provider", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: testServerName},
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	if err := p.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLoadPersonas(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				testRoleAnalyst: {
					DisplayName: testDisplayAnalyst,
					Roles:       []string{testRoleAnalyst},
					Tools: ToolRulesDef{
						Allow: []string{"trino_*"},
						Deny:  []string{"*_delete"},
					},
					Prompts: PromptsDef{
						SystemPrefix: testSystemPrefix,
					},
					Hints: map[string]string{"key": "value"},
				},
				testRoleAdmin: {
					DisplayName: "Administrator",
					Roles:       []string{testRoleAdmin},
					Tools: ToolRulesDef{
						Allow: []string{"*"},
					},
				},
			},
			DefaultPersona: testRoleAnalyst,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	// Check that personas were loaded
	pr := p.PersonaRegistry()

	analyst, ok := pr.Get(testRoleAnalyst)
	if !ok {
		t.Fatal("Get(analyst) returned false")
	}
	if analyst.DisplayName != "Data Analyst" {
		t.Errorf("analyst.DisplayName = %q", analyst.DisplayName)
	}

	admin, ok := pr.Get(testRoleAdmin)
	if !ok {
		t.Fatal("Get(admin) returned false")
	}
	if admin.DisplayName != "Administrator" {
		t.Errorf("admin.DisplayName = %q", admin.DisplayName)
	}
}

func TestMCPMiddlewareWithEnrichment(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
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
		t.Fatalf(testNewErrFmt, err)
	}

	// Verify MCP server was created with middleware configured
	if p.MCPServer() == nil {
		t.Error("MCPServer() is nil")
	}
}

// cfgHelpersTestData returns a shared test config map for cfg helper tests.
func cfgHelpersTestData() map[string]any {
	return map[string]any{
		"string_key":      "value",
		"int_key":         testIntVal,
		"float_key":       testFloatVal,
		"bool_key":        true,
		"duration_string": "30s",
		"duration_int":    testDurationIntSec,
		"duration_float":  testDurationFloatSec,
	}
}

func TestCfgString(t *testing.T) {
	cfg := cfgHelpersTestData()
	if v := cfgString(cfg, "string_key"); v != "value" {
		t.Errorf("cfgString(string_key) = %q", v)
	}
	if v := cfgString(cfg, testMissingKey); v != "" {
		t.Errorf("cfgString(missing) = %q", v)
	}
	if v := cfgString(cfg, "int_key"); v != "" {
		t.Errorf("cfgString(int_key) = %q (should be empty)", v)
	}
}

func TestCfgInt(t *testing.T) {
	cfg := cfgHelpersTestData()
	if v := cfgInt(cfg, "int_key", 0); v != testIntVal {
		t.Errorf("cfgInt(int_key) = %d", v)
	}
	if v := cfgInt(cfg, "float_key", 0); v != testFloatKeyExpected {
		t.Errorf("cfgInt(float_key) = %d", v)
	}
	if v := cfgInt(cfg, testMissingKey, testDefaultFallback); v != testDefaultFallback {
		t.Errorf("cfgInt(missing) = %d", v)
	}
}

func TestCfgBool(t *testing.T) {
	cfg := cfgHelpersTestData()
	if v := cfgBool(cfg, "bool_key"); !v {
		t.Error("cfgBool(bool_key) = false")
	}
	if v := cfgBool(cfg, testMissingKey); v {
		t.Error("cfgBool(missing) = true")
	}
}

func TestCfgBoolDefault(t *testing.T) {
	cfg := cfgHelpersTestData()
	if v := cfgBoolDefault(cfg, "bool_key", false); !v {
		t.Error("cfgBoolDefault(bool_key, false) = false")
	}
	if v := cfgBoolDefault(cfg, testMissingKey, true); !v {
		t.Error("cfgBoolDefault(missing, true) = false")
	}
}

func TestCfgDuration(t *testing.T) {
	cfg := cfgHelpersTestData()
	if v := cfgDuration(cfg, "duration_string", 0); v != testDuration30s {
		t.Errorf("cfgDuration(duration_string) = %v", v)
	}
	if v := cfgDuration(cfg, "duration_int", 0); v != testDurationIntSec*time.Second {
		t.Errorf("cfgDuration(duration_int) = %v", v)
	}
	if v := cfgDuration(cfg, "duration_float", 0); v != testDuration90s {
		t.Errorf("cfgDuration(duration_float) = %v", v)
	}
	if v := cfgDuration(cfg, testMissingKey, testDurationDefault); v != testDurationDefault {
		t.Errorf("cfgDuration(missing) = %v", v)
	}
}

func TestGetInstanceConfig(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Name: testServerName},
		Toolkits: map[string]any{
			"trino": map[string]any{
				testInstanceDefault: testInstancePrimary,
				testInstancesKey: map[string]any{
					testInstancePrimary: map[string]any{
						"host": "localhost",
						"port": testPort,
					},
					"secondary": map[string]any{
						"host": "other.host",
						"port": testPortSecondary,
					},
				},
			},
		},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	t.Run("get named instance", func(t *testing.T) {
		instanceCfg := p.getInstanceConfig("trino", testInstancePrimary)
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
		kindCfg := map[string]any{testInstanceDefault: testInstancePrimary}
		instances := map[string]any{
			testInstancePrimary: map[string]any{},
			"secondary":         map[string]any{},
		}
		result := resolveDefaultInstance(kindCfg, instances)
		if result != testInstancePrimary {
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
		result := p.getDataHubConfig(testInstanceDefault)
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid datahub config with url", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
								"url":     "http://datahub:8080",
								"token":   "test-token",
								"timeout": "30s",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig(testInstanceDefault)
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
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
								"endpoint": "http://datahub:9080",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig(testInstanceDefault)
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
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
								"url":   "http://datahub:8080",
								"debug": true,
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig(testInstanceDefault)
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
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
								"url": "http://datahub:8080",
							},
						},
					},
				},
			},
		}
		result := p.getDataHubConfig(testInstanceDefault)
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
		result := p.getTrinoConfig(testInstanceDefault)
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid trino config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"trino": map[string]any{
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
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
		result := p.getTrinoConfig(testInstanceDefault)
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
		result := p.getS3Config(testInstanceDefault)
		if result != nil {
			t.Error("expected nil for missing config")
		}
	})

	t.Run("valid s3 config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"s3": map[string]any{
						testInstancesKey: map[string]any{
							testInstanceDefault: map[string]any{
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
		result := p.getS3Config(testInstanceDefault)
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
						testInstancesKey: map[string]any{
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
			Server: ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{
				Provider: "datahub",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"datahub": map[string]any{
					testInstancesKey: map[string]any{
						testInstancePrimary: map[string]any{
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query: QueryConfig{
				Provider: "trino",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"trino": map[string]any{
					testInstancesKey: map[string]any{
						testInstancePrimary: map[string]any{
							"host": "trino.example.com",
							"port": testPort,
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage: StorageConfig{
				Provider: "s3",
				Instance: "nonexistent",
			},
			Toolkits: map[string]any{
				"s3": map[string]any{
					testInstancesKey: map[string]any{
						testInstancePrimary: map[string]any{
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Auth: AuthConfig{
			APIKeys: APIKeyAuthConfig{
				Enabled: true,
				Keys: []APIKeyDef{
					{Key: "test-key", Name: "test", Roles: []string{testRoleAdmin}},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	if p == nil {
		t.Fatal("New() returned nil")
	}
}

func TestPlatformStartError(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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

// cfgHelpersEdgeCaseData returns test data for cfg helper edge case tests.
func cfgHelpersEdgeCaseData() map[string]any {
	return map[string]any{
		"negative_int":    -testIntVal,
		"zero":            0,
		"empty_string":    "",
		"false_bool":      false,
		"negative_float":  -testFloatVal,
		"zero_float":      testZeroFloat,
		"invalid_dur_str": "invalid",
	}
}

func TestCfgIntEdgeCases(t *testing.T) {
	cfg := cfgHelpersEdgeCaseData()
	if v := cfgInt(cfg, "negative_int", 0); v != -testIntVal {
		t.Errorf("cfgInt(negative_int) = %d", v)
	}
	if v := cfgInt(cfg, "zero", testDefaultFallback); v != 0 {
		t.Errorf("cfgInt(zero) = %d", v)
	}
	if v := cfgInt(cfg, "negative_float", 0); v != testNegFloatKeyExpected {
		t.Errorf("cfgInt(negative_float) = %d", v)
	}
}

func TestCfgStringEdgeCases(t *testing.T) {
	cfg := cfgHelpersEdgeCaseData()
	if v := cfgString(cfg, "empty_string"); v != "" {
		t.Errorf("cfgString(empty_string) = %q", v)
	}
}

func TestCfgBoolEdgeCases(t *testing.T) {
	cfg := cfgHelpersEdgeCaseData()
	if v := cfgBool(cfg, "false_bool"); v {
		t.Error("cfgBool(false_bool) = true")
	}
	if v := cfgBoolDefault(cfg, "false_bool", true); v {
		t.Error("cfgBoolDefault(false_bool, true) = true")
	}
}

func TestCfgDurationEdgeCases(t *testing.T) {
	cfg := cfgHelpersEdgeCaseData()
	if v := cfgDuration(cfg, "invalid_dur_str", testEdgeFallback5s); v != testEdgeFallback5s {
		t.Errorf("cfgDuration(invalid_dur_str) = %v", v)
	}
	if v := cfgDuration(cfg, "zero", testDurationDefault); v != 0 {
		t.Errorf("cfgDuration(zero) = %v", v)
	}
	if v := cfgDuration(cfg, "zero_float", testDurationDefault); v != 0 {
		t.Errorf("cfgDuration(zero_float) = %v", v)
	}
}

func TestInstanceConfigMapTypes(t *testing.T) {
	t.Run("instances as slice (wrong type)", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: testServerName},
			Toolkits: map[string]any{
				"trino": map[string]any{
					testInstancesKey: []string{"item1", "item2"}, // wrong type
				},
			},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
		}

		instanceCfg := p.getInstanceConfig("trino", "any")
		if instanceCfg != nil {
			t.Error("getInstanceConfig should return nil for wrong instances type")
		}
	})

	t.Run("kind config not a map", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Name: testServerName},
			Toolkits: map[string]any{
				"trino": "not-a-map",
			},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				"viewer": {
					DisplayName: "Viewer",
					Roles:       []string{testRoleViewer},
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
				testRoleAdmin: {
					DisplayName: "Admin",
					Roles:       []string{testRoleAdmin},
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
		t.Fatalf(testNewErrFmt, err)
	}

	// Verify all personas loaded
	pr := p.PersonaRegistry()

	viewer, ok := pr.Get(testRoleViewer)
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

	admin, ok := pr.Get(testRoleAdmin)
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: validKey,
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
		}
		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil")
		}
		_ = p.Close()
	})

	t.Run("invalid base64 signing key", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "http://localhost:8080",
				SigningKey: "", // Empty - should auto-generate
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
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
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
			OAuth: OAuthConfig{
				Enabled: false,
			},
		}

		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
		}

		if p.OAuthServer() != nil {
			t.Error("OAuthServer() should be nil when OAuth is disabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with pre-registered clients", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
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
			t.Fatalf(testNewErrFmt, err)
		}

		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil when OAuth is enabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with upstream IdP", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
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
			t.Fatalf(testNewErrFmt, err)
		}

		if p.OAuthServer() == nil {
			t.Error("OAuthServer() should not be nil when OAuth is enabled")
		}
		_ = p.Close()
	})

	t.Run("OAuth enabled with DCR", func(t *testing.T) {
		cfg := &Config{
			Server:   ServerConfig{Name: testServerName},
			Semantic: SemanticConfig{Provider: testProviderNoop},
			Query:    QueryConfig{Provider: testProviderNoop},
			Storage:  StorageConfig{Provider: testProviderNoop},
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
			t.Fatalf(testNewErrFmt, err)
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
		Server: ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{
			Provider: "datahub",
			Instance: testInstancePrimary,
			Lineage: datahubsemantic.LineageConfig{
				Enabled:             true,
				MaxHops:             testLineageMaxHops,
				Inherit:             []string{"glossary_terms", "descriptions", "tags"},
				ConflictResolution:  testConflictNearest,
				PreferColumnLineage: true,
				CacheTTL:            testLineageCacheTTL,
				Timeout:             testLineageTimeout,
			},
		},
		Query:   QueryConfig{Provider: testProviderNoop},
		Storage: StorageConfig{Provider: testProviderNoop},
		Toolkits: map[string]any{
			"datahub": map[string]any{
				testInstancesKey: map[string]any{
					testInstancePrimary: map[string]any{
						"url":   "http://datahub.example.com:8080/api/graphql",
						"token": "test-token",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
	if lineageCfg.MaxHops != testLineageMaxHops {
		t.Errorf("LineageConfig().MaxHops = %d, want %d", lineageCfg.MaxHops, testLineageMaxHops)
	}
	if len(lineageCfg.Inherit) != testLineageMaxHops {
		t.Errorf("LineageConfig().Inherit len = %d, want %d", len(lineageCfg.Inherit), testLineageMaxHops)
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
	if lineageCfg.ConflictResolution != testConflictNearest {
		t.Errorf("LineageConfig().ConflictResolution = %q, want %q", lineageCfg.ConflictResolution, testConflictNearest)
	}
	if !lineageCfg.PreferColumnLineage {
		t.Error("LineageConfig().PreferColumnLineage = false, want true")
	}
	if lineageCfg.CacheTTL != testLineageCacheTTL {
		t.Errorf("LineageConfig().CacheTTL = %v, want %v", lineageCfg.CacheTTL, testLineageCacheTTL)
	}
	if lineageCfg.Timeout != testLineageTimeout {
		t.Errorf("LineageConfig().Timeout = %v, want %v", lineageCfg.Timeout, testLineageTimeout)
	}
}

// createTestAppDir creates a temporary directory with test app files.
func createTestAppDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html><head></head><body>test</body></html>"), testFilePerms); err != nil {
		t.Fatalf("Failed to create test index.html: %v", err)
	}
	return dir
}

func TestConvertCSP_NilInput(t *testing.T) {
	result := convertCSP(nil)
	if result != nil {
		t.Error("convertCSP(nil) should return nil")
	}
}

func TestConvertCSP_ResourceDomains(t *testing.T) {
	cfg := &CSPAppConfig{
		ResourceDomains: []string{testCDNDomain, "https://fonts.googleapis.com"},
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
	}
	if len(result.ResourceDomains) != 2 {
		t.Errorf("ResourceDomains len = %d, want 2", len(result.ResourceDomains))
	}
	if result.ResourceDomains[0] != testCDNDomain {
		t.Errorf("ResourceDomains[0] = %q", result.ResourceDomains[0])
	}
}

func TestConvertCSP_ConnectDomains(t *testing.T) {
	cfg := &CSPAppConfig{
		ConnectDomains: []string{"https://api.example.com"},
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
	}
	if len(result.ConnectDomains) != 1 {
		t.Errorf("ConnectDomains len = %d, want 1", len(result.ConnectDomains))
	}
}

func TestConvertCSP_FrameDomains(t *testing.T) {
	cfg := &CSPAppConfig{
		FrameDomains: []string{"https://embed.example.com"},
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
	}
	if len(result.FrameDomains) != 1 {
		t.Errorf("FrameDomains len = %d, want 1", len(result.FrameDomains))
	}
}

func TestConvertCSP_ClipboardWrite(t *testing.T) {
	cfg := &CSPAppConfig{
		ClipboardWrite: true,
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
	}
	if result.Permissions == nil {
		t.Fatal("Permissions should not be nil when ClipboardWrite is true")
	}
	if result.Permissions.ClipboardWrite == nil {
		t.Error("ClipboardWrite should not be nil")
	}
}

func TestConvertCSP_NoPermissions(t *testing.T) {
	cfg := &CSPAppConfig{
		ClipboardWrite: false,
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
	}
	if result.Permissions != nil {
		t.Error("Permissions should be nil when ClipboardWrite is false")
	}
}

func TestConvertCSP_FullConfig(t *testing.T) {
	cfg := &CSPAppConfig{
		ResourceDomains: []string{testCDNDomain},
		ConnectDomains:  []string{"https://api.example.com"},
		FrameDomains:    []string{"https://embed.example.com"},
		ClipboardWrite:  true,
	}
	result := convertCSP(cfg)
	if result == nil {
		t.Fatal(testCSPNilMsg)
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
}

func TestInitMCPApps_DisabledByDefault(t *testing.T) {
	p := newTestPlatform(t)
	if p.mcpAppsRegistry != nil {
		t.Error("mcpAppsRegistry should be nil when MCPApps disabled")
	}
}

func TestInitMCPApps_EnabledWithFilesystemApp(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"query_results": {
					Enabled:    true,
					Tools:      []string{"trino_query"},
					AssetsPath: testAppDir,
					EntryPoint: testEntryPoint,
					Config: map[string]any{
						"chartCDN":         "https://cdn.example.com/chart.js",
						"defaultChartType": "bar",
						"maxTableRows":     testMaxTableRows,
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
}

func TestInitMCPApps_MissingAssets(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"missing_app": {
					Enabled:    true,
					Tools:      []string{testToolName},
					AssetsPath: "/nonexistent/path",
					EntryPoint: testEntryPoint,
				},
			},
		},
	}

	_, err := New(WithConfig(cfg))
	if err == nil {
		t.Fatal("New() should fail with missing assets")
	}
}

func TestInitMCPApps_DisabledAppNotRegistered(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"query_results": {
					Enabled:    false,
					AssetsPath: testAppDir,
					EntryPoint: testEntryPoint,
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	if p.mcpAppsRegistry == nil {
		t.Fatal("mcpAppsRegistry should not be nil")
	}
	if p.mcpAppsRegistry.HasApps() {
		t.Error("Registry should have no apps when all disabled")
	}
}

func TestInitMCPApps_CSPConfig(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"test_app": {
					Enabled:    true,
					Tools:      []string{testToolName},
					AssetsPath: testAppDir,
					EntryPoint: testEntryPoint,
					CSP: &CSPAppConfig{
						ResourceDomains: []string{testCDNDomain},
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
		t.Fatalf(testNewErrFmt, err)
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
}

func TestInitMCPApps_CustomResourceURI(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"custom_app": {
					Enabled:     true,
					Tools:       []string{testToolName},
					AssetsPath:  testAppDir,
					EntryPoint:  "index.html",
					ResourceURI: "ui://custom-resource",
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	app := p.mcpAppsRegistry.Get("custom_app")
	if app == nil {
		t.Fatal("custom_app should be registered")
	}
	if app.ResourceURI != "ui://custom-resource" {
		t.Errorf("ResourceURI = %q, want ui://custom-resource", app.ResourceURI)
	}
}

func TestInitMCPApps_DefaultEntryPoint(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"default_entry": {
					Enabled:    true,
					Tools:      []string{testToolName},
					AssetsPath: testAppDir,
					// EntryPoint omitted - should default to index.html
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	app := p.mcpAppsRegistry.Get("default_entry")
	if app == nil {
		t.Fatal("default_entry should be registered")
	}
	if app.EntryPoint != "index.html" {
		t.Errorf("EntryPoint = %q, want index.html", app.EntryPoint)
	}
}

func TestInitMCPApps_ValidationError(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Enabled: true,
			Apps: map[string]AppConfig{
				"invalid_app": {
					Enabled:    true,
					Tools:      []string{}, // Empty tools - validation should fail
					AssetsPath: testAppDir,
					EntryPoint: testEntryPoint,
				},
			},
		},
	}

	_, err := New(WithConfig(cfg))
	if err == nil {
		t.Error("New() should fail with empty tools list")
	}
}

func TestHintManager(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				testRoleAnalyst: {
					DisplayName: testDisplayAnalyst,
					Roles:       []string{testRoleAnalyst},
					Hints: map[string]string{
						"custom_tool": "This is a custom hint from persona",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Audit: AuditConfig{
			Enabled: false,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	// Platform should have been created without error
	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestInitAuditNoopWithoutDatabase(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Audit: AuditConfig{
			Enabled:       true,
			LogToolCalls:  true,
			RetentionDays: testRetentionDays,
		},
		// Database DSN intentionally left empty
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	// Should succeed with noop logger when DB not configured
	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestLoadPersonasWithFullPromptConfig(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{
			Definitions: map[string]PersonaDef{
				testRoleAnalyst: {
					DisplayName: testDisplayAnalyst,
					Description: "Analyzes data and runs queries",
					Roles:       []string{testRoleAnalyst},
					Tools: ToolRulesDef{
						Allow: []string{"trino_*"},
					},
					Prompts: PromptsDef{
						SystemPrefix: testSystemPrefix,
						SystemSuffix: "Be concise.",
						Instructions: "Check DataHub first.",
					},
					Priority: testPriority,
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	pr := p.PersonaRegistry()
	analyst, ok := pr.Get(testRoleAnalyst)
	if !ok {
		t.Fatal("analyst persona not found")
	}

	if analyst.Description != "Analyzes data and runs queries" {
		t.Errorf("Description = %q", analyst.Description)
	}
	if analyst.Priority != testPriority {
		t.Errorf("Priority = %d, want %d", analyst.Priority, testPriority)
	}
	if analyst.Prompts.SystemPrefix != testSystemPrefix {
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
	if !containsSubstr(fullPrompt, testSystemPrefix) {
		t.Error("fullPrompt missing SystemPrefix")
	}
	if !containsSubstr(fullPrompt, "Check DataHub first.") {
		t.Error("fullPrompt missing Instructions")
	}
	if !containsSubstr(fullPrompt, "Be concise.") {
		t.Error("fullPrompt missing SystemSuffix")
	}
}

func TestNew_NilToolkitsConfig(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		// Toolkits intentionally nil
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	// The default RuleEngine is always created in initTuning, so it should not be nil
	if p.RuleEngine() == nil {
		t.Error("RuleEngine() should not be nil even without explicit config")
	}
}

func TestNew_NoAuthenticators_FallsBackToNoop(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		// Auth intentionally empty — no OIDC, no API keys, no OAuth
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	// Should fall back to NoopAuthenticator (L761)
	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestNew_DefaultOAuthTTL(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		OAuth: OAuthConfig{
			Enabled: true,
			Issuer:  "http://localhost:8080",
			// No explicit TTLs — should use default 1h AccessTokenTTL (L332)
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
					testInstancesKey: map[string]any{
						testInstanceDefault: map[string]any{
							"url": "http://datahub:8080",
							// timeout not set — should default to 30s (L911)
						},
					},
				},
			},
		},
	}

	cfg := p.getDataHubConfig(testInstanceDefault)
	if cfg == nil {
		t.Fatal("getDataHubConfig() returned nil")
	}
	if cfg.Timeout != testDefaultDataHubTO {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, testDefaultDataHubTO)
	}
}

func TestNew_DefaultTrinoTimeout(t *testing.T) {
	p := &Platform{
		config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					testInstancesKey: map[string]any{
						testInstanceDefault: map[string]any{
							"host": "localhost",
							// timeout not set — should default to 120s (L939)
						},
					},
				},
			},
		},
	}

	cfg := p.getTrinoConfig(testInstanceDefault)
	if cfg == nil {
		t.Fatal("getTrinoConfig() returned nil")
	}
	if cfg.Timeout != testDefaultTrinoTO {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, testDefaultTrinoTO)
	}
}

func TestClose_NilAuditStore(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		// No audit configured — auditStore will be nil (L1084)
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
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
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	// Verify tags appear in the tool description (L59)
	desc := p.buildInfoToolDescription()
	if !containsSubstr(desc, "fireworks") {
		t.Errorf("description %q does not contain tag 'fireworks'", desc)
	}
	if !containsSubstr(desc, "retail") {
		t.Errorf("description %q does not contain tag 'retail'", desc)
	}
}

func TestNew_SigningKeyExactly32Bytes(t *testing.T) {
	// "aaaaaaaaaabbbbbbbbbbccccccccccdd" is exactly 32 bytes
	// base64 of 32 bytes of "a" repeated: use a known 32-byte string
	import32Bytes := "YWFhYWFhYWFhYWJiYmJiYmJiYmJjY2NjY2NjY2NjZGQ=" // "aaaaaaaaaabbbbbbbbbbccccccccccdd"
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
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

func TestSessionStore_Accessor(t *testing.T) {
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	store := p.SessionStore()
	if store == nil {
		t.Error("SessionStore() should return non-nil store (memory default)")
	}
}

func TestWithSessionStore_Option(t *testing.T) {
	injected := session.NewMemoryStore(5 * time.Minute)
	defer func() { _ = injected.Close() }()

	p := newTestPlatform(t, WithSessionStore(injected))
	defer func() { _ = p.Close() }()

	if p.SessionStore() != injected {
		t.Error("SessionStore() should return injected store")
	}
}

func TestInitSessions_UnknownStore(t *testing.T) {
	_, err := New(WithConfig(&Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Sessions: SessionsConfig{Store: "redis"},
	}))
	if err == nil {
		t.Fatal("expected error for unknown session store")
	}
	if !containsSubstr(err.Error(), "unknown session store") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInitSessions_DatabaseWithoutDB(t *testing.T) {
	_, err := New(WithConfig(&Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Sessions: SessionsConfig{Store: SessionStoreDatabase},
	}))
	if err == nil {
		t.Fatal("expected error for database store without db")
	}
	if !containsSubstr(err.Error(), "no database configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseDedupState(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := parseDedupState(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("non-map input", func(t *testing.T) {
		result := parseDedupState("not a map")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("typed map[string]time.Time (memory store)", func(t *testing.T) {
		now := time.Now()
		input := map[string]time.Time{
			"table1": now,
			"table2": now.Add(-5 * time.Minute),
		}
		result := parseDedupState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].Equal(now) {
			t.Errorf("table1 time mismatch")
		}
	})

	t.Run("map[string]any with time.Time values (JSON)", func(t *testing.T) {
		now := time.Now()
		input := map[string]any{
			"table1": now,
			"table2": now.Add(-5 * time.Minute),
		}
		result := parseDedupState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].Equal(now) {
			t.Errorf("table1 time mismatch")
		}
	})

	t.Run("map with RFC3339 string values", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Nanosecond)
		input := map[string]any{
			"table1": now.Format(time.RFC3339Nano),
		}
		result := parseDedupState(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
	})

	t.Run("map with invalid string skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": "not-a-timestamp",
		}
		result := parseDedupState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for bad timestamp, got %d", len(result))
		}
	})

	t.Run("map with unsupported type skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": 12345,
		}
		result := parseDedupState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for int value, got %d", len(result))
		}
	})
}

func TestFlushEnrichmentState(t *testing.T) {
	t.Run("nil cache or store is no-op", func(_ *testing.T) {
		p := &Platform{}
		p.flushEnrichmentState() // should not panic
	})

	t.Run("empty export is no-op", func(_ *testing.T) {
		cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
		store := session.NewMemoryStore(30 * time.Minute)
		defer func() { _ = store.Close() }()

		p := &Platform{sessionCache: cache, sessionStore: store}
		p.flushEnrichmentState() // nothing to flush
	})

	t.Run("flushes dedup state to store", func(t *testing.T) {
		cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
		store := session.NewMemoryStore(30 * time.Minute)
		defer func() { _ = store.Close() }()

		// Create a session in the store
		ctx := context.Background()
		sess := &session.Session{
			ID:        "flush-sess",
			ExpiresAt: time.Now().Add(30 * time.Minute),
			State:     make(map[string]any),
		}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatalf("create: %v", err)
		}

		// Mark table as sent in cache
		cache.MarkSent("flush-sess", "catalog.schema.users")

		p := &Platform{sessionCache: cache, sessionStore: store}
		p.flushEnrichmentState()

		// Verify state was persisted
		got, err := store.Get(ctx, "flush-sess")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.State == nil {
			t.Fatal("expected non-nil state")
		}
		dedup, ok := got.State["enrichment_dedup"]
		if !ok {
			t.Fatal("expected enrichment_dedup key in state")
		}
		tables, ok := dedup.(map[string]time.Time)
		if !ok {
			t.Fatalf("expected map[string]time.Time, got %T", dedup)
		}
		if _, exists := tables["catalog.schema.users"]; !exists {
			t.Error("expected catalog.schema.users in flushed dedup state")
		}
	})
}

func TestLoadPersistedEnrichmentState(t *testing.T) {
	t.Run("nil cache or store is no-op", func(_ *testing.T) {
		p := &Platform{}
		p.loadPersistedEnrichmentState() // should not panic
	})

	t.Run("loads dedup state from store", func(t *testing.T) {
		cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
		store := session.NewMemoryStore(30 * time.Minute)
		defer func() { _ = store.Close() }()

		ctx := context.Background()
		now := time.Now()
		sess := &session.Session{
			ID:        "load-sess",
			ExpiresAt: now.Add(30 * time.Minute),
			State: map[string]any{
				"enrichment_dedup": map[string]any{
					"catalog.schema.orders": now.Add(-2 * time.Minute),
				},
			},
		}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatalf("create: %v", err)
		}

		p := &Platform{sessionCache: cache, sessionStore: store}
		p.loadPersistedEnrichmentState()

		// Verify cache was populated
		if !cache.WasSentRecently("load-sess", "catalog.schema.orders") {
			t.Error("expected catalog.schema.orders to be marked as sent")
		}
	})

	t.Run("skips sessions without dedup state", func(t *testing.T) {
		cache := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
		store := session.NewMemoryStore(30 * time.Minute)
		defer func() { _ = store.Close() }()

		ctx := context.Background()
		sess := &session.Session{
			ID:        "no-dedup",
			ExpiresAt: time.Now().Add(30 * time.Minute),
			State:     map[string]any{"other_key": "value"},
		}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatalf("create: %v", err)
		}

		p := &Platform{sessionCache: cache, sessionStore: store}
		p.loadPersistedEnrichmentState()

		if cache.SessionCount() != 0 {
			t.Error("expected 0 sessions loaded")
		}
	})
}

func TestFlushLoadRoundTrip(t *testing.T) {
	// Simulate: populate cache, flush to store, create new cache, load from store
	store := session.NewMemoryStore(30 * time.Minute)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	// Create session in store
	sess := &session.Session{
		ID:        "rt-sess",
		ExpiresAt: time.Now().Add(30 * time.Minute),
		State:     make(map[string]any),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Phase 1: populate cache and flush
	cache1 := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	cache1.MarkSent("rt-sess", "catalog.schema.products")

	p1 := &Platform{sessionCache: cache1, sessionStore: store}
	p1.flushEnrichmentState()

	// Phase 2: load into new cache
	cache2 := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	p2 := &Platform{sessionCache: cache2, sessionStore: store}
	p2.loadPersistedEnrichmentState()

	if !cache2.WasSentRecently("rt-sess", "catalog.schema.products") {
		t.Error("expected round-trip to preserve dedup state")
	}
}

func TestInitKnowledge_Disabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: false,
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestInitKnowledge_EnabledWithoutDatabase(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: true,
		},
		// Database DSN intentionally left empty — should use noop store
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestInitKnowledge_ApplyEnabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: true,
			Apply: KnowledgeApplyConfig{
				Enabled:             true,
				DataHubConnection:   "primary",
				RequireConfirmation: true,
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestInitKnowledge_ApplyWithDataHubConnection(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: true,
			Apply: KnowledgeApplyConfig{
				Enabled:             true,
				DataHubConnection:   testInstanceDefault,
				RequireConfirmation: true,
			},
		},
		Toolkits: map[string]any{
			testToolkitKeyDatahub: map[string]any{
				testInstancesKey: map[string]any{
					testInstanceDefault: map[string]any{
						testCfgKeyURL:   "http://datahub:8080",
						testCfgKeyToken: "test-token",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}
}

func TestCreateDataHubWriter_NoConnection(t *testing.T) {
	p := &Platform{
		config: &Config{
			Knowledge: KnowledgeConfig{
				Apply: KnowledgeApplyConfig{
					DataHubConnection: "nonexistent",
				},
			},
			Toolkits: map[string]any{},
		},
	}

	writer, err := p.createDataHubWriter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, ok := writer.(*knowledgekit.NoopDataHubWriter); !ok {
		t.Error("expected NoopDataHubWriter when connection not found")
	}
}

func TestCreateDataHubWriter_WithConnection(t *testing.T) {
	p := &Platform{
		config: &Config{
			Knowledge: KnowledgeConfig{
				Apply: KnowledgeApplyConfig{
					DataHubConnection: testInstanceDefault,
				},
			},
			Toolkits: map[string]any{
				testToolkitKeyDatahub: map[string]any{
					testInstancesKey: map[string]any{
						testInstanceDefault: map[string]any{
							testCfgKeyURL:   "http://datahub:8080",
							testCfgKeyToken: "test-token",
						},
					},
				},
			},
		},
	}

	writer, err := p.createDataHubWriter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, ok := writer.(*knowledgekit.DataHubClientWriter); !ok {
		t.Errorf("expected DataHubClientWriter, got %T", writer)
	}
}

func TestCreateDataHubWriter_InvalidConfig(t *testing.T) {
	p := &Platform{
		config: &Config{
			Knowledge: KnowledgeConfig{
				Apply: KnowledgeApplyConfig{
					DataHubConnection: testInstanceDefault,
				},
			},
			Toolkits: map[string]any{
				testToolkitKeyDatahub: map[string]any{
					testInstancesKey: map[string]any{
						testInstanceDefault: map[string]any{
							testCfgKeyURL:   "",
							testCfgKeyToken: "",
						},
					},
				},
			},
		},
	}

	_, err := p.createDataHubWriter()
	if err == nil {
		t.Error("expected error for invalid datahub config")
	}
}

// containsSubstr checks if s contains substr using strings.Contains.
func containsSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}
