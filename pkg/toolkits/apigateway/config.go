// Package apigateway provides an HTTP API gateway toolkit that proxies
// authenticated REST API calls through the platform's auth, persona, and
// audit pipeline. Sibling to pkg/toolkits/gateway, which proxies upstream
// MCP servers; this toolkit proxies arbitrary HTTP/JSON APIs.
//
// The toolkit exposes a small fixed set of MCP tools regardless of how
// many connections are registered or how many endpoints each upstream
// API has. v1 ships api_invoke_endpoint; api_list_endpoints and
// api_get_endpoint_schema follow once OpenAPI ingestion lands (see
// the RFC at issue #364).
package apigateway

import (
	"errors"
	"fmt"
	"time"
)

const (
	// Kind is the connection-instance kind discriminator. Operators see
	// this in the admin UI's connection picker.
	Kind = "api"

	// AuthModeNone disables outbound authentication.
	AuthModeNone = "none"
	// AuthModeBearer sends "Authorization: Bearer <credential>".
	AuthModeBearer = "bearer"
	// AuthModeAPIKey sends the credential as a header (default
	// "X-API-Key") or as a query parameter; placement and key name are
	// per-connection so APIs that use non-standard schemes (e.g. an
	// "api_key" query parameter, or a custom "X-Api-Token" header) can
	// be onboarded without code changes.
	AuthModeAPIKey = "api_key"

	// APIKeyPlacementHeader (default) sends the credential as an HTTP
	// header named by APIKeyHeader.
	APIKeyPlacementHeader = "header"
	// APIKeyPlacementQuery sends the credential as a URL query parameter
	// named by APIKeyParam.
	APIKeyPlacementQuery = "query"

	// DefaultAPIKeyHeader is the conventional API-key header name when
	// the connection does not specify one.
	DefaultAPIKeyHeader = "X-API-Key" // #nosec G101 -- header name, not a credential

	// TrustLevelUntrusted is the default. Advisory only in v1: the
	// field is parsed and validated but no platform code reads it.
	// Reserved for future response-shaping enforcement (see issue
	// #373) — operators setting this today get the same behavior as
	// not setting it.
	TrustLevelUntrusted = "untrusted"
	// TrustLevelTrusted is advisory only in v1; see TrustLevelUntrusted.
	TrustLevelTrusted = "trusted"

	// DefaultConnectTimeout caps the time spent establishing the
	// outbound connection (TCP + TLS handshake) on each invocation.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultCallTimeout caps the total per-call time including
	// upstream processing and response read.
	DefaultCallTimeout = 60 * time.Second

	// DefaultMaxResponseBytes caps how much of the upstream response
	// body the toolkit will return to the model. Larger payloads are
	// truncated; the response envelope flags truncation so the model
	// can react. Operators with a need for large bodies can raise this
	// per-connection or, when #372 lands, use the streaming-to-S3
	// variant to bypass the model entirely.
	DefaultMaxResponseBytes = int64(10 * 1024 * 1024)
)

// cfgKey* constants name the keys used to read a Config from a
// map[string]any (the form connections take in the platform's generic
// connection_instances store).
const (
	cfgKeyBaseURL          = "base_url"
	cfgKeyAuthMode         = "auth_mode"
	cfgKeyCredential       = "credential"     // #nosec G101 -- map key, not a secret
	cfgKeyAPIKeyHeader     = "api_key_header" // #nosec G101 -- map key, not a credential
	cfgKeyAPIKeyParam      = "api_key_param"  // #nosec G101 -- map key, not a credential
	cfgKeyAPIKeyPlacement  = "api_key_placement"
	cfgKeyConnectionName   = "connection_name"
	cfgKeyConnectTimeout   = "connect_timeout"
	cfgKeyCallTimeout      = "call_timeout"
	cfgKeyTrustLevel       = "trust_level"
	cfgKeyMaxResponseBytes = "max_response_bytes"
	// cfgKeyOpenAPISpec carries the raw OpenAPI 3.x document (YAML
	// or JSON) for this connection. Parsed at AddConnection time,
	// stored on the live connection, and consumed by
	// api_list_endpoints. Inline-only in v1; URL pin with scheduled
	// revalidation is deferred.
	cfgKeyOpenAPISpec = "openapi_spec"
)

// Config holds api-gateway toolkit configuration for a single upstream
// HTTP API connection.
type Config struct {
	// BaseURL is the upstream API root (e.g. "https://api.example.com").
	// Required. Trailing slash is stripped at parse time.
	BaseURL string
	// AuthMode is "none", "bearer", or "api_key" in v1. OAuth modes
	// land with #368.
	AuthMode string
	// Credential is the bearer token or API key. Ignored when AuthMode
	// is "none". Encrypted at rest via the platform's FieldEncryptor.
	Credential string
	// APIKeyPlacement is "header" (default) or "query" — only consulted
	// when AuthMode is "api_key".
	APIKeyPlacement string
	// APIKeyHeader is the header name to set when APIKeyPlacement is
	// "header". Defaults to "X-API-Key".
	APIKeyHeader string
	// APIKeyParam is the query parameter name when APIKeyPlacement is
	// "query". No default — required when placement is "query".
	APIKeyParam string
	// ConnectionName is the audit-visible connection identifier and the
	// value passed in the tool's `connection` argument. Defaults to
	// the toolkit instance name when unset.
	ConnectionName string
	// ConnectTimeout caps the dial step on each invocation.
	ConnectTimeout time.Duration
	// CallTimeout caps the total per-invocation time.
	CallTimeout time.Duration
	// TrustLevel is "untrusted" (default) or "trusted".
	TrustLevel string
	// MaxResponseBytes caps how much of an upstream response body is
	// returned to the model. Defaults to DefaultMaxResponseBytes.
	MaxResponseBytes int64
	// OpenAPISpec is the raw OpenAPI 3.x document (YAML or JSON)
	// for this connection. Optional. When non-empty the toolkit
	// parses it at AddConnection time and exposes its operations
	// via api_list_endpoints; an unparseable spec fails the
	// connection with a clear error rather than silently dropping.
	// Inline-only in v1.
	OpenAPISpec string
}

// MultiConfig holds parsed per-connection configs plus the aggregate
// toolkit's default connection name.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig validates and returns the parsed config for every
// instance. Per-instance parse errors fail the platform startup; HTTP
// connectivity failures are handled at invocation time, not here.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) (MultiConfig, error) {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := ParseConfig(r)
		if err != nil {
			return MultiConfig{}, fmt.Errorf("apigateway/%s: %w", name, err)
		}
		if c.ConnectionName == "" {
			c.ConnectionName = name
		}
		parsed[name] = c
	}
	return MultiConfig{DefaultName: defaultName, Instances: parsed}, nil
}

// ParseConfig parses a Config from a generic map (the form admin-saved
// connections take in the connection_instances table) and applies
// defaults. The returned Config is fully validated.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		AuthMode:         AuthModeNone,
		APIKeyPlacement:  APIKeyPlacementHeader,
		APIKeyHeader:     DefaultAPIKeyHeader,
		ConnectTimeout:   DefaultConnectTimeout,
		CallTimeout:      DefaultCallTimeout,
		TrustLevel:       TrustLevelUntrusted,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}

	c.BaseURL = trimTrailingSlash(getString(cfg, cfgKeyBaseURL))
	c.AuthMode = getStringDefault(cfg, cfgKeyAuthMode, c.AuthMode)
	c.Credential = getString(cfg, cfgKeyCredential)
	c.APIKeyPlacement = getStringDefault(cfg, cfgKeyAPIKeyPlacement, c.APIKeyPlacement)
	c.APIKeyHeader = getStringDefault(cfg, cfgKeyAPIKeyHeader, c.APIKeyHeader)
	c.APIKeyParam = getString(cfg, cfgKeyAPIKeyParam)
	c.ConnectionName = getString(cfg, cfgKeyConnectionName)
	c.ConnectTimeout = getDuration(cfg, cfgKeyConnectTimeout, c.ConnectTimeout)
	c.CallTimeout = getDuration(cfg, cfgKeyCallTimeout, c.CallTimeout)
	c.TrustLevel = getStringDefault(cfg, cfgKeyTrustLevel, c.TrustLevel)
	c.MaxResponseBytes = getInt64(cfg, cfgKeyMaxResponseBytes, c.MaxResponseBytes)
	c.OpenAPISpec = getString(cfg, cfgKeyOpenAPISpec)

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate returns an error if the configuration is missing required
// fields or contains invalid values.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("apigateway: base_url is required")
	}
	if err := c.validateAuth(); err != nil {
		return err
	}
	switch c.TrustLevel {
	case TrustLevelUntrusted, TrustLevelTrusted:
	default:
		return fmt.Errorf("apigateway: invalid trust_level %q (want untrusted or trusted)", c.TrustLevel)
	}
	if c.ConnectTimeout <= 0 {
		return errors.New("apigateway: connect_timeout must be positive")
	}
	if c.CallTimeout <= 0 {
		return errors.New("apigateway: call_timeout must be positive")
	}
	if c.MaxResponseBytes <= 0 {
		return errors.New("apigateway: max_response_bytes must be positive")
	}
	return nil
}

func (c Config) validateAuth() error {
	switch c.AuthMode {
	case AuthModeNone:
		return nil
	case AuthModeBearer:
		if c.Credential == "" {
			return errors.New("apigateway: credential is required when auth_mode is \"bearer\"")
		}
		return nil
	case AuthModeAPIKey:
		return c.validateAPIKeyAuth()
	default:
		return fmt.Errorf("apigateway: invalid auth_mode %q (want none, bearer, or api_key)", c.AuthMode)
	}
}

func (c Config) validateAPIKeyAuth() error {
	if c.Credential == "" {
		return errors.New("apigateway: credential is required when auth_mode is \"api_key\"")
	}
	switch c.APIKeyPlacement {
	case APIKeyPlacementHeader:
		if c.APIKeyHeader == "" {
			return errors.New("apigateway: api_key_header must not be empty")
		}
	case APIKeyPlacementQuery:
		if c.APIKeyParam == "" {
			return errors.New("apigateway: api_key_param is required when api_key_placement is \"query\"")
		}
	default:
		return fmt.Errorf("apigateway: invalid api_key_placement %q (want header or query)", c.APIKeyPlacement)
	}
	return nil
}

func trimTrailingSlash(s string) string {
	for s != "" && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
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

func getInt64(cfg map[string]any, key string, defaultVal int64) int64 {
	raw, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch v := raw.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return defaultVal
}
