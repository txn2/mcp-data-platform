package platform

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/auth"
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
	noopAuth := &middleware.NoopAuthenticator{}
	authz := &middleware.NoopAuthorizer{}
	logger := &middleware.NoopAuditLogger{}

	p := newTestPlatform(t,
		WithAuthenticator(noopAuth),
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
					Context: ContextDef{
						DescriptionPrefix: testSystemPrefix,
					},
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
			Enabled: new(true),
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
								"use_path_style":    true,
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
		if !result.UsePathStyle {
			t.Error("expected UsePathStyle to be true")
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

func TestCreatePortalS3ClientPathStyle(t *testing.T) {
	p := &Platform{
		config: &Config{
			Portal: PortalConfig{
				S3Connection: "minio",
			},
			Toolkits: map[string]any{
				"s3": map[string]any{
					testInstancesKey: map[string]any{
						"minio": map[string]any{
							"region":         "us-east-1",
							"endpoint":       "http://localhost:9000",
							"use_path_style": true,
						},
					},
				},
			},
		},
	}

	client, err := p.createPortalS3Client()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
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

func TestInitMCPApps_EnabledByDefault(t *testing.T) {
	p := newTestPlatform(t)
	if p.mcpAppsRegistry == nil {
		t.Fatal("mcpAppsRegistry should not be nil — MCPApps enabled by default")
	}
	app := p.mcpAppsRegistry.Get("platform-info")
	if app == nil {
		t.Error("built-in platform-info should be registered by default")
	}
}

func TestInitMCPApps_DisabledExplicitly(t *testing.T) {
	disabled := false
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps:  MCPAppsConfig{Enabled: &disabled},
	}
	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	if p.mcpAppsRegistry != nil {
		t.Error("mcpAppsRegistry should be nil when MCPApps explicitly disabled")
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
			Enabled: new(true),
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
			Enabled: new(true),
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
			Enabled: new(true),
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
	if !p.mcpAppsRegistry.HasApps() {
		t.Error("Registry should have built-in platform-info even when user apps are disabled")
	}
	if p.mcpAppsRegistry.Get("query_results") != nil {
		t.Error("query_results should not be registered when disabled")
	}
	if p.mcpAppsRegistry.Get("platform-info") == nil {
		t.Error("built-in platform-info should always be registered")
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
			Enabled: new(true),
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
			Enabled: new(true),
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
			Enabled: new(true),
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
			Enabled: new(true),
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

func TestMCPAppsConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit true", new(true), true},
		{"explicit false", new(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &MCPAppsConfig{Enabled: tt.enabled}
			if got := cfg.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitMCPApps_BuiltinPlatformInfoWithBranding(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Apps: map[string]AppConfig{
				"platform-info": {
					Enabled: true,
					Tools:   []string{"platform_info"},
					Config: map[string]any{
						"brand_name": "ACME Corp",
						"brand_url":  "https://example.com",
					},
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	app := p.mcpAppsRegistry.Get("platform-info")
	if app == nil {
		t.Fatal("platform-info should be registered")
	}
	if app.Content == nil {
		t.Error("platform-info should use embedded content when no assets_path given")
	}
	cfgMap, ok := app.Config.(map[string]any)
	if !ok || cfgMap["brand_name"] != "ACME Corp" {
		t.Errorf("branding config not merged: %v", app.Config)
	}
}

func TestInitMCPApps_BuiltinPlatformInfoWithAssetsPathOverride(t *testing.T) {
	testAppDir := createTestAppDir(t)
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Apps: map[string]AppConfig{
				"platform-info": {
					Enabled:    true,
					Tools:      []string{"platform_info"},
					AssetsPath: testAppDir,
					EntryPoint: "index.html",
				},
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}

	app := p.mcpAppsRegistry.Get("platform-info")
	if app == nil {
		t.Fatal("platform-info should be registered")
	}
	if app.Content != nil {
		t.Error("Content should be nil when assets_path override is set")
	}
	if app.AssetsPath != testAppDir {
		t.Errorf("AssetsPath = %q, want %q", app.AssetsPath, testAppDir)
	}
}

func TestInitMCPApps_BuiltinPlatformInfoWithInvalidAssetsPath(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		MCPApps: MCPAppsConfig{
			Apps: map[string]AppConfig{
				"platform-info": {
					Enabled:    true,
					Tools:      []string{"platform_info"},
					AssetsPath: "/nonexistent/platform-info",
					EntryPoint: "index.html",
				},
			},
		},
	}

	_, err := New(WithConfig(cfg))
	if err == nil {
		t.Fatal("New() should fail when platform-info assets_path is invalid")
	}
}

func TestInitAuditNoopWhenDisabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Audit: AuditConfig{
			Enabled: new(false),
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
			Enabled:       new(true),
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
					Context: ContextDef{
						DescriptionPrefix:         testSystemPrefix,
						AgentInstructionsSuffix:   "Check DataHub first.",
						AgentInstructionsOverride: "",
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
	if analyst.Context.DescriptionPrefix != testSystemPrefix {
		t.Errorf("DescriptionPrefix = %q", analyst.Context.DescriptionPrefix)
	}
	if analyst.Context.AgentInstructionsSuffix != "Check DataHub first." {
		t.Errorf("AgentInstructionsSuffix = %q", analyst.Context.AgentInstructionsSuffix)
	}

	// Test ApplyDescription
	desc := analyst.ApplyDescription("Base description")
	if !containsSubstr(desc, testSystemPrefix) {
		t.Error("ApplyDescription missing DescriptionPrefix")
	}
	if !containsSubstr(desc, "Base description") {
		t.Error("ApplyDescription missing base description")
	}

	// Test ApplyAgentInstructions
	instructions := analyst.ApplyAgentInstructions("Base instructions")
	if !containsSubstr(instructions, "Check DataHub first.") {
		t.Error("ApplyAgentInstructions missing AgentInstructionsSuffix")
	}
	if !containsSubstr(instructions, "Base instructions") {
		t.Error("ApplyAgentInstructions missing base instructions")
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

	t.Run("typed SentTableEntry map (memory store)", func(t *testing.T) {
		now := time.Now()
		input := map[string]middleware.SentTableEntry{
			"table1": {SentAt: now, TokenCount: 100},
			"table2": {SentAt: now.Add(-5 * time.Minute), TokenCount: 200},
		}
		result := parseDedupState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].SentAt.Equal(now) {
			t.Errorf("table1 time mismatch")
		}
		if result["table1"].TokenCount != 100 {
			t.Errorf("table1 token count: got %d, want 100", result["table1"].TokenCount)
		}
		if result["table2"].TokenCount != 200 {
			t.Errorf("table2 token count: got %d, want 200", result["table2"].TokenCount)
		}
	})

	t.Run("new JSON format with object values", func(t *testing.T) {
		now := time.Now().UTC()
		input := map[string]any{
			"table1": map[string]any{
				"sent_at":     now.Format(time.RFC3339Nano),
				"token_count": float64(150),
			},
		}
		result := parseDedupState(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result["table1"].TokenCount != 150 {
			t.Errorf("token count: got %d, want 150", result["table1"].TokenCount)
		}
	})

	t.Run("old format: map[string]any with time.Time values", func(t *testing.T) {
		now := time.Now()
		input := map[string]any{
			"table1": now,
			"table2": now.Add(-5 * time.Minute),
		}
		result := parseDedupState(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
		if !result["table1"].SentAt.Equal(now) {
			t.Errorf("table1 time mismatch")
		}
		if result["table1"].TokenCount != 0 {
			t.Errorf("old format should have TokenCount 0, got %d", result["table1"].TokenCount)
		}
	})

	t.Run("old format: map with RFC3339 string values", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Nanosecond)
		input := map[string]any{
			"table1": now.Format(time.RFC3339Nano),
		}
		result := parseDedupState(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result["table1"].TokenCount != 0 {
			t.Errorf("old format should have TokenCount 0, got %d", result["table1"].TokenCount)
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

	t.Run("new format: missing sent_at skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": map[string]any{
				"token_count": float64(100),
			},
		}
		result := parseDedupState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for missing sent_at, got %d", len(result))
		}
	})

	t.Run("new format: invalid sent_at type skipped", func(t *testing.T) {
		input := map[string]any{
			"table1": map[string]any{
				"sent_at":     12345,
				"token_count": float64(100),
			},
		}
		result := parseDedupState(input)
		if len(result) != 0 {
			t.Errorf("expected 0 entries for invalid sent_at type, got %d", len(result))
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

		// Mark table as sent in cache with token count
		cache.MarkSent("flush-sess", "catalog.schema.users", 250)

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
		tables, ok := dedup.(map[string]middleware.SentTableEntry)
		if !ok {
			t.Fatalf("expected map[string]middleware.SentTableEntry, got %T", dedup)
		}
		entry, exists := tables["catalog.schema.users"]
		if !exists {
			t.Error("expected catalog.schema.users in flushed dedup state")
		}
		if entry.TokenCount != 250 {
			t.Errorf("token count: got %d, want 250", entry.TokenCount)
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
	cache1.MarkSent("rt-sess", "catalog.schema.products", 350)

	p1 := &Platform{sessionCache: cache1, sessionStore: store}
	p1.flushEnrichmentState()

	// Phase 2: load into new cache
	cache2 := middleware.NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	p2 := &Platform{sessionCache: cache2, sessionStore: store}
	p2.loadPersistedEnrichmentState()

	if !cache2.WasSentRecently("rt-sess", "catalog.schema.products") {
		t.Error("expected round-trip to preserve dedup state")
	}
	if tc := cache2.GetTokenCount("rt-sess", "catalog.schema.products"); tc != 350 {
		t.Errorf("expected token count 350 after round-trip, got %d", tc)
	}
}

func TestInitKnowledge_Disabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: new(false),
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
			Enabled: new(true),
		},
		// Database DSN intentionally left empty — knowledge tools should NOT register
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Error(testMCPServerNilMsg)
	}

	// Without a database, knowledge stores should be nil (tools not registered)
	if p.KnowledgeInsightStore() != nil {
		t.Error("KnowledgeInsightStore() should be nil when no database is configured")
	}

	// Toolkit registry should not contain knowledge toolkit
	for _, tk := range p.ToolkitRegistry().All() {
		if tk.Kind() == "knowledge" {
			t.Error("knowledge toolkit should not be registered without a database")
		}
	}
}

func TestInitKnowledge_ApplyEnabled_NoDatabase(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: new(true),
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

	// Without a database, knowledge (including apply) should not register
	if p.KnowledgeInsightStore() != nil {
		t.Error("KnowledgeInsightStore() should be nil without database")
	}
	if p.KnowledgeChangesetStore() != nil {
		t.Error("KnowledgeChangesetStore() should be nil without database")
	}
}

func TestInitKnowledge_ApplyWithDataHubConnection(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Knowledge: KnowledgeConfig{
			Enabled: new(true),
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

func TestAuditStore_Accessor(t *testing.T) {
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	// Without database config, auditStore is nil
	if p.AuditStore() != nil {
		t.Error("AuditStore() should be nil without database configured")
	}
}

func TestAuthenticator_Accessor(t *testing.T) {
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	// Even without auth config, authenticator should be non-nil (noop fallback)
	if p.Authenticator() == nil {
		t.Error("Authenticator() should not be nil (falls back to noop)")
	}
}

func TestAPIKeyAuthenticator_Accessor(t *testing.T) {
	t.Run("nil when API keys disabled", func(t *testing.T) {
		p := newTestPlatform(t)
		defer func() { _ = p.Close() }()

		if p.APIKeyAuthenticator() != nil {
			t.Error("APIKeyAuthenticator() should be nil without API keys configured")
		}
	})

	t.Run("non-nil when API keys enabled", func(t *testing.T) {
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
		defer func() { _ = p.Close() }()

		if p.APIKeyAuthenticator() == nil {
			t.Error("APIKeyAuthenticator() should not be nil when API keys enabled")
		}
	})
}

func TestKnowledgeInsightStore_Accessor(t *testing.T) {
	t.Run("nil when knowledge disabled", func(t *testing.T) {
		p := newTestPlatform(t)
		defer func() { _ = p.Close() }()

		if p.KnowledgeInsightStore() != nil {
			t.Error("KnowledgeInsightStore() should be nil when knowledge disabled")
		}
	})

	t.Run("nil when knowledge enabled but no database", func(t *testing.T) {
		cfg := &Config{
			Server:    ServerConfig{Name: testServerName},
			Semantic:  SemanticConfig{Provider: testProviderNoop},
			Query:     QueryConfig{Provider: testProviderNoop},
			Storage:   StorageConfig{Provider: testProviderNoop},
			Knowledge: KnowledgeConfig{Enabled: new(true)},
		}
		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
		}
		defer func() { _ = p.Close() }()

		if p.KnowledgeInsightStore() != nil {
			t.Error("KnowledgeInsightStore() should be nil when no database configured")
		}
	})
}

func TestKnowledgeChangesetStore_Accessor(t *testing.T) {
	t.Run("nil when knowledge disabled", func(t *testing.T) {
		p := newTestPlatform(t)
		defer func() { _ = p.Close() }()

		if p.KnowledgeChangesetStore() != nil {
			t.Error("KnowledgeChangesetStore() should be nil when knowledge disabled")
		}
	})

	t.Run("nil when knowledge enabled but no database", func(t *testing.T) {
		cfg := &Config{
			Server:    ServerConfig{Name: testServerName},
			Semantic:  SemanticConfig{Provider: testProviderNoop},
			Query:     QueryConfig{Provider: testProviderNoop},
			Storage:   StorageConfig{Provider: testProviderNoop},
			Knowledge: KnowledgeConfig{Enabled: new(true)},
		}
		p, err := New(WithConfig(cfg))
		if err != nil {
			t.Fatalf(testNewErrFmt, err)
		}
		defer func() { _ = p.Close() }()

		if p.KnowledgeChangesetStore() != nil {
			t.Error("KnowledgeChangesetStore() should be nil without database")
		}
	})
}

func TestKnowledgeDataHubWriter_Accessor(t *testing.T) {
	t.Run("nil when knowledge disabled", func(t *testing.T) {
		p := newTestPlatform(t)
		defer func() { _ = p.Close() }()

		if p.KnowledgeDataHubWriter() != nil {
			t.Error("KnowledgeDataHubWriter() should be nil when knowledge disabled")
		}
	})
}

// containsSubstr checks if s contains substr using strings.Contains.
func containsSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestPlatform_ConfigStore_FileMode(t *testing.T) {
	cfg := &Config{
		ConfigStore: ConfigStoreConfig{Mode: "file"},
		Server:      ServerConfig{Name: testServerName},
	}
	applyDefaults(cfg)

	p, err := New(
		WithConfig(cfg),
		WithSemanticProvider(semantic.NewNoopProvider()),
		WithQueryProvider(query.NewNoopProvider()),
		WithStorageProvider(storage.NewNoopProvider()),
		WithPersonaRegistry(persona.NewRegistry()),
		WithToolkitRegistry(registry.NewRegistry()),
		WithAuthenticator(&middleware.NoopAuthenticator{}),
		WithAuthorizer(&middleware.NoopAuthorizer{}),
		WithAuditLogger(&middleware.NoopAuditLogger{}),
		WithSessionStore(session.NewMemoryStore(time.Hour)),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	cs := p.ConfigStore()
	if cs == nil {
		t.Fatal("ConfigStore() returned nil")
	}
	if cs.Mode() != "file" {
		t.Errorf("ConfigStore().Mode() = %q, want %q", cs.Mode(), "file")
	}
}

func TestNew_WithToolVisibilityFilter(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Tools: ToolsConfig{
			Allow: []string{"trino_*", "datahub_*"},
			Deny:  []string{"*_delete_*"},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.MCPServer() == nil {
		t.Fatal(testMCPServerNilMsg)
	}
}

func TestPlatformTools(t *testing.T) {
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	tools := p.PlatformTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 platform tools, got %d", len(tools))
	}

	// Verify platform_info
	if tools[0].Name != "platform_info" {
		t.Errorf("expected tool[0] name platform_info, got %s", tools[0].Name)
	}
	if tools[0].Kind != "platform" {
		t.Errorf("expected tool[0] kind platform, got %s", tools[0].Kind)
	}

	// Verify list_connections
	if tools[1].Name != "list_connections" {
		t.Errorf("expected tool[1] name list_connections, got %s", tools[1].Name)
	}
	if tools[1].Kind != "platform" {
		t.Errorf("expected tool[1] kind platform, got %s", tools[1].Kind)
	}
}

func TestInjectToolkitPlatformConfig(t *testing.T) {
	t.Run("nil toolkits", func(_ *testing.T) {
		p := &Platform{config: &Config{
			Progress: ProgressConfig{Enabled: true},
		}}
		// Should not panic when Toolkits is nil.
		p.injectToolkitPlatformConfig()
	})

	t.Run("progress disabled", func(t *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{"host": "localhost"},
					},
				},
			},
			Progress: ProgressConfig{Enabled: false},
		}}
		p.injectToolkitPlatformConfig()

		instanceCfg, ok := p.config.Toolkits["trino"].(map[string]any)["instances"].(map[string]any)["primary"].(map[string]any) //nolint:errcheck // test assertion chain
		if !ok {
			t.Fatal("unexpected config structure")
		}
		if _, exists := instanceCfg["progress_enabled"]; exists {
			t.Error("progress_enabled should not be set when progress is disabled")
		}
	})

	t.Run("no trino toolkit", func(_ *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"datahub": map[string]any{},
			},
			Progress: ProgressConfig{Enabled: true},
		}}
		// Should not panic when no trino toolkit exists.
		p.injectToolkitPlatformConfig()
	})

	t.Run("trino not map", func(_ *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": "invalid",
			},
			Progress: ProgressConfig{Enabled: true},
		}}
		// Should not panic when trino config is not a map.
		p.injectToolkitPlatformConfig()
	})

	t.Run("instances not map", func(_ *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": "invalid",
				},
			},
			Progress: ProgressConfig{Enabled: true},
		}}
		// Should not panic when instances is not a map.
		p.injectToolkitPlatformConfig()
	})

	t.Run("injects progress_enabled", func(t *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"primary":   map[string]any{"host": "host1"},
						"secondary": map[string]any{"host": "host2"},
					},
				},
			},
			Progress: ProgressConfig{Enabled: true},
		}}
		p.injectToolkitPlatformConfig()

		instances, ok := p.config.Toolkits["trino"].(map[string]any)["instances"].(map[string]any) //nolint:errcheck // test assertion chain
		if !ok {
			t.Fatal("unexpected config structure")
		}
		for name, v := range instances {
			instanceCfg, castOK := v.(map[string]any) //nolint:errcheck // test assertion chain
			if !castOK {
				t.Fatalf("instance %q: unexpected type", name)
			}
			got, gotOK := instanceCfg["progress_enabled"].(bool)
			if !gotOK || !got {
				t.Errorf("instance %q: progress_enabled = %v, want true", name, got)
			}
		}
	})

	t.Run("instance not map skipped", func(t *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"valid":   map[string]any{"host": "host1"},
						"invalid": "not-a-map",
					},
				},
			},
			Progress: ProgressConfig{Enabled: true},
		}}
		// Should not panic when an instance is not a map.
		p.injectToolkitPlatformConfig()

		validCfg, ok := p.config.Toolkits["trino"].(map[string]any)["instances"].(map[string]any)["valid"].(map[string]any) //nolint:errcheck // test assertion chain
		if !ok {
			t.Fatal("unexpected config structure")
		}
		got, gotOK := validCfg["progress_enabled"].(bool)
		if !gotOK || !got {
			t.Errorf("valid instance: progress_enabled = %v, want true", got)
		}
	})
}

func TestInjectToolkitPlatformConfig_UnwrapJSON(t *testing.T) {
	t.Run("injects unwrap_json_default when enabled", func(t *testing.T) {
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{"host": "host1"},
					},
				},
			},
			// UnwrapJSON defaults to true (nil = enabled).
		}}
		p.injectToolkitPlatformConfig()

		instanceCfg, ok := p.config.Toolkits["trino"].(map[string]any)["instances"].(map[string]any)["primary"].(map[string]any) //nolint:errcheck // test assertion chain
		if !ok {
			t.Fatal("unexpected config structure")
		}
		got, gotOK := instanceCfg["unwrap_json_default"].(bool)
		if !gotOK || !got {
			t.Errorf("unwrap_json_default = %v, want true", got)
		}
	})

	t.Run("does not inject unwrap_json_default when disabled", func(t *testing.T) {
		unwrapFalse := false
		p := &Platform{config: &Config{
			Toolkits: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"primary": map[string]any{"host": "host1"},
					},
				},
			},
			Injection: InjectionConfig{UnwrapJSON: &unwrapFalse},
		}}
		p.injectToolkitPlatformConfig()

		instanceCfg, ok := p.config.Toolkits["trino"].(map[string]any)["instances"].(map[string]any)["primary"].(map[string]any) //nolint:errcheck // test assertion chain
		if !ok {
			t.Fatal("unexpected config structure")
		}
		if _, exists := instanceCfg["unwrap_json_default"]; exists {
			t.Error("unwrap_json_default should not be set when unwrap_json is disabled")
		}
	})
}

func TestBuildServerCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		wantTools     bool
		wantLogging   bool
		wantResources bool
		wantPrompts   bool
	}{
		{
			name:          "minimal config: tools and logging always present",
			config:        Config{},
			wantTools:     true,
			wantLogging:   true,
			wantResources: false,
			wantPrompts:   true,
		},
		{
			name: "resources enabled",
			config: Config{
				Resources: ResourcesConfig{Enabled: true},
			},
			wantTools:     true,
			wantLogging:   true,
			wantResources: true,
			wantPrompts:   true,
		},
		{
			name: "prompts configured via server.prompts",
			config: Config{
				Server: ServerConfig{
					Prompts: []PromptConfig{{Name: "test", Content: "test"}},
				},
			},
			wantTools:     true,
			wantLogging:   true,
			wantResources: false,
			wantPrompts:   true,
		},
		{
			name: "prompts configured via prompts_dir",
			config: Config{
				Tuning: TuningConfig{PromptsDir: "/some/dir"},
			},
			wantTools:     true,
			wantLogging:   true,
			wantResources: false,
			wantPrompts:   true,
		},
		{
			name: "prompts configured via knowledge enabled",
			config: Config{
				Knowledge: KnowledgeConfig{Enabled: new(true)},
			},
			wantTools:     true,
			wantLogging:   true,
			wantResources: false,
			wantPrompts:   true,
		},
		{
			name: "all capabilities enabled",
			config: Config{
				Resources: ResourcesConfig{Enabled: true},
				Knowledge: KnowledgeConfig{Enabled: new(true)},
			},
			wantTools:     true,
			wantLogging:   true,
			wantResources: true,
			wantPrompts:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{config: &tt.config}
			caps := p.buildServerCapabilities()

			if caps == nil {
				t.Fatal("buildServerCapabilities returned nil")
			}
			if (caps.Tools != nil) != tt.wantTools {
				t.Errorf("Tools: got %v, want present=%v", caps.Tools, tt.wantTools)
			}
			if (caps.Logging != nil) != tt.wantLogging {
				t.Errorf("Logging: got %v, want present=%v", caps.Logging, tt.wantLogging)
			}
			if (caps.Resources != nil) != tt.wantResources {
				t.Errorf("Resources: got %v, want present=%v", caps.Resources, tt.wantResources)
			}
			if (caps.Prompts != nil) != tt.wantPrompts {
				t.Errorf("Prompts: got %v, want present=%v", caps.Prompts, tt.wantPrompts)
			}
		})
	}
}

func TestConvertIconDefs(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		got := convertIconDefs(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := convertIconDefs(map[string]IconDef{})
		if got != nil {
			t.Errorf("expected nil for empty map, got %v", got)
		}
	})

	t.Run("converts entries", func(t *testing.T) {
		input := map[string]IconDef{
			"trino_query": {Source: "https://example.com/trino.svg", MIMEType: "image/svg+xml"},
			"s3_list":     {Source: "https://example.com/s3.png", MIMEType: "image/png"},
		}
		got := convertIconDefs(input)
		if len(got) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(got))
		}
		if got["trino_query"].Source != "https://example.com/trino.svg" {
			t.Errorf("trino_query source = %q", got["trino_query"].Source)
		}
		if got["trino_query"].MIMEType != "image/svg+xml" {
			t.Errorf("trino_query mime = %q", got["trino_query"].MIMEType)
		}
		if got["s3_list"].Source != "https://example.com/s3.png" {
			t.Errorf("s3_list source = %q", got["s3_list"].Source)
		}
	})
}

func TestAddIconMiddleware(t *testing.T) {
	t.Run("disabled does nothing", func(_ *testing.T) {
		p := &Platform{
			config: &Config{Icons: IconsConfig{Enabled: false}},
		}
		// Should not panic even without mcpServer
		p.addIconMiddleware()
	})

	t.Run("enabled with config", func(_ *testing.T) {
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
		p := &Platform{
			config: &Config{
				Icons: IconsConfig{
					Enabled: true,
					Tools: map[string]IconDef{
						"trino_query": {Source: "https://example.com/trino.svg", MIMEType: "image/svg+xml"},
					},
					Resources: map[string]IconDef{
						"schema://test": {Source: "https://example.com/schema.svg"},
					},
					Prompts: map[string]IconDef{
						"knowledge": {Source: "https://example.com/knowledge.svg"},
					},
				},
			},
			mcpServer: server,
		}
		// Should not panic and should register middleware
		p.addIconMiddleware()
	})
}

func TestInjectToolkitPlatformConfig_Elicitation(t *testing.T) {
	t.Run("injects elicitation config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Elicitation: ElicitationConfig{
					Enabled: true,
					CostEstimation: CostEstimationConfig{
						Enabled:      true,
						RowThreshold: 500000,
					},
					PIIConsent: PIIConsentConfig{Enabled: true},
				},
				Toolkits: map[string]any{
					toolkitKindTrino: map[string]any{
						"instances": map[string]any{
							"primary": map[string]any{
								"host": "localhost",
								"user": "test",
							},
						},
					},
				},
			},
		}

		p.injectToolkitPlatformConfig()

		instances := p.trinoInstanceConfigs()
		primaryCfg, ok := instances["primary"].(map[string]any)
		if !ok {
			t.Fatal("primary instance config not found")
		}

		elicit, ok := primaryCfg["elicitation"].(map[string]any)
		if !ok {
			t.Fatal("elicitation config not injected")
		}
		if enabled, _ := elicit["enabled"].(bool); !enabled {
			t.Error("elicitation.enabled should be true")
		}
		cost, ok := elicit["cost_estimation"].(map[string]any)
		if !ok {
			t.Fatal("cost_estimation not found")
		}
		if enabled, _ := cost["enabled"].(bool); !enabled {
			t.Error("cost_estimation.enabled should be true")
		}
		if cost["row_threshold"] != int64(500000) {
			t.Errorf("row_threshold = %v, want 500000", cost["row_threshold"])
		}
		pii, ok := elicit["pii_consent"].(map[string]any)
		if !ok {
			t.Fatal("pii_consent not found")
		}
		if enabled, _ := pii["enabled"].(bool); !enabled {
			t.Error("pii_consent.enabled should be true")
		}
	})

	t.Run("both progress and elicitation", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Progress:    ProgressConfig{Enabled: true},
				Elicitation: ElicitationConfig{Enabled: true},
				Toolkits: map[string]any{
					toolkitKindTrino: map[string]any{
						"instances": map[string]any{
							"primary": map[string]any{
								"host": "localhost",
							},
						},
					},
				},
			},
		}

		p.injectToolkitPlatformConfig()

		instances := p.trinoInstanceConfigs()
		primaryCfg, ok := instances["primary"].(map[string]any)
		if !ok {
			t.Fatal("primary instance config not found")
		}

		if enabled, _ := primaryCfg["progress_enabled"].(bool); !enabled {
			t.Error("progress_enabled should be injected")
		}
		if _, ok := primaryCfg["elicitation"].(map[string]any); !ok {
			t.Error("elicitation config should be injected")
		}
	})
}

func TestNew_WorkflowGatingEnabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Workflow: WorkflowConfig{
			RequireDiscoveryBeforeQuery: true,
			Escalation: EscalationConfig{
				AfterWarnings: 5,
			},
		},
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.workflowTracker == nil {
		t.Fatal("workflowTracker should be initialized when workflow gating is enabled")
	}

	// Verify tracker has default tools configured
	discoveryNames := p.workflowTracker.DiscoveryToolNames()
	if len(discoveryNames) == 0 {
		t.Error("discovery tools should be populated with defaults")
	}

	queryNames := p.workflowTracker.QueryToolNames()
	if len(queryNames) == 0 {
		t.Error("query tools should be populated with defaults")
	}
}

func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return m
}

func TestInjectPortalLogo(t *testing.T) {
	svgContent := `<svg viewBox="0 0 40 40"><circle cx="20" cy="20" r="10"/></svg>`

	t.Run("fetches SVG and injects as logo_svg", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: srv.URL + "/logo.svg"},
		}}
		cfg := map[string]any{"brand_name": "Test"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_svg"] != svgContent {
			t.Errorf("logo_svg = %v, want %q", m["logo_svg"], svgContent)
		}
		if m["logo_url"] != nil {
			t.Error("logo_url should be nil when SVG was fetched")
		}
	})

	t.Run("falls back to logo_url on non-SVG content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not-svg"))
		}))
		defer srv.Close()

		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: srv.URL + "/logo.png"},
		}}
		cfg := map[string]any{"brand_name": "Test"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_url"] != srv.URL+"/logo.png" {
			t.Errorf("logo_url = %v, want %q", m["logo_url"], srv.URL+"/logo.png")
		}
		if m["logo_svg"] != nil {
			t.Error("logo_svg should be nil for non-SVG")
		}
	})

	t.Run("falls back to logo_url on fetch error", func(t *testing.T) {
		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: "http://127.0.0.1:1/unreachable.svg"},
		}}
		cfg := map[string]any{"brand_name": "Test"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_url"] != "http://127.0.0.1:1/unreachable.svg" {
			t.Errorf("logo_url = %v, want unreachable URL", m["logo_url"])
		}
	})

	t.Run("does not overwrite explicit logo_svg", func(t *testing.T) {
		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: "https://example.com/logo.svg"},
		}}
		cfg := map[string]any{"logo_svg": "<svg>custom</svg>"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_svg"] != "<svg>custom</svg>" {
			t.Errorf("logo_svg was overwritten: %v", m["logo_svg"])
		}
	})

	t.Run("does not overwrite explicit logo_url", func(t *testing.T) {
		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: "https://example.com/logo.svg"},
		}}
		cfg := map[string]any{"logo_url": "https://other.com/logo.png"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_url"] != "https://other.com/logo.png" {
			t.Errorf("logo_url = %v, want %q", m["logo_url"], "https://other.com/logo.png")
		}
	})

	t.Run("no-op when portal logo is empty", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		cfg := map[string]any{"brand_name": "Test"}
		m := mustMap(t, p.injectPortalLogo(cfg))
		if m["logo_url"] != nil {
			t.Errorf("logo_url should be nil when portal logo is empty, got %v", m["logo_url"])
		}
	})

	t.Run("creates map when config is nil", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		p := &Platform{config: &Config{
			Portal: PortalConfig{Logo: srv.URL + "/logo.svg"},
		}}
		m := mustMap(t, p.injectPortalLogo(nil))
		if m["logo_svg"] != svgContent {
			t.Errorf("logo_svg = %v, want %q", m["logo_svg"], svgContent)
		}
	})
}

func TestFetchLogoSVG(t *testing.T) {
	svgContent := `<svg viewBox="0 0 40 40"><circle cx="20" cy="20" r="10"/></svg>`

	t.Run("returns SVG content", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		got, err := fetchLogoSVG(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != svgContent {
			t.Errorf("got %q, want %q", got, svgContent)
		}
	})

	t.Run("rejects non-SVG content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNG"))
		}))
		defer srv.Close()

		_, err := fetchLogoSVG(srv.URL + "/logo.png")
		if err == nil {
			t.Fatal("expected error for non-SVG content type")
		}
	})

	t.Run("rejects non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := fetchLogoSVG(srv.URL + "/missing.svg")
		if err == nil {
			t.Fatal("expected error for 404")
		}
	})

	t.Run("rejects non-HTTP scheme", func(t *testing.T) {
		_, err := fetchLogoSVG("ftp://example.com/logo.svg")
		if err == nil {
			t.Fatal("expected error for non-HTTP scheme")
		}
	})

	t.Run("handles SVG with charset in content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		got, err := fetchLogoSVG(srv.URL + "/logo.svg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != svgContent {
			t.Errorf("got %q, want %q", got, svgContent)
		}
	})
}

func TestBrandURL(t *testing.T) {
	t.Run("returns empty when not set", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		if got := p.BrandURL(); got != "" {
			t.Errorf("BrandURL() = %q, want empty", got)
		}
	})

	t.Run("returns cached value from injectPortalLogo", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		cfg := map[string]any{"brand_url": "https://example.com"}
		_ = p.injectPortalLogo(cfg)
		if got := p.BrandURL(); got != "https://example.com" {
			t.Errorf("BrandURL() = %q, want %q", got, "https://example.com")
		}
	})
}

func TestInjectPortalLogo_CachesBrandURL(t *testing.T) {
	t.Run("caches brand_url from config", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		cfg := map[string]any{"brand_url": "https://platform.io"}
		_ = p.injectPortalLogo(cfg)
		if p.resolvedBrandURL != "https://platform.io" {
			t.Errorf("resolvedBrandURL = %q, want %q", p.resolvedBrandURL, "https://platform.io")
		}
	})

	t.Run("caches brand_url even without portal logo", func(t *testing.T) {
		p := &Platform{config: &Config{}} // no Portal.Logo
		cfg := map[string]any{"brand_url": "https://noportallogo.io", "logo_svg": "<svg/>"}
		_ = p.injectPortalLogo(cfg)
		if p.resolvedBrandURL != "https://noportallogo.io" {
			t.Errorf("resolvedBrandURL = %q, want %q", p.resolvedBrandURL, "https://noportallogo.io")
		}
		// Also verify logo_svg was cached even without portal.Logo
		if p.resolvedBrandLogoSVG != "<svg/>" {
			t.Errorf("resolvedBrandLogoSVG = %q, want %q", p.resolvedBrandLogoSVG, "<svg/>")
		}
	})

	t.Run("does not set brand_url when absent", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		cfg := map[string]any{"brand_name": "Test"}
		_ = p.injectPortalLogo(cfg)
		if p.resolvedBrandURL != "" {
			t.Errorf("resolvedBrandURL = %q, want empty", p.resolvedBrandURL)
		}
	})
}

func TestResolveImplementorLogo(t *testing.T) {
	svgContent := `<svg viewBox="0 0 32 32"><rect width="32" height="32"/></svg>`

	t.Run("fetches and caches SVG", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(svgContent))
		}))
		defer srv.Close()

		p := &Platform{config: &Config{
			Portal: PortalConfig{Implementor: ImplementorConfig{Logo: srv.URL + "/impl.svg"}},
		}}

		got := p.ResolveImplementorLogo()
		if got != svgContent {
			t.Errorf("ResolveImplementorLogo() = %q, want %q", got, svgContent)
		}

		// Second call should return cached value (no HTTP request)
		srv.Close()
		got2 := p.ResolveImplementorLogo()
		if got2 != svgContent {
			t.Errorf("cached ResolveImplementorLogo() = %q, want %q", got2, svgContent)
		}
	})

	t.Run("returns empty when logo URL is empty", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		if got := p.ResolveImplementorLogo(); got != "" {
			t.Errorf("ResolveImplementorLogo() = %q, want empty", got)
		}
	})

	t.Run("returns empty on fetch failure", func(t *testing.T) {
		p := &Platform{config: &Config{
			Portal: PortalConfig{Implementor: ImplementorConfig{Logo: "http://127.0.0.1:1/unreachable.svg"}},
		}}
		if got := p.ResolveImplementorLogo(); got != "" {
			t.Errorf("ResolveImplementorLogo() = %q, want empty on fetch failure", got)
		}
	})
}

func TestNew_WorkflowGatingDisabled(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: testServerName},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		// Workflow not set — disabled by default
	}

	p, err := New(WithConfig(cfg))
	if err != nil {
		t.Fatalf(testNewErrFmt, err)
	}
	defer func() { _ = p.Close() }()

	if p.workflowTracker != nil {
		t.Error("workflowTracker should be nil when workflow gating is disabled")
	}
}

func TestBuildConfigEntryMap(t *testing.T) {
	p := &Platform{
		config: &Config{
			Server: ServerConfig{
				Description:       "test desc",
				AgentInstructions: "test instructions",
			},
		},
	}
	m := p.buildConfigEntryMap()
	if m["server.description"] != "test desc" {
		t.Errorf("description = %q, want %q", m["server.description"], "test desc")
	}
	if m["server.agent_instructions"] != "test instructions" {
		t.Errorf("agent_instructions = %q, want %q", m["server.agent_instructions"], "test instructions")
	}
}

func TestApplyConfigEntryPlatform(t *testing.T) {
	p := &Platform{config: &Config{}}
	p.applyConfigEntry("server.description", "new desc")
	if p.config.Server.Description != "new desc" {
		t.Errorf("Description = %q, want %q", p.config.Server.Description, "new desc")
	}
	p.applyConfigEntry("server.agent_instructions", "new instr")
	if p.config.Server.AgentInstructions != "new instr" {
		t.Errorf("AgentInstructions = %q, want %q", p.config.Server.AgentInstructions, "new instr")
	}
}

func TestFileDefaults(t *testing.T) {
	p := &Platform{
		fileDefaults: map[string]string{
			"server.description": "file desc",
		},
	}
	fd := p.FileDefaults()
	if fd["server.description"] != "file desc" {
		t.Errorf("FileDefaults[server.description] = %q, want %q", fd["server.description"], "file desc")
	}
}

func TestInitConfigStoreNoDatabase(t *testing.T) {
	p := &Platform{
		config: &Config{
			Server: ServerConfig{
				Description:       "file desc",
				AgentInstructions: "file instr",
			},
		},
	}
	if err := p.initConfigStore(); err != nil {
		t.Fatalf("initConfigStore() error = %v", err)
	}
	if p.configStore == nil {
		t.Fatal("configStore should not be nil")
	}
	if p.configStore.Mode() != "file" {
		t.Errorf("Mode() = %q, want %q", p.configStore.Mode(), "file")
	}
	if p.fileDefaults["server.description"] != "file desc" {
		t.Errorf("fileDefaults[server.description] = %q, want %q", p.fileDefaults["server.description"], "file desc")
	}
}

func TestDecodeEncryptionKey(t *testing.T) {
	t.Run("hex encoded", func(t *testing.T) {
		// 32 bytes as hex = 64 hex chars
		hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		key := decodeEncryptionKey(hexKey)
		if len(key) != 32 {
			t.Errorf("expected 32 bytes, got %d", len(key))
		}
	})

	t.Run("base64 encoded", func(t *testing.T) {
		// 32 random bytes as base64
		raw := make([]byte, 32)
		for i := range raw {
			raw[i] = byte(i)
		}
		b64 := base64.StdEncoding.EncodeToString(raw)
		key := decodeEncryptionKey(b64)
		if len(key) != 32 {
			t.Errorf("expected 32 bytes, got %d", len(key))
		}
	})

	t.Run("raw 32 bytes", func(t *testing.T) {
		rawKey := "01234567890123456789012345678901" // exactly 32 chars
		key := decodeEncryptionKey(rawKey)
		if len(key) != 32 {
			t.Errorf("expected 32 bytes, got %d", len(key))
		}
	})
}

func TestMergeDBConnectionsIntoConfig(t *testing.T) {
	t.Run("merges DB connections into enabled toolkit", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"trino": map[string]any{cfgKeyEnabled: true},
				},
			},
			connectionStore: &mockConnectionStoreForTest{
				instances: []ConnectionInstance{
					{Kind: "trino", Name: "prod", Config: map[string]any{"host": "trino.local"}},
				},
			},
		}
		p.mergeDBConnectionsIntoConfig()

		kindMap, ok := p.config.Toolkits["trino"].(map[string]any)
		if !ok {
			t.Fatal("trino kind map should exist")
		}
		instances, ok := kindMap[cfgKeyInstances].(map[string]any)
		if !ok {
			t.Fatal("instances map should exist")
		}
		if _, ok := instances["prod"]; !ok {
			t.Error("prod instance should exist")
		}
	})

	t.Run("skips disabled toolkit kind", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"datahub": map[string]any{cfgKeyEnabled: false},
				},
			},
			connectionStore: &mockConnectionStoreForTest{
				instances: []ConnectionInstance{
					{Kind: "datahub", Name: "catalog", Config: map[string]any{"endpoint": "http://localhost"}},
				},
			},
		}
		p.mergeDBConnectionsIntoConfig()

		kindMap, ok := p.config.Toolkits["datahub"].(map[string]any)
		if !ok {
			t.Fatal("datahub kind map should exist")
		}
		if _, ok := kindMap[cfgKeyInstances]; ok {
			t.Error("instances should not be created for disabled kind")
		}
	})

	t.Run("skips kind not in config", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{},
			},
			connectionStore: &mockConnectionStoreForTest{
				instances: []ConnectionInstance{
					{Kind: "trino", Name: "prod", Config: map[string]any{"host": "trino.local"}},
				},
			},
		}
		p.mergeDBConnectionsIntoConfig()

		if _, ok := p.config.Toolkits["trino"]; ok {
			t.Error("trino kind should not be created when not in config")
		}
	})

	t.Run("file config takes precedence", func(t *testing.T) {
		p := &Platform{
			config: &Config{
				Toolkits: map[string]any{
					"trino": map[string]any{
						cfgKeyEnabled: true,
						cfgKeyInstances: map[string]any{
							"prod": map[string]any{"host": "file-host"},
						},
					},
				},
			},
			connectionStore: &mockConnectionStoreForTest{
				instances: []ConnectionInstance{
					{Kind: "trino", Name: "prod", Config: map[string]any{"host": "db-host"}},
				},
			},
		}
		p.mergeDBConnectionsIntoConfig()

		kindMap, ok := p.config.Toolkits["trino"].(map[string]any)
		if !ok {
			t.Fatal("trino kind map should exist")
		}
		instances, ok := kindMap[cfgKeyInstances].(map[string]any)
		if !ok {
			t.Fatal("instances map should exist")
		}
		prodCfg, ok := instances["prod"].(map[string]any)
		if !ok {
			t.Fatal("prod config should exist")
		}
		if prodCfg["host"] != "file-host" {
			t.Errorf("expected file-host, got %v", prodCfg["host"])
		}
	})

	t.Run("nil store is safe", func(_ *testing.T) {
		p := &Platform{config: &Config{}}
		p.mergeDBConnectionsIntoConfig() // should not panic
	})
}

// mockConnectionStoreForTest is a simple mock for testing merge logic.
type mockConnectionStoreForTest struct {
	instances []ConnectionInstance
}

func (m *mockConnectionStoreForTest) List(_ context.Context) ([]ConnectionInstance, error) {
	return m.instances, nil
}

func (*mockConnectionStoreForTest) Get(_ context.Context, _, _ string) (*ConnectionInstance, error) {
	return nil, ErrConnectionNotFound
}

func (*mockConnectionStoreForTest) Set(_ context.Context, _ ConnectionInstance) error { return nil }

func (*mockConnectionStoreForTest) Delete(_ context.Context, _, _ string) error {
	return ErrConnectionNotFound
}

// --- Persona store tests ---

// mockPersonaStoreForTest is a simple mock for testing loadDBPersonas.
type mockPersonaStoreForTest struct {
	defs    []PersonaDefinition
	listErr error
}

func (m *mockPersonaStoreForTest) List(_ context.Context) ([]PersonaDefinition, error) {
	return m.defs, m.listErr
}

func (*mockPersonaStoreForTest) Get(_ context.Context, _ string) (*PersonaDefinition, error) {
	return nil, ErrPersonaNotFound
}

func (*mockPersonaStoreForTest) Set(_ context.Context, _ PersonaDefinition) error { return nil }

func (*mockPersonaStoreForTest) Delete(_ context.Context, _ string) error {
	return ErrPersonaNotFound
}

func TestLoadDBPersonas(t *testing.T) {
	t.Run("loads personas from store", func(t *testing.T) {
		reg := persona.NewRegistry()
		_ = reg.Register(&persona.Persona{Name: "admin", DisplayName: "Admin", Roles: []string{"admin"}})

		p := &Platform{
			personaRegistry: reg,
			personaStore: &mockPersonaStoreForTest{
				defs: []PersonaDefinition{
					{
						Name:        "analyst",
						DisplayName: "Data Analyst",
						Roles:       []string{"analyst"},
						ToolsAllow:  []string{"trino_*"},
						ToolsDeny:   []string{},
						ConnsAllow:  []string{},
						ConnsDeny:   []string{},
					},
				},
			},
		}
		p.loadDBPersonas()

		got, ok := reg.Get("analyst")
		if !ok {
			t.Fatal("analyst persona should exist after loadDBPersonas")
		}
		if got.DisplayName != "Data Analyst" {
			t.Errorf("expected Data Analyst, got %s", got.DisplayName)
		}
	})

	t.Run("DB overrides file persona", func(t *testing.T) {
		reg := persona.NewRegistry()
		_ = reg.Register(&persona.Persona{Name: "analyst", DisplayName: "Old Name", Roles: []string{"analyst"}})

		p := &Platform{
			personaRegistry: reg,
			personaStore: &mockPersonaStoreForTest{
				defs: []PersonaDefinition{
					{
						Name:        "analyst",
						DisplayName: "New Name",
						Roles:       []string{"analyst", "viewer"},
						ToolsAllow:  []string{},
						ToolsDeny:   []string{},
						ConnsAllow:  []string{},
						ConnsDeny:   []string{},
					},
				},
			},
		}
		p.loadDBPersonas()

		got, _ := reg.Get("analyst")
		if got.DisplayName != "New Name" {
			t.Errorf("expected New Name, got %s", got.DisplayName)
		}
		if len(got.Roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(got.Roles))
		}
	})

	t.Run("handles list error gracefully", func(_ *testing.T) {
		reg := persona.NewRegistry()
		p := &Platform{
			personaRegistry: reg,
			personaStore: &mockPersonaStoreForTest{
				listErr: errors.New("db error"),
			},
		}
		p.loadDBPersonas() // should not panic
	})

	t.Run("nil store is safe", func(_ *testing.T) {
		p := &Platform{personaStore: nil}
		p.loadDBPersonas() // should not panic
	})
}

func TestInitPersonaStore(t *testing.T) {
	t.Run("creates noop store when no database", func(t *testing.T) {
		p := &Platform{}
		p.initPersonaStore()

		if p.personaStore == nil {
			t.Fatal("personaStore should not be nil")
		}
		if _, ok := p.personaStore.(*NoopPersonaStore); !ok {
			t.Error("expected NoopPersonaStore when db is nil")
		}
	})

	t.Run("creates postgres store when database available", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("creating sqlmock: %v", err)
		}
		defer func() { _ = db.Close() }()

		p := &Platform{db: db}
		p.initPersonaStore()

		if p.personaStore == nil {
			t.Fatal("personaStore should not be nil")
		}
		if _, ok := p.personaStore.(*PostgresPersonaStore); !ok {
			t.Error("expected PostgresPersonaStore when db is set")
		}
	})
}

func TestPersonaStoreAccessor(t *testing.T) {
	noop := &NoopPersonaStore{}
	p := &Platform{personaStore: noop}
	if p.PersonaStore() != noop {
		t.Error("PersonaStore() should return the assigned store")
	}
}

// mockAPIKeyStoreForTest is a simple mock for testing loadDBAPIKeys.
type mockAPIKeyStoreForTest struct {
	defs    []APIKeyDefinition
	listErr error
}

func (m *mockAPIKeyStoreForTest) List(_ context.Context) ([]APIKeyDefinition, error) {
	return m.defs, m.listErr
}

func (*mockAPIKeyStoreForTest) Set(_ context.Context, _ APIKeyDefinition) error { return nil }

func (*mockAPIKeyStoreForTest) Delete(_ context.Context, _ string) error {
	return ErrAPIKeyNotFound
}

func TestLoadDBAPIKeys(t *testing.T) {
	t.Run("loads keys from store", func(t *testing.T) {
		apiKeyAuth := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{})

		p := &Platform{
			apiKeyAuth: apiKeyAuth,
			apiKeyStore: &mockAPIKeyStoreForTest{
				defs: []APIKeyDefinition{
					{
						Name:    "db-key",
						KeyHash: "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012",
						Email:   "db@example.com",
						Roles:   []string{"analyst"},
					},
				},
			},
		}
		p.loadDBAPIKeys()

		keys := apiKeyAuth.ListKeys()
		found := false
		for _, k := range keys {
			if k.Name == "db-key" {
				found = true
				if k.Email != "db@example.com" {
					t.Errorf("email = %q, want %q", k.Email, "db@example.com")
				}
				if len(k.Roles) != 1 || k.Roles[0] != "analyst" {
					t.Errorf("roles = %v, want [analyst]", k.Roles)
				}
			}
		}
		if !found {
			t.Fatal("db-key should appear in ListKeys after loadDBAPIKeys")
		}
	})

	t.Run("handles list error gracefully", func(_ *testing.T) {
		apiKeyAuth := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{})
		p := &Platform{
			apiKeyAuth: apiKeyAuth,
			apiKeyStore: &mockAPIKeyStoreForTest{
				listErr: errors.New("db error"),
			},
		}
		p.loadDBAPIKeys() // should not panic
	})

	t.Run("nil store is safe", func(_ *testing.T) {
		p := &Platform{apiKeyStore: nil}
		p.loadDBAPIKeys() // should not panic
	})
}

func TestInitAPIKeyStore(t *testing.T) {
	t.Run("creates noop store when no database", func(t *testing.T) {
		p := &Platform{}
		p.initAPIKeyStore()

		if p.apiKeyStore == nil {
			t.Fatal("apiKeyStore should not be nil")
		}
		if _, ok := p.apiKeyStore.(*NoopAPIKeyStore); !ok {
			t.Error("expected NoopAPIKeyStore when db is nil")
		}
	})

	t.Run("creates postgres store when database available", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("creating sqlmock: %v", err)
		}
		defer func() { _ = db.Close() }()

		p := &Platform{db: db}
		p.initAPIKeyStore()

		if p.apiKeyStore == nil {
			t.Fatal("apiKeyStore should not be nil")
		}
		if _, ok := p.apiKeyStore.(*PostgresAPIKeyStore); !ok {
			t.Error("expected PostgresAPIKeyStore when db is set")
		}
	})
}

func TestAPIKeyStoreAccessor(t *testing.T) {
	noop := &NoopAPIKeyStore{}
	p := &Platform{apiKeyStore: noop}
	if p.APIKeyStore() != noop {
		t.Error("APIKeyStore() should return the assigned store")
	}
}
