package platform

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

// defaultServerName is the default server name used when none is configured.
const defaultServerName = "mcp-data-platform"

// Default configuration values.
const (
	defaultMaxOpenConns       = 25
	defaultRetentionDays      = 90
	defaultQualityThreshold   = 0.7
	defaultElicitRowThreshold = 1_000_000
)

// Session store backend names.
const (
	SessionStoreMemory   = "memory"
	SessionStoreDatabase = "database"
)

// Config store mode names.
const (
	ConfigStoreModeFile     = "file"
	ConfigStoreModeDatabase = "database"
)

// Default durations for configuration.
var (
	defaultCacheTTL         = 5 * time.Minute
	defaultSessionTimeout   = 30 * time.Minute
	defaultGracePeriod      = 25 * time.Second
	defaultPreShutdownDelay = 2 * time.Second
	defaultCleanupInterval  = 1 * time.Minute
)

// ConfigStoreConfig configures how configuration is stored and managed.
type ConfigStoreConfig struct {
	Mode string `yaml:"mode"` // "file" (default) or "database"
}

// Config holds the complete platform configuration.
type Config struct {
	APIVersion    string              `yaml:"apiVersion"`
	ConfigStore   ConfigStoreConfig   `yaml:"config_store"`
	Server        ServerConfig        `yaml:"server"`
	Auth          AuthConfig          `yaml:"auth"`
	OAuth         OAuthConfig         `yaml:"oauth"`
	Database      DatabaseConfig      `yaml:"database"`
	Personas      PersonasConfig      `yaml:"personas"`
	Toolkits      map[string]any      `yaml:"toolkits"`
	Tools         ToolsConfig         `yaml:"tools"`
	Semantic      SemanticConfig      `yaml:"semantic"`
	Query         QueryConfig         `yaml:"query"`
	Storage       StorageConfig       `yaml:"storage"`
	Injection     InjectionConfig     `yaml:"injection"`
	Tuning        TuningConfig        `yaml:"tuning"`
	Audit         AuditConfig         `yaml:"audit"`
	MCPApps       MCPAppsConfig       `yaml:"mcpapps"`
	Sessions      SessionsConfig      `yaml:"sessions"`
	Knowledge     KnowledgeConfig     `yaml:"knowledge"`
	Admin         AdminConfig         `yaml:"admin"`
	Resources     ResourcesConfig     `yaml:"resources"`
	Progress      ProgressConfig      `yaml:"progress"`
	ClientLogging ClientLoggingConfig `yaml:"client_logging"`
	Icons         IconsConfig         `yaml:"icons"`
	Elicitation   ElicitationConfig   `yaml:"elicitation"`
}

// AdminConfig configures the admin REST API.
type AdminConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Portal      bool   `yaml:"portal"`       // enable admin UI portal (default: false)
	Persona     string `yaml:"persona"`      // required admin persona (default: "admin")
	PathPrefix  string `yaml:"path_prefix"`  // URL prefix (default: "/api/v1/admin")
	PortalTitle string `yaml:"portal_title"` // sidebar title (default: "Admin Portal")
}

// KnowledgeConfig configures the knowledge capture feature.
type KnowledgeConfig struct {
	Enabled bool                 `yaml:"enabled"`
	Apply   KnowledgeApplyConfig `yaml:"apply"`
}

// KnowledgeApplyConfig configures the apply_knowledge tool.
type KnowledgeApplyConfig struct {
	Enabled             bool   `yaml:"enabled"`
	DataHubConnection   string `yaml:"datahub_connection"`
	RequireConfirmation bool   `yaml:"require_confirmation"`
}

// ServerConfig configures the MCP server.
type ServerConfig struct {
	Name              string           `yaml:"name"`
	Version           string           `yaml:"version"`
	Description       string           `yaml:"description"`
	Tags              []string         `yaml:"tags"`               // Discovery keywords for routing
	AgentInstructions string           `yaml:"agent_instructions"` // Inline operational guidance for AI agents
	Prompts           []PromptConfig   `yaml:"prompts"`            // Platform-level MCP prompts
	Transport         string           `yaml:"transport"`          // "stdio", "http" (or "sse" for backward compat)
	Address           string           `yaml:"address"`
	TLS               TLSConfig        `yaml:"tls"`
	Streamable        StreamableConfig `yaml:"streamable"`
	Shutdown          ShutdownConfig   `yaml:"shutdown"`
}

// ShutdownConfig configures graceful shutdown timing.
type ShutdownConfig struct {
	// GracePeriod is the maximum time to drain in-flight requests after
	// receiving a shutdown signal. Defaults to 25s (fits within K8s 30s
	// terminationGracePeriodSeconds with headroom for pre-shutdown delay).
	GracePeriod time.Duration `yaml:"grace_period"`

	// PreShutdownDelay is the time to sleep after marking the pod as
	// not-ready and before starting the HTTP drain. This gives the K8s
	// load balancer time to deregister the pod. Defaults to 2s.
	PreShutdownDelay time.Duration `yaml:"pre_shutdown_delay"`
}

// StreamableConfig configures the Streamable HTTP transport.
type StreamableConfig struct {
	// SessionTimeout is how long an idle session persists before cleanup.
	// Defaults to 30 minutes.
	SessionTimeout time.Duration `yaml:"session_timeout"`
	// Stateless disables session tracking (no Mcp-Session-Id validation).
	Stateless bool `yaml:"stateless"`
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

// ToolsConfig configures global tool visibility filtering for tools/list responses.
// This is a visibility filter to reduce token usage — not a security boundary.
// Persona auth continues to gate tools/call independently.
type ToolsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
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
	TrinoSemanticEnrichment  bool               `yaml:"trino_semantic_enrichment"`
	DataHubQueryEnrichment   bool               `yaml:"datahub_query_enrichment"`
	S3SemanticEnrichment     bool               `yaml:"s3_semantic_enrichment"`
	DataHubStorageEnrichment bool               `yaml:"datahub_storage_enrichment"`
	EstimateRowCounts        bool               `yaml:"estimate_row_counts"`
	SessionDedup             SessionDedupConfig `yaml:"session_dedup"`
}

// SessionDedupConfig configures session-level metadata deduplication.
type SessionDedupConfig struct {
	// Enabled controls whether session dedup is active. Defaults to true.
	Enabled *bool `yaml:"enabled"`

	// Mode controls what is sent for previously-enriched tables.
	// Values: "reference" (default), "summary", "none".
	Mode string `yaml:"mode"`

	// EntryTTL is how long a table's enrichment is considered fresh.
	// Defaults to the semantic cache TTL (typically 5m).
	EntryTTL time.Duration `yaml:"entry_ttl"`

	// SessionTimeout is how long an idle session persists before cleanup.
	// Defaults to the server's streamable session timeout (typically 30m).
	SessionTimeout time.Duration `yaml:"session_timeout"`
}

// IsEnabled returns whether session dedup is enabled, defaulting to true.
func (c *SessionDedupConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// EffectiveMode returns the dedup mode, defaulting to "reference".
func (c *SessionDedupConfig) EffectiveMode() string {
	if c.Mode == "" {
		return "reference"
	}
	return c.Mode
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

// ResourcesConfig configures MCP resource templates.
type ResourcesConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ProgressConfig configures progress notifications during tool execution.
type ProgressConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ClientLoggingConfig configures server-to-client log message notifications.
type ClientLoggingConfig struct {
	Enabled bool `yaml:"enabled"`
}

// IconsConfig configures visual metadata for tools, resources, and prompts.
type IconsConfig struct {
	// Enabled is the master switch for icon injection.
	Enabled bool `yaml:"enabled"`

	// Tools maps tool names to their icon definitions.
	Tools map[string]IconDef `yaml:"tools"`

	// Resources maps resource URI templates to their icon definitions.
	Resources map[string]IconDef `yaml:"resources"`

	// Prompts maps prompt names to their icon definitions.
	Prompts map[string]IconDef `yaml:"prompts"`
}

// IconDef defines an icon for config-driven injection.
type IconDef struct {
	// Source is the icon URL (HTTP/HTTPS) or data URI.
	Source string `yaml:"src"`

	// MIMEType is the optional MIME type (e.g., "image/svg+xml").
	MIMEType string `yaml:"mime_type,omitempty"`
}

// ElicitationConfig configures user confirmation for expensive operations.
type ElicitationConfig struct {
	// Enabled is the master switch for all elicitation features.
	Enabled bool `yaml:"enabled"`

	// CostEstimation configures query cost estimation and confirmation.
	CostEstimation CostEstimationConfig `yaml:"cost_estimation"`

	// PIIConsent configures PII access consent.
	PIIConsent PIIConsentConfig `yaml:"pii_consent"`
}

// CostEstimationConfig configures query cost estimation.
type CostEstimationConfig struct {
	// Enabled controls whether query cost estimation triggers elicitation.
	Enabled bool `yaml:"enabled"`

	// RowThreshold is the estimated row count above which confirmation is requested.
	// Default: 1000000 (1 million rows).
	RowThreshold int64 `yaml:"row_threshold"`
}

// PIIConsentConfig configures PII access consent.
type PIIConsentConfig struct {
	// Enabled controls whether PII table access triggers elicitation.
	Enabled bool `yaml:"enabled"`
}

// SessionsConfig configures session externalization.
type SessionsConfig struct {
	// Store selects the session storage backend: "memory" (default) or "database".
	Store string `yaml:"store"`

	// TTL is the session lifetime. Defaults to streamable.session_timeout.
	TTL time.Duration `yaml:"ttl"`

	// IdleTimeout is the idle session eviction threshold.
	// Defaults to streamable.session_timeout.
	IdleTimeout time.Duration `yaml:"idle_timeout"`

	// CleanupInterval is how often the cleanup routine runs. Defaults to 1m.
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

// LoadConfig loads configuration from a file.
// The path is expected to come from command line arguments, controlled by the administrator.
func LoadConfig(path string) (*Config, error) {
	// #nosec G304 -- path is from CLI args, controlled by admin
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	return LoadConfigFromBytes(data)
}

// LoadConfigFromBytes loads configuration from raw YAML bytes.
// Environment variables are expanded before parsing. The apiVersion field
// is validated against the default version registry.
func LoadConfigFromBytes(data []byte) (*Config, error) {
	// Expand environment variables
	expanded := []byte(expandEnvVars(string(data)))

	// Peek at the version before full parse
	version := PeekVersion(expanded)
	reg := DefaultRegistry()

	info, err := resolveVersion(reg, version)
	if err != nil {
		return nil, fmt.Errorf("config version: %w", err)
	}

	// Warn on deprecated versions
	if info.Status == VersionDeprecated {
		slog.Warn("config apiVersion is deprecated",
			"version", version,
			"message", info.DeprecationMessage,
		)
	}

	// Parse the config
	var cfg *Config
	if info.Converter != nil {
		cfg, err = info.Converter(expanded)
		if err != nil {
			return nil, fmt.Errorf("converting config from %s: %w", version, err)
		}
	} else {
		cfg = &Config{}
		if err := yaml.Unmarshal(expanded, cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	// Ensure APIVersion is set
	if cfg.APIVersion == "" {
		cfg.APIVersion = version
	}

	applyDefaults(cfg)

	return cfg, nil
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
	applyConfigStoreDefaults(cfg)
	applyServerDefaults(cfg)
	applyServiceDefaults(cfg)
	applySessionDedupDefaults(cfg)
	applySessionDefaults(cfg)
	applyAdminDefaults(cfg)
	applyElicitationDefaults(cfg)
}

// applyElicitationDefaults sets defaults for elicitation config.
func applyElicitationDefaults(cfg *Config) {
	if cfg.Elicitation.CostEstimation.RowThreshold == 0 {
		cfg.Elicitation.CostEstimation.RowThreshold = defaultElicitRowThreshold
	}
}

// applyConfigStoreDefaults sets defaults for config store settings.
func applyConfigStoreDefaults(cfg *Config) {
	if cfg.ConfigStore.Mode == "" {
		cfg.ConfigStore.Mode = ConfigStoreModeFile
	}
}

// applyAdminDefaults sets defaults for admin API config.
func applyAdminDefaults(cfg *Config) {
	if cfg.Admin.Persona == "" {
		cfg.Admin.Persona = "admin"
	}
	if cfg.Admin.PathPrefix == "" {
		cfg.Admin.PathPrefix = "/api/v1/admin"
	}
	if cfg.Admin.PortalTitle == "" {
		cfg.Admin.PortalTitle = "Admin Portal"
	}
}

// applyServerDefaults sets defaults for server-related config fields.
func applyServerDefaults(cfg *Config) {
	if cfg.Server.Name == "" {
		cfg.Server.Name = defaultServerName
	}
	if cfg.Server.Version == "" {
		cfg.Server.Version = "1.0.0"
	}
	if cfg.Server.Transport == "" {
		cfg.Server.Transport = "stdio"
	}
	if cfg.Server.Streamable.SessionTimeout == 0 {
		cfg.Server.Streamable.SessionTimeout = defaultSessionTimeout
	}
	if cfg.Server.Shutdown.GracePeriod == 0 {
		cfg.Server.Shutdown.GracePeriod = defaultGracePeriod
	}
	if cfg.Server.Shutdown.PreShutdownDelay == 0 {
		cfg.Server.Shutdown.PreShutdownDelay = defaultPreShutdownDelay
	}
}

// applyServiceDefaults sets defaults for database, semantic, audit, and tuning config.
func applyServiceDefaults(cfg *Config) {
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = defaultMaxOpenConns
	}
	if cfg.Semantic.Cache.TTL == 0 {
		cfg.Semantic.Cache.TTL = defaultCacheTTL
	}
	if cfg.Audit.RetentionDays == 0 {
		cfg.Audit.RetentionDays = defaultRetentionDays
	}
	if cfg.Tuning.Rules.QualityThreshold == 0 {
		cfg.Tuning.Rules.QualityThreshold = defaultQualityThreshold
	}
}

// applySessionDedupDefaults sets session dedup defaults from related config values.
func applySessionDedupDefaults(cfg *Config) {
	if cfg.Injection.SessionDedup.EntryTTL == 0 {
		cfg.Injection.SessionDedup.EntryTTL = cfg.Semantic.Cache.TTL
	}
	if cfg.Injection.SessionDedup.SessionTimeout == 0 {
		cfg.Injection.SessionDedup.SessionTimeout = cfg.Server.Streamable.SessionTimeout
	}
}

// applySessionDefaults sets session config defaults from related config values.
func applySessionDefaults(cfg *Config) {
	if cfg.Sessions.Store == "" {
		cfg.Sessions.Store = SessionStoreMemory
	}
	if cfg.Sessions.TTL == 0 {
		cfg.Sessions.TTL = cfg.Server.Streamable.SessionTimeout
	}
	if cfg.Sessions.IdleTimeout == 0 {
		cfg.Sessions.IdleTimeout = cfg.Server.Streamable.SessionTimeout
	}
	if cfg.Sessions.CleanupInterval == 0 {
		cfg.Sessions.CleanupInterval = defaultCleanupInterval
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	var errs []string

	if c.Auth.OIDC.Enabled && c.Auth.OIDC.Issuer == "" {
		errs = append(errs, "auth.oidc.issuer is required when OIDC is enabled")
	}

	errs = c.validateConfigStore(errs)
	errs = c.validateOAuth(errs)
	errs = c.validateSessions(errs)

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors: %s", strings.Join(errs, "; "))
	}

	return nil
}

// validateConfigStore checks config store configuration validity and appends any errors.
func (c *Config) validateConfigStore(errs []string) []string {
	if c.ConfigStore.Mode == ConfigStoreModeDatabase && c.Database.DSN == "" {
		errs = append(errs, "database.dsn is required when config_store.mode is \"database\"")
	}
	return errs
}

// validateOAuth checks OAuth configuration validity and appends any errors.
func (c *Config) validateOAuth(errs []string) []string {
	if !c.OAuth.Enabled {
		return errs
	}
	if c.OAuth.Issuer == "" {
		errs = append(errs, "oauth.issuer is required when OAuth is enabled")
	}
	// Upstream IdP is required for the authorization flow.
	if c.OAuth.Upstream == nil {
		return errs
	}
	if c.OAuth.Upstream.Issuer == "" {
		errs = append(errs, "oauth.upstream.issuer is required")
	}
	if c.OAuth.Upstream.ClientID == "" {
		errs = append(errs, "oauth.upstream.client_id is required")
	}
	if c.OAuth.Upstream.RedirectURI == "" {
		errs = append(errs, "oauth.upstream.redirect_uri is required")
	}
	return errs
}

// validateSessions checks session configuration validity and appends any errors.
func (c *Config) validateSessions(errs []string) []string {
	if c.Sessions.Store == SessionStoreDatabase && c.Database.DSN == "" {
		errs = append(errs, "database.dsn is required when sessions.store is \"database\"")
	}
	return errs
}
