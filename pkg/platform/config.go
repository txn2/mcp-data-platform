// Package platform provides the main platform orchestration.
package platform

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

// Config holds the complete platform configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Auth      AuthConfig      `yaml:"auth"`
	OAuth     OAuthConfig     `yaml:"oauth"`
	Database  DatabaseConfig  `yaml:"database"`
	Personas  PersonasConfig  `yaml:"personas"`
	Toolkits  map[string]any  `yaml:"toolkits"`
	Semantic  SemanticConfig  `yaml:"semantic"`
	Query     QueryConfig     `yaml:"query"`
	Storage   StorageConfig   `yaml:"storage"`
	Injection InjectionConfig `yaml:"injection"`
	Tuning    TuningConfig    `yaml:"tuning"`
	Audit     AuditConfig     `yaml:"audit"`
	MCPApps   MCPAppsConfig   `yaml:"mcpapps"`
}

// ServerConfig configures the MCP server.
type ServerConfig struct {
	Name              string         `yaml:"name"`
	Version           string         `yaml:"version"`
	Description       string         `yaml:"description"`
	Tags              []string       `yaml:"tags"`               // Discovery keywords for routing
	AgentInstructions string         `yaml:"agent_instructions"` // Inline operational guidance for AI agents
	Prompts           []PromptConfig `yaml:"prompts"`            // Platform-level MCP prompts
	Transport         string         `yaml:"transport"`          // "stdio", "sse"
	Address           string         `yaml:"address"`
	TLS               TLSConfig      `yaml:"tls"`
}

// PromptConfig defines a platform-level MCP prompt.
type PromptConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Content     string `yaml:"content"`
}

// TLSConfig configures TLS.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig configures authentication.
type AuthConfig struct {
	OIDC           OIDCAuthConfig   `yaml:"oidc"`
	APIKeys        APIKeyAuthConfig `yaml:"api_keys"`
	AllowAnonymous bool             `yaml:"allow_anonymous"` // default: false
}

// OIDCAuthConfig configures OIDC authentication.
type OIDCAuthConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Issuer        string `yaml:"issuer"`
	ClientID      string `yaml:"client_id"`
	Audience      string `yaml:"audience"`
	RoleClaimPath string `yaml:"role_claim_path"`
	RolePrefix    string `yaml:"role_prefix"`
}

// APIKeyAuthConfig configures API key authentication.
type APIKeyAuthConfig struct {
	Enabled bool        `yaml:"enabled"`
	Keys    []APIKeyDef `yaml:"keys"`
}

// APIKeyDef defines an API key.
type APIKeyDef struct {
	Key   string   `yaml:"key"`
	Name  string   `yaml:"name"`
	Roles []string `yaml:"roles"`
}

// OAuthConfig configures the OAuth server.
type OAuthConfig struct {
	Enabled    bool                `yaml:"enabled"`
	Issuer     string              `yaml:"issuer"`
	SigningKey string              `yaml:"signing_key"` // Base64-encoded HMAC key for JWT signing
	Clients    []OAuthClientConfig `yaml:"clients"`
	DCR        DCRConfig           `yaml:"dcr"`
	Upstream   *UpstreamIDPConfig  `yaml:"upstream,omitempty"`
}

// OAuthClientConfig defines a pre-registered OAuth client.
type OAuthClientConfig struct {
	ID           string   `yaml:"id"`
	Secret       string   `yaml:"secret"`
	RedirectURIs []string `yaml:"redirect_uris"`
}

// DCRConfig configures Dynamic Client Registration.
type DCRConfig struct {
	Enabled                 bool     `yaml:"enabled"`
	AllowedRedirectPatterns []string `yaml:"allowed_redirect_patterns"`
}

// UpstreamIDPConfig configures the upstream identity provider (e.g., Keycloak).
type UpstreamIDPConfig struct {
	Issuer       string `yaml:"issuer"`        // Keycloak issuer URL
	ClientID     string `yaml:"client_id"`     // MCP Server's client ID in Keycloak
	ClientSecret string `yaml:"client_secret"` // MCP Server's client secret
	RedirectURI  string `yaml:"redirect_uri"`  // Callback URL (e.g., http://localhost:8080/oauth/callback)
}

// DatabaseConfig configures the database connection.
type DatabaseConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
}

// PersonasConfig holds persona definitions.
type PersonasConfig struct {
	Definitions    map[string]PersonaDef `yaml:",inline"`
	DefaultPersona string                `yaml:"default_persona"`
	RoleMapping    RoleMappingConfig     `yaml:"role_mapping"`
}

// PersonaDef defines a persona.
type PersonaDef struct {
	DisplayName string            `yaml:"display_name"`
	Description string            `yaml:"description,omitempty"`
	Roles       []string          `yaml:"roles"`
	Tools       ToolRulesDef      `yaml:"tools"`
	Prompts     PromptsDef        `yaml:"prompts"`
	Hints       map[string]string `yaml:"hints,omitempty"`
	Priority    int               `yaml:"priority,omitempty"`
}

// ToolRulesDef defines tool access rules.
type ToolRulesDef struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// PromptsDef defines prompt customizations.
type PromptsDef struct {
	SystemPrefix string `yaml:"system_prefix,omitempty"`
	SystemSuffix string `yaml:"system_suffix,omitempty"`
	Instructions string `yaml:"instructions,omitempty"`
}

// RoleMappingConfig configures role mapping.
type RoleMappingConfig struct {
	OIDCToPersona map[string]string `yaml:"oidc_to_persona"`
	UserPersonas  map[string]string `yaml:"user_personas"`
}

// SemanticConfig configures the semantic layer.
type SemanticConfig struct {
	Provider   string                        `yaml:"provider"` // "datahub", "noop"
	Instance   string                        `yaml:"instance"`
	Cache      CacheConfig                   `yaml:"cache"`
	URNMapping URNMappingConfig              `yaml:"urn_mapping"`
	Lineage    datahubsemantic.LineageConfig `yaml:"lineage"`
}

// URNMappingConfig configures URN translation between query engines and metadata catalogs.
// This is necessary when Trino catalog/platform names differ from DataHub's metadata catalog names.
type URNMappingConfig struct {
	// Platform overrides the platform name used in DataHub URN building.
	// For example, if Trino queries a PostgreSQL database, set this to "postgres"
	// so URNs match DataHub's platform identifier.
	Platform string `yaml:"platform"`

	// CatalogMapping maps catalog names between systems.
	// For semantic provider: maps Trino catalogs to DataHub catalogs (rdbms → warehouse)
	// For query provider: maps DataHub catalogs to Trino catalogs (warehouse → rdbms)
	CatalogMapping map[string]string `yaml:"catalog_mapping"`
}

// CacheConfig configures caching.
type CacheConfig struct {
	Enabled bool          `yaml:"enabled"`
	TTL     time.Duration `yaml:"ttl"`
}

// QueryConfig configures the query provider.
type QueryConfig struct {
	Provider   string           `yaml:"provider"` // "trino", "noop"
	Instance   string           `yaml:"instance"`
	URNMapping URNMappingConfig `yaml:"urn_mapping"`
}

// StorageConfig configures the storage provider.
type StorageConfig struct {
	Provider string `yaml:"provider"` // "s3", "noop"
	Instance string `yaml:"instance"`
}

// InjectionConfig configures cross-injection.
type InjectionConfig struct {
	TrinoSemanticEnrichment  bool `yaml:"trino_semantic_enrichment"`
	DataHubQueryEnrichment   bool `yaml:"datahub_query_enrichment"`
	S3SemanticEnrichment     bool `yaml:"s3_semantic_enrichment"`
	DataHubStorageEnrichment bool `yaml:"datahub_storage_enrichment"`
}

// TuningConfig configures AI tuning.
type TuningConfig struct {
	Rules      RulesConfig `yaml:"rules"`
	PromptsDir string      `yaml:"prompts_dir"`
}

// RulesConfig configures operational rules.
type RulesConfig struct {
	RequireDataHubCheck bool    `yaml:"require_datahub_check"`
	WarnOnDeprecated    bool    `yaml:"warn_on_deprecated"`
	QualityThreshold    float64 `yaml:"quality_threshold"`
}

// AuditConfig configures audit logging.
type AuditConfig struct {
	Enabled       bool `yaml:"enabled"`
	LogToolCalls  bool `yaml:"log_tool_calls"`
	RetentionDays int  `yaml:"retention_days"`
}

// MCPAppsConfig configures MCP Apps support for interactive UI components.
type MCPAppsConfig struct {
	// Enabled is the master switch for MCP Apps support.
	Enabled bool `yaml:"enabled"`

	// Apps configures individual MCP Apps.
	Apps map[string]AppConfig `yaml:"apps"`
}

// AppConfig configures an individual MCP App.
type AppConfig struct {
	// Enabled controls whether this app is active.
	Enabled bool `yaml:"enabled"`

	// Tools lists the tool names this app attaches to.
	Tools []string `yaml:"tools"`

	// AssetsPath is the absolute filesystem path to the app's assets directory.
	// This should point to a directory containing the app's HTML/JS/CSS files.
	AssetsPath string `yaml:"assets_path"`

	// ResourceURI is the MCP resource URI for this app (e.g., "ui://query-results").
	// If not specified, defaults to "ui://<app-name>".
	ResourceURI string `yaml:"resource_uri"`

	// EntryPoint is the main HTML file within AssetsPath (e.g., "index.html").
	// Defaults to "index.html" if not specified.
	EntryPoint string `yaml:"entry_point"`

	// CSP defines Content Security Policy requirements for the app.
	CSP *CSPAppConfig `yaml:"csp"`

	// Config holds app-specific configuration that will be injected
	// into the HTML as JSON.
	Config map[string]any `yaml:"config"`
}

// CSPAppConfig defines Content Security Policy requirements for an MCP App.
type CSPAppConfig struct {
	// ResourceDomains lists origins for static resources (scripts, images, styles, fonts).
	ResourceDomains []string `yaml:"resource_domains"`

	// ConnectDomains lists origins for network requests (fetch/XHR/WebSocket).
	ConnectDomains []string `yaml:"connect_domains"`

	// FrameDomains lists origins for nested iframes.
	FrameDomains []string `yaml:"frame_domains"`

	// ClipboardWrite requests write access to the clipboard.
	ClipboardWrite bool `yaml:"clipboard_write"`
}

// LoadConfig loads configuration from a file.
// The path is expected to come from command line arguments, controlled by the administrator.
func LoadConfig(path string) (*Config, error) {
	// #nosec G304 -- path is from CLI args, controlled by admin
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables
	data = []byte(expandEnvVars(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Apply defaults
	applyDefaults(&cfg)

	return &cfg, nil
}

// expandEnvVars expands ${VAR} patterns in the string.
func expandEnvVars(s string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1]
		return os.Getenv(varName)
	})
}

// applyDefaults applies default values to the config.
func applyDefaults(cfg *Config) {
	if cfg.Server.Name == "" {
		cfg.Server.Name = "mcp-data-platform"
	}
	if cfg.Server.Version == "" {
		cfg.Server.Version = "1.0.0"
	}
	if cfg.Server.Transport == "" {
		cfg.Server.Transport = "stdio"
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 25
	}
	if cfg.Semantic.Cache.TTL == 0 {
		cfg.Semantic.Cache.TTL = 5 * time.Minute
	}
	if cfg.Audit.RetentionDays == 0 {
		cfg.Audit.RetentionDays = 90
	}
	if cfg.Tuning.Rules.QualityThreshold == 0 {
		cfg.Tuning.Rules.QualityThreshold = 0.7
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	var errs []string

	if c.Auth.OIDC.Enabled && c.Auth.OIDC.Issuer == "" {
		errs = append(errs, "auth.oidc.issuer is required when OIDC is enabled")
	}

	if c.OAuth.Enabled {
		if c.OAuth.Issuer == "" {
			errs = append(errs, "oauth.issuer is required when OAuth is enabled")
		}
		// Upstream IdP is required for the authorization flow
		if c.OAuth.Upstream != nil {
			if c.OAuth.Upstream.Issuer == "" {
				errs = append(errs, "oauth.upstream.issuer is required")
			}
			if c.OAuth.Upstream.ClientID == "" {
				errs = append(errs, "oauth.upstream.client_id is required")
			}
			if c.OAuth.Upstream.RedirectURI == "" {
				errs = append(errs, "oauth.upstream.redirect_uri is required")
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors: %s", strings.Join(errs, "; "))
	}

	return nil
}
