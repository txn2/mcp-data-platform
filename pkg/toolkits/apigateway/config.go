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
	"maps"
	"strings"
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
	// AuthModeBasic sends "Authorization: Basic base64(username:password)"
	// per RFC 7617. Required for the long tail of older REST APIs (Jenkins,
	// on-prem Jira / Confluence Server / DC, internal apps) that never moved
	// to bearer or OAuth. RFC 7617 §2 forbids ":" in the userid; password
	// may be empty (some APIs accept "token:" as a bearer-token-in-username
	// pattern). Password is encrypted at rest via the platform's
	// FieldEncryptor (the "password" config key is already in the
	// sensitive-keys list).
	AuthModeBasic = "basic"

	// AuthModeOAuth2ClientCredentials acquires a bearer token via
	// the OAuth 2.1 client_credentials grant — server-to-server,
	// no human in the loop. The platform exchanges the configured
	// client_id + client_secret for a token at OAuth.TokenURL and
	// applies it as "Authorization: Bearer <token>" on outbound
	// calls. Tokens are cached + refreshed automatically by the
	// underlying golang.org/x/oauth2 library; no DB state is
	// required because every restart can re-acquire from credentials.
	// The authorization_code grant (which DOES require DB-persisted
	// refresh tokens + a browser flow) is its own follow-up issue.
	AuthModeOAuth2ClientCredentials = "oauth2_client_credentials" // #nosec G101 -- mode name, not a credential

	// AuthModeOAuth2AuthorizationCode runs the user-driven OAuth 2.1
	// authorization-code grant: an admin completes a one-time browser
	// flow at connection setup; the resulting refresh token is
	// persisted (encrypted) so subsequent platform restarts and
	// background workloads keep working without further interaction.
	// Tokens are refreshed automatically before expiry. Requires the
	// platform's database (refresh-token state survives restarts).
	AuthModeOAuth2AuthorizationCode = "oauth2_authorization_code" // #nosec G101 -- mode name, not a credential

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
	cfgKeyUsername         = "username"
	cfgKeyPassword         = "password" // #nosec G101 -- map key, not a credential; encryption handled by platform FieldEncryptor sensitive-keys list
	cfgKeyConnectionName   = "connection_name"
	cfgKeyConnectTimeout   = "connect_timeout"
	cfgKeyCallTimeout      = "call_timeout"
	cfgKeyTrustLevel       = "trust_level"
	cfgKeyMaxResponseBytes = "max_response_bytes"
	// cfgKeyStaticHeaders holds operator-configured headers that the
	// toolkit appends to every outbound request. Required for upstreams
	// that demand BOTH an Authorization bearer AND a separate
	// subscription/key header (Google Cloud's x-goog-user-project
	// quota-billing header, vendor subscription keys, etc.). Stored
	// as a map[string]any whose values are encrypted at rest by
	// platform.FieldEncryptor (see CfgKeyStaticHeaders in
	// pkg/platform/fieldcrypt.go).
	cfgKeyStaticHeaders = "static_headers"
	// cfgKeyCatalogID names the api_catalogs row that supplies this
	// connection's OpenAPI specs. Empty = connection has no spec
	// surface (api_list_endpoints returns empty + note;
	// api_get_endpoint_schema is unusable). Specs live in the
	// globally-owned catalog, not in the connection — multiple
	// connections to the same vendor API share one catalog instead
	// of duplicating the documentation.
	cfgKeyCatalogID = "catalog_id"

	// OAuth2 config keys are top-level (not nested under "oauth2")
	// because the platform's FieldEncryptor walks only the top
	// level of the config map. A nested oauth2.client_secret would
	// be stored unencrypted; flat keys let the existing
	// encryption-at-rest pickup work without changes to the
	// encryptor. Mirrors how the MCP gateway lays out oauth_*.
	cfgKeyOAuth2TokenURL     = "oauth2_token_url"           // #nosec G101 -- map key, not a credential
	cfgKeyOAuth2ClientID     = "oauth2_client_id"           // #nosec G101 -- map key, not a credential
	cfgKeyOAuth2ClientSecret = "oauth2_client_secret"       // #nosec G101 -- map key, not a credential
	cfgKeyOAuth2Scopes       = "oauth2_scopes"              // #nosec G101 -- map key, not a credential
	cfgKeyOAuth2EndpointAuth = "oauth2_endpoint_auth_style" // #nosec G101 -- "header" or "params" — map key, not a credential
	cfgKeyOAuth2AuthURL      = "oauth2_authorization_url"   // #nosec G101 -- map key, not a credential
	cfgKeyOAuth2Prompt       = "oauth2_prompt"              // #nosec G101 -- map key, not a credential
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
	// Username is the userid for HTTP Basic auth (RFC 7617). Required
	// when AuthMode is "basic". Ignored otherwise. Not a secret on its
	// own (per RFC 7617 §2 the userid is sent in clear after base64
	// decoding regardless), so it is not encrypted at rest.
	Username string
	// Password is the password for HTTP Basic auth. May be empty: some
	// legacy APIs accept a bearer token in the userid slot with an empty
	// password (the "token:" pattern). Encrypted at rest via the
	// platform's FieldEncryptor; the "password" cfg key is already in
	// the sensitive-keys list.
	Password string
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
	// CatalogID names the api_catalogs row whose component specs
	// describe this connection's upstream API. Empty = no spec
	// surface. The catalog is global and may back many connections;
	// editing it propagates to all of them via Toolkit.ReloadConnection.
	CatalogID string
	// OAuth2 carries the OAuth 2.1 parameters used when AuthMode
	// is oauth2_client_credentials. Empty for non-OAuth modes.
	OAuth2 OAuth2Config
	// StaticHeaders are operator-configured headers attached to every
	// outbound request, in addition to whatever AuthMode contributes.
	// Required for upstreams that demand a non-Authorization header on
	// top of the OAuth bearer (Google's x-goog-user-project,
	// vendor subscription keys, etc.). Operator-supplied; the model
	// never sets or overrides these. Values are encrypted at rest.
	StaticHeaders map[string]string
}

// OAuth2Config describes the OAuth 2.1 client_credentials grant
// parameters. The platform exchanges ClientID + ClientSecret at
// TokenURL for an access token (cached + refreshed by the
// golang.org/x/oauth2 library) and applies it as
// "Authorization: Bearer <token>" on outbound calls.
//
// Authorization-code (browser-driven, refresh-token-persisting)
// grants are deferred to a follow-up — they require DB state
// (PKCE verifier table, refresh-token cache) and an admin reauth
// callback handler that this PR intentionally does not bring in.
type OAuth2Config struct {
	// TokenURL is the upstream's token endpoint. Required.
	TokenURL string
	// ClientID is the platform's registered client id. Required.
	ClientID string
	// ClientSecret is the platform's registered client secret.
	// Required. Encrypted at rest via the platform's
	// FieldEncryptor (sensitive-key list already includes
	// "client_secret"; the nested map's value is encrypted before
	// storage in connection_instances.config).
	ClientSecret string
	// Scopes is an optional list of OAuth scopes to request.
	Scopes []string
	// EndpointAuthStyle controls how the client credentials are
	// transmitted at token-fetch time. "header" (default) sends
	// them as HTTP Basic auth on the token request; "params"
	// sends them as POST body parameters. Some IdPs require one
	// or the other; "header" is the OAuth 2.1 default.
	EndpointAuthStyle string
	// AuthorizationURL is the upstream's authorization endpoint.
	// Required only for the authorization_code grant — that's
	// where the platform redirects the admin's browser to start
	// the flow.
	AuthorizationURL string
	// Prompt is an optional OIDC prompt parameter (RFC OIDC
	// §3.1.2.1). Common values: "login" (force credential prompt),
	// "consent" (force consent screen), "select_account",
	// "none" (silent auth). Empty by default — the IdP decides.
	// Operators of strict OIDC realms (Keycloak, Auth0, Okta)
	// typically set this to "login" so admin Reconnect actions
	// always re-prompt the user. Pure-OAuth (non-OIDC) providers
	// often reject unknown parameters with invalid_request, so
	// leave empty for those.
	Prompt string
}

// EndpointAuthStyle values.
const (
	OAuth2AuthStyleHeader = "header"
	OAuth2AuthStyleParams = "params"
)

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
	c.Username = getString(cfg, cfgKeyUsername)
	c.Password = getString(cfg, cfgKeyPassword)
	c.ConnectionName = getString(cfg, cfgKeyConnectionName)
	c.ConnectTimeout = getDuration(cfg, cfgKeyConnectTimeout, c.ConnectTimeout)
	c.CallTimeout = getDuration(cfg, cfgKeyCallTimeout, c.CallTimeout)
	c.TrustLevel = getStringDefault(cfg, cfgKeyTrustLevel, c.TrustLevel)
	c.MaxResponseBytes = getInt64(cfg, cfgKeyMaxResponseBytes, c.MaxResponseBytes)
	c.CatalogID = getString(cfg, cfgKeyCatalogID)
	c.OAuth2 = parseOAuth2Config(cfg)
	c.StaticHeaders = getStringMap(cfg, cfgKeyStaticHeaders)

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
	return c.validateStaticHeaders()
}

// validateStaticHeaders refuses operator config that would collide with
// the toolkit's auth path or with hop-by-hop headers Go forbids on a
// request. A static header attempting to set Authorization (or the
// auth-mode-reserved header for api_key+header) would silently lose to
// the auth layer at request time — fail loudly here instead.
func (c Config) validateStaticHeaders() error {
	if len(c.StaticHeaders) == 0 {
		return nil
	}
	authHeader := authHeaderForConfig(c)
	for name, value := range c.StaticHeaders {
		if name == "" {
			return errors.New("apigateway: static_headers contains an empty header name")
		}
		if !isValidHeaderName(name) {
			return fmt.Errorf("apigateway: static_headers name %q contains characters not permitted in an HTTP header name", name)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("apigateway: static_headers[%q] contains CR/LF/NUL — header smuggling vector", name)
		}
		if strings.EqualFold(name, authorizationHeader) {
			return errors.New("apigateway: static_headers must not set Authorization; configure auth via auth_mode")
		}
		if authHeader != "" && strings.EqualFold(name, authHeader) {
			return fmt.Errorf("apigateway: static_headers must not set %q — already managed by auth_mode", name)
		}
		if isReservedHopHeader(name) {
			return fmt.Errorf("apigateway: static_headers must not set hop-by-hop or net/http-managed header %q", name)
		}
	}
	return nil
}

// isValidHeaderName matches RFC 7230 token chars. Permissive enough for
// real-world headers (x-goog-user-project, X-Subscription-Key) and
// strict enough to refuse spaces / control chars that would let an
// operator inject CRLF via a header name.
func isValidHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return name != ""
}

// isReservedHopHeader names headers Go's net/http manages on the
// request itself (Host, Content-Length) or that are meaningless on a
// per-call basis (Connection, Transfer-Encoding, Upgrade). Setting
// these from operator config would either be silently overridden or
// break the transport.
func isReservedHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding",
		"upgrade", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer":
		return true
	}
	return false
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
	case AuthModeBasic:
		return c.validateBasicAuth()
	case AuthModeOAuth2ClientCredentials:
		return c.validateOAuth2()
	case AuthModeOAuth2AuthorizationCode:
		return c.validateOAuth2AuthCode()
	default:
		return fmt.Errorf("apigateway: invalid auth_mode %q (want none, bearer, api_key, basic, oauth2_client_credentials, or oauth2_authorization_code)", c.AuthMode)
	}
}

// validateBasicAuth enforces RFC 7617 + the platform's smuggling
// defenses for the "basic" auth mode. The userid (username) must be
// non-empty and contain no ":" (RFC 7617 §2 forbids it because the
// decoder splits on the first colon). Both fields must be free of
// CR/LF/NUL because neither RFC 7617 nor base64 stops an operator from
// pasting a "username\r\nX-Smuggled: 1" string that would inject
// extra headers after the toolkit's Authorization line. The password
// may be empty: some legacy APIs accept a bearer token in the userid
// slot with an empty password (the "token:" pattern), so refusing
// empty here would block a real use case.
func (c Config) validateBasicAuth() error {
	if c.Username == "" {
		return errors.New("apigateway: username is required when auth_mode is \"basic\"")
	}
	// Smuggling defenses run before the colon check: a payload like
	// "alice\r\nX-Smuggled: 1" contains both CRLF and ":" and we want
	// the security-relevant error to surface, not the RFC compliance
	// one.
	if strings.ContainsAny(c.Username, "\r\n\x00") {
		return errors.New("apigateway: username contains CR/LF/NUL header smuggling vector")
	}
	if strings.ContainsAny(c.Password, "\r\n\x00") {
		return errors.New("apigateway: password contains CR/LF/NUL header smuggling vector")
	}
	if strings.Contains(c.Username, ":") {
		return errors.New("apigateway: username must not contain \":\" (RFC 7617 §2 forbids it in the userid)")
	}
	return nil
}

// validateOAuth2AuthCode adds the authorization_code-specific
// requirement (AuthorizationURL) on top of the client_credentials
// validation. ClientSecret is still required because OAuth 2.1
// authorization-code with confidential clients exchanges
// (client_id, client_secret, code) for tokens.
func (c Config) validateOAuth2AuthCode() error {
	if err := c.validateOAuth2(); err != nil {
		return err
	}
	if c.OAuth2.AuthorizationURL == "" {
		return errors.New("apigateway: oauth2.authorization_url is required when auth_mode is \"oauth2_authorization_code\"")
	}
	return nil
}

func (c Config) validateOAuth2() error {
	if c.OAuth2.TokenURL == "" {
		return errors.New("apigateway: oauth2.token_url is required when auth_mode is \"oauth2_client_credentials\"")
	}
	if c.OAuth2.ClientID == "" {
		return errors.New("apigateway: oauth2.client_id is required when auth_mode is \"oauth2_client_credentials\"")
	}
	if c.OAuth2.ClientSecret == "" {
		return errors.New("apigateway: oauth2.client_secret is required when auth_mode is \"oauth2_client_credentials\"")
	}
	switch c.OAuth2.EndpointAuthStyle {
	case OAuth2AuthStyleHeader, OAuth2AuthStyleParams:
		return nil
	default:
		return fmt.Errorf("apigateway: invalid oauth2.endpoint_auth_style %q (want %q or %q)",
			c.OAuth2.EndpointAuthStyle, OAuth2AuthStyleHeader, OAuth2AuthStyleParams)
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

// parseOAuth2Config extracts the OAuth2 fields from the top-level
// config map (flat keys; see the cfgKeyOAuth2* declarations for
// the rationale). Returns a zero-value OAuth2Config when no OAuth2
// keys are set; validation against AuthMode happens in Validate.
func parseOAuth2Config(cfg map[string]any) OAuth2Config {
	return OAuth2Config{
		TokenURL:          getString(cfg, cfgKeyOAuth2TokenURL),
		ClientID:          getString(cfg, cfgKeyOAuth2ClientID),
		ClientSecret:      getString(cfg, cfgKeyOAuth2ClientSecret),
		EndpointAuthStyle: getStringDefault(cfg, cfgKeyOAuth2EndpointAuth, OAuth2AuthStyleHeader),
		Scopes:            getStringSlice(cfg, cfgKeyOAuth2Scopes),
		AuthorizationURL:  getString(cfg, cfgKeyOAuth2AuthURL),
		Prompt:            getString(cfg, cfgKeyOAuth2Prompt),
	}
}

// getStringMap reads a map[string]string from the config map. Accepts
// map[string]string (programmatic construction) or map[string]any (YAML/JSON
// unmarshaling). Non-string values are skipped. Empty/missing returns nil.
func getStringMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]string, len(v))
		maps.Copy(out, v)
		return out
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]string, len(v))
		for k, val := range v {
			if s, isStr := val.(string); isStr {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// getStringSlice reads a []string from the config map. Accepts
// either []string (programmatic construction) or []any (YAML
// unmarshaling). Empty/missing returns nil.
func getStringSlice(cfg map[string]any, key string) []string {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, isStr := item.(string); isStr {
				out = append(out, s)
			}
		}
		return out
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
