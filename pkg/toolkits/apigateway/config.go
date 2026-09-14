// Package apigateway provides an HTTP API gateway toolkit that proxies
// authenticated REST API calls through the platform's auth, persona, and
// audit pipeline. Sibling to pkg/toolkits/gateway, which proxies upstream
// MCP servers; this toolkit proxies arbitrary HTTP/JSON APIs.
//
// The toolkit exposes a small fixed set of MCP tools regardless of how
// many connections are registered or how many endpoints each upstream
// API has: api_discover walks a connection's catalog at the depth the
// caller sets, api_invoke_endpoint calls one operation, and api_export
// streams one into an asset.
package apigateway

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
)

const (
	// Kind is the connection-instance kind discriminator. Operators see
	// this in the admin UI's connection picker.
	Kind = "api"

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
	DefaultConnectTimeout = upstreamauth.DefaultConnectTimeout
	// DefaultCallTimeout caps the total per-call time including
	// upstream processing and response read.
	DefaultCallTimeout = upstreamauth.DefaultCallTimeout

	// DefaultMaxResponseBytes is the upstream read cap: the most the
	// gateway reads of any one response (a page of a walk, an inline
	// call). It bounds transfer and buffering, not what reaches the
	// model; that is MaxInlineBytes.
	DefaultMaxResponseBytes = upstreamauth.DefaultMaxResponseBytes

	// DefaultMaxInlineBytes is the inline budget: the most a rendered
	// api_invoke_endpoint tool result may hold. It is a model-context
	// budget, sized so a response that fits is one an agent can read
	// (issue #1587). A result past it has its body cut, is flagged with
	// body_truncated, and is steered to api_export, which streams the
	// whole response into an asset without a context cost.
	//
	// The value is set from what a client accepts. Issue #1606 measured
	// a 64,213-character tool result refused and spilled to a file, so
	// the ceiling is under 64 KiB; 32 KiB leaves room under it for a
	// client stricter than the one measured. An operator whose client
	// takes more raises max_inline_bytes on the connection.
	//
	// The budget is applied to the rendered result rather than to the
	// bytes read, because the two differ by more than a constant: a
	// 26,809-byte JSON response measured on that same issue rendered as
	// a 64,238-character result, so a read-side budget of any size lets
	// results past the ceiling through unflagged. Fitting drops the
	// indentation before it drops any content, so a response that fits
	// compactly is still returned whole.
	DefaultMaxInlineBytes = int64(32 * 1024)
)

// The credential vocabulary an operator configures on an api connection.
// The values are defined by internal/upstreamauth, which owns the
// outbound authentication policy for every HTTP-based connection kind;
// they are aliased here because they are part of this toolkit's public
// API and of what an operator types into auth_mode.
const (
	AuthModeNone                    = upstreamauth.AuthModeNone
	AuthModeBearer                  = upstreamauth.AuthModeBearer
	AuthModeAPIKey                  = upstreamauth.AuthModeAPIKey
	AuthModeBasic                   = upstreamauth.AuthModeBasic
	AuthModeOAuth                   = upstreamauth.AuthModeOAuth
	AuthModeOAuth2ClientCredentials = upstreamauth.AuthModeOAuth2ClientCredentials
	AuthModeOAuth2AuthorizationCode = upstreamauth.AuthModeOAuth2AuthorizationCode
	AuthModeSignedJWT               = upstreamauth.AuthModeSignedJWT
	AuthModeMTLS                    = upstreamauth.AuthModeMTLS

	CredentialPlacementHeader = upstreamauth.CredentialPlacementHeader
	CredentialPlacementQuery  = upstreamauth.CredentialPlacementQuery

	DefaultAPIKeyHeader = upstreamauth.DefaultAPIKeyHeader
)

// cfgKey* constants name the keys used to read a Config from a
// map[string]any (the form connections take in the platform's generic
// connection_instances store).
const (
	cfgKeyBaseURL        = "base_url"
	cfgKeyTrustLevel     = "trust_level"
	cfgKeyMaxInlineBytes = "max_inline_bytes"

	// The credential, timeout, response-cap, static-header and TLS
	// keys are read by internal/upstreamauth from the same config map:
	// auth_mode, credential, api_key_header, api_key_param,
	// api_key_placement, username, password, connect_timeout,
	// call_timeout, max_response_bytes, static_headers,
	// mtls_client_cert_pem, mtls_client_key_pem, tls_ca_bundle_pem and
	// identity_passthrough. They are not re-declared here so the two
	// packages cannot come to disagree about a key's spelling.

	// cfgKeyCatalogID names the api_catalogs row that supplies this
	// connection's OpenAPI specs. Empty = connection has no spec
	// surface (api_discover answers with a note and no operations,
	// and cannot resolve an operation_id). Specs live in the
	// globally-owned catalog, not in the connection — multiple
	// connections to the same vendor API share one catalog instead
	// of duplicating the documentation.
	cfgKeyCatalogID = "catalog_id"

	// cfgKeyDescription is an optional human-readable description of the
	// connection, surfaced via ListConnections (and thus the admin UI and
	// the list_connections MCP tool). When unset, ListConnections falls
	// back to the base URL so existing connections keep their current
	// subtitle. Set by the built-in platform-admin self-connection to
	// explain what the connection is for.
	cfgKeyDescription = "description"

	// cfgKeyHandler selects how the connection's operations are
	// resolved. Empty (the default) proxies to BaseURL; HandlerInternal
	// dispatches to the in-process handler wired via SetInternalHandler
	// (issue #1005, the built-in util connection).
	cfgKeyHandler = "handler"
)

// HandlerInternal marks a connection whose operations are resolved by
// an in-process handler instead of an outbound proxy. The connection
// still carries a catalog (so api_discover works unchanged), but invoke/export requests are served by the
// http.Handler wired via Toolkit.SetInternalHandler rather than dialed
// to an upstream. Used by the built-in "util" connection (issue #1005).
const HandlerInternal = "internal"

// internalBaseURL is the synthetic base URL assigned to
// handler=internal connections so the shared URL-join and validation
// path (buildURL, validatePath) applies unchanged. The .invalid TLD is
// reserved (RFC 2606) and the internal round tripper never dials, so
// the host can never be reached even if misrouted.
const internalBaseURL = "http://internal.invalid"

// Config holds api-gateway toolkit configuration for a single upstream
// HTTP API connection.
type Config struct {
	// BaseURL is the upstream API root (e.g. "https://api.example.com").
	// Required. Trailing slash is stripped at parse time.
	BaseURL string
	// Description is an optional human-readable description of the
	// connection. Empty for ordinary connections (ListConnections falls
	// back to BaseURL); set by the built-in platform-admin
	// self-connection to explain its purpose in the admin UI.
	Description string
	// AuthMode is "none", "bearer", or "api_key" in v1. OAuth modes
	// land with #368.
	AuthMode string
	// Credential is the bearer token or API key. Ignored when AuthMode
	// is "none". Encrypted at rest via the platform's FieldEncryptor.
	Credential string
	// CredentialPlacement is "header" (default) or "query" — only consulted
	// when AuthMode is "api_key".
	CredentialPlacement string
	// APIKeyHeader is the header name to set when CredentialPlacement is
	// "header". Defaults to "X-API-Key".
	APIKeyHeader string
	// APIKeyParam is the query parameter name when CredentialPlacement is
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
	// ConnectionName is the audit-visible connection identifier and
	// the value passed in the tool's `connection` argument. Always
	// populated from the toolkit instance name by ParseMultiConfig /
	// addParsedConnection: there is no operator-facing override,
	// because instance name and ConnectionName were always 1:1 and
	// two names for one concept confused admins.
	ConnectionName string
	// ConnectTimeout caps the dial step on each invocation.
	ConnectTimeout time.Duration
	// CallTimeout caps the total per-invocation time.
	CallTimeout time.Duration
	// TrustLevel is "untrusted" (default) or "trusted".
	TrustLevel string
	// MaxResponseBytes is the upstream read cap: the most the gateway
	// reads of any one response. Defaults to DefaultMaxResponseBytes.
	MaxResponseBytes int64
	// MaxInlineBytes is the inline budget: the most of a response
	// returned through a tool result. Defaults to DefaultMaxInlineBytes;
	// the read cap bounds it, so a connection whose MaxResponseBytes is
	// lower returns at most that.
	MaxInlineBytes int64
	// CatalogID names the api_catalogs row whose component specs
	// describe this connection's upstream API. Empty = no spec
	// surface. The catalog is global and may back many connections;
	// editing it propagates to all of them via Toolkit.ReloadConnection.
	CatalogID string
	// OAuth2 carries the OAuth 2.1 parameters used when AuthMode
	// is oauth2_client_credentials. Empty for non-OAuth modes.
	OAuth2 OAuth2Config
	// SignedJWT carries the assertion parameters used when AuthMode is
	// AuthModeSignedJWT, where the gateway mints a short-lived JWT per call
	// from an identifier and a signing key issued out of band, and when
	// the OAuth grant is jwt_bearer, where that assertion is exchanged
	// at the token endpoint for an access token. Aliased
	// straight from the shared seam rather than mirrored, because
	// nothing in it is this toolkit's to define.
	SignedJWT SignedJWTConfig
	// StaticHeaders are operator-configured headers attached to every
	// outbound request, in addition to whatever AuthMode contributes.
	// Required for upstreams that demand a non-Authorization header on
	// top of the OAuth bearer (Google's x-goog-user-project,
	// vendor subscription keys, etc.). Operator-supplied; the model
	// never sets or overrides these. Values are encrypted at rest.
	StaticHeaders map[string]string
	// MTLSClientCertPEM is the PEM-encoded X.509 client certificate
	// chain (leaf first) the gateway presents during the TLS
	// handshake. Public material, stored in plain text. Required
	// alongside MTLSClientKeyPEM and optional otherwise; the toolkit
	// refuses ambiguous configs (one set, the other empty).
	MTLSClientCertPEM string
	// MTLSClientKeyPEM is the PEM-encoded private key matching
	// MTLSClientCertPEM. Encrypted at rest via the platform's
	// FieldEncryptor; the "mtls_client_key_pem" cfg key is in the
	// sensitive-keys list in pkg/platform/fieldcrypt.go. Validation
	// runs the cert + key through tls.X509KeyPair so a key that does
	// not match the cert is rejected at write time, not on first
	// outbound call.
	MTLSClientKeyPEM string
	// TLSCABundlePEM is an optional PEM bundle of root CA
	// certificates added to the TLS trust store for outbound
	// requests on this connection. Appended to the system root
	// pool, not substituted: public CAs remain trusted. Required
	// when the upstream's TLS certificate is signed by a private
	// CA (cluster-internal CA, mesh CA, corporate root) that the
	// host's default cert store does not carry.
	TLSCABundlePEM string
	// Handler selects the resolution mode: "" proxies to BaseURL,
	// HandlerInternal dispatches to the toolkit's in-process handler.
	// When internal, BaseURL is optional (a synthetic, never-dialed
	// placeholder is filled in) and AuthMode must be "none" — there is
	// no upstream to authenticate against.
	Handler string
	// IdentityPassthrough forwards the acting caller's inbound bearer
	// token (the one that authenticated the MCP session, read from the
	// request context) as the outbound Authorization header, instead of
	// applying this connection's shared credential. It exists for the
	// built-in platform-admin self-connection: a loopback call to the
	// platform's own admin API must be authenticated, authorized, and
	// audited as the real admin who made the MCP call, not as a single
	// shared connection identity. When set, AuthMode should be "none"
	// (the shared-credential Authenticator is skipped) and an empty
	// inbound token is a hard error rather than an anonymous call.
	IdentityPassthrough bool
	// RequiredPathPrefix is the path every raw method+path call on the
	// connection must start with; a path outside it is refused before it
	// is sent, naming the prefixed path and the operation_id the catalog
	// lists for it. Empty = no prefix is required. It exists for an
	// upstream whose base URL is a host root that also serves routes the
	// connection does not describe, so a missing prefix reaches the wrong
	// handler instead of failing: the built-in platform-admin connection
	// sets "/api/v1" (#1707).
	RequiredPathPrefix string
}

// OAuth2Config describes the OAuth 2.1 grant parameters. The platform
// obtains an access token at TokenURL and applies it as
// "Authorization: Bearer <token>" on outbound calls: by exchanging
// ClientID + ClientSecret (client_credentials), by refreshing the token
// an administrator's one-time browser sign-in persisted
// (authorization_code), or by exchanging an assertion signed with the
// connection's SignedJWT key (jwt_bearer).
type OAuth2Config struct {
	// Grant is the OAuth flow, populated by ParseConfig from the
	// canonical oauth_grant (or derived from a legacy auth_mode). One
	// of connoauth.GrantClientCredentials,
	// connoauth.GrantAuthorizationCode or connoauth.GrantJWTBearer. The
	// authenticator and validation dispatch on this rather than on the
	// auth_mode string.
	Grant string
	// TokenURL is the upstream's token endpoint. Required.
	TokenURL string
	// ClientID is the platform's registered client id. Required, except
	// for jwt_bearer, where the assertion identifies the client.
	ClientID string
	// ClientSecret is the platform's registered client secret.
	// Required, except for jwt_bearer. Encrypted at rest via the platform's
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

// EndpointAuthStyle values, owned by internal/upstreamauth and aliased
// here because they are part of this toolkit's public API.
const (
	OAuth2AuthStyleHeader = upstreamauth.OAuth2AuthStyleHeader
	OAuth2AuthStyleParams = upstreamauth.OAuth2AuthStyleParams
)

// SignedJWTConfig describes the assertion the gateway mints when
// AuthMode is AuthModeSignedJWT. Defined by internal/upstreamauth,
// which owns the mode for every HTTP-based connection kind; aliased
// here because it is part of this toolkit's public API.
type SignedJWTConfig = upstreamauth.SignedJWTConfig

// The signing algorithms auth_mode=signed_jwt supports, and the
// defaults an unset lifetime and skew take. Aliased from the shared
// seam for the same reason as the mode names above.
const (
	SignedJWTAlgHS256 = upstreamauth.SignedJWTAlgHS256
	SignedJWTAlgRS256 = upstreamauth.SignedJWTAlgRS256
	SignedJWTAlgES256 = upstreamauth.SignedJWTAlgES256

	DefaultSignedJWTTokenLifetime = upstreamauth.DefaultSignedJWTTokenLifetime
	DefaultSignedJWTIssuedAtSkew  = upstreamauth.DefaultSignedJWTIssuedAtSkew
)

// MultiConfig holds parsed per-connection configs plus the aggregate
// toolkit's default connection name.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig validates and returns the parsed config for every
// instance. Per-instance parse errors are logged and the bad instance is
// skipped so one misconfigured connection cannot block startup. HTTP
// connectivity failures are handled at invocation time, not here.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) (MultiConfig, error) {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := ParseConfig(r)
		if err != nil {
			slog.Warn("skipping invalid connection instance",
				"kind", Kind, "instance", name, "error", err)
			continue
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
// defaults. The credential, timeout, response-cap, static-header and
// TLS keys are read by internal/upstreamauth from the same map; the
// keys below are this toolkit's own. The returned Config is fully
// validated.
func ParseConfig(cfg map[string]any) (Config, error) {
	up, err := upstreamauth.Parse(Kind, upstreamErrPrefix, cfgmap.String(cfg, cfgKeyBaseURL), cfg)
	if err != nil {
		//nolint:wrapcheck // the seam builds its messages with this toolkit's prefix; wrapping would state it twice
		return Config{}, err
	}
	c := configFromUpstream(up)
	c.BaseURL = trimTrailingSlash(cfgmap.String(cfg, cfgKeyBaseURL))
	c.TrustLevel = cfgmap.StringDefault(cfg, cfgKeyTrustLevel, TrustLevelUntrusted)
	c.MaxInlineBytes = cfgmap.Int64(cfg, cfgKeyMaxInlineBytes, DefaultMaxInlineBytes)
	c.CatalogID = cfgmap.String(cfg, cfgKeyCatalogID)
	c.Description = cfgmap.String(cfg, cfgKeyDescription)
	c.Handler = cfgmap.String(cfg, cfgKeyHandler)
	c.RequiredPathPrefix = trimTrailingSlash(cfgmap.String(cfg, cfgKeyRequiredPathPrefix))
	if c.Handler == HandlerInternal && c.BaseURL == "" {
		c.BaseURL = internalBaseURL
	}

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate returns an error if the configuration is missing required
// fields or contains invalid values. The auth, timeout, static-header,
// passthrough and TLS rules belong to internal/upstreamauth; they are
// called individually rather than through its Validate so this
// toolkit's own checks stay interleaved in the order an operator has
// always seen them.
func (c Config) Validate() error {
	up := c.upstream()
	if c.BaseURL == "" {
		return errors.New("apigateway: base_url is required")
	}
	if err := up.ValidateAuth(); err != nil {
		//nolint:wrapcheck // as above: already an operator-facing "apigateway: ..." message
		return err
	}
	switch c.TrustLevel {
	case TrustLevelUntrusted, TrustLevelTrusted:
	default:
		return fmt.Errorf("apigateway: invalid trust_level %q (want untrusted or trusted)", c.TrustLevel)
	}
	if err := up.ValidateTransport(); err != nil {
		//nolint:wrapcheck // as above
		return err
	}
	if c.MaxInlineBytes <= 0 {
		return errors.New("apigateway: max_inline_bytes must be positive")
	}
	return firstConfigError(
		up.ValidateStaticHeaders,
		up.ValidateIdentityPassthrough,
		c.validateHandler,
		up.ValidateTLSMaterial,
		c.validateRequiredPathPrefix,
	)
}

// firstConfigError returns the first non-nil error from the checks, in
// order. Collapsing the sequential sub-validators into one loop keeps
// Config.Validate under the cyclomatic-complexity ceiling as new
// per-field validators are added.
func firstConfigError(checks ...func() error) error {
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// validateHandler enforces the handler=internal invariants: no shared
// credential (there is no upstream to authenticate against, and a
// configured auth_mode would imply one) and no identity passthrough
// (the in-process handler must never see the caller's bearer token —
// fetch_url forwarding it to an arbitrary destination would be a
// credential leak).
func (c Config) validateHandler() error {
	switch c.Handler {
	case "", HandlerInternal:
	default:
		return fmt.Errorf("apigateway: invalid handler %q (want %q or empty)", c.Handler, HandlerInternal)
	}
	if c.Handler != HandlerInternal {
		return nil
	}
	if c.AuthMode != AuthModeNone {
		return fmt.Errorf("apigateway: handler=internal requires auth_mode=none, got %q", c.AuthMode)
	}
	if c.IdentityPassthrough {
		return errors.New("apigateway: handler=internal is incompatible with identity_passthrough")
	}
	return nil
}

// IsOAuthAuthorizationCode reports whether the connection uses the
// OAuth authorization_code grant (canonical AuthModeOAuth plus that
// grant). The admin redirect handler and the kind handler gate the
// one-time browser flow on this, so they do not depend on the raw
// auth_mode string shape.
func (c Config) IsOAuthAuthorizationCode() bool {
	return c.upstream().IsOAuthAuthorizationCode()
}

func trimTrailingSlash(s string) string {
	for s != "" && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
