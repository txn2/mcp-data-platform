// Package gateway provides an MCP gateway toolkit that proxies tools from
// an upstream MCP server through the platform's auth, persona, and audit pipeline.
package gateway

import (
	"errors"
	"fmt"
	"time"
)

const (
	// Kind is the toolkit kind identifier. Each connection of this kind is
	// a remote MCP server that the platform's gateway feature proxies. The
	// kind value is what operators see in the connection picker; the
	// gateway terminology is reserved for the platform-side feature
	// (admin endpoints, internal package, DB tables) that does the proxying.
	Kind = "mcp"

	// AuthModeNone disables outbound authentication.
	AuthModeNone = "none"
	// AuthModeBearer sends "Authorization: Bearer <credential>" on upstream requests.
	AuthModeBearer = "bearer"
	// AuthModeAPIKey sends "X-API-Key: <credential>" on upstream requests.
	AuthModeAPIKey = "api_key"
	// AuthModeOAuth acquires a bearer token via an OAuth 2.1 grant
	// (client_credentials in v1) and refreshes it before expiry.
	AuthModeOAuth = "oauth"

	// OAuthGrantClientCredentials is the machine-to-machine grant type.
	// Requires a token URL, client id, and client secret. No user
	// interaction — the platform exchanges the credentials for a token on
	// behalf of all platform users.
	OAuthGrantClientCredentials = "client_credentials"

	// TrustLevelUntrusted is the default. Upstream responses are treated as
	// untrusted content (reserved for future enforcement).
	TrustLevelUntrusted = "untrusted"
	// TrustLevelTrusted bypasses future content sanitization. Use only for
	// first-party upstreams under the operator's control.
	TrustLevelTrusted = "trusted"

	// DefaultConnectTimeout is the default timeout for initial upstream connection + tool discovery.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultCallTimeout is the default per-tool-call timeout for upstream forwarding.
	DefaultCallTimeout = 60 * time.Second

	// NamespaceSeparator joins the connection name and remote tool name (e.g. "crm__get_contact").
	NamespaceSeparator = "__"
)

// Config holds gateway toolkit configuration for a single upstream MCP connection.
type Config struct {
	// Endpoint is the streamable HTTP URL of the upstream MCP server. Required.
	Endpoint string
	// AuthMode is "none", "bearer", "api_key", or "oauth".
	AuthMode string
	// Credential is the bearer token or API key. Ignored when AuthMode is "none" or "oauth".
	Credential string
	// OAuth carries the OAuth-specific configuration used when AuthMode is "oauth".
	OAuth OAuthConfig
	// ConnectionName is the audit-visible connection identifier and also the
	// tool-name prefix. Defaults to the toolkit instance name when unset.
	ConnectionName string
	// ConnectTimeout caps the initial connection + ListTools call.
	ConnectTimeout time.Duration
	// CallTimeout caps each forwarded tool invocation.
	CallTimeout time.Duration
	// TrustLevel is "untrusted" (default) or "trusted".
	TrustLevel string
}

// OAuthConfig describes the OAuth 2.1 parameters used when AuthMode is
// "oauth". The grant is client_credentials in v1 — the platform holds
// client_id + client_secret and exchanges them for a bearer token on
// every platform user's behalf. Per-user authorization-code flow is a
// planned follow-up; until then rule-by-rule user attribution still
// lives in the platform's audit log.
type OAuthConfig struct {
	// Grant selects the OAuth flow. Currently only "client_credentials"
	// is accepted.
	Grant string
	// TokenURL is the upstream's OAuth token endpoint.
	TokenURL string
	// ClientID is the platform's registered client id with the upstream.
	ClientID string
	// ClientSecret is the platform's registered client secret. Encrypted
	// at rest (same field-level encryption as Credential).
	ClientSecret string
	// Scope is the optional space-delimited scope string.
	Scope string
}

// MultiConfig holds one or more parsed per-connection gateway configs along
// with the aggregate toolkit's default connection name.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig validates and returns the parsed config for every
// instance. Per-instance parse errors are surfaced as fatal (operator must
// fix the config); dial/connectivity errors are handled at load time in
// NewMulti, not here.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) (MultiConfig, error) {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := ParseConfig(r)
		if err != nil {
			return MultiConfig{}, fmt.Errorf("gateway/%s: %w", name, err)
		}
		if c.ConnectionName == "" {
			c.ConnectionName = name
		}
		parsed[name] = c
	}
	return MultiConfig{DefaultName: defaultName, Instances: parsed}, nil
}

// ParseConfig parses a gateway configuration from a map.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		AuthMode:       AuthModeNone,
		ConnectTimeout: DefaultConnectTimeout,
		CallTimeout:    DefaultCallTimeout,
		TrustLevel:     TrustLevelUntrusted,
	}

	c.Endpoint = getString(cfg, "endpoint")
	c.AuthMode = getStringDefault(cfg, "auth_mode", c.AuthMode)
	c.Credential = getString(cfg, "credential")
	c.OAuth = parseOAuthConfig(cfg)
	c.ConnectionName = getString(cfg, "connection_name")
	c.ConnectTimeout = getDuration(cfg, "connect_timeout", c.ConnectTimeout)
	c.CallTimeout = getDuration(cfg, "call_timeout", c.CallTimeout)
	c.TrustLevel = getStringDefault(cfg, "trust_level", c.TrustLevel)

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate returns an error if the configuration is missing required fields
// or contains invalid values.
func (c Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("gateway: endpoint is required")
	}
	if err := c.validateAuth(); err != nil {
		return err
	}
	switch c.TrustLevel {
	case TrustLevelUntrusted, TrustLevelTrusted:
	default:
		return fmt.Errorf("gateway: invalid trust_level %q (want untrusted or trusted)", c.TrustLevel)
	}
	if c.ConnectTimeout <= 0 {
		return errors.New("gateway: connect_timeout must be positive")
	}
	if c.CallTimeout <= 0 {
		return errors.New("gateway: call_timeout must be positive")
	}
	return nil
}

// validateAuth checks the credential / OAuth shape based on AuthMode.
func (c Config) validateAuth() error {
	switch c.AuthMode {
	case AuthModeNone:
		return nil
	case AuthModeBearer, AuthModeAPIKey:
		if c.Credential == "" {
			return fmt.Errorf("gateway: credential is required when auth_mode is %q", c.AuthMode)
		}
		return nil
	case AuthModeOAuth:
		return validateOAuth(c.OAuth)
	default:
		return fmt.Errorf("gateway: invalid auth_mode %q (want none, bearer, api_key, or oauth)", c.AuthMode)
	}
}

func validateOAuth(o OAuthConfig) error {
	if o.Grant != OAuthGrantClientCredentials {
		return fmt.Errorf("gateway: oauth.grant %q not supported (only %q in v1)",
			o.Grant, OAuthGrantClientCredentials)
	}
	if o.TokenURL == "" {
		return errors.New("gateway: oauth.token_url is required")
	}
	if o.ClientID == "" {
		return errors.New("gateway: oauth.client_id is required")
	}
	if o.ClientSecret == "" {
		return errors.New("gateway: oauth.client_secret is required")
	}
	return nil
}

// parseOAuthConfig extracts the oauth section from the raw config map.
// The raw config nests oauth fields under an "oauth" key (preferred) or
// reads them directly from "oauth_*" prefixed top-level keys (legacy /
// flattened form for simple admin UIs).
func parseOAuthConfig(cfg map[string]any) OAuthConfig {
	if nested, ok := cfg["oauth"].(map[string]any); ok {
		return OAuthConfig{
			Grant:        getStringDefault(nested, "grant", OAuthGrantClientCredentials),
			TokenURL:     getString(nested, "token_url"),
			ClientID:     getString(nested, "client_id"),
			ClientSecret: getString(nested, "client_secret"),
			Scope:        getString(nested, "scope"),
		}
	}
	return OAuthConfig{
		Grant:        getStringDefault(cfg, "oauth_grant", OAuthGrantClientCredentials),
		TokenURL:     getString(cfg, "oauth_token_url"),
		ClientID:     getString(cfg, "oauth_client_id"),
		ClientSecret: getString(cfg, "oauth_client_secret"),
		Scope:        getString(cfg, "oauth_scope"),
	}
}

func getString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func getStringDefault(cfg map[string]any, key, defaultVal string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func getDuration(cfg map[string]any, key string, defaultVal time.Duration) time.Duration {
	raw, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch v := raw.(type) {
	case string:
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	case time.Duration:
		return v
	case int:
		return time.Duration(v) * time.Second
	case int64:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v) * time.Second
	}
	return defaultVal
}
