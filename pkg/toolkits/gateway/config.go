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
	// AuthMode is "none", "bearer", or "api_key".
	AuthMode string
	// Credential is the bearer token or API key. Ignored when AuthMode is "none".
	Credential string
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
	switch c.AuthMode {
	case AuthModeNone, AuthModeBearer, AuthModeAPIKey:
	default:
		return fmt.Errorf("gateway: invalid auth_mode %q (want none, bearer, or api_key)", c.AuthMode)
	}
	if c.AuthMode != AuthModeNone && c.Credential == "" {
		return fmt.Errorf("gateway: credential is required when auth_mode is %q", c.AuthMode)
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
