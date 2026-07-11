package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/platform/dedup"
	"github.com/txn2/mcp-data-platform/pkg/platform/reflexivecapture"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
)

// defaultServerName is the default server name used when none is configured.
const defaultServerName = "mcp-data-platform"

// Constants for repeated identifiers used throughout the platform package.
// Defined in one place so the same literal does not appear repeatedly
// across platform.go, info_tool.go, prompt_tool.go, etc.
const (
	// instanceDefault is the conventional instance *name* used for the
	// default connection of any toolkit kind. This is a value, not a
	// config-map key (the "default" schema key lives in the toolkitcfg
	// package that resolves instance config).
	instanceDefault = "default"
	// kindPlatform identifies tools provided directly by the platform
	// (not a toolkit), e.g. platform_info, list_connections.
	kindPlatform = "platform"
	// Toolkit kind identifiers used across the platform for connection-source
	// mapping, semantic-provider selection, and auto-enable defaulting. The
	// prompt layer keeps its own copy for capability bullets and workflow gating.
	kindDataHub = "datahub"
	kindTrino   = "trino"
	kindS3      = "s3"
	kindMCP     = "mcp"
	kindAPI     = "api"
	// toolListConns is the unified platform-provided list-connections
	// tool name.
	toolListConns = "list_connections"
	// entryPointHTML is the conventional MCP App entry point file.
	entryPointHTML = "index.html"
	// ConfigKeyServerDescription is the config_entries key for the live
	// server description override. Exported so admin handlers can use
	// the same canonical key (a divergent rename in either package
	// would silently misroute admin writes from platform reads).
	ConfigKeyServerDescription = "server.description"
	// ConfigKeyServerAgentInstructions is the config_entries key for
	// the live MCP `instructions` field (server-side agent guidance).
	ConfigKeyServerAgentInstructions = "server.agent_instructions"
	// ConfigKeyToolsDeny is the config_entries key for the JSON-encoded
	// platform-wide tool deny list (controls tools/list visibility).
	ConfigKeyToolsDeny = "tools.deny"

	// Backwards-compatible aliases retained so this file's call sites
	// stay tidy without re-flowing every reference.
	cfgKeyServerDescription       = ConfigKeyServerDescription
	cfgKeyServerAgentInstructions = ConfigKeyServerAgentInstructions
)

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

// Default durations for configuration.
var (
	defaultCacheTTL         = 5 * time.Minute
	defaultSessionTimeout   = 30 * time.Minute
	defaultGracePeriod      = 25 * time.Second
	defaultPreShutdownDelay = 2 * time.Second
	defaultCleanupInterval  = 1 * time.Minute
)

// Config holds the complete platform configuration.
type Config struct {
	APIVersion string           `yaml:"apiVersion"`
	Meta       ConfigMeta       `yaml:"config"`
	Server     ServerConfig     `yaml:"server"`
	Auth       AuthConfig       `yaml:"auth"`
	OAuth      OAuthConfig      `yaml:"oauth"`
	Database   DatabaseConfig   `yaml:"database"`
	Personas   PersonasConfig   `yaml:"personas"`
	Toolkits   map[string]any   `yaml:"toolkits"`
	Tools      ToolsConfig      `yaml:"tools"`
	Semantic   SemanticConfig   `yaml:"semantic"`
	Query      QueryConfig      `yaml:"query"`
	Storage    StorageConfig    `yaml:"storage"`
	Enrichment EnrichmentConfig `yaml:"enrichment"`
	Tuning     TuningConfig     `yaml:"tuning"`

	// EnrichmentDeprecated accepts the legacy "injection" key so configs written
	// before the rename to "enrichment" still load. applyEnrichmentCompat folds it
	// onto Enrichment and warns. Remove in a future apiVersion.
	EnrichmentDeprecated *EnrichmentConfig   `yaml:"injection"`
	Audit                AuditConfig         `yaml:"audit"`
	MCPApps              MCPAppsConfig       `yaml:"mcpapps"`
	Sessions             SessionsConfig      `yaml:"sessions"`
	Knowledge            KnowledgeConfig     `yaml:"knowledge"`
	Memory               MemoryConfig        `yaml:"memory"`
	Portal               PortalConfig        `yaml:"portal"`
	Admin                AdminConfig         `yaml:"admin"`
	Resources            ResourcesConfig     `yaml:"resources"`
	Progress             ProgressConfig      `yaml:"progress"`
	ClientLogging        ClientLoggingConfig `yaml:"client_logging"`
	Icons                IconsConfig         `yaml:"icons"`
	Elicitation          ElicitationConfig   `yaml:"elicitation"`
	Workflow             WorkflowConfig      `yaml:"workflow"`
	SessionGate          SessionGateConfig   `yaml:"session_gate"`
	APIGateway           APIGatewayConfig    `yaml:"apigateway"`
	Observability        ObservabilityConfig `yaml:"observability"`

	// runtimeMu guards fields that can be mutated at runtime via the admin
	// API (Tools.DescriptionOverrides, Tools.Deny). Other fields are
	// loaded once from YAML and not protected here. Unexported so YAML
	// marshaling ignores it.
	runtimeMu sync.RWMutex
}

// ConfigMeta holds meta-configuration controlling how the config file itself is
// parsed. It is read from the top-level `config:` block.
type ConfigMeta struct {
	// Strict controls how unrecognized YAML keys are handled. When nil (the
	// default for this release) unknown keys are logged as a prominent warning
	// and ignored, so existing configs keep loading while operators are alerted
	// to drift. Set `config.strict: true` to reject unknown keys with a hard
	// error instead (recommended: catches typos and stale keys at startup).
	//
	// A future release will flip the default so unknown keys are an error
	// unless `config.strict: false` is set as a temporary escape hatch.
	Strict *bool `yaml:"strict"`
}

// IsStrict reports whether unrecognized YAML keys should be a hard error.
// Defaults to false (warn-and-continue) for this release.
func (m ConfigMeta) IsStrict() bool {
	return m.Strict != nil && *m.Strict
}

// strictDecode decodes expanded YAML into cfg with KnownFields(true). It
// returns the list of unrecognized-key errors (empty when every key is
// recognized) and a non-nil error only for malformed YAML. cfg is populated
// with all recognized fields even when unknown keys are present, so a single
// decode both parses the config and detects drift.
//
// Only keys that map to a typed struct field are inspected. Keys nested under
// free-form maps — the `toolkits` tree, each persona definition's *name*, and a
// toolkit's `config:` map — accept arbitrary keys by design and are not
// flagged. (The fields *within* a persona definition are typed and are
// checked.) The doc round-trip test validates toolkit shape separately.
func strictDecode(expanded []byte, cfg *Config) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)

	if err := dec.Decode(cfg); err != nil {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return typeErr.Errors, nil
		}
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return nil, nil
}

// detectUnknownFields reports the unrecognized keys in expanded YAML, or nil
// when every key maps to a known field. It decodes into a throwaway Config and
// is intended for tests and validation tooling.
func detectUnknownFields(expanded []byte) []string {
	var probe Config
	unknown, _ := strictDecode(expanded, &probe)
	return unknown
}

// decodeConfigStrict parses expanded YAML into a Config in a single pass,
// enforcing the config.strict unknown-key policy: unrecognized keys are a hard
// error when config.strict is true, otherwise a logged warning.
func decodeConfigStrict(expanded []byte) (*Config, error) {
	cfg := &Config{}
	unknown, err := strictDecode(expanded, cfg)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		if cfg.Meta.IsStrict() {
			return nil, fmt.Errorf(
				"strict config parsing: %d unrecognized key(s): %s",
				len(unknown), strings.Join(unknown, "; "))
		}
		slog.Warn("config contains unrecognized keys that are ignored; "+
			"fix or remove them, or set config.strict: true to reject them "+
			"(a future release will make rejection the default)",
			"unknown_keys", unknown)
	}
	return cfg, nil
}

// defaultAdminPersona is the persona name that grants platform admin.
const defaultAdminPersona = "admin"

// AdminConfig configures the admin REST API.
// Enabled by default (nil = enabled); set enabled: false to disable. The mounted
// routes still sit behind the platform's normal persona/role auth, so enabling
// exposes the route, not unauthenticated access.
type AdminConfig struct {
	Enabled    *bool  `yaml:"enabled"`
	Persona    string `yaml:"persona"`     // required admin persona (default: "admin")
	PathPrefix string `yaml:"path_prefix"` // URL prefix (default: "/api/v1/admin")
}

// IsEnabled reports whether the admin REST API is enabled, defaulting to true
// when not explicitly set.
func (c *AdminConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// KnowledgeConfig configures the knowledge capture feature.
// Enabled by default when a database is available. Set enabled: false to disable.
type KnowledgeConfig struct {
	Enabled *bool                          `yaml:"enabled"`
	Apply   KnowledgeApplyConfig           `yaml:"apply"`
	Pages   knowledgepage.PageGuardsConfig `yaml:"pages"` // write guards (#705); see knowledgepage
	// ReflexiveCapture configures auto-capture of query-error corrections (#635):
	// when a query errors and a later related query on the same connection
	// succeeds in the same session, the platform mints a "misconception + fix"
	// memory. Captures enter review (reviewed sink-class), so the blast radius is
	// a pending insight, not live catalog state. Auto-enabled with the memory
	// subsystem; see pkg/platform/reflexivecapture.
	ReflexiveCapture reflexivecapture.Config `yaml:"reflexive_capture"`

	// SearchProviderTimeout bounds each knowledge provider arm in the `search`
	// fan-out; default 5s, a negative value disables it.
	SearchProviderTimeout time.Duration `yaml:"search_provider_timeout"`
	// SearchEmbedTimeout bounds the serial intent-embedding step in `search`,
	// independent of the fan-out bound; default 5s, a negative value disables it.
	SearchEmbedTimeout time.Duration `yaml:"search_embed_timeout"`
}

// KnowledgeApplyConfig configures the apply_knowledge tool.
// Enabled by default when a database is available (nil = enabled); set
// enabled: false to disable. Still gated behind database availability.
type KnowledgeApplyConfig struct {
	Enabled             *bool  `yaml:"enabled"`
	DataHubConnection   string `yaml:"datahub_connection"`
	RequireConfirmation bool   `yaml:"require_confirmation"`
}

// IsEnabled reports whether apply_knowledge is enabled, defaulting to true when
// not explicitly set.
func (c *KnowledgeApplyConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// MemoryConfig configures the persistent memory layer.
// Memory is enabled by default when a database is available.
// Set enabled: false to explicitly disable.
type MemoryConfig struct {
	Enabled   *bool           `yaml:"enabled"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	Staleness StalenessConfig `yaml:"staleness"`
}

// EmbeddingConfig configures the embedding provider for vector search.
type EmbeddingConfig struct {
	Provider string            `yaml:"provider"` // "ollama" or "noop"
	Ollama   OllamaEmbedConfig `yaml:"ollama"`
}

// OllamaEmbedConfig configures the Ollama embedding provider.
type OllamaEmbedConfig struct {
	URL     string        `yaml:"url"`
	Model   string        `yaml:"model"`
	Timeout time.Duration `yaml:"timeout"`
	// MaxInputBytes caps the byte length of each text sent to Ollama.
	// Zero selects embedding.DefaultMaxInputBytes. Raise it only when
	// running an embedding model with a larger context than
	// nomic-embed-text's 2048 tokens. See embedding.DefaultMaxInputBytes
	// and #623.
	MaxInputBytes int `yaml:"max_input_bytes"`
}

// StalenessConfig configures the memory staleness watcher.
type StalenessConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Interval  time.Duration `yaml:"interval"`
	BatchSize int           `yaml:"batch_size"`
}

// Default bucket and prefix for portal artifact storage.
const (
	defaultPortalS3Bucket = "portal-assets"
	defaultPortalS3Prefix = "artifacts/"
)

// PortalConfig configures the asset portal for saving AI-generated artifacts.
// Enabled by default when a database is available. Set enabled: false to disable.
type PortalConfig struct {
	Enabled         *bool                 `yaml:"enabled"`
	Title           string                `yaml:"title"`             // sidebar/branding title (default: "MCP Data Platform")
	Tagline         string                `yaml:"tagline"`           // login-screen subtitle (default: "Sign in to access the platform.")
	OIDCButtonLabel string                `yaml:"oidc_button_label"` // login-screen SSO button text (default: "Sign in with OIDC")
	Logo            string                `yaml:"logo"`              // URL to logo (fallback for both themes)
	LogoLight       string                `yaml:"logo_light"`        // URL to logo for light theme
	LogoDark        string                `yaml:"logo_dark"`         // URL to logo for dark theme
	S3Connection    string                `yaml:"s3_connection"`     // name of the S3 toolkit instance to use
	S3Bucket        string                `yaml:"s3_bucket"`         // bucket for artifact storage (default: "portal-assets")
	S3Prefix        string                `yaml:"s3_prefix"`         // key prefix within the bucket (default: "artifacts/")
	PublicBaseURL   string                `yaml:"public_base_url"`   // base URL for portal links (e.g., "https://portal.example.com")
	MaxContentSize  int                   `yaml:"max_content_size"`  // max artifact size in bytes (default: 10MB)
	Implementor     ImplementorConfig     `yaml:"implementor"`       // optional implementor brand (far-left header zone)
	RateLimit       PortalRateLimitConfig `yaml:"rate_limit"`
	Export          PortalExportConfig    `yaml:"export"` // trino_export configuration
}

// PortalExportConfig configures the trino_export tool.
type PortalExportConfig struct {
	Enabled        *bool  `yaml:"enabled"`         // auto-enabled when portal+trino are configured
	MaxRows        int    `yaml:"max_rows"`        // hard row cap (default: 100000)
	MaxBytes       int64  `yaml:"max_bytes"`       // hard byte cap (default: 100MB)
	DefaultTimeout string `yaml:"default_timeout"` // default query timeout (default: "5m")
	MaxTimeout     string `yaml:"max_timeout"`     // max query timeout (default: "10m")
}

// ImplementorConfig configures the optional implementor brand shown in the
// far-left zone of the public viewer header (e.g., "ACME Corp").
type ImplementorConfig struct {
	Name string `yaml:"name"` // display name (e.g., "ACME Corp")
	Logo string `yaml:"logo"` // URL to logo SVG (fetched at startup for inline rendering)
	URL  string `yaml:"url"`  // link URL (e.g., "https://acme.com")
}

// PortalRateLimitConfig configures rate limiting for the public portal viewer.
type PortalRateLimitConfig struct {
	RequestsPerMinute int `yaml:"requests_per_minute"` // default: 60
	BurstSize         int `yaml:"burst_size"`          // default: 10
	// TrustedProxies lists CIDRs whose X-Forwarded-For headers are trusted for
	// client attribution, mirroring oauth.rate_limit.trusted_proxies. Empty (the
	// default) trusts none: the direct peer address is used and forwarding
	// headers are ignored, which is the correct safe default when the
	// deployment's proxy topology is unknown. Set this to your reverse-proxy or
	// ingress CIDRs so per-client limiting attributes to the real client instead
	// of collapsing onto the proxy IP.
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// defaultMaxContentSize is the default maximum artifact size (10 MB).
const defaultMaxContentSize = 10 * 1024 * 1024

// ServerConfig configures the MCP server.
type ServerConfig struct {
	Name              string           `yaml:"name"`
	Version           string           `yaml:"version"`
	Description       string           `yaml:"description"`
	Tags              []string         `yaml:"tags"`               // Discovery keywords for routing
	AgentInstructions string           `yaml:"agent_instructions"` // Inline operational guidance for AI agents
	Prompts           []PromptConfig   `yaml:"prompts"`            // Platform-level MCP prompts
	BuiltinPrompts    map[string]bool  `yaml:"builtin_prompts"`    // Enable/disable built-in workflow prompts
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

// PromptArgumentConfig defines an argument for a platform-level MCP prompt.
type PromptArgumentConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// PromptConfig defines a platform-level MCP prompt.
type PromptConfig struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Content     string                 `yaml:"content"`
	Arguments   []PromptArgumentConfig `yaml:"arguments"`
}

// TLSConfig configures TLS.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig configures authentication.
type AuthConfig struct {
	OIDC           OIDCAuthConfig       `yaml:"oidc"`
	APIKeys        APIKeyAuthConfig     `yaml:"api_keys"`
	BrowserSession BrowserSessionConfig `yaml:"browser_session"`
	AllowAnonymous bool                 `yaml:"allow_anonymous"` // default: false
}

// OIDCAuthConfig configures OIDC authentication.
type OIDCAuthConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Issuer        string   `yaml:"issuer"`
	ClientID      string   `yaml:"client_id"`
	ClientSecret  string   `yaml:"client_secret"` // #nosec G117 -- OIDC secret from admin config
	Audience      string   `yaml:"audience"`
	RoleClaimPath string   `yaml:"role_claim_path"`
	RolePrefix    string   `yaml:"role_prefix"`
	Scopes        []string `yaml:"scopes"` // default: [openid, profile, email]
}

// BrowserSessionConfig configures cookie-based browser sessions.
type BrowserSessionConfig struct {
	Enabled    bool          `yaml:"enabled"`
	CookieName string        `yaml:"cookie_name"` // default: "mcp_session"
	TTL        time.Duration `yaml:"ttl"`         // default: 8h
	SigningKey string        `yaml:"signing_key"` // base64-encoded HMAC key
	Secure     *bool         `yaml:"secure"`      // HTTPS-only cookie; defaults to true, set false only for local HTTP
	Domain     string        `yaml:"domain"`
	// SameSite is the cookie SameSite attribute: "lax" (default), "strict", or
	// "none". "none" requires secure=true and makes X-CSRF-Token the sole CSRF
	// defense (a startup warning is logged).
	SameSite string `yaml:"same_site"`
}

// IsSecure reports whether the session cookie carries the Secure attribute,
// defaulting to true (nil); set secure: false only for local HTTP.
func (b *BrowserSessionConfig) IsSecure() bool {
	if b.Secure == nil {
		return true
	}
	return *b.Secure
}

// APIKeyAuthConfig configures API key authentication.
type APIKeyAuthConfig struct {
	Enabled bool        `yaml:"enabled"`
	Keys    []APIKeyDef `yaml:"keys"`
}

// APIKeyDef defines an API key.
type APIKeyDef struct {
	Key         string   `yaml:"key"`
	Name        string   `yaml:"name"`
	Email       string   `yaml:"email"`
	Description string   `yaml:"description"`
	Roles       []string `yaml:"roles"`
}

// OAuthConfig configures the OAuth server.
type OAuthConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Issuer     string `yaml:"issuer"`
	SigningKey string `yaml:"signing_key"` // Base64-encoded HMAC key for JWT signing
	// PreviousSigningKeys are base64-encoded HMAC keys retained for verification
	// only after a signing-key rotation. New tokens are always signed with
	// SigningKey; tokens minted with a prior key still verify while that key
	// remains here. Rotate in two phases (add the new key here as verify-only
	// everywhere first, then promote it to signing_key) so no replica signs with
	// a key its peers have not yet learned; see the "Rotating the signing key"
	// runbook in docs/auth/oauth-server.md.
	PreviousSigningKeys []string `yaml:"previous_signing_keys"`
	// AllowEphemeralSigningKey permits an HTTP deployment to boot without a
	// configured signing_key, generating a per-process key instead. UNSAFE for
	// multi-replica deployments: each replica mints tokens its peers reject.
	// Intended only for single-replica HTTP dev setups. Default false.
	AllowEphemeralSigningKey bool                 `yaml:"allow_ephemeral_signing_key"`
	Clients                  []OAuthClientConfig  `yaml:"clients"`
	DCR                      DCRConfig            `yaml:"dcr"`
	RateLimit                OAuthRateLimitConfig `yaml:"rate_limit"`
	Upstream                 *UpstreamIDPConfig   `yaml:"upstream,omitempty"`
}

// errOAuthSigningKeyRequiredMsg is the message emitted by both the config
// validation gate and the boot-time signing-key gate when an HTTP OAuth
// deployment has no configured key. Shared so the two gates stay in sync.
const errOAuthSigningKeyRequiredMsg = "oauth.signing_key is required for http transport " +
	"(set oauth.allow_ephemeral_signing_key: true only for a single-replica dev setup)"

// requiresConfiguredSigningKey reports whether the deployment must supply an
// explicit OAuth signing key rather than fall back to a per-process ephemeral
// key. It is true when the OAuth server is enabled on an HTTP-family transport
// and the ephemeral escape hatch is not set: across replicas every token must be
// signed with the same key, or a replica rejects tokens minted by its peers
// (an intermittent-auth-failure class of bug). stdio is single-process by
// construction, so it keeps the auto-generate behavior.
func (c *Config) requiresConfiguredSigningKey() bool {
	return c.OAuth.Enabled &&
		isHTTPTransport(c.Server.Transport) &&
		!c.OAuth.AllowEphemeralSigningKey
}

// OAuthRateLimitConfig configures rate limiting for the unauthenticated OAuth
// /token and /register endpoints. Limiting is on by default (Enabled nil ==
// enabled); each endpoint has a per-IP limit plus an internal global backstop
// that bounds total throughput regardless of client attribution.
type OAuthRateLimitConfig struct {
	Enabled *bool `yaml:"enabled"`
	// TrustedProxies lists CIDRs whose X-Forwarded-For headers are trusted for
	// client attribution. Empty (the default) trusts none: the direct peer
	// address is used and forwarding headers are ignored, which is the correct
	// safe default when the deployment's proxy topology is unknown.
	TrustedProxies []string           `yaml:"trusted_proxies"`
	Token          OAuthRateLimitRule `yaml:"token"`    // default: 60 rpm, burst 10
	Register       OAuthRateLimitRule `yaml:"register"` // default: 10 rpm, burst 3
}

// OAuthRateLimitRule configures a single endpoint's per-IP limit.
type OAuthRateLimitRule struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
	Burst             int `yaml:"burst"`
}

// OAuthClientConfig defines a pre-registered OAuth client.
type OAuthClientConfig struct {
	ID           string   `yaml:"id"`
	Secret       string   `yaml:"secret"` // #nosec G117 -- API key secret from admin YAML config
	RedirectURIs []string `yaml:"redirect_uris"`
}

// DCRConfig configures Dynamic Client Registration.
type DCRConfig struct {
	Enabled bool `yaml:"enabled"`
	// AllowedRedirectPatterns are regex patterns for redirect URIs that
	// dynamically registered clients may use. The registration endpoint is
	// unauthenticated, so registration is denied when this is empty unless
	// AllowAllRedirectURIs explicitly opts out of pattern matching.
	AllowedRedirectPatterns []string `yaml:"allowed_redirect_patterns"`
	AllowAllRedirectURIs    bool     `yaml:"allow_all_redirect_uris"`
}

// UpstreamIDPConfig configures the upstream identity provider (e.g., Keycloak).
type UpstreamIDPConfig struct {
	Issuer       string `yaml:"issuer"`        // OIDC issuer URL (any OIDC-compliant IdP, e.g. Keycloak)
	ClientID     string `yaml:"client_id"`     // MCP Server's client ID in the upstream IdP
	ClientSecret string `yaml:"client_secret"` // #nosec G117 -- MCP Server's client secret from admin YAML config
	RedirectURI  string `yaml:"redirect_uri"`  // Callback URL (e.g., http://localhost:8080/oauth/callback)

	// AuthorizationEndpoint and TokenEndpoint optionally override OIDC discovery.
	// By default the broker discovers these from
	// <issuer>/.well-known/openid-configuration; set them only for IdPs whose
	// discovery document is broken or unreachable. Discovery is skipped entirely
	// only when BOTH are set.
	AuthorizationEndpoint string `yaml:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `yaml:"token_endpoint,omitempty"`
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
	DisplayName string             `yaml:"display_name"`
	Description string             `yaml:"description,omitempty"`
	Roles       []string           `yaml:"roles"`
	Tools       ToolRulesDef       `yaml:"tools"`
	Connections ConnectionRulesDef `yaml:"connections"`
	Context     ContextDef         `yaml:"context"`
	Priority    int                `yaml:"priority,omitempty"`
}

// ConnectionRulesDef defines connection access rules in config.
type ConnectionRulesDef struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// ToolsConfig configures global tool visibility filtering for tools/list responses.
// This is a visibility filter to reduce token usage — not a security boundary.
// Persona auth continues to gate tools/call independently.
type ToolsConfig struct {
	Allow                []string          `yaml:"allow"`
	Deny                 []string          `yaml:"deny"`
	DescriptionOverrides map[string]string `yaml:"description_overrides"`
}

// ToolRulesDef defines tool access rules.
type ToolRulesDef struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// ContextDef defines per-persona context overrides.
type ContextDef struct {
	DescriptionPrefix         string `yaml:"description_prefix,omitempty"`
	DescriptionOverride       string `yaml:"description_override,omitempty"`
	AgentInstructionsSuffix   string `yaml:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string `yaml:"agent_instructions_override,omitempty"`
}

// RoleMappingConfig configures role mapping.
type RoleMappingConfig struct {
	OIDCToPersona map[string]string `yaml:"oidc_to_persona"`
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

// EnrichmentConfig configures cross-enrichment.
type EnrichmentConfig struct {
	// The four cross-enrichment flags default to true (nil = enabled). They are
	// the platform's core differentiator and are read-only: each no-ops safely
	// when its provider (semantic / query / storage) is absent, so defaulting on
	// is safe and removes the need to opt every deployment into enrichment. Set
	// the flag to false explicitly to disable. Read via the Is*Enabled helpers.
	TrinoSemanticEnrichment  *bool              `yaml:"trino_semantic_enrichment"`
	DataHubQueryEnrichment   *bool              `yaml:"datahub_query_enrichment"`
	S3SemanticEnrichment     *bool              `yaml:"s3_semantic_enrichment"`
	DataHubStorageEnrichment *bool              `yaml:"datahub_storage_enrichment"`
	EstimateRowCounts        bool               `yaml:"estimate_row_counts"`
	SessionDedup             SessionDedupConfig `yaml:"session_dedup"`

	// UnwrapJSON defaults the unwrap_json parameter to true on trino_query
	// and trino_execute, so single-row VARCHAR-of-JSON responses (e.g. from
	// OpenSearch/Elasticsearch raw_query) are returned as parsed objects
	// instead of double-encoded strings. Defaults to true (nil = enabled).
	UnwrapJSON *bool `yaml:"unwrap_json"`

	// ColumnContextFiltering limits column-level semantic enrichment to
	// columns referenced in the SQL query. Saves tokens when queries
	// touch a subset of a wide table. Defaults to true (nil = enabled).
	ColumnContextFiltering *bool `yaml:"column_context_filtering"`

	// SearchSchemaPreview adds a bounded column-name+type preview to
	// datahub_search query_context, eliminating the intermediate
	// datahub_get_schema or trino_describe_table call before writing SQL.
	// Defaults to true (nil = enabled).
	SearchSchemaPreview *bool `yaml:"search_schema_preview"`

	// SchemaPreviewMaxColumns caps how many columns appear in each
	// schema preview. Defaults to 15 (nil = 15).
	SchemaPreviewMaxColumns *int `yaml:"schema_preview_max_columns"`

	// SemanticFallback enables the issue #444 fallback path: when a
	// URN-equality lookup for a Trino table misses on the semantic
	// provider, the platform calls SearchTables with Mode=semantic
	// and surfaces the top hit as a SUGGESTED match (annotated with
	// match_kind=semantic so the model knows it is similarity-
	// inferred, not URN-resolved). Audit rows record
	// enrichment_match_kind so operators can measure false-positive
	// rate. Default off. Requires the semantic provider to support
	// the "semantic" search mode (DataHub does as of v1.8.1).
	SemanticFallback *bool `yaml:"semantic_fallback"`

	// SemanticFallbackTopK is how many similarity-search hits the
	// fallback surfaces per miss. Default 1. Caps at 10 to keep
	// suggested-match output bounded; operators wanting broader
	// recall should adjust persona scope or query patterns, not
	// flood the response with low-rank suggestions.
	SemanticFallbackTopK *int `yaml:"semantic_fallback_top_k"`

	// MemoryLimit caps how many memory records the platform recalls and
	// renders into the memory_context enrichment block per tool call.
	// Defaults to 5 (nil = 5). Issue #761.
	MemoryLimit *int `yaml:"memory_limit"`

	// MemoryContextBudgetBytes bounds the byte size of the rendered memory
	// summaries appended per tool call. Records beyond the budget are listed
	// as compact id+reference stubs (still fetchable, not dropped); at least
	// one record is always rendered. Defaults to 1500 (nil = 1500). Set to 0
	// to disable the budget (render every recalled record). Issue #761.
	MemoryContextBudgetBytes *int `yaml:"memory_context_budget_bytes"`

	// MemorySummaryBytes caps each rendered memory record to a summary-first
	// excerpt so the agent fetches the full record by reference only when it
	// matters. Defaults to 280 (nil = 280). Set to 0 to render full content.
	// Issue #761.
	MemorySummaryBytes *int `yaml:"memory_summary_bytes"`
}

// IsUnwrapJSONEnabled returns whether unwrap_json defaults to true,
// defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsUnwrapJSONEnabled() bool {
	if c.UnwrapJSON == nil {
		return true
	}
	return *c.UnwrapJSON
}

// IsColumnContextFilteringEnabled returns whether column context filtering
// is enabled, defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsColumnContextFilteringEnabled() bool {
	if c.ColumnContextFiltering == nil {
		return true
	}
	return *c.ColumnContextFiltering
}

// IsTrinoSemanticEnrichmentEnabled reports whether Trino results are enriched
// with semantic context, defaulting to true when not explicitly set. The
// enrichment no-ops when no semantic provider is configured.
func (c *EnrichmentConfig) IsTrinoSemanticEnrichmentEnabled() bool {
	return c.TrinoSemanticEnrichment == nil || *c.TrinoSemanticEnrichment
}

// IsDataHubQueryEnrichmentEnabled reports whether DataHub results are enriched
// with query/availability context, defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsDataHubQueryEnrichmentEnabled() bool {
	return c.DataHubQueryEnrichment == nil || *c.DataHubQueryEnrichment
}

// IsS3SemanticEnrichmentEnabled reports whether S3 results are enriched with
// semantic context, defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsS3SemanticEnrichmentEnabled() bool {
	return c.S3SemanticEnrichment == nil || *c.S3SemanticEnrichment
}

// IsDataHubStorageEnrichmentEnabled reports whether DataHub results are enriched
// with storage context, defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsDataHubStorageEnrichmentEnabled() bool {
	return c.DataHubStorageEnrichment == nil || *c.DataHubStorageEnrichment
}

// defaultSchemaPreviewMaxColumns is the default cap for schema preview columns.
const defaultSchemaPreviewMaxColumns = 15

// IsSearchSchemaPreviewEnabled returns whether search schema preview
// is enabled, defaulting to true when not explicitly set.
func (c *EnrichmentConfig) IsSearchSchemaPreviewEnabled() bool {
	if c.SearchSchemaPreview == nil {
		return true
	}
	return *c.SearchSchemaPreview
}

// EffectiveSchemaPreviewMaxColumns returns the configured max columns
// for schema preview, defaulting to 15 when not explicitly set.
func (c *EnrichmentConfig) EffectiveSchemaPreviewMaxColumns() int {
	if c.SchemaPreviewMaxColumns == nil {
		return defaultSchemaPreviewMaxColumns
	}
	return *c.SchemaPreviewMaxColumns
}

// defaultSemanticFallbackTopK is the default number of similarity-
// search results surfaced per URN miss when the semantic fallback
// fires.
const defaultSemanticFallbackTopK = 1

// maxSemanticFallbackTopK caps the configurable top-K so a stray
// large value cannot dominate a response with low-rank suggestions.
const maxSemanticFallbackTopK = 10

// IsSemanticFallbackEnabled returns whether the issue #444 fallback
// is enabled. Defaults to false; operators opt in explicitly because
// similarity matches are heuristic and may surface false positives.
func (c *EnrichmentConfig) IsSemanticFallbackEnabled() bool {
	if c.SemanticFallback == nil {
		return false
	}
	return *c.SemanticFallback
}

// EffectiveSemanticFallbackTopK returns the configured top-K for the
// semantic fallback, clamped to [1, maxSemanticFallbackTopK]. Returns
// defaultSemanticFallbackTopK when unset; clamps to bounds when set
// outside the valid range.
func (c *EnrichmentConfig) EffectiveSemanticFallbackTopK() int {
	if c.SemanticFallbackTopK == nil {
		return defaultSemanticFallbackTopK
	}
	k := *c.SemanticFallbackTopK
	if k < 1 {
		return 1
	}
	if k > maxSemanticFallbackTopK {
		return maxSemanticFallbackTopK
	}
	return k
}

// Memory-enrichment budget defaults (issue #761). The limit preserves the
// historical hardcoded count; the byte budget and summary cap are new bounds
// tuned so a mature deployment's per-query enrichment stays a supporting note
// rather than crowding out the analyzed data.
const (
	defaultMemoryLimit              = 5
	defaultMemoryContextBudgetBytes = 1500
	defaultMemorySummaryBytes       = 280
)

// EffectiveMemoryLimit returns the configured memory-enrichment record limit,
// defaulting to 5 when unset and flooring non-positive values at the default.
func (c *EnrichmentConfig) EffectiveMemoryLimit() int {
	if c.MemoryLimit == nil || *c.MemoryLimit <= 0 {
		return defaultMemoryLimit
	}
	return *c.MemoryLimit
}

// EffectiveMemoryContextBudgetBytes returns the configured memory_context byte
// budget, defaulting to 1500 when unset. A configured 0 disables the budget
// (every recalled record is rendered); negative values are treated as the
// default rather than as "disabled" to avoid a stray sign silently unbounding
// the payload.
func (c *EnrichmentConfig) EffectiveMemoryContextBudgetBytes() int {
	if c.MemoryContextBudgetBytes == nil {
		return defaultMemoryContextBudgetBytes
	}
	if *c.MemoryContextBudgetBytes < 0 {
		return defaultMemoryContextBudgetBytes
	}
	return *c.MemoryContextBudgetBytes
}

// EffectiveMemorySummaryBytes returns the configured per-record summary cap,
// defaulting to 280 when unset. A configured 0 disables truncation (full
// content is rendered); negative values fall back to the default.
func (c *EnrichmentConfig) EffectiveMemorySummaryBytes() int {
	if c.MemorySummaryBytes == nil {
		return defaultMemorySummaryBytes
	}
	if *c.MemorySummaryBytes < 0 {
		return defaultMemorySummaryBytes
	}
	return *c.MemorySummaryBytes
}

// SessionDedupConfig configures session-level metadata deduplication. It lives in
// the dedup sub-package (extracted to keep pkg/platform under its size budget);
// the alias keeps the platform.SessionDedupConfig name stable for callers.
type SessionDedupConfig = dedup.Config

// TuningConfig configures AI tuning.
type TuningConfig struct {
	Rules      RulesConfig `yaml:"rules"`
	PromptsDir string      `yaml:"prompts_dir"`
}

// RulesConfig configures operational rules.
type RulesConfig struct {
	QualityThreshold float64 `yaml:"quality_threshold"`
}

// AuditConfig configures audit logging.
// Enabled by default when a database is available. Set enabled: false to disable.
// LogToolCalls is also enabled by default (nil = enabled) whenever audit is
// enabled; set log_tool_calls: false to keep audit on but skip per-tool-call rows.
type AuditConfig struct {
	Enabled       *bool `yaml:"enabled"`
	LogToolCalls  *bool `yaml:"log_tool_calls"`
	RetentionDays int   `yaml:"retention_days"`
}

// IsToolCallLoggingEnabled reports whether per-tool-call audit logging is
// enabled. It requires audit itself to be enabled and defaults to true when
// log_tool_calls is not explicitly set.
func (c *AuditConfig) IsToolCallLoggingEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled) && !isExplicitlyDisabled(c.LogToolCalls)
}

// ObservabilityConfig configures the portal-facing observability
// features that read from Prometheus. The metrics emitters themselves
// are configured via environment (see pkg/observability/config.go);
// this YAML section configures the authenticated PromQL query proxy
// that the portal uses to read those metrics back.
type ObservabilityConfig struct {
	Prometheus PrometheusConfig `yaml:"prometheus"`
}

// PrometheusConfig points the PromQL query proxy at a Prometheus
// instance. An empty URL leaves the proxy unconfigured: its endpoints
// return 503 so the portal can render a clean empty state.
type PrometheusConfig struct {
	URL       string          `yaml:"url"`
	Timeout   time.Duration   `yaml:"timeout"`
	BasicAuth BasicAuthConfig `yaml:"basic_auth"`
	// RateLimitPerSecond caps proxied queries per persona per second.
	// Zero selects the default (10).
	RateLimitPerSecond int `yaml:"rate_limit_per_second"`
}

// BasicAuthConfig holds optional HTTP basic-auth credentials forwarded
// to Prometheus. Both empty means no auth header is sent.
type BasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// MCPAppsConfig configures MCP Apps support for interactive UI components.
type MCPAppsConfig struct {
	// Enabled is the master switch for MCP Apps support.
	// Nil (not set) defaults to true — the built-in platform-info app is always registered.
	// Set to false explicitly to disable all MCP Apps.
	Enabled *bool `yaml:"enabled"`

	// Apps configures individual MCP Apps.
	Apps map[string]AppConfig `yaml:"apps"`
}

// IsEnabled returns whether MCP Apps support is enabled.
// Defaults to true when not explicitly set.
func (c *MCPAppsConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// AppConfig configures an individual MCP App.
type AppConfig struct {
	// Enabled controls whether this app is active.
	Enabled bool `yaml:"enabled"`

	// Tools lists the tool names this app attaches to.
	Tools []string `yaml:"tools"`

	// AssetsPath is the absolute filesystem path to the app's assets directory.
	// This should point to a directory containing the app's HTML/JS/CSS files.
	// Optional for built-in apps that use embedded assets; setting it overrides the embedded content.
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

// ResourcesConfig configures MCP resource templates and managed resources.
type ResourcesConfig struct {
	// Enabled gates the schema/glossary/availability resource templates and the
	// DataHub->Trino resource links (ResourceLinksEnabled). Read-only serving, so
	// it defaults to true (nil = enabled); set false to disable. Read via IsEnabled.
	Enabled *bool               `yaml:"enabled"`
	Custom  []CustomResourceDef `yaml:"custom"`  // always registered when non-empty
	Managed ManagedResourcesCfg `yaml:"managed"` // human-uploaded resources via portal
}

// IsEnabled reports whether the resource templates and DataHub->Trino resource
// links are served, defaulting to true when not explicitly set.
func (c *ResourcesConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// defaultManagedResourcesS3Bucket is the default S3 bucket for managed resources.
const defaultManagedResourcesS3Bucket = "managed-resources"

// ManagedResourcesCfg configures human-uploaded resources stored in S3/Postgres.
// Enabled by default when a database is available. Set enabled: false to disable.
type ManagedResourcesCfg struct {
	Enabled      *bool  `yaml:"enabled"`       // nil = auto (enabled when DB available)
	URIScheme    string `yaml:"uri_scheme"`    // default: "mcp"
	S3Connection string `yaml:"s3_connection"` // name of S3 toolkit instance
	S3Bucket     string `yaml:"s3_bucket"`     // bucket for resource blobs (default: "managed-resources")
}

// CustomResourceDef defines a user-configured static MCP resource.
type CustomResourceDef struct {
	URI         string `yaml:"uri"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	MIMEType    string `yaml:"mime_type"`
	Content     string `yaml:"content,omitempty"`      // inline text/JSON/SVG
	ContentFile string `yaml:"content_file,omitempty"` // absolute or relative path
}

// ProgressConfig configures progress notifications during tool execution.
// Enabled by default (nil = enabled); set enabled: false to disable.
type ProgressConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled reports whether progress notifications are enabled, defaulting
// to true when not explicitly set.
func (c *ProgressConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// ClientLoggingConfig configures server-to-client log message notifications.
// Enabled by default (nil = enabled); set enabled: false to disable.
type ClientLoggingConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled reports whether client logging is enabled, defaulting to true
// when not explicitly set.
func (c *ClientLoggingConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// IconsConfig configures visual metadata for tools, resources, and prompts.
type IconsConfig struct {
	// Enabled is the master switch for icon injection.
	// Enabled by default (nil = enabled); set enabled: false to disable.
	Enabled *bool `yaml:"enabled"`

	// Tools maps tool names to their icon definitions.
	Tools map[string]IconDef `yaml:"tools"`

	// Resources maps resource URI templates to their icon definitions.
	Resources map[string]IconDef `yaml:"resources"`

	// Prompts maps prompt names to their icon definitions.
	Prompts map[string]IconDef `yaml:"prompts"`
}

// IsEnabled reports whether icon injection is enabled, defaulting to true
// when not explicitly set.
func (c *IconsConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
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
	// Enabled by default (nil = enabled); set enabled: false to disable.
	Enabled *bool `yaml:"enabled"`

	// CostEstimation configures query cost estimation and confirmation.
	CostEstimation CostEstimationConfig `yaml:"cost_estimation"`

	// PIIConsent configures PII access consent.
	PIIConsent PIIConsentConfig `yaml:"pii_consent"`
}

// IsEnabled reports whether elicitation is enabled, defaulting to true when
// not explicitly set.
func (c *ElicitationConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// CostEstimationConfig configures query cost estimation.
type CostEstimationConfig struct {
	// Enabled controls whether query cost estimation triggers elicitation.
	// Enabled by default (nil = enabled); set enabled: false to disable.
	Enabled *bool `yaml:"enabled"`

	// RowThreshold is the estimated row count above which confirmation is requested.
	// Default: 1000000 (1 million rows).
	RowThreshold int64 `yaml:"row_threshold"`
}

// IsEnabled reports whether cost estimation is enabled, defaulting to true
// when not explicitly set.
func (c *CostEstimationConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// PIIConsentConfig configures PII access consent.
type PIIConsentConfig struct {
	// Enabled controls whether PII table access triggers elicitation.
	// Enabled by default (nil = enabled); set enabled: false to disable.
	Enabled *bool `yaml:"enabled"`
}

// IsEnabled reports whether PII consent prompting is enabled, defaulting to
// true when not explicitly set.
func (c *PIIConsentConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// WorkflowConfig configures the search-first gate that refuses Trino query
// tools until a discovery tool has been called in the session.
type WorkflowConfig struct {
	// RequireSearch is the search-first hard gate. Enabled by default
	// (nil = enabled); set require_search: false to fully disable gating.
	// When enabled, query tools are refused (the handler never runs) until a
	// discovery tool has been called at least once in the session.
	RequireSearch *bool `yaml:"require_search"`

	// DiscoveryTools lists tool names that satisfy the gate.
	// Defaults to just "search", the universal discovery front door.
	DiscoveryTools []string `yaml:"discovery_tools"`

	// QueryTools lists tool names that are gated by discovery.
	// Defaults to trino_query and trino_execute.
	QueryTools []string `yaml:"query_tools"`
}

// IsRequireSearchEnabled reports whether the search-first gate is enabled,
// defaulting to true when not explicitly set.
func (c *WorkflowConfig) IsRequireSearchEnabled() bool {
	return !isExplicitlyDisabled(c.RequireSearch)
}

// SessionGateConfig configures the session initialization gate that requires
// agents to call platform_info before using any other tool.
type SessionGateConfig struct {
	// Enabled activates the session initialization gate.
	Enabled bool `yaml:"enabled"`

	// InitTool is the tool that initializes the session (default: "platform_info").
	InitTool string `yaml:"init_tool"`

	// ExemptTools lists tool names that bypass the gate (e.g., "list_connections").
	ExemptTools []string `yaml:"exempt_tools"`
}

// APIGatewayConfig holds platform-level tuning for the api-kind toolkit.
// Connection-level configuration (base_url, auth_mode, credentials, etc.)
// lives in the connection store; this struct is for cluster-wide knobs that
// affect every api connection, primarily the embedding-job queue's
// concurrency.
type APIGatewayConfig struct {
	// EmbedJobs tunes the api-gateway embedding job queue.
	EmbedJobs APIGatewayEmbedJobsConfig `yaml:"embed_jobs"`

	// Memory bounds the gateway's response-body memory footprint across
	// all connections so a burst of large responses cannot OOMKill the
	// pod (issue #535).
	Memory APIGatewayMemoryConfig `yaml:"memory"`

	// SelfConnection configures the built-in platform-admin connection
	// that points the API gateway at the platform's own admin REST API.
	SelfConnection APIGatewaySelfConnectionConfig `yaml:"self_connection"`
}

// APIGatewaySelfConnectionConfig configures the built-in "platform-admin"
// API-gateway connection (issue #543), which lets an admin drive the
// platform's own /api/v1/admin/* surface through api_list_endpoints /
// api_invoke_endpoint. Its catalog is sourced from the OpenAPI document
// embedded in the binary, so it stays in sync with the running version
// with no manual catalog maintenance.
//
// Auto-enabled when the prerequisites are met (HTTP transport with the
// admin API mounted, a database, and the API-gateway toolkit). Set
// enabled: false to opt out.
type APIGatewaySelfConnectionConfig struct {
	// Enabled gates self-registration. Nil = auto (on when prerequisites
	// are met); set explicitly to override.
	Enabled *bool `yaml:"enabled"`
	// BaseURL overrides the loopback admin API base URL the connection
	// targets. Empty derives http://127.0.0.1:<port> from the server
	// listen address. Set this only when the admin API is reachable at a
	// different loopback address than the main listener.
	BaseURL string `yaml:"base_url"`
}

// SelfConnectionEnabled reports whether the built-in platform-admin
// connection should self-register, given whether its runtime
// prerequisites are satisfied. A nil Enabled defaults to the
// prerequisite result (auto); an explicit value overrides it but an
// operator cannot force it on when the prerequisites are absent.
func (c APIGatewaySelfConnectionConfig) SelfConnectionEnabled(prereqsMet bool) bool {
	if c.Enabled == nil {
		return prereqsMet
	}
	return *c.Enabled && prereqsMet
}

// APIGatewayMemoryConfig bounds the memory the api gateway commits to
// response-body handling, the structural fix for issue #535: per-request
// size caps bound a single call, but nothing bounded the SUM of
// concurrent calls, so a burst of large responses (each under its cap)
// could collectively exhaust the heap and get the container OOMKilled.
type APIGatewayMemoryConfig struct {
	// MaxInFlightBytes is the global ceiling on bytes committed to
	// api_invoke_endpoint response-body buffering across all api
	// connections. A buffered read that would push committed bytes past
	// this is rejected with a structured 429 (retryable) before the
	// buffer is allocated. 0 = disabled (no global cap; per-connection
	// max_response_bytes still applies per request). api_export is exempt:
	// it streams directly to S3 (issue #537) and never buffers the full
	// body, so it does not consume this budget.
	//
	// Sizing: budget roughly 3x the raw body size per concurrent large
	// request (raw body + decoded copy + JSON-escaped envelope copy) and
	// leave headroom for GC and the other toolkits' working set. A safe
	// target keeps
	//   max_in_flight_bytes ≈ (container_memory_limit × 0.6) / 3
	// so peak buffering stays well under the heap even at full
	// utilization. Do NOT set this to the whole container limit or
	// GOMEMLIMIT — that leaves no room for the transient marshaling
	// copies or for GC.
	MaxInFlightBytes int64 `yaml:"max_in_flight_bytes"`

	// RawMaxBytes caps a single raw passthrough response on the
	// /api/v1/gateway/{connection}/invoke-raw REST route
	// (all-or-nothing). An upstream whose declared Content-Length
	// exceeds this is rejected with 413 (non-retryable) before any bytes
	// are streamed. 0 = no cap: the raw path streams (io.Copy) instead
	// of buffering, so process memory stays bounded regardless of body
	// size — the cap is a policy guard, not a memory guard.
	RawMaxBytes int64 `yaml:"raw_max_bytes"`
}

// APIGatewayEmbedJobsConfig tunes the per-pod embedding worker.
type APIGatewayEmbedJobsConfig struct {
	// Workers is the number of goroutines per pod that claim and
	// process jobs in parallel. Multiple goroutines share the queue;
	// the lease + SKIP LOCKED predicate in Claim prevents two
	// goroutines (in the same pod or across pods) from picking the
	// same job. Zero or negative falls back to 1, which preserves
	// the pre-#430 single-goroutine behavior. Production deployments
	// with many specs and a fast embedder benefit from 2-4; CPU-only
	// embedders typically saturate at 1 because the bottleneck is
	// the embedding model, not the gateway. See #430.
	Workers int `yaml:"workers"`

	// EmbedTimeout caps an individual batched embedding HTTP call the
	// worker issues to Ollama's /api/embed endpoint. The shared
	// embedding.DefaultTimeout (30s) is tuned for the singular
	// /api/embeddings path used by request-path callers (memory recall,
	// capture_insight, etc.) where Ollama returns in 1-3s. The batched
	// path can take 60+ seconds for a 32-text chunk on CPU-only Ollama;
	// using the 30s default makes the worker timeout-storm on every
	// spec write (#445). Zero or negative falls back to 5 minutes,
	// which covers a 32-text batch on CPU Ollama with margin. Operators
	// on GPU embedders can lower this to keep the failure floor tight.
	EmbedTimeout time.Duration `yaml:"embed_timeout"`

	// BatchSize is the number of operations the worker hands to
	// the embedding provider per upstream EmbedBatch call.
	// Smaller batches keep one slow chunk's lost progress small
	// at the cost of more per-call overhead; larger batches
	// amortize that overhead. Zero or negative falls back to
	// 32 (embedjobs.DefaultEmbedBatchSize), the value that
	// shipped before this knob was operator-controlled.
	//
	// Tune lower (e.g. 16) when a CPU-only provider's per-batch
	// latency exceeds EmbedTimeout on full chunks; tune higher
	// (e.g. 64) on GPU providers where per-call overhead
	// dominates per-text compute. See #479.
	BatchSize int `yaml:"batch_size"`

	// LeaseDuration is the lifetime a Claim stamps on a job and
	// the cadence the worker's heartbeat goroutine renews it.
	// The reaper releases leases past this window so a pod that
	// genuinely crashed mid-embed has its job picked up by
	// another worker. Zero or negative falls back to 10 minutes
	// (embedjobs.DefaultLeaseDuration), the value that shipped
	// before this knob was operator-controlled.
	//
	// On CPU-only embedders processing large specs (~150+ ops),
	// total compute can exceed 10 minutes even though every
	// individual batch finishes in 2-3 minutes. The heartbeat
	// (lease_duration / 3 cadence) keeps the lease alive while
	// chunks are completing, so this value caps "pod went
	// silent" rather than "embed batch is slow" — but it must
	// still be greater than EmbedTimeout so a single batch can
	// finish inside one lease window before the heartbeat fires
	// its first renewal. See #479.
	LeaseDuration time.Duration `yaml:"lease_duration"`

	// RetentionDays bounds how long finished index_jobs history is
	// kept. The queue records one row per reconciler sweep per unit
	// (every 5 minutes, on every replica), so succeeded history grows
	// without limit; the retainer periodically deletes succeeded and
	// resolved-failed rows older than this window. Open failures
	// (status='failed' with no resolved_at) and in-flight rows
	// (pending / running) are never purged regardless of age, so the
	// failure-triage surface and the active queue are unaffected.
	//
	// Zero falls back to 14 days (indexjobs.DefaultRetentionDays), a
	// window that keeps a useful span of throughput / latency / job-log
	// history for the admin Indexing dashboard while bounding the table.
	// A negative value disables retention entirely (history grows
	// unbounded), for deployments that prefer to manage cleanup
	// externally. See #523.
	RetentionDays int `yaml:"retention_days"`
}

// isExplicitlyDisabled returns true only when the pointer is non-nil and false.
// A nil pointer means "use the default" (enabled when prerequisites are met).
func isExplicitlyDisabled(b *bool) bool {
	return b != nil && !*b
}

// SessionsConfig configures session externalization.
type SessionsConfig struct {
	// Store selects the session storage backend: "memory" (default) or "database".
	Store string `yaml:"store"`

	// TTL is the session lifetime. Defaults to streamable.session_timeout.
	TTL time.Duration `yaml:"ttl"`

	// CleanupInterval is how often the cleanup routine runs. Defaults to 1m.
	CleanupInterval time.Duration `yaml:"cleanup_interval"`

	// BroadcastChannel overrides the postgres LISTEN/NOTIFY channel name
	// used for cross-replica MCP notification fan-out. Defaults to
	// "mcp_notifications". Override when multiple deployments share a
	// single postgres instance and must NOT cross-broadcast
	// tools/list_changed events to each other's downstream agents.
	BroadcastChannel string `yaml:"broadcast_channel"`

	// Handles configures explicit session handles (issue #792). When enabled,
	// platform_info mints a session_id that the model threads back as an
	// ordinary argument on every subsequent tool call — the pattern the MCP
	// 2026-07-28 spec recommends after removing the protocol-level session and
	// the Mcp-Session-Id header (SEP-2567).
	Handles SessionHandlesConfig `yaml:"handles"`
}

// defaultSessionHandleTTL is the lifetime of an explicit session handle when
// sessions.handles.ttl is unset.
const defaultSessionHandleTTL = 8 * time.Hour

// SessionHandlesConfig configures explicit session handles (issue #792): the
// platform_info-minted session_id that the model passes back on every tool
// call, replacing reliance on the transport-level Mcp-Session-Id header.
type SessionHandlesConfig struct {
	// Enabled activates handle minting, schema advertisement, and validation.
	// Default on (nil = enabled); set enabled: false for byte-identical legacy
	// transport-session behavior.
	Enabled *bool `yaml:"enabled"`

	// TTL is the handle lifetime, refreshed on use. Defaults to 8h.
	TTL time.Duration `yaml:"ttl"`

	// Require refuses any gated tool call that does not carry a valid
	// platform_info-minted handle (SESSION_REQUIRED). A transport session is not
	// accepted as a fallback (issue #800). Default on (nil = required); set
	// require: false for a softer landing during rollout, where a handle-less
	// call falls back to the transport session instead of being refused.
	Require *bool `yaml:"require"`
}

// IsEnabled reports whether explicit session handles are enabled, defaulting to
// true when not explicitly set.
func (c SessionHandlesConfig) IsEnabled() bool {
	return !isExplicitlyDisabled(c.Enabled)
}

// IsRequired reports whether a valid platform_info-minted handle is required on
// every gated tool call, defaulting to true when not explicitly set.
func (c SessionHandlesConfig) IsRequired() bool {
	return !isExplicitlyDisabled(c.Require)
}

// HandleTTL returns the configured handle lifetime, or the 8h default when
// unset or non-positive.
func (c SessionHandlesConfig) HandleTTL() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return defaultSessionHandleTTL
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
	return loadConfigWithRegistry(data, DefaultRegistry())
}

// loadConfigWithRegistry is LoadConfigFromBytes with the version registry
// injected so the version-dispatch branches (deprecated warning, converter
// path) can be exercised in tests; the default registry ships only the current
// v1.
func loadConfigWithRegistry(data []byte, reg *VersionRegistry) (*Config, error) {
	// Expand environment variables
	expanded := []byte(expandEnvVars(string(data)))

	// Peek at the version before full parse
	version := PeekVersion(expanded)

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
		// Legacy apiVersions are translated by their converter, which owns the
		// legacy schema. Unknown-key detection (below) is scoped to the current
		// schema and does not run here; today only v1 exists with a nil
		// converter, so every real config takes the strict-checked path.
		cfg, err = info.Converter(expanded)
		if err != nil {
			return nil, fmt.Errorf("converting config from %s: %w", version, err)
		}
	} else if cfg, err = decodeConfigStrict(expanded); err != nil {
		return nil, err
	}

	// Ensure APIVersion is set
	if cfg.APIVersion == "" {
		cfg.APIVersion = version
	}

	applyDefaults(cfg)

	return cfg, nil
}

// expandEnvVars expands ${VAR} and ${VAR:-default} patterns in the string.
func expandEnvVars(s string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		expr := match[2 : len(match)-1]
		// Support ${VAR:-default} syntax.
		if varName, defaultVal, ok := strings.Cut(expr, ":-"); ok {
			if val := os.Getenv(varName); val != "" {
				return val
			}
			return defaultVal
		}
		return os.Getenv(expr)
	})
}

// applyDefaults applies default values to the config.
func applyDefaults(cfg *Config) {
	applyEnrichmentCompat(cfg)
	applyServerDefaults(cfg)
	applyServiceDefaults(cfg)
	applySessionDedupDefaults(cfg)
	applySessionDefaults(cfg)
	applyAdminDefaults(cfg)
	applyPortalDefaults(cfg)
	applyResourceDefaults(cfg)
	applyElicitationDefaults(cfg)
	applySessionGateDefaults(cfg)
}

// applyEnrichmentCompat folds the deprecated "injection" config key onto the
// canonical Enrichment field. The new "enrichment" key wins when both are set.
func applyEnrichmentCompat(cfg *Config) {
	if cfg.EnrichmentDeprecated == nil {
		return
	}
	slog.Warn("config key 'injection' is deprecated; rename it to 'enrichment'")
	if reflect.ValueOf(cfg.Enrichment).IsZero() {
		cfg.Enrichment = *cfg.EnrichmentDeprecated
	}
	cfg.EnrichmentDeprecated = nil
}

// applyPortalDefaults sets defaults for portal config.
func applyPortalDefaults(cfg *Config) {
	if cfg.Portal.Title == "" {
		cfg.Portal.Title = "MCP Data Platform"
	}
	if cfg.Portal.MaxContentSize == 0 {
		cfg.Portal.MaxContentSize = defaultMaxContentSize
	}
	if cfg.Portal.S3Bucket == "" {
		cfg.Portal.S3Bucket = defaultPortalS3Bucket
	}
	if cfg.Portal.S3Prefix == "" {
		cfg.Portal.S3Prefix = defaultPortalS3Prefix
	}
}

// applyResourceDefaults sets defaults for managed resources config.
func applyResourceDefaults(cfg *Config) {
	if cfg.Resources.Managed.S3Bucket == "" {
		cfg.Resources.Managed.S3Bucket = defaultManagedResourcesS3Bucket
	}
}

// applyElicitationDefaults sets defaults for elicitation config.
func applyElicitationDefaults(cfg *Config) {
	if cfg.Elicitation.CostEstimation.RowThreshold == 0 {
		cfg.Elicitation.CostEstimation.RowThreshold = defaultElicitRowThreshold
	}
}

// defaultInitTool is the tool that initializes a session.
const defaultInitTool = "platform_info"

// applySessionGateDefaults sets defaults for session gate config.
func applySessionGateDefaults(cfg *Config) {
	if cfg.SessionGate.InitTool == "" {
		cfg.SessionGate.InitTool = defaultInitTool
	}
}

// applyConfigStoreDefaults sets defaults for config store settings.
// applyAdminDefaults sets defaults for admin API config.
func applyAdminDefaults(cfg *Config) {
	if cfg.Admin.Persona == "" {
		cfg.Admin.Persona = defaultAdminPersona
	}
	if cfg.Admin.PathPrefix == "" {
		cfg.Admin.PathPrefix = "/api/v1/admin"
	}
}

// applyServerDefaults sets defaults for server-related config fields.
func applyServerDefaults(cfg *Config) {
	if cfg.Server.Name == "" {
		cfg.Server.Name = defaultServerName
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
	if cfg.Enrichment.SessionDedup.EntryTTL == 0 {
		cfg.Enrichment.SessionDedup.EntryTTL = cfg.Semantic.Cache.TTL
	}
	if cfg.Enrichment.SessionDedup.SessionTimeout == 0 {
		cfg.Enrichment.SessionDedup.SessionTimeout = cfg.Server.Streamable.SessionTimeout
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
	if cfg.Sessions.CleanupInterval == 0 {
		cfg.Sessions.CleanupInterval = defaultCleanupInterval
	}
}

// ApplyConfigEntry updates a live config field for a whitelisted config entry key.
//
// In addition to the static keys, any key matching tool.<name>.description
// is treated as a per-tool description override and written into
// Tools.DescriptionOverrides. An empty value removes the override so the
// tool reverts to its default (built-in or file-config) description.
//
// The "tools.deny" key carries a JSON-encoded []string. An empty/blank
// value clears the deny list. A malformed JSON value is logged and
// IGNORED — the existing live slice is left untouched so a corrupt
// config_entries row can't silently open up tools that were supposed to
// be hidden by the file config.
//
// Writes to the runtime-mutable Tools.* fields go through the runtimeMu
// lock so concurrent reads (notably the description-override middleware
// and tools.deny visibility checks) see consistent state.
func (c *Config) ApplyConfigEntry(key, value string) {
	switch key {
	case cfgKeyServerDescription:
		c.Server.Description = value
		return
	case cfgKeyServerAgentInstructions:
		c.Server.AgentInstructions = value
		return
	case ConfigKeyToolsDeny:
		deny, err := parseToolsDenyValue(value)
		if err != nil {
			slog.Warn("ignoring malformed tools.deny config entry; live deny list unchanged",
				"error", err)
			return
		}
		c.SetToolsDeny(deny)
		return
	}
	if name, ok := toolDescriptionKey(key); ok {
		c.SetToolDescriptionOverride(name, value)
	}
}

// SetToolDescriptionOverride writes a single per-tool description override
// under the runtime lock. An empty value removes the override.
func (c *Config) SetToolDescriptionOverride(name, value string) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.Tools.DescriptionOverrides == nil {
		c.Tools.DescriptionOverrides = make(map[string]string)
	}
	if value == "" {
		delete(c.Tools.DescriptionOverrides, name)
		return
	}
	c.Tools.DescriptionOverrides[name] = value
}

// ToolDescriptionOverridesSnapshot returns a shallow copy of the live
// description overrides map. Callers may mutate the returned map without
// affecting the live config.
func (c *Config) ToolDescriptionOverridesSnapshot() map[string]string {
	c.runtimeMu.RLock()
	defer c.runtimeMu.RUnlock()
	return maps.Clone(c.Tools.DescriptionOverrides)
}

// SetToolsDeny replaces the tools.deny slice atomically under the runtime lock.
func (c *Config) SetToolsDeny(deny []string) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.Tools.Deny = deny
}

// ToolsDenySnapshot returns a copy of the current tools.deny slice.
// Callers may mutate the returned slice without affecting the live config.
func (c *Config) ToolsDenySnapshot() []string {
	c.runtimeMu.RLock()
	defer c.runtimeMu.RUnlock()
	if len(c.Tools.Deny) == 0 {
		return nil
	}
	return append([]string(nil), c.Tools.Deny...)
}

// ToolsAllowSnapshot returns a copy of tools.allow. Currently allow is
// not mutable at runtime, but callers should still go through this
// accessor in case that changes.
func (c *Config) ToolsAllowSnapshot() []string {
	c.runtimeMu.RLock()
	defer c.runtimeMu.RUnlock()
	if len(c.Tools.Allow) == 0 {
		return nil
	}
	return append([]string(nil), c.Tools.Allow...)
}

// parseToolsDenyValue decodes the JSON-encoded []string stored in the
// "tools.deny" config_entry. Returns (nil, nil) for empty/blank input
// (an explicit empty deny list). Returns an error for malformed JSON or
// non-array values so the caller can refuse to clobber the live slice.
func parseToolsDenyValue(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, fmt.Errorf("tools.deny: %w", err)
	}
	return out, nil
}

// toolDescriptionKey returns the tool name embedded in a
// "tool.<name>.description" config key, or ("", false) if the key does
// not match the pattern.
func toolDescriptionKey(key string) (string, bool) {
	const prefix = "tool."
	const suffix = ".description"
	if len(key) <= len(prefix)+len(suffix) {
		return "", false
	}
	if key[:len(prefix)] != prefix || key[len(key)-len(suffix):] != suffix {
		return "", false
	}
	return key[len(prefix) : len(key)-len(suffix)], true
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	var errs []string

	if c.Auth.OIDC.Enabled && c.Auth.OIDC.Issuer == "" {
		errs = append(errs, "auth.oidc.issuer is required when OIDC is enabled")
	}

	errs = c.validateOAuth(errs)
	errs = c.validateSessions(errs)
	errs = c.validateBrowserSession(errs)

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors: %s", strings.Join(errs, "; "))
	}

	return nil
}

// validateOAuth checks OAuth configuration validity and appends any errors.
func (c *Config) validateOAuth(errs []string) []string {
	if !c.OAuth.Enabled {
		return errs
	}
	if c.OAuth.Issuer == "" {
		errs = append(errs, "oauth.issuer is required when OAuth is enabled")
	}
	// On an HTTP deployment a missing signing key would auto-generate a
	// per-process key each replica's peers reject. Refuse to boot unless the
	// operator opts into the ephemeral key explicitly.
	if c.requiresConfiguredSigningKey() && c.OAuth.SigningKey == "" {
		errs = append(errs, errOAuthSigningKeyRequiredMsg)
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

// validateBrowserSession checks browser session configuration validity and appends any errors.
func (c *Config) validateBrowserSession(errs []string) []string {
	if !c.Auth.BrowserSession.Enabled {
		return errs
	}
	if !c.Auth.OIDC.Enabled {
		errs = append(errs, "auth.oidc must be enabled when browser_session is enabled")
	}
	if c.Auth.BrowserSession.SigningKey == "" {
		errs = append(errs, "auth.browser_session.signing_key is required")
	}
	// Reject an unrecognized same_site value at startup. ParseSameSite maps any
	// typo to the zero value, which resolves to Lax, so without this check a
	// misspelled "strict" would silently run as Lax and weaken the intended
	// cross-site posture with no error.
	if !browsersession.IsValidSameSite(c.Auth.BrowserSession.SameSite) {
		errs = append(errs, fmt.Sprintf(
			"auth.browser_session.same_site=%q is invalid (want \"lax\", \"strict\", or \"none\")",
			c.Auth.BrowserSession.SameSite))
	}
	// SameSite=None requires the Secure attribute; browsers silently drop a
	// SameSite=None cookie that is not Secure, which would break all portal
	// login. Reject the combination at startup rather than ship a session
	// that never persists.
	if browsersession.ParseSameSite(c.Auth.BrowserSession.SameSite) == http.SameSiteNoneMode &&
		!c.Auth.BrowserSession.IsSecure() {
		errs = append(errs, "auth.browser_session.same_site=none requires auth.browser_session.secure=true")
	}
	return errs
}

// validateSessions checks session configuration validity and appends any errors.
func (c *Config) validateSessions(errs []string) []string {
	if c.Sessions.Store == SessionStoreDatabase && c.Database.DSN == "" {
		errs = append(errs, "database.dsn is required when sessions.store is \"database\"")
	}
	// BroadcastChannel only takes effect for database-backed sessions
	// (the sessionsync broadcasters consult it only on the postgres path). Skip
	// validation entirely on memory-store deployments so an operator
	// who set the field experimentally and switched back to memory
	// doesn't get blocked at startup over a value the platform will
	// never read.
	if c.Sessions.Store == SessionStoreDatabase {
		// Postgres truncates LISTEN identifiers at NAMEDATALEN-1
		// (63 bytes by default) but pg_notify accepts the full
		// string. A long-name override would silently misroute:
		// LISTEN registers the truncated name, NOTIFY uses the full
		// one, and replicas never hear each other — exactly the
		// multi-tenant isolation failure mode the override is meant
		// to prevent. Reject up front.
		const maxListenIdentifier = 63
		if len(c.Sessions.BroadcastChannel) > maxListenIdentifier {
			errs = append(errs, fmt.Sprintf(
				"sessions.broadcast_channel must be ≤%d bytes (postgres LISTEN identifier limit); got %d",
				maxListenIdentifier, len(c.Sessions.BroadcastChannel)))
		}
		// Postgres unquoted identifier grammar: starts with a letter
		// or underscore, then letters/digits/underscores/dollar-signs.
		// pq.Listener.Listen accepts this without explicit quoting,
		// so an invalid character set would error at runtime in
		// NewBroadcaster rather than at config validation. Catching
		// it here gives the operator a precise message instead of the
		// "memory (postgres unavailable, cross-replica fan-out
		// disabled)" fallback log.
		if c.Sessions.BroadcastChannel != "" && !validListenIdentifier(c.Sessions.BroadcastChannel) {
			errs = append(errs, fmt.Sprintf(
				"sessions.broadcast_channel %q is not a valid postgres identifier (must match [A-Za-z_][A-Za-z0-9_$]*)",
				c.Sessions.BroadcastChannel))
		}
	}
	return errs
}

// validListenIdentifier reports whether s is a valid postgres unquoted
// identifier — start with letter or underscore, then letters, digits,
// underscores, or dollar signs.
func validListenIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !validIdentifierRune(r, i == 0) {
			return false
		}
	}
	return true
}

// validIdentifierRune reports whether r is allowed at the given
// position in a postgres unquoted identifier (first==true gates the
// digit/dollar suffix-only characters).
func validIdentifierRune(r rune, first bool) bool {
	if isLetterOrUnderscore(r) {
		return true
	}
	if first {
		return false
	}
	return r == '$' || (r >= '0' && r <= '9')
}

// isLetterOrUnderscore reports whether r is an ASCII letter or
// underscore — the alphabet portion of postgres's unquoted-identifier
// grammar.
func isLetterOrUnderscore(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
}
