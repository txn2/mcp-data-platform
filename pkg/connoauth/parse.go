package connoauth

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// Canonical connection-config keys for the OAuth flow. These are the
// `oauth_*` vocabulary (originally the MCP gateway's), chosen as the
// single shared schema across every toolkit kind. New connections and
// every migrated row use these keys; ParseConfig reads them first.
const (
	// ConfigKeyAuthMode is the top-level auth selector. The OAuth value
	// is AuthModeOAuth; the specific flow is carried by ConfigKeyGrant.
	ConfigKeyAuthMode = "auth_mode"
	// ConfigKeyGrant selects the OAuth flow: GrantAuthorizationCode or
	// GrantClientCredentials.
	ConfigKeyGrant = "oauth_grant"
	// ConfigKeyTokenURL is the IdP token endpoint.
	ConfigKeyTokenURL = "oauth_token_url" // #nosec G101 -- config-map key, not a credential
	// ConfigKeyAuthorizationURL is the IdP authorization endpoint
	// (authorization_code only).
	ConfigKeyAuthorizationURL = "oauth_authorization_url"
	// ConfigKeyClientID is the client identifier registered with the IdP.
	ConfigKeyClientID = "oauth_client_id"
	// ConfigKeyClientSecret is the matching client credential. Encrypted
	// at rest by the connection store's FieldEncryptor.
	ConfigKeyClientSecret = "oauth_client_secret" // #nosec G101 -- config-map key, not a credential
	// ConfigKeyScope is the space-delimited scope list (the OAuth 2.0
	// wire shape).
	ConfigKeyScope = "oauth_scope"
	// ConfigKeyPrompt is the optional OIDC prompt parameter.
	ConfigKeyPrompt = "oauth_prompt"
	// ConfigKeyEndpointAuthStyle selects how client credentials reach
	// the token endpoint: AuthStyleHeader (default) or AuthStyleParams.
	ConfigKeyEndpointAuthStyle = "oauth_endpoint_auth_style" // #nosec G101 -- config-map key, not a credential
)

// Grant values for ConfigKeyGrant.
const (
	// GrantAuthorizationCode is the browser-driven, refresh-token-
	// persisting flow.
	GrantAuthorizationCode = "authorization_code"
	// GrantClientCredentials is the machine-to-machine flow.
	GrantClientCredentials = "client_credentials"
)

// AuthModeOAuth is the canonical ConfigKeyAuthMode value for an OAuth
// connection. The grant is carried separately in ConfigKeyGrant so the
// auth_mode enum does not explode as new grants (device_code, etc.) are
// added.
const AuthModeOAuth = "oauth"

// EndpointAuthStyle values for ConfigKeyEndpointAuthStyle.
const (
	AuthStyleHeader = "header"
	AuthStyleParams = "params"
)

// Legacy `oauth2_*` config keys (originally the HTTP API gateway's).
// ParseConfig falls back to these when the canonical key is absent, so
// connection rows authored before the schema unified still parse. The
// database migration rewrites these to the canonical keys; the fallback
// remains as a one-release safety net for file-configured connections
// and externally-generated config blobs, and is scheduled for removal
// (see the issue's removal milestone).
const (
	legacyKeyTokenURL          = "oauth2_token_url"           // #nosec G101 -- config-map key, not a credential
	legacyKeyAuthorizationURL  = "oauth2_authorization_url"   // #nosec G101 -- config-map key, not a credential
	legacyKeyClientID          = "oauth2_client_id"           // #nosec G101 -- config-map key, not a credential
	legacyKeyClientSecret      = "oauth2_client_secret"       // #nosec G101 -- config-map key, not a credential
	legacyKeyScopes            = "oauth2_scopes"              // #nosec G101 -- config-map key, not a credential
	legacyKeyPrompt            = "oauth2_prompt"              // #nosec G101 -- config-map key, not a credential
	legacyKeyEndpointAuthStyle = "oauth2_endpoint_auth_style" // #nosec G101 -- config-map key, not a credential

	// Legacy auth_mode values that encoded the grant in the mode string.
	legacyAuthModeAuthorizationCode = "oauth2_authorization_code" // #nosec G101 -- mode name, not a credential
	legacyAuthModeClientCredentials = "oauth2_client_credentials" // #nosec G101 -- mode name, not a credential
)

// ErrInvalidConfig is returned by ParseConfig when the OAuth config is
// structurally malformed (wrong value type for a key, or an
// unrecognized grant / auth_mode). It wraps a descriptive message;
// callers map it to a 400 / startup-skip without string-matching.
var ErrInvalidConfig = fmt.Errorf("connoauth: invalid oauth config")

// deprecationWarned dedups the legacy-key deprecation warning to one
// emission per (kind, name) per process lifetime, so a connection read
// on every outbound request does not flood the log.
var deprecationWarned sync.Map // map[string]struct{} keyed by kind + "/" + name

// ParseConfig builds a Config from a connection's raw config map,
// reading the canonical `oauth_*` keys first and falling back to the
// legacy `oauth2_*` keys when a canonical key is absent. Mixed input
// resolves per field (canonical wins). The result is normalized
// regardless of input shape: scopes become a []string split from the
// canonical space-delimited form (or joined from the legacy array), and
// the endpoint auth style becomes the oauth2.AuthStyle enum.
//
// CABundlePEM is intentionally not read here: it derives from the
// connection's TLS config (a non-OAuth concern) and is layered on by
// the caller.
//
// kind and name identify the connection solely for the deduped
// deprecation warning emitted when any legacy key is the source value.
// A nil or empty config returns the zero Config and no error; the
// caller validates required fields for the chosen grant.
func ParseConfig(kind, name string, cfg map[string]any) (Config, error) {
	if cfg == nil {
		return Config{}, nil
	}
	legacy := false
	pick := func(canonicalKey, legacyKey string) string {
		if v := getStringValue(cfg, canonicalKey); v != "" {
			return v
		}
		if v := getStringValue(cfg, legacyKey); v != "" {
			legacy = true
			return v
		}
		return ""
	}

	grant, err := resolveGrant(cfg, &legacy)
	if err != nil {
		return Config{}, err
	}
	scopes, err := resolveScopes(cfg, &legacy)
	if err != nil {
		return Config{}, err
	}
	authStyle, err := resolveAuthStyle(cfg, &legacy)
	if err != nil {
		return Config{}, err
	}

	out := Config{
		Grant:             grant,
		AuthorizationURL:  pick(ConfigKeyAuthorizationURL, legacyKeyAuthorizationURL),
		TokenURL:          pick(ConfigKeyTokenURL, legacyKeyTokenURL),
		ClientID:          pick(ConfigKeyClientID, legacyKeyClientID),
		ClientSecret:      pick(ConfigKeyClientSecret, legacyKeyClientSecret),
		Scopes:            scopes,
		EndpointAuthStyle: authStyle,
		Prompt:            pick(ConfigKeyPrompt, legacyKeyPrompt),
	}
	if legacy {
		warnLegacyOnce(kind, name)
	}
	return out, nil
}

// resolveGrant determines the OAuth grant. The canonical ConfigKeyGrant
// wins; absent that, a legacy auth_mode that encoded the grant is
// translated. A canonical auth_mode of "oauth" (or an empty auth_mode)
// with no explicit grant defaults to client_credentials, matching the
// gateway toolkit's historical default. An unrecognized grant or a
// non-OAuth auth_mode with no grant is an error.
func resolveGrant(cfg map[string]any, legacy *bool) (string, error) {
	if g := getStringValue(cfg, ConfigKeyGrant); g != "" {
		if g != GrantAuthorizationCode && g != GrantClientCredentials {
			return "", fmt.Errorf("unknown oauth_grant %q (want %q or %q): %w",
				g, GrantAuthorizationCode, GrantClientCredentials, ErrInvalidConfig)
		}
		return g, nil
	}
	switch mode := getStringValue(cfg, ConfigKeyAuthMode); mode {
	case legacyAuthModeAuthorizationCode:
		*legacy = true
		return GrantAuthorizationCode, nil
	case legacyAuthModeClientCredentials:
		*legacy = true
		return GrantClientCredentials, nil
	case AuthModeOAuth, "":
		return GrantClientCredentials, nil
	default:
		return "", fmt.Errorf("cannot derive oauth grant from auth_mode %q: %w", mode, ErrInvalidConfig)
	}
}

// resolveScopes reads the canonical space-delimited oauth_scope string,
// falling back to the legacy oauth2_scopes array (joined). A wrong
// value type for either key is an error. Absent both, returns nil.
func resolveScopes(cfg map[string]any, legacy *bool) ([]string, error) {
	if raw, ok := cfg[ConfigKeyScope]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("config key %s must be a space-delimited string: %w", ConfigKeyScope, ErrInvalidConfig)
		}
		return strings.Fields(s), nil
	}
	if raw, ok := cfg[legacyKeyScopes]; ok {
		*legacy = true
		return coerceScopeArray(raw)
	}
	return nil, nil
}

// coerceScopeArray converts the legacy oauth2_scopes value into a
// []string. JSONB decodes arrays as []any, so both []string and []any
// (of strings) are accepted; any other shape, or a non-string element,
// is malformed.
func coerceScopeArray(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("config key %s elements must be strings: %w", legacyKeyScopes, ErrInvalidConfig)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("config key %s must be an array of strings: %w", legacyKeyScopes, ErrInvalidConfig)
	}
}

// resolveAuthStyle maps the canonical or legacy endpoint-auth-style
// string onto the oauth2 constant. Empty resolves to AuthStyleInHeader
// (the OAuth 2.1 default); a non-empty unrecognized value is an error
// so an operator typo surfaces at parse time rather than silently
// degrading to the default.
func resolveAuthStyle(cfg map[string]any, legacy *bool) (oauth2.AuthStyle, error) {
	s := getStringValue(cfg, ConfigKeyEndpointAuthStyle)
	if s == "" {
		if v := getStringValue(cfg, legacyKeyEndpointAuthStyle); v != "" {
			*legacy = true
			s = v
		}
	}
	switch s {
	case "", AuthStyleHeader:
		return oauth2.AuthStyleInHeader, nil
	case AuthStyleParams:
		return oauth2.AuthStyleInParams, nil
	default:
		return oauth2.AuthStyleInHeader, fmt.Errorf("unknown %s %q (want %q or %q): %w",
			ConfigKeyEndpointAuthStyle, s, AuthStyleHeader, AuthStyleParams, ErrInvalidConfig)
	}
}

// warnLegacyOnce emits a single deprecation warning per (kind, name).
func warnLegacyOnce(kind, name string) {
	dedupKey := kind + "/" + name
	if _, loaded := deprecationWarned.LoadOrStore(dedupKey, struct{}{}); loaded {
		return
	}
	slog.Warn("connoauth: connection uses deprecated oauth2_* config keys; "+
		"run the unify-oauth-config migration or re-save the connection to adopt the canonical oauth_* keys",
		"kind", kind, "name", name)
}

// getStringValue returns cfg[key] coerced to a string, or "" when the
// key is absent or not a string.
func getStringValue(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}
