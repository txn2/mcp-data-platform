package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

const (
	cfgTestVersionV1         = "v1"
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
	cfgTestDefaultGrace      = 25 * time.Second
	cfgTestDefaultPreDelay   = 2 * time.Second
	cfgTestCustomGrace       = 20 * time.Second
	cfgTestCustomPreDelay    = 3 * time.Second
	cfgTestDefaultCleanupInt = 1 * time.Minute
	cfgTestCustomSessionsTTL = 15 * time.Minute
	cfgTestCustomCleanup     = 2 * time.Minute
	cfgTestPersonaSuperadmin = "superadmin"
	cfgTestToolListConns     = "list_connections"
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

func TestLoadConfig_WithAPIVersion(t *testing.T) {
	cfg := loadTestConfig(t, `
apiVersion: v1
server:
  name: test-platform
  transport: stdio
`)
	if cfg.APIVersion != cfgTestVersionV1 {
		t.Errorf("APIVersion = %q, want %q", cfg.APIVersion, cfgTestVersionV1)
	}
	if cfg.Server.Name != cfgTestPlatformName {
		t.Errorf("config Server.Name = %q, want %q", cfg.Server.Name, cfgTestPlatformName)
	}
}

func TestLoadConfig_WithoutAPIVersion(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
  transport: stdio
`)
	if cfg.APIVersion != cfgTestVersionV1 {
		t.Errorf("APIVersion = %q, want %q (should default to v1)", cfg.APIVersion, cfgTestVersionV1)
	}
}

func TestLoadConfig_UnknownAPIVersion(t *testing.T) {
	configPath := writeTestConfig(t, `
apiVersion: v99
server:
  name: test-platform
`)
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("LoadConfig() expected error for unknown apiVersion")
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

	if cfg.Server.Name != defaultServerName {
		t.Errorf("Server.Name = %q, want %q", cfg.Server.Name, defaultServerName)
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
	if cfg.Server.Shutdown.GracePeriod != cfgTestDefaultGrace {
		t.Errorf("Server.Shutdown.GracePeriod = %v, want %v", cfg.Server.Shutdown.GracePeriod, cfgTestDefaultGrace)
	}
	if cfg.Server.Shutdown.PreShutdownDelay != cfgTestDefaultPreDelay {
		t.Errorf("Server.Shutdown.PreShutdownDelay = %v, want %v", cfg.Server.Shutdown.PreShutdownDelay, cfgTestDefaultPreDelay)
	}
	if cfg.Enrichment.EstimateRowCounts {
		t.Error("Injection.EstimateRowCounts should default to false")
	}
	if cfg.SessionGate.InitTool != defaultInitTool {
		t.Errorf("SessionGate.InitTool = %q, want %q", cfg.SessionGate.InitTool, defaultInitTool)
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

	t.Run("http + oauth enabled + no signing key = error naming the key", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Transport: "http"},
			OAuth:  OAuthConfig{Enabled: true, Issuer: "https://oauth.example.com"},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate() expected error for http + oauth + no signing key")
		}
		if !strings.Contains(err.Error(), "oauth.signing_key") {
			t.Errorf("error = %v, want it to name oauth.signing_key", err)
		}
	})

	t.Run("http + oauth + ephemeral escape hatch = no error", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Transport: "http"},
			OAuth: OAuthConfig{
				Enabled:                  true,
				Issuer:                   "https://oauth.example.com",
				AllowEphemeralSigningKey: true,
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil with allow_ephemeral_signing_key", err)
		}
	})

	t.Run("http + oauth + signing key configured = no error", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Transport: "http"},
			OAuth: OAuthConfig{
				Enabled:    true,
				Issuer:     "https://oauth.example.com",
				SigningKey: "c2lnbmluZy1rZXktYXQtbGVhc3QtMzItYnl0ZXMtbG9uZw==",
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil with signing key set", err)
		}
	})

	t.Run("stdio + oauth + no signing key = no signing-key error", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Transport: "stdio"},
			OAuth:  OAuthConfig{Enabled: true, Issuer: "https://oauth.example.com"},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil — stdio keeps auto-generate behavior", err)
		}
	})

	t.Run("sse transport is treated as http for the signing-key gate", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{Transport: "sse"},
			OAuth:  OAuthConfig{Enabled: true, Issuer: "https://oauth.example.com"},
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "oauth.signing_key") {
			t.Errorf("Validate() error = %v, want signing_key error for sse transport", err)
		}
	})

	t.Run("BroadcastChannel under 63 bytes is accepted", func(t *testing.T) {
		cfg := &Config{
			Sessions: SessionsConfig{Store: SessionStoreDatabase, BroadcastChannel: "deploy_alpha_events"},
			Database: DatabaseConfig{DSN: "postgres://x"},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil for short channel", err)
		}
	})

	t.Run("BroadcastChannel exactly 63 bytes is accepted", func(t *testing.T) {
		cfg := &Config{
			Sessions: SessionsConfig{Store: SessionStoreDatabase, BroadcastChannel: strings.Repeat("a", 63)},
			Database: DatabaseConfig{DSN: "postgres://x"},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil at boundary", err)
		}
	})

	t.Run("BroadcastChannel ignored on memory store", func(t *testing.T) {
		cfg := &Config{
			Sessions: SessionsConfig{Store: SessionStoreMemory, BroadcastChannel: "this.is.invalid!"},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil — channel is unused on memory store", err)
		}
	})

	t.Run("BroadcastChannel with invalid characters is rejected", func(t *testing.T) {
		cases := []string{
			"deploy alpha",      // space
			"123events",         // leading digit
			"alpha-events",      // dash
			"$invalid",          // leading dollar
			"deploy.production", // period
		}
		for _, ch := range cases {
			cfg := &Config{
				Sessions: SessionsConfig{Store: SessionStoreDatabase, BroadcastChannel: ch},
				Database: DatabaseConfig{DSN: "postgres://x"},
			}
			err := cfg.Validate()
			if err == nil {
				t.Errorf("Validate() expected error for invalid identifier %q", ch)
				continue
			}
			if !strings.Contains(err.Error(), "valid postgres identifier") {
				t.Errorf("for %q: error = %v, want message about valid postgres identifier", ch, err)
			}
		}
	})

	t.Run("BroadcastChannel with valid identifiers is accepted", func(t *testing.T) {
		cases := []string{
			"deploy_alpha",
			"_underscore_first",
			"a$dollar_after_first",
			"X1Y2Z3",
		}
		for _, ch := range cases {
			cfg := &Config{
				Sessions: SessionsConfig{Store: SessionStoreDatabase, BroadcastChannel: ch},
				Database: DatabaseConfig{DSN: "postgres://x"},
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() error = %v, want nil for valid identifier %q", err, ch)
			}
		}
	})

	t.Run("BroadcastChannel over 63 bytes is rejected", func(t *testing.T) {
		// Postgres truncates LISTEN identifiers at NAMEDATALEN-1 = 63
		// bytes. A long-name override would silently misroute (LISTEN
		// uses truncated, NOTIFY uses full) — exactly the multi-tenant
		// failure mode the override exists to prevent.
		cfg := &Config{
			Sessions: SessionsConfig{Store: SessionStoreDatabase, BroadcastChannel: strings.Repeat("a", 64)},
			Database: DatabaseConfig{DSN: "postgres://x"},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate() expected error for >63 byte channel name")
		}
		if !strings.Contains(err.Error(), "≤63") {
			t.Errorf("Validate() error = %v, want message mentioning ≤63 bytes", err)
		}
	})

	t.Run("browser session without OIDC", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleQ==",
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Error("Validate() expected error for browser session without OIDC")
		}
	})

	t.Run("browser session without signing key", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{
					Enabled: true,
					Issuer:  "https://auth.example.com",
				},
				BrowserSession: BrowserSessionConfig{
					Enabled: true,
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Error("Validate() expected error for browser session without signing key")
		}
	})

	t.Run("browser session valid config", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{
					Enabled: true,
					Issuer:  "https://auth.example.com",
				},
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleXRoYXRpc2F0bGVhc3QzMmJ5dGVzbG9uZyEh",
				},
			},
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("browser session disabled skips validation", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				BrowserSession: BrowserSessionConfig{
					Enabled: false,
				},
			},
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("same_site none without secure is rejected", func(t *testing.T) {
		secureFalse := false
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{Enabled: true, Issuer: "https://auth.example.com"},
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleXRoYXRpc2F0bGVhc3QzMmJ5dGVzbG9uZyEh",
					SameSite:   "none",
					Secure:     &secureFalse,
				},
			},
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "same_site=none requires") {
			t.Errorf("Validate() error = %v, want same_site=none requires secure", err)
		}
	})

	t.Run("same_site none with secure omitted is valid (defaults true)", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{Enabled: true, Issuer: "https://auth.example.com"},
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleXRoYXRpc2F0bGVhc3QzMmJ5dGVzbG9uZyEh",
					SameSite:   "none",
					// Secure omitted: IsSecure() defaults to true, so none is valid.
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("misspelled same_site is rejected", func(t *testing.T) {
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{Enabled: true, Issuer: "https://auth.example.com"},
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleXRoYXRpc2F0bGVhc3QzMmJ5dGVzbG9uZyEh",
					SameSite:   "stict", // typo for "strict"
				},
			},
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "same_site=\"stict\" is invalid") {
			t.Errorf("Validate() error = %v, want invalid same_site", err)
		}
	})

	t.Run("same_site none with secure is valid", func(t *testing.T) {
		secureTrue := true
		cfg := &Config{
			Auth: AuthConfig{
				OIDC: OIDCAuthConfig{Enabled: true, Issuer: "https://auth.example.com"},
				BrowserSession: BrowserSessionConfig{
					Enabled:    true,
					SigningKey: "dGVzdGtleXRoYXRpc2F0bGVhc3QzMmJ5dGVzbG9uZyEh",
					SameSite:   "none",
					Secure:     &secureTrue,
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
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

// personas.default_persona granted its persona to every caller whose roles
// matched nothing, so a config that still sets it is refused at startup rather
// than started with different access than the operator wrote down.
func TestConfig_Validate_RejectsDefaultPersona(t *testing.T) {
	cfg := &Config{Personas: PersonasConfig{DefaultPersona: cfgTestRoleAdmin}}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a config setting personas.default_persona")
	}
	if !strings.Contains(err.Error(), "default_persona") {
		t.Errorf("error does not name the offending key: %v", err)
	}
	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error does not tell the operator the key was removed: %v", err)
	}
}

func TestConfig_Validate_AcceptsUnsetDefaultPersona(t *testing.T) {
	cfg := &Config{Personas: PersonasConfig{
		Definitions: map[string]PersonaDef{cfgTestRoleAdmin: {Roles: []string{cfgTestRoleAdmin}}},
	}}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for a config with no default_persona", err)
	}
}

// PersonasConfig.Definitions is an inline map, so the field must stay declared:
// without it, default_persona would decode as a persona *named*
// "default_persona" instead of being rejected.
func TestConfig_DefaultPersonaParsesIntoItsOwnField(t *testing.T) {
	var cfg Config
	unknown, err := strictDecode([]byte("personas:\n  default_persona: admin\n"), &cfg)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(unknown) != 0 {
		t.Errorf("unknown keys = %v, want none", unknown)
	}
	if cfg.Personas.DefaultPersona != cfgTestRoleAdmin {
		t.Errorf("DefaultPersona = %q, want %q", cfg.Personas.DefaultPersona, cfgTestRoleAdmin)
	}
	if _, isPersona := cfg.Personas.Definitions["default_persona"]; isPersona {
		t.Error("default_persona decoded as a persona definition")
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
		Context: ContextDef{
			DescriptionPrefix: "You are an admin.",
		},
	}
	if def.DisplayName != "Administrator" {
		t.Errorf("DisplayName = %q", def.DisplayName)
	}
	if len(def.Tools.Allow) != 1 || def.Tools.Allow[0] != "*" {
		t.Errorf("Tools.Allow = %v", def.Tools.Allow)
	}
}

func TestConfigTypes_EnrichmentConfig(t *testing.T) {
	// The four cross-enrichment flags default to enabled when unset (#571): an
	// operator should not have to opt every deployment into the core feature.
	var def EnrichmentConfig
	if !def.IsTrinoSemanticEnrichmentEnabled() {
		t.Error("TrinoSemanticEnrichment should default enabled")
	}
	if !def.IsDataHubQueryEnrichmentEnabled() {
		t.Error("DataHubQueryEnrichment should default enabled")
	}
	if !def.IsS3SemanticEnrichmentEnabled() {
		t.Error("S3SemanticEnrichment should default enabled")
	}
	if !def.IsDataHubStorageEnrichmentEnabled() {
		t.Error("DataHubStorageEnrichment should default enabled")
	}

	// An explicit false disables.
	off := EnrichmentConfig{TrinoSemanticEnrichment: new(false)}
	if off.IsTrinoSemanticEnrichmentEnabled() {
		t.Error("explicit false should disable TrinoSemanticEnrichment")
	}
}

func TestConfigTypes_TuningConfig(t *testing.T) {
	cfg := TuningConfig{
		Rules: RulesConfig{
			QualityThreshold: cfgTestQualityThreshold,
		},
		PromptsDir: "/prompts",
	}
	if cfg.Rules.QualityThreshold != cfgTestQualityThreshold {
		t.Error("Rules.QualityThreshold mismatch")
	}
	if cfg.PromptsDir != "/prompts" {
		t.Errorf("PromptsDir = %q", cfg.PromptsDir)
	}
}

func TestConfigTypes_AuditConfig(t *testing.T) {
	cfg := AuditConfig{
		Enabled:       new(true),
		LogToolCalls:  new(true),
		RetentionDays: cfgTestRetentionDays,
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
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

func TestApplyDefaults_PortalS3(t *testing.T) {
	t.Run("empty defaults applied", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Portal.S3Bucket != "portal-assets" {
			t.Errorf("S3Bucket = %q, want %q", cfg.Portal.S3Bucket, "portal-assets")
		}
		if cfg.Portal.S3Prefix != "artifacts/" {
			t.Errorf("S3Prefix = %q, want %q", cfg.Portal.S3Prefix, "artifacts/")
		}
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		cfg := &Config{}
		cfg.Portal.S3Bucket = "my-bucket"
		cfg.Portal.S3Prefix = "custom/"
		applyDefaults(cfg)
		if cfg.Portal.S3Bucket != "my-bucket" {
			t.Errorf("S3Bucket = %q, want %q", cfg.Portal.S3Bucket, "my-bucket")
		}
		if cfg.Portal.S3Prefix != "custom/" {
			t.Errorf("S3Prefix = %q, want %q", cfg.Portal.S3Prefix, "custom/")
		}
	})
}

func TestApplyDefaults_ManagedResourcesS3Bucket(t *testing.T) {
	t.Run("empty defaults to managed-resources", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Resources.Managed.S3Bucket != "managed-resources" {
			t.Errorf("S3Bucket = %q, want %q", cfg.Resources.Managed.S3Bucket, "managed-resources")
		}
	})

	t.Run("explicit value preserved", func(t *testing.T) {
		cfg := &Config{}
		cfg.Resources.Managed.S3Bucket = "my-resources"
		applyDefaults(cfg)
		if cfg.Resources.Managed.S3Bucket != "my-resources" {
			t.Errorf("S3Bucket = %q, want %q", cfg.Resources.Managed.S3Bucket, "my-resources")
		}
	})
}

func TestApplyDefaults_ShutdownConfig(t *testing.T) {
	t.Run("defaults applied", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Server.Shutdown.GracePeriod != cfgTestDefaultGrace {
			t.Errorf("GracePeriod = %v, want %v", cfg.Server.Shutdown.GracePeriod, cfgTestDefaultGrace)
		}
		if cfg.Server.Shutdown.PreShutdownDelay != cfgTestDefaultPreDelay {
			t.Errorf("PreShutdownDelay = %v, want %v", cfg.Server.Shutdown.PreShutdownDelay, cfgTestDefaultPreDelay)
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		cfg := &Config{
			Server: ServerConfig{
				Shutdown: ShutdownConfig{
					GracePeriod:      cfgTestCustomGrace,
					PreShutdownDelay: cfgTestCustomPreDelay,
				},
			},
		}
		applyDefaults(cfg)
		if cfg.Server.Shutdown.GracePeriod != cfgTestCustomGrace {
			t.Errorf("GracePeriod = %v, want %v (should preserve)", cfg.Server.Shutdown.GracePeriod, cfgTestCustomGrace)
		}
		if cfg.Server.Shutdown.PreShutdownDelay != cfgTestCustomPreDelay {
			t.Errorf("PreShutdownDelay = %v, want %v (should preserve)", cfg.Server.Shutdown.PreShutdownDelay, cfgTestCustomPreDelay)
		}
	})
}

func TestLoadConfig_ShutdownFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
  shutdown:
    grace_period: 20s
    pre_shutdown_delay: 3s
`)
	if cfg.Server.Shutdown.GracePeriod != cfgTestCustomGrace {
		t.Errorf("GracePeriod = %v, want %v", cfg.Server.Shutdown.GracePeriod, cfgTestCustomGrace)
	}
	if cfg.Server.Shutdown.PreShutdownDelay != cfgTestCustomPreDelay {
		t.Errorf("PreShutdownDelay = %v, want %v", cfg.Server.Shutdown.PreShutdownDelay, cfgTestCustomPreDelay)
	}
}

func TestApplyDefaults_SessionDedupDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// Session dedup should inherit from semantic cache TTL and streamable session timeout
	if cfg.Enrichment.SessionDedup.EntryTTL != cfgTestDefaultCacheTTL {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v", cfg.Enrichment.SessionDedup.EntryTTL, cfgTestDefaultCacheTTL)
	}
	if cfg.Enrichment.SessionDedup.SessionTimeout != cfgTestDefaultSessTTL {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v", cfg.Enrichment.SessionDedup.SessionTimeout, cfgTestDefaultSessTTL)
	}
}

func TestApplyDefaults_SessionDedupPreservesExisting(t *testing.T) {
	cfg := &Config{
		Enrichment: EnrichmentConfig{
			SessionDedup: SessionDedupConfig{
				EntryTTL:       cfgTestEntryTTL10m,
				SessionTimeout: cfgTestSessTO60m,
			},
		},
	}
	applyDefaults(cfg)

	if cfg.Enrichment.SessionDedup.EntryTTL != cfgTestEntryTTL10m {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v (should preserve)", cfg.Enrichment.SessionDedup.EntryTTL, cfgTestEntryTTL10m)
	}
	if cfg.Enrichment.SessionDedup.SessionTimeout != cfgTestSessTO60m {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v (should preserve)", cfg.Enrichment.SessionDedup.SessionTimeout, cfgTestSessTO60m)
	}
}

func TestLoadConfig_DeprecatedInjectionKey(t *testing.T) {
	// A config written before the rename uses the legacy "injection" key; it must
	// still load, folding onto the canonical Enrichment field.
	cfg := loadTestConfig(t, `
server:
  name: test-platform
injection:
  trino_semantic_enrichment: false
  semantic_fallback: true
`)
	if cfg.Enrichment.IsTrinoSemanticEnrichmentEnabled() {
		t.Error("legacy injection key did not disable trino_semantic_enrichment")
	}
	if !cfg.Enrichment.IsSemanticFallbackEnabled() {
		t.Error("legacy injection key did not enable semantic_fallback")
	}
	if cfg.EnrichmentDeprecated != nil {
		t.Error("EnrichmentDeprecated should be cleared after compat fold")
	}
}

func TestLoadConfig_EnrichmentKeyWinsOverInjection(t *testing.T) {
	// When both keys are present the canonical "enrichment" key wins.
	cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  trino_semantic_enrichment: true
injection:
  trino_semantic_enrichment: false
`)
	if !cfg.Enrichment.IsTrinoSemanticEnrichmentEnabled() {
		t.Error("enrichment key should win over the deprecated injection key")
	}
}

func TestLoadConfig_SessionDedupFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  trino_semantic_enrichment: true
  session_dedup:
    enabled: false
    mode: summary
    entry_ttl: 10m
    session_timeout: 1h
`)
	if cfg.Enrichment.SessionDedup.IsEnabled() {
		t.Error("SessionDedup.IsEnabled() = true, want false")
	}
	if cfg.Enrichment.SessionDedup.EffectiveMode() != "summary" {
		t.Errorf("SessionDedup.EffectiveMode() = %q, want %q", cfg.Enrichment.SessionDedup.EffectiveMode(), "summary")
	}
	if cfg.Enrichment.SessionDedup.EntryTTL != cfgTestEntryTTL10m {
		t.Errorf("SessionDedup.EntryTTL = %v, want %v", cfg.Enrichment.SessionDedup.EntryTTL, cfgTestEntryTTL10m)
	}
	if cfg.Enrichment.SessionDedup.SessionTimeout != time.Hour {
		t.Errorf("SessionDedup.SessionTimeout = %v, want %v", cfg.Enrichment.SessionDedup.SessionTimeout, time.Hour)
	}
}

func TestApplyDefaults_PortalTitle(t *testing.T) {
	t.Run("defaults to MCP Data Platform", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Portal.Title != "MCP Data Platform" {
			t.Errorf("Portal.Title = %q, want %q", cfg.Portal.Title, "MCP Data Platform")
		}
	})

	t.Run("preserves explicit title", func(t *testing.T) {
		cfg := &Config{
			Portal: PortalConfig{Title: "My Custom Platform"},
		}
		applyDefaults(cfg)
		if cfg.Portal.Title != "My Custom Platform" {
			t.Errorf("Portal.Title = %q, want %q (should preserve)", cfg.Portal.Title, "My Custom Platform")
		}
	})

	t.Run("composes the title from the brand", func(t *testing.T) {
		cfg := &Config{
			Portal: PortalConfig{BrandName: "ACME"},
		}
		applyDefaults(cfg)
		if cfg.Portal.Title != "ACME Portal" {
			t.Errorf("Portal.Title = %q, want %q", cfg.Portal.Title, "ACME Portal")
		}
	})

	t.Run("explicit title beats the brand", func(t *testing.T) {
		cfg := &Config{
			Portal: PortalConfig{BrandName: "ACME", Title: "ACME Analytics"},
		}
		applyDefaults(cfg)
		if cfg.Portal.Title != "ACME Analytics" {
			t.Errorf("Portal.Title = %q, want %q", cfg.Portal.Title, "ACME Analytics")
		}
	})
}

func TestApplyDefaults_PortalBrandFromMCPApps(t *testing.T) {
	// An existing deployment that branded its platform-info app keeps the
	// brand, and keeps the title it renders today: only a brand named in the
	// portal block opts into the composed title, so an upgrade never renames
	// the portal out from under the operator.
	t.Run("backfills brand from the app config without renaming the portal", func(t *testing.T) {
		cfg := &Config{
			MCPApps: MCPAppsConfig{Apps: map[string]AppConfig{
				builtinPlatformInfoName: {Config: map[string]any{
					"brand_name": "ACME",
					"brand_url":  "https://acme.example.com",
				}},
			}},
		}
		applyDefaults(cfg)
		if cfg.Portal.BrandName != "ACME" {
			t.Errorf("Portal.BrandName = %q, want %q", cfg.Portal.BrandName, "ACME")
		}
		if cfg.Portal.BrandURL != "https://acme.example.com" {
			t.Errorf("Portal.BrandURL = %q, want %q", cfg.Portal.BrandURL, "https://acme.example.com")
		}
		if cfg.Portal.Title != "MCP Data Platform" {
			t.Errorf("Portal.Title = %q, want %q (upgrade must not rename)", cfg.Portal.Title, "MCP Data Platform")
		}
	})

	// A disabled MCP Apps subsystem must not drive what the portal renders.
	t.Run("ignores the app config when mcpapps is disabled", func(t *testing.T) {
		cfg := &Config{
			MCPApps: MCPAppsConfig{
				Enabled: new(false),
				Apps: map[string]AppConfig{
					builtinPlatformInfoName: {Config: map[string]any{
						"brand_name": "OldCo",
						"brand_url":  "https://oldco.example.com",
					}},
				},
			},
		}
		applyDefaults(cfg)
		if cfg.Portal.BrandName != "" || cfg.Portal.BrandURL != "" {
			t.Errorf("brand = (%q, %q), want empty", cfg.Portal.BrandName, cfg.Portal.BrandURL)
		}
	})

	t.Run("portal block wins over the app config", func(t *testing.T) {
		cfg := &Config{
			Portal: PortalConfig{BrandName: "Contoso", BrandURL: "https://contoso.example.com"},
			MCPApps: MCPAppsConfig{Apps: map[string]AppConfig{
				builtinPlatformInfoName: {Config: map[string]any{
					"brand_name": "ACME",
					"brand_url":  "https://acme.example.com",
				}},
			}},
		}
		applyDefaults(cfg)
		if cfg.Portal.BrandName != "Contoso" {
			t.Errorf("Portal.BrandName = %q, want %q", cfg.Portal.BrandName, "Contoso")
		}
		if cfg.Portal.BrandURL != "https://contoso.example.com" {
			t.Errorf("Portal.BrandURL = %q, want %q", cfg.Portal.BrandURL, "https://contoso.example.com")
		}
	})

	t.Run("no platform-info app leaves the brand empty", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Portal.BrandName != "" || cfg.Portal.BrandURL != "" {
			t.Errorf("brand = (%q, %q), want empty", cfg.Portal.BrandName, cfg.Portal.BrandURL)
		}
	})
}

func TestLoadConfig_PortalBrandingFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
portal:
  enabled: true
  title: "ACME Data Platform"
  logo: "https://cdn.example.com/logo.svg"
  logo_light: "https://cdn.example.com/logo-light.svg"
  logo_dark: "https://cdn.example.com/logo-dark.svg"
`)
	if cfg.Portal.Enabled == nil || !*cfg.Portal.Enabled {
		t.Error("Portal.Enabled = false, want true")
	}
	if cfg.Portal.Title != "ACME Data Platform" {
		t.Errorf("Portal.Title = %q, want %q", cfg.Portal.Title, "ACME Data Platform")
	}
	if cfg.Portal.Logo != "https://cdn.example.com/logo.svg" {
		t.Errorf("Portal.Logo = %q", cfg.Portal.Logo)
	}
	if cfg.Portal.LogoLight != "https://cdn.example.com/logo-light.svg" {
		t.Errorf("Portal.LogoLight = %q", cfg.Portal.LogoLight)
	}
	if cfg.Portal.LogoDark != "https://cdn.example.com/logo-dark.svg" {
		t.Errorf("Portal.LogoDark = %q", cfg.Portal.LogoDark)
	}
}

func TestApplyDefaults_AdminConfig(t *testing.T) {
	t.Run("defaults applied", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.Admin.Persona != "admin" {
			t.Errorf("Admin.Persona = %q, want %q", cfg.Admin.Persona, "admin")
		}
		if cfg.Admin.PathPrefix != "/api/v1/admin" {
			t.Errorf("Admin.PathPrefix = %q, want %q", cfg.Admin.PathPrefix, "/api/v1/admin")
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{
				Enabled:    new(true),
				Persona:    cfgTestPersonaSuperadmin,
				PathPrefix: "/admin/v2",
			},
		}
		applyDefaults(cfg)
		if cfg.Admin.Persona != cfgTestPersonaSuperadmin {
			t.Errorf("Admin.Persona = %q, want %q (should preserve)", cfg.Admin.Persona, cfgTestPersonaSuperadmin)
		}
		if cfg.Admin.PathPrefix != "/admin/v2" {
			t.Errorf("Admin.PathPrefix = %q, want %q (should preserve)", cfg.Admin.PathPrefix, "/admin/v2")
		}
	})
}

func TestLoadConfig_AdminFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
admin:
  enabled: true
  persona: superadmin
  path_prefix: /admin/v2
`)
	if !cfg.Admin.IsEnabled() {
		t.Error("Admin.IsEnabled() = false, want true")
	}
	if cfg.Admin.Persona != cfgTestPersonaSuperadmin {
		t.Errorf("Admin.Persona = %q, want %q", cfg.Admin.Persona, cfgTestPersonaSuperadmin)
	}
	if cfg.Admin.PathPrefix != "/admin/v2" {
		t.Errorf("Admin.PathPrefix = %q, want %q", cfg.Admin.PathPrefix, "/admin/v2")
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

func TestApplyDefaults_SessionsConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Sessions.Store != "memory" {
		t.Errorf("Sessions.Store = %q, want %q", cfg.Sessions.Store, "memory")
	}
	if cfg.Sessions.TTL != cfgTestDefaultSessTTL {
		t.Errorf("Sessions.TTL = %v, want %v", cfg.Sessions.TTL, cfgTestDefaultSessTTL)
	}
	if cfg.Sessions.CleanupInterval != cfgTestDefaultCleanupInt {
		t.Errorf("Sessions.CleanupInterval = %v, want %v", cfg.Sessions.CleanupInterval, cfgTestDefaultCleanupInt)
	}
}

func TestApplyDefaults_SessionsPreservesExisting(t *testing.T) {
	cfg := &Config{
		Sessions: SessionsConfig{
			Store:           SessionStoreDatabase,
			TTL:             cfgTestCustomSessionsTTL,
			CleanupInterval: cfgTestCustomCleanup,
		},
	}
	applyDefaults(cfg)

	if cfg.Sessions.Store != SessionStoreDatabase {
		t.Errorf("Sessions.Store = %q, want %q (should preserve)", cfg.Sessions.Store, SessionStoreDatabase)
	}
	if cfg.Sessions.TTL != cfgTestCustomSessionsTTL {
		t.Errorf("Sessions.TTL = %v, want %v (should preserve)", cfg.Sessions.TTL, cfgTestCustomSessionsTTL)
	}
	if cfg.Sessions.CleanupInterval != cfgTestCustomCleanup {
		t.Errorf("Sessions.CleanupInterval = %v, want %v (should preserve)", cfg.Sessions.CleanupInterval, cfgTestCustomCleanup)
	}
}

func TestLoadConfig_SessionsFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
sessions:
  store: database
  ttl: 15m
  cleanup_interval: 2m
`)
	if cfg.Sessions.Store != SessionStoreDatabase {
		t.Errorf("Sessions.Store = %q, want %q", cfg.Sessions.Store, SessionStoreDatabase)
	}
	if cfg.Sessions.TTL != cfgTestCustomSessionsTTL {
		t.Errorf("Sessions.TTL = %v, want %v", cfg.Sessions.TTL, cfgTestCustomSessionsTTL)
	}
	if cfg.Sessions.CleanupInterval != cfgTestCustomCleanup {
		t.Errorf("Sessions.CleanupInterval = %v, want %v", cfg.Sessions.CleanupInterval, cfgTestCustomCleanup)
	}
}

func TestConfigValidate_SessionsDatabaseWithoutDSN(t *testing.T) {
	cfg := &Config{
		Sessions: SessionsConfig{
			Store: SessionStoreDatabase,
		},
		Database: DatabaseConfig{
			DSN: "",
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for sessions.store=database without DSN")
	}
}

func TestConfigValidate_SessionsDatabaseWithDSN(t *testing.T) {
	cfg := &Config{
		Sessions: SessionsConfig{
			Store: SessionStoreDatabase,
		},
		Database: DatabaseConfig{
			DSN: "postgres://localhost/test",
		},
	}
	err := cfg.Validate()
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestLoadConfig_ToolsConfig(t *testing.T) {
	t.Run("allow and deny", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
tools:
  allow:
    - "trino_*"
    - "datahub_*"
  deny:
    - "*_delete_*"
`)
		if len(cfg.Tools.Allow) != 2 {
			t.Fatalf("Tools.Allow length = %d, want 2", len(cfg.Tools.Allow))
		}
		if cfg.Tools.Allow[0] != "trino_*" {
			t.Errorf("Tools.Allow[0] = %q, want %q", cfg.Tools.Allow[0], "trino_*")
		}
		if cfg.Tools.Allow[1] != "datahub_*" {
			t.Errorf("Tools.Allow[1] = %q, want %q", cfg.Tools.Allow[1], "datahub_*")
		}
		if len(cfg.Tools.Deny) != 1 {
			t.Fatalf("Tools.Deny length = %d, want 1", len(cfg.Tools.Deny))
		}
		if cfg.Tools.Deny[0] != "*_delete_*" {
			t.Errorf("Tools.Deny[0] = %q, want %q", cfg.Tools.Deny[0], "*_delete_*")
		}
	})

	t.Run("empty tools section", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
tools: {}
`)
		if len(cfg.Tools.Allow) != 0 {
			t.Errorf("Tools.Allow length = %d, want 0", len(cfg.Tools.Allow))
		}
		if len(cfg.Tools.Deny) != 0 {
			t.Errorf("Tools.Deny length = %d, want 0", len(cfg.Tools.Deny))
		}
	})

	t.Run("no tools section", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if len(cfg.Tools.Allow) != 0 {
			t.Errorf("Tools.Allow length = %d, want 0", len(cfg.Tools.Allow))
		}
		if len(cfg.Tools.Deny) != 0 {
			t.Errorf("Tools.Deny length = %d, want 0", len(cfg.Tools.Deny))
		}
	})
}

func TestEnrichmentConfig_IsUnwrapJSONEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if !cfg.IsUnwrapJSONEnabled() {
			t.Error("expected nil UnwrapJSON to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		v := true
		cfg := &EnrichmentConfig{UnwrapJSON: &v}
		if !cfg.IsUnwrapJSONEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		v := false
		cfg := &EnrichmentConfig{UnwrapJSON: &v}
		if cfg.IsUnwrapJSONEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with unwrap_json false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  unwrap_json: false
`)
		if cfg.Enrichment.IsUnwrapJSONEnabled() {
			t.Error("expected unwrap_json: false to disable unwrap")
		}
	})

	t.Run("YAML loading without unwrap_json", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  trino_semantic_enrichment: true
`)
		if !cfg.Enrichment.IsUnwrapJSONEnabled() {
			t.Error("expected missing unwrap_json to default to true")
		}
	})
}

func TestEnrichmentConfig_IsColumnContextFilteringEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if !cfg.IsColumnContextFilteringEnabled() {
			t.Error("expected nil ColumnContextFiltering to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		v := true
		cfg := &EnrichmentConfig{ColumnContextFiltering: &v}
		if !cfg.IsColumnContextFilteringEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		v := false
		cfg := &EnrichmentConfig{ColumnContextFiltering: &v}
		if cfg.IsColumnContextFilteringEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with column_context_filtering false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  column_context_filtering: false
`)
		if cfg.Enrichment.IsColumnContextFilteringEnabled() {
			t.Error("expected column_context_filtering: false to disable filtering")
		}
	})

	t.Run("YAML loading without column_context_filtering", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  trino_semantic_enrichment: true
`)
		if !cfg.Enrichment.IsColumnContextFilteringEnabled() {
			t.Error("expected missing column_context_filtering to default to true")
		}
	})
}

func TestEnrichmentConfig_IsSearchSchemaPreviewEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if !cfg.IsSearchSchemaPreviewEnabled() {
			t.Error("expected nil SearchSchemaPreview to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		v := true
		cfg := &EnrichmentConfig{SearchSchemaPreview: &v}
		if !cfg.IsSearchSchemaPreviewEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		v := false
		cfg := &EnrichmentConfig{SearchSchemaPreview: &v}
		if cfg.IsSearchSchemaPreviewEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with search_schema_preview false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  search_schema_preview: false
`)
		if cfg.Enrichment.IsSearchSchemaPreviewEnabled() {
			t.Error("expected search_schema_preview: false to disable")
		}
	})
}

func TestEnrichmentConfig_EffectiveSchemaPreviewMaxColumns(t *testing.T) {
	t.Run("nil defaults to 15", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.EffectiveSchemaPreviewMaxColumns() != defaultSchemaPreviewMaxColumns {
			t.Errorf("expected %d, got %d", defaultSchemaPreviewMaxColumns, cfg.EffectiveSchemaPreviewMaxColumns())
		}
	})

	t.Run("explicit value", func(t *testing.T) {
		v := 5
		cfg := &EnrichmentConfig{SchemaPreviewMaxColumns: &v}
		if cfg.EffectiveSchemaPreviewMaxColumns() != 5 {
			t.Errorf("expected 5, got %d", cfg.EffectiveSchemaPreviewMaxColumns())
		}
	})

	t.Run("YAML loading", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  schema_preview_max_columns: 10
`)
		if cfg.Enrichment.EffectiveSchemaPreviewMaxColumns() != 10 {
			t.Errorf("expected 10, got %d", cfg.Enrichment.EffectiveSchemaPreviewMaxColumns())
		}
	})
}

func TestEnrichmentConfig_EffectiveMemoryLimit(t *testing.T) {
	t.Run("nil defaults to 5", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.EffectiveMemoryLimit() != defaultMemoryLimit {
			t.Errorf("expected %d, got %d", defaultMemoryLimit, cfg.EffectiveMemoryLimit())
		}
	})
	t.Run("non-positive floors to default", func(t *testing.T) {
		v := 0
		cfg := &EnrichmentConfig{MemoryLimit: &v}
		if cfg.EffectiveMemoryLimit() != defaultMemoryLimit {
			t.Errorf("expected %d, got %d", defaultMemoryLimit, cfg.EffectiveMemoryLimit())
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		v := 12
		cfg := &EnrichmentConfig{MemoryLimit: &v}
		if cfg.EffectiveMemoryLimit() != 12 {
			t.Errorf("expected 12, got %d", cfg.EffectiveMemoryLimit())
		}
	})
	t.Run("YAML loading", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  memory_limit: 3
`)
		if cfg.Enrichment.EffectiveMemoryLimit() != 3 {
			t.Errorf("expected 3, got %d", cfg.Enrichment.EffectiveMemoryLimit())
		}
	})
}

func TestEnrichmentConfig_EffectiveMemoryContextBudgetBytes(t *testing.T) {
	t.Run("nil defaults", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.EffectiveMemoryContextBudgetBytes() != defaultMemoryContextBudgetBytes {
			t.Errorf("expected %d, got %d", defaultMemoryContextBudgetBytes, cfg.EffectiveMemoryContextBudgetBytes())
		}
	})
	t.Run("zero disables the budget", func(t *testing.T) {
		v := 0
		cfg := &EnrichmentConfig{MemoryContextBudgetBytes: &v}
		if cfg.EffectiveMemoryContextBudgetBytes() != 0 {
			t.Errorf("expected 0 (disabled), got %d", cfg.EffectiveMemoryContextBudgetBytes())
		}
	})
	t.Run("negative falls back to default", func(t *testing.T) {
		v := -10
		cfg := &EnrichmentConfig{MemoryContextBudgetBytes: &v}
		if cfg.EffectiveMemoryContextBudgetBytes() != defaultMemoryContextBudgetBytes {
			t.Errorf("expected %d, got %d", defaultMemoryContextBudgetBytes, cfg.EffectiveMemoryContextBudgetBytes())
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		v := 4096
		cfg := &EnrichmentConfig{MemoryContextBudgetBytes: &v}
		if cfg.EffectiveMemoryContextBudgetBytes() != 4096 {
			t.Errorf("expected 4096, got %d", cfg.EffectiveMemoryContextBudgetBytes())
		}
	})
}

func TestEnrichmentConfig_EffectiveMemorySummaryBytes(t *testing.T) {
	t.Run("nil defaults", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.EffectiveMemorySummaryBytes() != defaultMemorySummaryBytes {
			t.Errorf("expected %d, got %d", defaultMemorySummaryBytes, cfg.EffectiveMemorySummaryBytes())
		}
	})
	t.Run("zero disables truncation", func(t *testing.T) {
		v := 0
		cfg := &EnrichmentConfig{MemorySummaryBytes: &v}
		if cfg.EffectiveMemorySummaryBytes() != 0 {
			t.Errorf("expected 0 (disabled), got %d", cfg.EffectiveMemorySummaryBytes())
		}
	})
	t.Run("negative falls back to default", func(t *testing.T) {
		v := -1
		cfg := &EnrichmentConfig{MemorySummaryBytes: &v}
		if cfg.EffectiveMemorySummaryBytes() != defaultMemorySummaryBytes {
			t.Errorf("expected %d, got %d", defaultMemorySummaryBytes, cfg.EffectiveMemorySummaryBytes())
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		v := 500
		cfg := &EnrichmentConfig{MemorySummaryBytes: &v}
		if cfg.EffectiveMemorySummaryBytes() != 500 {
			t.Errorf("expected 500, got %d", cfg.EffectiveMemorySummaryBytes())
		}
	})
}

func TestEnrichmentConfig_IsSemanticFallbackEnabled(t *testing.T) {
	t.Run("nil defaults to off", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.IsSemanticFallbackEnabled() {
			t.Error("default should be disabled per issue #444 acceptance criteria")
		}
	})
	t.Run("explicit true", func(t *testing.T) {
		v := true
		cfg := &EnrichmentConfig{SemanticFallback: &v}
		if !cfg.IsSemanticFallbackEnabled() {
			t.Error("expected enabled when explicitly true")
		}
	})
	t.Run("explicit false", func(t *testing.T) {
		v := false
		cfg := &EnrichmentConfig{SemanticFallback: &v}
		if cfg.IsSemanticFallbackEnabled() {
			t.Error("expected disabled when explicitly false")
		}
	})
	t.Run("YAML loading", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
enrichment:
  semantic_fallback: true
`)
		if !cfg.Enrichment.IsSemanticFallbackEnabled() {
			t.Error("expected enabled from YAML")
		}
	})
}

func TestEnrichmentConfig_EffectiveSemanticFallbackTopK(t *testing.T) {
	t.Run("nil defaults to 1", func(t *testing.T) {
		cfg := &EnrichmentConfig{}
		if cfg.EffectiveSemanticFallbackTopK() != defaultSemanticFallbackTopK {
			t.Errorf("got %d, want %d", cfg.EffectiveSemanticFallbackTopK(), defaultSemanticFallbackTopK)
		}
	})
	t.Run("explicit value", func(t *testing.T) {
		v := 5
		cfg := &EnrichmentConfig{SemanticFallbackTopK: &v}
		if cfg.EffectiveSemanticFallbackTopK() != 5 {
			t.Errorf("got %d, want 5", cfg.EffectiveSemanticFallbackTopK())
		}
	})
	t.Run("clamps zero to 1", func(t *testing.T) {
		v := 0
		cfg := &EnrichmentConfig{SemanticFallbackTopK: &v}
		if cfg.EffectiveSemanticFallbackTopK() != 1 {
			t.Errorf("got %d, want 1 (clamped)", cfg.EffectiveSemanticFallbackTopK())
		}
	})
	t.Run("clamps negative to 1", func(t *testing.T) {
		v := -3
		cfg := &EnrichmentConfig{SemanticFallbackTopK: &v}
		if cfg.EffectiveSemanticFallbackTopK() != 1 {
			t.Errorf("got %d, want 1 (clamped)", cfg.EffectiveSemanticFallbackTopK())
		}
	})
	t.Run("clamps above max", func(t *testing.T) {
		v := 999
		cfg := &EnrichmentConfig{SemanticFallbackTopK: &v}
		if cfg.EffectiveSemanticFallbackTopK() != maxSemanticFallbackTopK {
			t.Errorf("got %d, want %d (clamped)", cfg.EffectiveSemanticFallbackTopK(), maxSemanticFallbackTopK)
		}
	})
}

func TestApplyDefaults_SessionGateConfig(t *testing.T) {
	t.Run("defaults init tool to platform_info", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.SessionGate.InitTool != defaultInitTool {
			t.Errorf("SessionGate.InitTool = %q, want %q", cfg.SessionGate.InitTool, defaultInitTool)
		}
	})

	t.Run("preserves explicit init tool", func(t *testing.T) {
		cfg := &Config{
			SessionGate: SessionGateConfig{
				InitTool: "custom_init",
			},
		}
		applyDefaults(cfg)
		if cfg.SessionGate.InitTool != "custom_init" {
			t.Errorf("SessionGate.InitTool = %q, want %q", cfg.SessionGate.InitTool, "custom_init")
		}
	})

	t.Run("disabled by default", func(t *testing.T) {
		cfg := &Config{}
		applyDefaults(cfg)
		if cfg.SessionGate.Enabled {
			t.Error("SessionGate.Enabled should default to false")
		}
	})
}

func TestLoadConfig_SessionGateFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
session_gate:
  enabled: true
  init_tool: platform_info
  exempt_tools:
    - list_connections
    - read_resource
`)
	if !cfg.SessionGate.Enabled {
		t.Error("SessionGate.Enabled = false, want true")
	}
	if cfg.SessionGate.InitTool != "platform_info" {
		t.Errorf("SessionGate.InitTool = %q, want %q", cfg.SessionGate.InitTool, "platform_info")
	}
	if len(cfg.SessionGate.ExemptTools) != 2 {
		t.Fatalf("SessionGate.ExemptTools length = %d, want 2", len(cfg.SessionGate.ExemptTools))
	}
	if cfg.SessionGate.ExemptTools[0] != cfgTestToolListConns {
		t.Errorf("ExemptTools[0] = %q, want %q", cfg.SessionGate.ExemptTools[0], cfgTestToolListConns)
	}
}

func TestParseToolsDenyValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"whitespace", "   ", nil, false},
		{"valid array", `["a","b"]`, []string{"a", "b"}, false},
		{"empty array", `[]`, []string{}, false},
		{"invalid json", "not json", nil, true},
		{"non-array json", `"a string"`, nil, true},
	}
	for _, tc := range tests {
		got, err := parseToolsDenyValue(tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: parseToolsDenyValue(%q) err=%v, wantErr=%v", tc.name, tc.value, err, tc.wantErr)
			continue
		}
		if tc.wantErr {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: parseToolsDenyValue(%q) got %v, want %v", tc.name, tc.value, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: parseToolsDenyValue(%q)[%d] = %q, want %q", tc.name, tc.value, i, got[i], tc.want[i])
			}
		}
	}
}

func TestToolDescriptionKey(t *testing.T) {
	tests := []struct {
		key      string
		wantName string
		wantOK   bool
	}{
		{"tool.trino_query.description", "trino_query", true},
		{"tool.dev-mock__echo.description", "dev-mock__echo", true},
		{"tool..description", "", false},
		{"server.description", "", false},
		{"tool.x.description-suffix", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		gotName, gotOK := toolDescriptionKey(tc.key)
		if gotName != tc.wantName || gotOK != tc.wantOK {
			t.Errorf("toolDescriptionKey(%q) = (%q, %v), want (%q, %v)",
				tc.key, gotName, gotOK, tc.wantName, tc.wantOK)
		}
	}
}

func TestProgressConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &ProgressConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &ProgressConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &ProgressConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with progress.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
progress:
  enabled: false
`)
		if cfg.Progress.IsEnabled() {
			t.Error("expected progress.enabled: false to disable progress notifications")
		}
	})

	t.Run("YAML loading without progress block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.Progress.IsEnabled() {
			t.Error("expected missing progress block to default to true")
		}
	})
}

func TestAdminConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		if !(&AdminConfig{}).IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})
	t.Run("explicit true", func(t *testing.T) {
		if !(&AdminConfig{Enabled: new(true)}).IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})
	t.Run("explicit false", func(t *testing.T) {
		if (&AdminConfig{Enabled: new(false)}).IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})
	t.Run("YAML admin.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\nadmin:\n  enabled: false\n")
		if cfg.Admin.IsEnabled() {
			t.Error("expected admin.enabled: false to disable the admin API")
		}
	})
	t.Run("YAML without admin block", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\n")
		if !cfg.Admin.IsEnabled() {
			t.Error("expected missing admin block to default to enabled")
		}
	})
}

func TestKnowledgeApplyConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		if !(&KnowledgeApplyConfig{}).IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})
	t.Run("explicit true", func(t *testing.T) {
		if !(&KnowledgeApplyConfig{Enabled: new(true)}).IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})
	t.Run("explicit false", func(t *testing.T) {
		if (&KnowledgeApplyConfig{Enabled: new(false)}).IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})
}

func TestAuditConfig_IsToolCallLoggingEnabled(t *testing.T) {
	t.Run("both nil defaults to enabled", func(t *testing.T) {
		if !(&AuditConfig{}).IsToolCallLoggingEnabled() {
			t.Error("expected nil audit + nil log_tool_calls to default to enabled")
		}
	})
	t.Run("log_tool_calls explicit false", func(t *testing.T) {
		if (&AuditConfig{LogToolCalls: new(false)}).IsToolCallLoggingEnabled() {
			t.Error("expected log_tool_calls: false to disable per-call logging")
		}
	})
	t.Run("audit explicit false disables tool-call logging", func(t *testing.T) {
		if (&AuditConfig{Enabled: new(false)}).IsToolCallLoggingEnabled() {
			t.Error("expected audit.enabled: false to disable per-call logging even with log_tool_calls nil")
		}
	})
	t.Run("both explicit true", func(t *testing.T) {
		if !(&AuditConfig{Enabled: new(true), LogToolCalls: new(true)}).IsToolCallLoggingEnabled() {
			t.Error("expected both true to enable per-call logging")
		}
	})
}

func TestAuditConfig_IsParameterLoggingEnabled(t *testing.T) {
	t.Run("nil defaults to enabled", func(t *testing.T) {
		if !(&AuditConfig{}).IsParameterLoggingEnabled() {
			t.Error("expected nil log_parameters to default to enabled")
		}
	})
	t.Run("explicit false disables", func(t *testing.T) {
		if (&AuditConfig{LogParameters: new(false)}).IsParameterLoggingEnabled() {
			t.Error("expected log_parameters: false to disable parameter capture")
		}
	})
	t.Run("explicit true enables", func(t *testing.T) {
		if !(&AuditConfig{LogParameters: new(true)}).IsParameterLoggingEnabled() {
			t.Error("expected log_parameters: true to enable parameter capture")
		}
	})
}

func TestAuditConfig_DeliveryMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to async", "", AuditDeliveryAsync},
		{"async", "async", AuditDeliveryAsync},
		{"sync", "sync", AuditDeliverySync},
		{"sync mixed case", "Sync", AuditDeliverySync},
		{"sync padded", "  sync  ", AuditDeliverySync},
		{"unrecognized resolves to async", "synch", AuditDeliveryAsync},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&AuditConfig{Delivery: tc.in}).DeliveryMode()
			if got != tc.want {
				t.Errorf("DeliveryMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAuditConfig_ValidateDelivery(t *testing.T) {
	valid := []string{"", "async", "sync", "SYNC", " async "}
	for _, v := range valid {
		if err := (&AuditConfig{Delivery: v}).ValidateDelivery(); err != nil {
			t.Errorf("ValidateDelivery(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"synch", "asynchronous", "none", "off"}
	for _, v := range invalid {
		if err := (&AuditConfig{Delivery: v}).ValidateDelivery(); err == nil {
			t.Errorf("ValidateDelivery(%q) expected error, got nil", v)
		}
	}
}

func TestConfigValidate_RejectsBadAuditDelivery(t *testing.T) {
	cfg := &Config{Audit: AuditConfig{Delivery: "synch"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected Validate to reject audit.delivery=synch")
	}
	if !strings.Contains(err.Error(), "audit.delivery") {
		t.Errorf("expected error to mention audit.delivery, got %v", err)
	}
}

func TestWorkflowConfig_IsRequireSearchEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		if !(&WorkflowConfig{}).IsRequireSearchEnabled() {
			t.Error("expected nil RequireSearch to default to true")
		}
	})
	t.Run("explicit true", func(t *testing.T) {
		if !(&WorkflowConfig{RequireSearch: new(true)}).IsRequireSearchEnabled() {
			t.Error("expected explicit true to return true")
		}
	})
	t.Run("explicit false", func(t *testing.T) {
		if (&WorkflowConfig{RequireSearch: new(false)}).IsRequireSearchEnabled() {
			t.Error("expected explicit false to return false")
		}
	})
	t.Run("YAML workflow.require_search false", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\nworkflow:\n  require_search: false\n")
		if cfg.Workflow.IsRequireSearchEnabled() {
			t.Error("expected require_search: false to disable the gate")
		}
	})
	t.Run("YAML without workflow block", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\n")
		if !cfg.Workflow.IsRequireSearchEnabled() {
			t.Error("expected missing workflow block to default to enabled")
		}
	})
}

func TestRateLimitConfig(t *testing.T) {
	t.Run("nil Enabled defaults to true", func(t *testing.T) {
		if !(&RateLimitConfig{}).IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})
	t.Run("explicit false disables", func(t *testing.T) {
		if (&RateLimitConfig{Enabled: new(false)}).IsEnabled() {
			t.Error("expected explicit false to disable")
		}
	})
	t.Run("explicit true enables", func(t *testing.T) {
		if !(&RateLimitConfig{Enabled: new(true)}).IsEnabled() {
			t.Error("expected explicit true to enable")
		}
	})
	t.Run("EffectiveRPM default when unset", func(t *testing.T) {
		if got := (&RateLimitConfig{}).EffectiveRPM(); got != defaultRateLimitRPM {
			t.Errorf("EffectiveRPM() = %d, want default %d", got, defaultRateLimitRPM)
		}
	})
	t.Run("EffectiveRPM default when non-positive", func(t *testing.T) {
		if got := (&RateLimitConfig{RequestsPerMinute: -5}).EffectiveRPM(); got != defaultRateLimitRPM {
			t.Errorf("EffectiveRPM() = %d, want default %d", got, defaultRateLimitRPM)
		}
	})
	t.Run("EffectiveRPM honors positive value", func(t *testing.T) {
		if got := (&RateLimitConfig{RequestsPerMinute: 500}).EffectiveRPM(); got != 500 {
			t.Errorf("EffectiveRPM() = %d, want 500", got)
		}
	})
	t.Run("EffectiveBurst default when unset", func(t *testing.T) {
		if got := (&RateLimitConfig{}).EffectiveBurst(); got != defaultRateLimitBurst {
			t.Errorf("EffectiveBurst() = %d, want default %d", got, defaultRateLimitBurst)
		}
	})
	t.Run("EffectiveBurst default when non-positive", func(t *testing.T) {
		if got := (&RateLimitConfig{Burst: 0}).EffectiveBurst(); got != defaultRateLimitBurst {
			t.Errorf("EffectiveBurst() = %d, want default %d", got, defaultRateLimitBurst)
		}
	})
	t.Run("EffectiveBurst honors positive value", func(t *testing.T) {
		if got := (&RateLimitConfig{Burst: 15}).EffectiveBurst(); got != 15 {
			t.Errorf("EffectiveBurst() = %d, want 15", got)
		}
	})
	t.Run("YAML rate_limit block parses", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\nrate_limit:\n  enabled: false\n  requests_per_minute: 90\n  burst: 12\n  exempt_tools: [search]\n")
		if cfg.RateLimit.IsEnabled() {
			t.Error("expected rate_limit.enabled: false to disable")
		}
		if cfg.RateLimit.EffectiveRPM() != 90 {
			t.Errorf("EffectiveRPM() = %d, want 90", cfg.RateLimit.EffectiveRPM())
		}
		if cfg.RateLimit.EffectiveBurst() != 12 {
			t.Errorf("EffectiveBurst() = %d, want 12", cfg.RateLimit.EffectiveBurst())
		}
		if len(cfg.RateLimit.ExemptTools) != 1 || cfg.RateLimit.ExemptTools[0] != "search" {
			t.Errorf("ExemptTools = %v, want [search]", cfg.RateLimit.ExemptTools)
		}
	})
	t.Run("missing rate_limit block defaults to enabled", func(t *testing.T) {
		cfg := loadTestConfig(t, "server:\n  name: test-platform\n")
		if !cfg.RateLimit.IsEnabled() {
			t.Error("expected missing rate_limit block to default to enabled")
		}
	})
}

func TestClientLoggingConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &ClientLoggingConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &ClientLoggingConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &ClientLoggingConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with client_logging.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
client_logging:
  enabled: false
`)
		if cfg.ClientLogging.IsEnabled() {
			t.Error("expected client_logging.enabled: false to disable client logging")
		}
	})

	t.Run("YAML loading without client_logging block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.ClientLogging.IsEnabled() {
			t.Error("expected missing client_logging block to default to true")
		}
	})
}

func TestIconsConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &IconsConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &IconsConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &IconsConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with icons.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
icons:
  enabled: false
`)
		if cfg.Icons.IsEnabled() {
			t.Error("expected icons.enabled: false to disable icon injection")
		}
	})

	t.Run("YAML loading without icons block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.Icons.IsEnabled() {
			t.Error("expected missing icons block to default to true")
		}
	})
}

func TestElicitationConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &ElicitationConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &ElicitationConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &ElicitationConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with elicitation.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
elicitation:
  enabled: false
`)
		if cfg.Elicitation.IsEnabled() {
			t.Error("expected elicitation.enabled: false to disable elicitation")
		}
	})

	t.Run("YAML loading without elicitation block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.Elicitation.IsEnabled() {
			t.Error("expected missing elicitation block to default to true")
		}
	})
}

func TestCostEstimationConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &CostEstimationConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &CostEstimationConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &CostEstimationConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with cost_estimation.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
elicitation:
  cost_estimation:
    enabled: false
`)
		if cfg.Elicitation.CostEstimation.IsEnabled() {
			t.Error("expected cost_estimation.enabled: false to disable cost estimation")
		}
	})

	t.Run("YAML loading without cost_estimation block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.Elicitation.CostEstimation.IsEnabled() {
			t.Error("expected missing cost_estimation block to default to true")
		}
	})
}

func TestPIIConsentConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		cfg := &PIIConsentConfig{}
		if !cfg.IsEnabled() {
			t.Error("expected nil Enabled to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		cfg := &PIIConsentConfig{Enabled: new(true)}
		if !cfg.IsEnabled() {
			t.Error("expected explicit true to return true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		cfg := &PIIConsentConfig{Enabled: new(false)}
		if cfg.IsEnabled() {
			t.Error("expected explicit false to return false")
		}
	})

	t.Run("YAML loading with pii_consent.enabled false", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
elicitation:
  pii_consent:
    enabled: false
`)
		if cfg.Elicitation.PIIConsent.IsEnabled() {
			t.Error("expected pii_consent.enabled: false to disable PII consent prompts")
		}
	})

	t.Run("YAML loading without pii_consent block", func(t *testing.T) {
		cfg := loadTestConfig(t, `
server:
  name: test-platform
`)
		if !cfg.Elicitation.PIIConsent.IsEnabled() {
			t.Error("expected missing pii_consent block to default to true")
		}
	})
}

// TestDefaultOnFeatures_NoConfigBlocks is the acceptance test for issue #784:
// with none of progress/client_logging/icons/elicitation present in config,
// all six gated switches must resolve active.
func TestDefaultOnFeatures_NoConfigBlocks(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  name: test-platform
`)

	if !cfg.Progress.IsEnabled() {
		t.Error("progress should default to enabled")
	}
	if !cfg.ClientLogging.IsEnabled() {
		t.Error("client_logging should default to enabled")
	}
	if !cfg.Icons.IsEnabled() {
		t.Error("icons should default to enabled")
	}
	if !cfg.Elicitation.IsEnabled() {
		t.Error("elicitation should default to enabled")
	}
	if !cfg.Elicitation.CostEstimation.IsEnabled() {
		t.Error("elicitation.cost_estimation should default to enabled")
	}
	if !cfg.Elicitation.PIIConsent.IsEnabled() {
		t.Error("elicitation.pii_consent should default to enabled")
	}
}
