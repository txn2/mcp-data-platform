package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
}
