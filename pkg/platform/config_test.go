package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

func TestLoadConfig(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		// Create temp config file
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		configContent := `
server:
  name: test-platform
  transport: stdio
auth:
  oidc:
    enabled: false
  api_keys:
    enabled: false
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		cfg, err := LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Server.Name != "test-platform" {
			t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, "test-platform")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadConfig("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("LoadConfig() expected error for missing file")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(configPath, []byte("invalid: yaml: content:"), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		_, err := LoadConfig(configPath)
		if err == nil {
			t.Error("LoadConfig() expected error for invalid YAML")
		}
	})

	t.Run("environment variable expansion", func(t *testing.T) {
		t.Setenv("TEST_SERVER_NAME", "env-platform")

		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		configContent := `
server:
  name: ${TEST_SERVER_NAME}
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		cfg, err := LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Server.Name != "env-platform" {
			t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, "env-platform")
		}
	})

	t.Run("URN mapping config parsing", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		configContent := `
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
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		cfg, err := LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Semantic.URNMapping.Platform != "postgres" {
			t.Errorf("Semantic.URNMapping.Platform = %q, want %q", cfg.Semantic.URNMapping.Platform, "postgres")
		}
		if cfg.Semantic.URNMapping.CatalogMapping["rdbms"] != "warehouse" {
			t.Errorf("CatalogMapping[rdbms] = %q, want %q", cfg.Semantic.URNMapping.CatalogMapping["rdbms"], "warehouse")
		}
		if cfg.Semantic.URNMapping.CatalogMapping["iceberg"] != "datalake" {
			t.Errorf("CatalogMapping[iceberg] = %q, want %q", cfg.Semantic.URNMapping.CatalogMapping["iceberg"], "datalake")
		}
	})
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
	if cfg.Database.MaxOpenConns != 25 {
		t.Errorf("Database.MaxOpenConns = %d, want %d", cfg.Database.MaxOpenConns, 25)
	}
	if cfg.Semantic.Cache.TTL != 5*time.Minute {
		t.Errorf("Semantic.Cache.TTL = %v, want %v", cfg.Semantic.Cache.TTL, 5*time.Minute)
	}
	if cfg.Audit.RetentionDays != 90 {
		t.Errorf("Audit.RetentionDays = %d, want %d", cfg.Audit.RetentionDays, 90)
	}
	if cfg.Tuning.Rules.QualityThreshold != 0.7 {
		t.Errorf("Tuning.Rules.QualityThreshold = %f, want %f", cfg.Tuning.Rules.QualityThreshold, 0.7)
	}
}

func TestApplyDefaults_PreservesExisting(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Name:      "custom-name",
			Transport: "sse",
		},
		Database: DatabaseConfig{
			MaxOpenConns: 50,
		},
	}
	applyDefaults(cfg)

	if cfg.Server.Name != "custom-name" {
		t.Errorf("Server.Name = %q, want %q (should preserve existing)", cfg.Server.Name, "custom-name")
	}
	if cfg.Server.Transport != "sse" {
		t.Errorf("Server.Transport = %q, want %q (should preserve existing)", cfg.Server.Transport, "sse")
	}
	if cfg.Database.MaxOpenConns != 50 {
		t.Errorf("Database.MaxOpenConns = %d, want %d (should preserve existing)", cfg.Database.MaxOpenConns, 50)
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

func TestConfigTypes(t *testing.T) {
	t.Run("ServerConfig", func(t *testing.T) {
		cfg := ServerConfig{
			Name:      "test",
			Transport: "sse",
			Address:   ":8080",
			TLS: TLSConfig{
				Enabled:  true,
				CertFile: "/path/cert.pem",
				KeyFile:  "/path/key.pem",
			},
		}
		if cfg.Name != "test" {
			t.Errorf("Name = %q", cfg.Name)
		}
		if !cfg.TLS.Enabled {
			t.Error("TLS.Enabled = false")
		}
	})

	t.Run("PersonasConfig", func(t *testing.T) {
		cfg := PersonasConfig{
			DefaultPersona: "admin",
			RoleMapping: RoleMappingConfig{
				OIDCToPersona: map[string]string{"admin_role": "admin"},
				UserPersonas:  map[string]string{"user1": "analyst"},
			},
		}
		if cfg.DefaultPersona != "admin" {
			t.Errorf("DefaultPersona = %q", cfg.DefaultPersona)
		}
		if cfg.RoleMapping.OIDCToPersona["admin_role"] != "admin" {
			t.Error("OIDCToPersona mapping incorrect")
		}
	})

	t.Run("PersonaDef", func(t *testing.T) {
		def := PersonaDef{
			DisplayName: "Administrator",
			Roles:       []string{"admin"},
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
	})

	t.Run("InjectionConfig", func(t *testing.T) {
		cfg := InjectionConfig{
			TrinoSemanticEnrichment:  true,
			DataHubQueryEnrichment:   true,
			S3SemanticEnrichment:     true,
			DataHubStorageEnrichment: true,
		}
		if !cfg.TrinoSemanticEnrichment {
			t.Error("TrinoSemanticEnrichment = false")
		}
	})

	t.Run("TuningConfig", func(t *testing.T) {
		cfg := TuningConfig{
			Rules: RulesConfig{
				RequireDataHubCheck: true,
				WarnOnDeprecated:    true,
				QualityThreshold:    0.8,
			},
			PromptsDir: "/prompts",
		}
		if !cfg.Rules.RequireDataHubCheck {
			t.Error("Rules.RequireDataHubCheck = false")
		}
		if cfg.PromptsDir != "/prompts" {
			t.Errorf("PromptsDir = %q", cfg.PromptsDir)
		}
	})

	t.Run("AuditConfig", func(t *testing.T) {
		cfg := AuditConfig{
			Enabled:       true,
			LogToolCalls:  true,
			RetentionDays: 30,
		}
		if !cfg.Enabled {
			t.Error("Enabled = false")
		}
		if cfg.RetentionDays != 30 {
			t.Errorf("RetentionDays = %d", cfg.RetentionDays)
		}
	})

	t.Run("URNMappingConfig", func(t *testing.T) {
		cfg := URNMappingConfig{
			Platform: "postgres",
			CatalogMapping: map[string]string{
				"rdbms":   "warehouse",
				"iceberg": "datalake",
			},
		}
		if cfg.Platform != "postgres" {
			t.Errorf("Platform = %q, want %q", cfg.Platform, "postgres")
		}
		if cfg.CatalogMapping["rdbms"] != "warehouse" {
			t.Errorf("CatalogMapping[rdbms] = %q, want %q", cfg.CatalogMapping["rdbms"], "warehouse")
		}
		if cfg.CatalogMapping["iceberg"] != "datalake" {
			t.Errorf("CatalogMapping[iceberg] = %q, want %q", cfg.CatalogMapping["iceberg"], "datalake")
		}
	})

	t.Run("SemanticConfig with URNMapping", func(t *testing.T) {
		cfg := SemanticConfig{
			Provider: "datahub",
			Instance: "primary",
			Cache: CacheConfig{
				Enabled: true,
				TTL:     5 * time.Minute,
			},
			URNMapping: URNMappingConfig{
				Platform:       "postgres",
				CatalogMapping: map[string]string{"rdbms": "warehouse"},
			},
		}
		if cfg.Provider != "datahub" {
			t.Errorf("Provider = %q", cfg.Provider)
		}
		if cfg.URNMapping.Platform != "postgres" {
			t.Errorf("URNMapping.Platform = %q", cfg.URNMapping.Platform)
		}
		if cfg.URNMapping.CatalogMapping["rdbms"] != "warehouse" {
			t.Errorf("URNMapping.CatalogMapping[rdbms] = %q", cfg.URNMapping.CatalogMapping["rdbms"])
		}
	})

	t.Run("SemanticConfig with Lineage", func(t *testing.T) {
		cfg := SemanticConfig{
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
		}
		if !cfg.Lineage.Enabled {
			t.Error("Lineage.Enabled = false, want true")
		}
		if cfg.Lineage.MaxHops != 3 {
			t.Errorf("Lineage.MaxHops = %d, want 3", cfg.Lineage.MaxHops)
		}
		if len(cfg.Lineage.Inherit) != 3 {
			t.Errorf("Lineage.Inherit len = %d, want 3", len(cfg.Lineage.Inherit))
		}
		if cfg.Lineage.ConflictResolution != "nearest" {
			t.Errorf("Lineage.ConflictResolution = %q, want %q", cfg.Lineage.ConflictResolution, "nearest")
		}
		if !cfg.Lineage.PreferColumnLineage {
			t.Error("Lineage.PreferColumnLineage = false, want true")
		}
		if cfg.Lineage.CacheTTL != 10*time.Minute {
			t.Errorf("Lineage.CacheTTL = %v, want %v", cfg.Lineage.CacheTTL, 10*time.Minute)
		}
		if cfg.Lineage.Timeout != 5*time.Second {
			t.Errorf("Lineage.Timeout = %v, want %v", cfg.Lineage.Timeout, 5*time.Second)
		}
	})
}

func TestLoadConfig_DataHubDebugFromYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configContent := `
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
`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Verify the datahub toolkit config was loaded
	datahubCfgAny, ok := cfg.Toolkits["datahub"]
	if !ok {
		t.Fatal("expected datahub toolkit config")
	}

	datahubCfg, ok := datahubCfgAny.(map[string]any)
	if !ok {
		t.Fatal("expected datahub toolkit config to be a map")
	}

	// Verify instances were parsed
	instances, ok := datahubCfg["instances"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub instances config")
	}

	primaryInstance, ok := instances["primary"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub primary instance config")
	}

	// Verify debug field was parsed
	debug, ok := primaryInstance["debug"].(bool)
	if !ok {
		t.Fatal("expected debug field in primary instance")
	}
	if !debug {
		t.Error("expected debug to be true")
	}
}

func TestLoadConfig_DataHubDebugDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configContent := `
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
`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Verify the datahub toolkit config was loaded
	datahubCfgAny, ok := cfg.Toolkits["datahub"]
	if !ok {
		t.Fatal("expected datahub toolkit config")
	}

	datahubCfg, ok := datahubCfgAny.(map[string]any)
	if !ok {
		t.Fatal("expected datahub toolkit config to be a map")
	}

	// Verify instances were parsed
	instances, ok := datahubCfg["instances"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub instances config")
	}

	primaryInstance, ok := instances["primary"].(map[string]any)
	if !ok {
		t.Fatal("expected datahub primary instance config")
	}

	// Verify debug field is not present (defaults to false when not specified)
	_, hasDebug := primaryInstance["debug"]
	if hasDebug {
		t.Error("expected debug field to not be present when not specified")
	}
}

func TestLoadConfig_LineageFromYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configContent := `
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
`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Verify lineage config was parsed correctly
	if !cfg.Semantic.Lineage.Enabled {
		t.Error("Semantic.Lineage.Enabled = false, want true")
	}
	if cfg.Semantic.Lineage.MaxHops != 3 {
		t.Errorf("Semantic.Lineage.MaxHops = %d, want 3", cfg.Semantic.Lineage.MaxHops)
	}
	if len(cfg.Semantic.Lineage.Inherit) != 3 {
		t.Errorf("Semantic.Lineage.Inherit len = %d, want 3", len(cfg.Semantic.Lineage.Inherit))
	}
	expectedInherit := []string{"glossary_terms", "descriptions", "tags"}
	for i, want := range expectedInherit {
		if cfg.Semantic.Lineage.Inherit[i] != want {
			t.Errorf("Semantic.Lineage.Inherit[%d] = %q, want %q", i, cfg.Semantic.Lineage.Inherit[i], want)
		}
	}
	if cfg.Semantic.Lineage.ConflictResolution != "nearest" {
		t.Errorf("Semantic.Lineage.ConflictResolution = %q, want %q", cfg.Semantic.Lineage.ConflictResolution, "nearest")
	}
	if !cfg.Semantic.Lineage.PreferColumnLineage {
		t.Error("Semantic.Lineage.PreferColumnLineage = false, want true")
	}
	if cfg.Semantic.Lineage.CacheTTL != 15*time.Minute {
		t.Errorf("Semantic.Lineage.CacheTTL = %v, want %v", cfg.Semantic.Lineage.CacheTTL, 15*time.Minute)
	}
	if cfg.Semantic.Lineage.Timeout != 10*time.Second {
		t.Errorf("Semantic.Lineage.Timeout = %v, want %v", cfg.Semantic.Lineage.Timeout, 10*time.Second)
	}

	// Verify column transforms
	if len(cfg.Semantic.Lineage.ColumnTransforms) != 1 {
		t.Fatalf("Semantic.Lineage.ColumnTransforms len = %d, want 1", len(cfg.Semantic.Lineage.ColumnTransforms))
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
		t.Fatalf("Semantic.Lineage.Aliases len = %d, want 1", len(cfg.Semantic.Lineage.Aliases))
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
