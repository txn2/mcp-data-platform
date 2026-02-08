package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

const (
	cfgTestPlatformName      = "test-platform"
	cfgTestProviderPostgres  = "postgres"
	cfgTestCatalogWarehouse  = "warehouse"
	cfgTestCatalogDatalake   = "datalake"
	cfgTestCatalogRdbms      = "rdbms"
	cfgTestCatalogIceberg    = "iceberg"
	cfgTestDefaultMaxConns   = 25
	cfgTestDefaultRetention  = 90
	cfgTestDefaultQuality    = 0.7
	cfgTestDefaultCacheTTL   = 5 * time.Minute
	cfgTestDefaultSessTTL    = 30 * time.Minute
	cfgTestCustomMaxConns    = 50
	cfgTestCustomSessTTL     = 10 * time.Minute
	cfgTestLineageMaxHops    = 3
	cfgTestLineageInheritLen = 3
	cfgTestLineageCacheTTL   = 15 * time.Minute
	cfgTestLineageTimeout    = 10 * time.Second
	cfgTestFilePerms         = 0o600
	cfgTestConflictNearest   = "nearest"
	cfgTestRoleAdmin         = "admin"
	cfgTestToolkitDatahub    = "datahub"
	cfgTestQualityThreshold  = 0.8
	cfgTestRetentionDays     = 30
	cfgTestStreamableSessTTL = 15 * time.Minute
	cfgTestLineageTO         = 5 * time.Second
	cfgTestEntryTTL10m       = 10 * time.Minute
	cfgTestSessTO60m         = 60 * time.Minute
)

// writeTestConfig writes a YAML config to a temp dir and returns the path.
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), cfgTestFilePerms); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return configPath
}

// loadTestConfig writes YAML and loads it, failing on error.
func loadTestConfig(t *testing.T, content string) *Config {
	t.Helper()
	configPath := writeTestConfig(t, content)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func TestLoadConfig_ValidFile(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
  transport: stdio
auth:
  oidc:
    enabled: false
  api_keys:
    enabled: false
`)
	if cfg.Server.Name != cfgTestPlatformName {
		t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, cfgTestPlatformName)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("LoadConfig() expected error for missing file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	configPath := writeTestConfig(t, "invalid: yaml: content:")
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("LoadConfig() expected error for invalid YAML")
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_SERVER_NAME", "env-platform")
	cfg := loadTestConfig(t, `
server:
  name: ${TEST_SERVER_NAME}
`)
	if cfg.Server.Name != "env-platform" {
		t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, "env-platform")
	}
}

func TestLoadConfig_URNMapping(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
semantic:
  provider: datahub
  instance: primary
  urn_mapping:
    platform: postgres
    catalog_mapping:
      rdbms: warehouse
      iceberg: datalake
`)
	if cfg.Semantic.URNMapping.Platform != cfgTestProviderPostgres {
		t.Errorf("Semantic.URNMapping.Platform = %q, want %q", cfg.Semantic.URNMapping.Platform, cfgTestProviderPostgres)
	}
	if cfg.Semantic.URNMapping.CatalogMapping[cfgTestCatalogRdbms] != cfgTestCatalogWarehouse {
		t.Errorf("CatalogMapping[rdbms] = %q, want %q", cfg.Semantic.URNMapping.CatalogMapping[cfgTestCatalogRdbms], cfgTestCatalogWarehouse)
	}
	if cfg.Semantic.URNMapping.CatalogMapping[cfgTestCatalogIceberg] != cfgTestCatalogDatalake {
		t.Errorf("CatalogMapping[iceberg] = %q, want %q", cfg.Semantic.URNMapping.CatalogMapping[cfgTestCatalogIceberg], cfgTestCatalogDatalake)
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("MY_VAR", "value123")
	t.Setenv("ANOTHER_VAR", "another")

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"single var", "prefix-${MY_VAR}-suffix", "prefix-value123-suffix"},
		{"multiple vars", "${MY_VAR} and ${ANOTHER_VAR}", "value123 and another"},
		{"no vars", "no variables here", "no variables here"},
		{"empty var", "${UNDEFINED_VAR}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandEnvVars(tt.input)
			if result != tt.expect {
				t.Errorf("expandEnvVars(%q) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Server.Name != "mcp-data-platform" {
		t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, "mcp-data-platform")
	}
	if cfg.Server.Transport != "stdio" {
		t.Errorf("Server.Transport = %q, want %q", cfg.Server.Transport, "stdio")
	}
	if cfg.Database.MaxOpenConns != cfgTestDefaultMaxConns {
		t.Errorf("Database.MaxOpenConns = %d, want %d", cfg.Database.MaxOpenConns, cfgTestDefaultMaxConns)
	}
	if cfg.Semantic.Cache.TTL != cfgTestDefaultCacheTTL {
		t.Errorf("Semantic.Cache.TTL = %v, want %v", cfg.Semantic.Cache.TTL, cfgTestDefaultCacheTTL)
	}
	if cfg.Audit.RetentionDays != cfgTestDefaultRetention {
		t.Errorf("Audit.RetentionDays = %d, want %d", cfg.Audit.RetentionDays, cfgTestDefaultRetention)
	}
	if cfg.Tuning.Rules.QualityThreshold != cfgTestDefaultQuality {
		t.Errorf("Tuning.Rules.QualityThreshold = %f, want %f", cfg.Tuning.Rules.QualityThreshold, cfgTestDefaultQuality)
	}
	if cfg.Server.Streamable.SessionTimeout != cfgTestDefaultSessTTL {
		t.Errorf("Server.Streamable.SessionTimeout = %v, want %v", cfg.Server.Streamable.SessionTimeout, cfgTestDefaultSessTTL)
	}
}

func TestApplyDefaults_PreservesExisting(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Name:      "custom-name",
			Transport: "sse",
			Streamable: StreamableConfig{
				SessionTimeout: cfgTestCustomSessTTL,
				Stateless:      true,
			},
		},
		Database: DatabaseConfig{
			MaxOpenConns: cfgTestCustomMaxConns,
		},
	}
	applyDefaults(cfg)

	if cfg.Server.Name != "custom-name" {
		t.Errorf("Server.Name = %q, want %q (should preserve existing)", cfg.Server.Name, "custom-name")
	}
	if cfg.Server.Transport != "sse" {
		t.Errorf("Server.Transport = %q, want %q (should preserve existing)", cfg.Server.Transport, "sse")
	}
	if cfg.Database.MaxOpenConns != cfgTestCustomMaxConns {
		t.Errorf("Database.MaxOpenConns = %d, want %d (should preserve existing)", cfg.Database.MaxOpenConns, cfgTestCustomMaxConns)
	}
	if cfg.Server.Streamable.SessionTimeout != cfgTestCustomSessTTL {
		t.Errorf("Server.Streamable.SessionTimeout = %v, want %v (should preserve existing)", cfg.Server.Streamable.SessionTimeout, cfgTestCustomSessTTL)
	}
	if !cfg.Server.Streamable.Stateless {
		t.Error("Server.Streamable.Stateless = false, want true (should preserve existing)")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
	})

	t.Run("OIDC enabled without issuer", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{
					Enabled: true,
					Issuer:  "",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Error("Validate() expected error for OIDC without issuer")
		}
	})

	t.Run("OAuth enabled without database", func(t *testing.T) {
		cfg := &Config{
			OAuth: OAuthConfig{
				Enabled: true,
			},
			Database: DatabaseConfig{
				DSN: "",
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Error("Validate() expected error for OAuth without database")
		}
	})

	t.Run("multiple validation errors", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{
					Enabled: true,
					Issuer:  "",
				},
			},
			OAuth: OAuthConfig{
				Enabled: true,
			},
			Database: DatabaseConfig{
				DSN: "",
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Error("Validate() expected error for multiple issues")
		}
	})
}

func TestLoadConfig_StreamableFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
  transport: http
  streamable:
    session_timeout: 15m
    stateless: true
`)
	if cfg.Server.Transport != "http" {
		t.Errorf("Server.Transport = %q, want %q", cfg.Server.Transport, "http")
	}
	if cfg.Server.Streamable.SessionTimeout != cfgTestStreamableSessTTL {
		t.Errorf("Server.Streamable.SessionTimeout = %v, want %v", cfg.Server.Streamable.SessionTimeout, cfgTestStreamableSessTTL)
	}
	if !cfg.Server.Streamable.Stateless {
		t.Error("Server.Streamable.Stateless = false, want true")
	}
}

func TestConfigTypes_ServerConfig(t *testing.T) {
	cfg := ServerConfig{
		Name:      "test",
		Transport: "http",
		Address:   ":8080",
		TLS: TLSConfig{
			Enabled:  true,
			CertFile: "/path/cert.pem",
			KeyFile:  "/path/key.pem",
		},
		Streamable: StreamableConfig{
			SessionTimeout: cfgTestLineageCacheTTL,
			Stateless:      true,
		},
	}
	if cfg.Name != "test" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if !cfg.TLS.Enabled {
		t.Error("TLS.Enabled = false")
	}
	if cfg.Streamable.SessionTimeout != cfgTestLineageCacheTTL {
		t.Errorf("Streamable.SessionTimeout = %v, want %v", cfg.Streamable.SessionTimeout, cfgTestLineageCacheTTL)
	}
	if !cfg.Streamable.Stateless {
		t.Error("Streamable.Stateless = false, want true")
	}
}

func TestConfigTypes_PersonasConfig(t *testing.T) {
	cfg := PersonasConfig{
		DefaultPersona: cfgTestRoleAdmin,
		RoleMapping: RoleMappingConfig{
			OIDCToPersona: map[string]string{"admin_role": cfgTestRoleAdmin},
			UserPersonas:  map[string]string{"user1": "analyst"},
		},
	}
	if cfg.DefaultPersona != cfgTestRoleAdmin {
		t.Errorf("DefaultPersona = %q", cfg.DefaultPersona)
	}
	if cfg.RoleMapping.OIDCToPersona["admin_role"] != cfgTestRoleAdmin {
		t.Error("OIDCToPersona mapping incorrect")
	}
}

func TestConfigTypes_PersonaDef(t *testing.T) {
	def := PersonaDef{
		DisplayName: "Administrator",
		Roles:       []string{cfgTestRoleAdmin},
		Tools: ToolRulesDef{
			Allow: []string{"*"},
			Deny:  []string{"dangerous_*"},
		},
		Prompts: PromptsDef{
			SystemPrefix: "You are an admin.",
		},
		Hints: map[string]string{"key": "value"},
	}
	if def.DisplayName != "Administrator" {
		t.Errorf("DisplayName = %q", def.DisplayName)
	}
	if len(def.Tools.Allow) != 1 || def.Tools.Allow[0] != "*" {
		t.Errorf("Tools.Allow = %v", def.Tools.Allow)
	}
}

func TestConfigTypes_InjectionConfig(t *testing.T) {
	cfg := InjectionConfig{
		TrinoSemanticEnrichment:  true,
		DataHubQueryEnrichment:   true,
		S3SemanticEnrichment:     true,
		DataHubStorageEnrichment: true,
	}
	if !cfg.TrinoSemanticEnrichment {
		t.Error("TrinoSemanticEnrichment = false")
	}
}

func TestConfigTypes_TuningConfig(t *testing.T) {
	cfg := TuningConfig{
		Rules: RulesConfig{
			RequireDataHubCheck: true,
			WarnOnDeprecated:    true,
			QualityThreshold:    cfgTestQualityThreshold,
		},
		PromptsDir: "/prompts",
	}
	if !cfg.Rules.RequireDataHubCheck {
		t.Error("Rules.RequireDataHubCheck = false")
	}
	if cfg.PromptsDir != "/prompts" {
		t.Errorf("PromptsDir = %q", cfg.PromptsDir)
	}
}

func TestConfigTypes_AuditConfig(t *testing.T) {
	cfg := AuditConfig{
		Enabled:       true,
		LogToolCalls:  true,
		RetentionDays: cfgTestRetentionDays,
	}
	if !cfg.Enabled {
		t.Error("Enabled = false")
	}
	if cfg.RetentionDays != cfgTestRetentionDays {
		t.Errorf("RetentionDays = %d", cfg.RetentionDays)
	}
}

func TestConfigTypes_URNMappingConfig(t *testing.T) {
	cfg := URNMappingConfig{
		Platform: cfgTestProviderPostgres,
		CatalogMapping: map[string]string{
			cfgTestCatalogRdbms:   cfgTestCatalogWarehouse,
			cfgTestCatalogIceberg: cfgTestCatalogDatalake,
		},
	}
	if cfg.Platform != cfgTestProviderPostgres {
		t.Errorf("Platform = %q, want %q", cfg.Platform, cfgTestProviderPostgres)
	}
	if cfg.CatalogMapping[cfgTestCatalogRdbms] != cfgTestCatalogWarehouse {
		t.Errorf("CatalogMapping[rdbms] = %q, want %q", cfg.CatalogMapping[cfgTestCatalogRdbms], cfgTestCatalogWarehouse)
	}
	if cfg.CatalogMapping[cfgTestCatalogIceberg] != cfgTestCatalogDatalake {
		t.Errorf("CatalogMapping[iceberg] = %q, want %q", cfg.CatalogMapping[cfgTestCatalogIceberg], cfgTestCatalogDatalake)
	}
}

func TestConfigTypes_SemanticConfigWithURNMapping(t *testing.T) {
	cfg := SemanticConfig{
		Provider: cfgTestToolkitDatahub,
		Instance: "primary",
		Cache: CacheConfig{
			Enabled: true,
			TTL:     cfgTestDefaultCacheTTL,
		},
		URNMapping: URNMappingConfig{
			Platform:       cfgTestProviderPostgres,
			CatalogMapping: map[string]string{cfgTestCatalogRdbms: cfgTestCatalogWarehouse},
		},
	}
	if cfg.Provider != cfgTestToolkitDatahub {
		t.Errorf("Provider = %q", cfg.Provider)
	}
	if cfg.URNMapping.Platform != cfgTestProviderPostgres {
		t.Errorf("URNMapping.Platform = %q", cfg.URNMapping.Platform)
	}
	if cfg.URNMapping.CatalogMapping[cfgTestCatalogRdbms] != cfgTestCatalogWarehouse {
		t.Errorf("URNMapping.CatalogMapping[rdbms] = %q", cfg.URNMapping.CatalogMapping[cfgTestCatalogRdbms])
	}
}

func TestConfigTypes_SemanticConfigWithLineage(t *testing.T) {
	cfg := SemanticConfig{
		Provider: cfgTestToolkitDatahub,
		Instance: "primary",
		Lineage: datahubsemantic.LineageConfig{
			Enabled:             true,
			MaxHops:             cfgTestLineageMaxHops,
			Inherit:             []string{"glossary_terms", "descriptions", "tags"},
			ConflictResolution:  cfgTestConflictNearest,
			PreferColumnLineage: true,
			CacheTTL:            cfgTestCustomSessTTL,
			Timeout:             cfgTestLineageTO,
		},
	}
	if !cfg.Lineage.Enabled {
		t.Error("Lineage.Enabled = false, want true")
	}
	if cfg.Lineage.MaxHops != cfgTestLineageMaxHops {
		t.Errorf("Lineage.MaxHops = %d, want %d", cfg.Lineage.MaxHops, cfgTestLineageMaxHops)
	}
	if len(cfg.Lineage.Inherit) != cfgTestLineageInheritLen {
		t.Errorf("Lineage.Inherit len = %d, want %d", len(cfg.Lineage.Inherit), cfgTestLineageInheritLen)
	}
	if cfg.Lineage.ConflictResolution != cfgTestConflictNearest {
		t.Errorf("Lineage.ConflictResolution = %q, want %q", cfg.Lineage.ConflictResolution, cfgTestConflictNearest)
	}
	if !cfg.Lineage.PreferColumnLineage {
		t.Error("Lineage.PreferColumnLineage = false, want true")
	}
	if cfg.Lineage.CacheTTL != cfgTestCustomSessTTL {
		t.Errorf("Lineage.CacheTTL = %v, want %v", cfg.Lineage.CacheTTL, cfgTestCustomSessTTL)
	}
	if cfg.Lineage.Timeout != cfgTestLineageTO {
		t.Errorf("Lineage.Timeout = %v, want %v", cfg.Lineage.Timeout, cfgTestLineageTO)
	}
}

func TestSessionDedupConfig_IsEnabled(t *testing.T) {
	t.Run("nil enabled defaults to true", func(t *testing.T) {
		cfg := &SessionDedupConfig{}
		if !cfg.IsEnabled() {
			t.Error("IsEnabled() = false, want true (default)")
		}
	})

	t.Run("explicitly true", func(t *testing.T) {
		enabled := true
		cfg := &SessionDedupConfig{Enabled: &enabled}
		if !cfg.IsEnabled() {
			t.Error("IsEnabled() = false, want true")
		}
	})

	t.Run("explicitly false", func(t *testing.T) {
		disabled := false
		cfg := &SessionDedupConfig{Enabled: &disabled}
		if cfg.IsEnabled() {
			t.Error("IsEnabled() = true, want false")
		}
	})
}

func TestSessionDedupConfig_EffectiveMode(t *testing.T) {
	t.Run("empty defaults to reference", func(t *testing.T) {
		cfg := &SessionDedupConfig{}
		if got := cfg.EffectiveMode(); got != "reference" {
			t.Errorf("EffectiveMode() = %q, want %q", got, "reference")
		}
	})

	t.Run("summary mode", func(t *testing.T) {
		cfg := &SessionDedupConfig{Mode: "summary"}
		if got := cfg.EffectiveMode(); got != "summary" {
			t.Errorf("EffectiveMode() = %q, want %q", got, "summary")
		}
	})

	t.Run("none mode", func(t *testing.T) {
		cfg := &SessionDedupConfig{Mode: "none"}
		if got := cfg.EffectiveMode(); got != "none" {
			t.Errorf("EffectiveMode() = %q, want %q", got, "none")
		}
	})
}

func TestApplyDefaults_SessionDedupDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// Session dedup should inherit from semantic cache TTL and streamable session timeout
	if cfg.Injection.SessionDedup.EntryTTL != cfgTestDefaultCacheTTL {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v", cfg.Injection.SessionDedup.EntryTTL, cfgTestDefaultCacheTTL)
	}
	if cfg.Injection.SessionDedup.SessionTimeout != cfgTestDefaultSessTTL {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v", cfg.Injection.SessionDedup.SessionTimeout, cfgTestDefaultSessTTL)
	}
}

func TestApplyDefaults_SessionDedupPreservesExisting(t *testing.T) {
	cfg := &Config{
		Injection: InjectionConfig{
			SessionDedup: SessionDedupConfig{
				EntryTTL:       cfgTestEntryTTL10m,
				SessionTimeout: cfgTestSessTO60m,
			},
		},
	}
	applyDefaults(cfg)

	if cfg.Injection.SessionDedup.EntryTTL != cfgTestEntryTTL10m {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v (should preserve)", cfg.Injection.SessionDedup.EntryTTL, cfgTestEntryTTL10m)
	}
	if cfg.Injection.SessionDedup.SessionTimeout != cfgTestSessTO60m {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v (should preserve)", cfg.Injection.SessionDedup.SessionTimeout, cfgTestSessTO60m)
	}
}

func TestLoadConfig_SessionDedupFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
injection:
  trino_semantic_enrichment: true
  session_dedup:
    enabled: false
    mode: summary
    entry_ttl: 10m
    session_timeout: 1h
`)
	if cfg.Injection.SessionDedup.IsEnabled() {
		t.Error("SessionDedup.IsEnabled() = true, want false")
	}
	if cfg.Injection.SessionDedup.EffectiveMode() != "summary" {
		t.Errorf("SessionDedup.EffectiveMode() = %q, want %q", cfg.Injection.SessionDedup.EffectiveMode(), "summary")
	}
	if cfg.Injection.SessionDedup.EntryTTL != cfgTestEntryTTL10m {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v", cfg.Injection.SessionDedup.EntryTTL, cfgTestEntryTTL10m)
	}
	if cfg.Injection.SessionDedup.SessionTimeout != time.Hour {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v", cfg.Injection.SessionDedup.SessionTimeout, time.Hour)
	}
}

func TestLoadConfig_DataHubDebugFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        endpoint: "http://datahub.example.com:8080"
        token: "test-token"
        debug: true
    default: primary
`)
	primaryInstance := requireDataHubPrimaryInstance(t, cfg)
	debug, ok := primaryInstance["debug"].(bool)
	if !ok {
		t.Fatal("expected debug field in primary instance")
	}
	if !debug {
		t.Error("expected debug to be true")
	}
}

// requireDataHubPrimaryInstance extracts the primary datahub instance config from a loaded Config.
func requireDataHubPrimaryInstance(t *testing.T, cfg *Config) map[string]any {
	t.Helper()
	datahubCfgAny, ok := cfg.Toolkits[cfgTestToolkitDatahub]
	if !ok {
		t.Fatal("expected datahub toolkit config")
	}
	datahubCfg, ok := datahubCfgAny.(map[string]any)
	if !ok {
		t.Fatal("expected datahub toolkit config to be a map")
	}
	instances, ok := datahubCfg["instances"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub instances config")
	}
	primaryInstance, ok := instances["primary"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub primary instance config")
	}
	return primaryInstance
}

func TestLoadConfig_DataHubDebugDefaultsFalse(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
toolkits:
  datahub:
    enabled: true
    instances:
      primary:
        endpoint: "http://datahub.example.com:8080"
        token: "test-token"
    default: primary
`)
	primaryInstance := requireDataHubPrimaryInstance(t, cfg)
	_, hasDebug := primaryInstance["debug"]
	if hasDebug {
		t.Error("expected debug field to not be present when not specified")
	}
}

// assertLineageBasics verifies the basic lineage config fields.
func assertLineageBasics(t *testing.T, lineage datahubsemantic.LineageConfig) {
	t.Helper()
	if !lineage.Enabled {
		t.Error("Lineage.Enabled = false, want true")
	}
	if lineage.MaxHops != cfgTestLineageMaxHops {
		t.Errorf("Lineage.MaxHops = %d, want %d", lineage.MaxHops, cfgTestLineageMaxHops)
	}
	if len(lineage.Inherit) != cfgTestLineageInheritLen {
		t.Errorf("Lineage.Inherit len = %d, want %d", len(lineage.Inherit), cfgTestLineageInheritLen)
	}
	if lineage.ConflictResolution != cfgTestConflictNearest {
		t.Errorf("Lineage.ConflictResolution = %q, want %q", lineage.ConflictResolution, cfgTestConflictNearest)
	}
	if !lineage.PreferColumnLineage {
		t.Error("Lineage.PreferColumnLineage = false, want true")
	}
}

func TestLoadConfig_LineageFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
semantic:
  provider: datahub
  instance: primary
  lineage:
    enabled: true
    max_hops: 3
    inherit:
      - glossary_terms
      - descriptions
      - tags
    conflict_resolution: nearest
    prefer_column_lineage: true
    cache_ttl: 15m
    timeout: 10s
    column_transforms:
      - target_pattern: "*_flattened"
        strip_prefix: "payload."
    aliases:
      - source: "warehouse.raw.events"
        targets:
          - "warehouse.analytics.*"
        column_mapping:
          user_id: payload.user_id
`)
	assertLineageBasics(t, cfg.Semantic.Lineage)

	expectedInherit := []string{"glossary_terms", "descriptions", "tags"}
	for i, want := range expectedInherit {
		if cfg.Semantic.Lineage.Inherit[i] != want {
			t.Errorf("Semantic.Lineage.Inherit[%d] = %q, want %q", i, cfg.Semantic.Lineage.Inherit[i], want)
		}
	}
	if cfg.Semantic.Lineage.CacheTTL != cfgTestLineageCacheTTL {
		t.Errorf("Semantic.Lineage.CacheTTL = %v, want %v", cfg.Semantic.Lineage.CacheTTL, cfgTestLineageCacheTTL)
	}
	if cfg.Semantic.Lineage.Timeout != cfgTestLineageTimeout {
		t.Errorf("Semantic.Lineage.Timeout = %v, want %v", cfg.Semantic.Lineage.Timeout, cfgTestLineageTimeout)
	}

	// Verify column transforms
	if len(cfg.Semantic.Lineage.ColumnTransforms) != 1 {
		t.Fatalf("ColumnTransforms len = %d, want 1", len(cfg.Semantic.Lineage.ColumnTransforms))
	}
	transform := cfg.Semantic.Lineage.ColumnTransforms[0]
	if transform.TargetPattern != "*_flattened" {
		t.Errorf("ColumnTransforms[0].TargetPattern = %q, want %q", transform.TargetPattern, "*_flattened")
	}
	if transform.StripPrefix != "payload." {
		t.Errorf("ColumnTransforms[0].StripPrefix = %q, want %q", transform.StripPrefix, "payload.")
	}

	// Verify aliases
	if len(cfg.Semantic.Lineage.Aliases) != 1 {
		t.Fatalf("Aliases len = %d, want 1", len(cfg.Semantic.Lineage.Aliases))
	}
	alias := cfg.Semantic.Lineage.Aliases[0]
	if alias.Source != "warehouse.raw.events" {
		t.Errorf("Aliases[0].Source = %q, want %q", alias.Source, "warehouse.raw.events")
	}
	if len(alias.Targets) != 1 || alias.Targets[0] != "warehouse.analytics.*" {
		t.Errorf("Aliases[0].Targets = %v, want [warehouse.analytics.*]", alias.Targets)
	}
	if alias.ColumnMapping["user_id"] != "payload.user_id" {
		t.Errorf("Aliases[0].ColumnMapping[user_id] = %q, want %q", alias.ColumnMapping["user_id"], "payload.user_id")
	}
}
