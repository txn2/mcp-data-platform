// Package upstreamauth holds the outbound authentication and transport
// policy shared by the platform's HTTP-based connection kinds.
//
// A connection kind that reaches an upstream over HTTP has to answer the
// same questions whatever it carries on the wire: which credential goes
// on the request, which headers the operator pins and the model may not
// touch, how long a dial and a call may take, how much of a response may
// be read, and which TLS material the handshake presents. Those answers
// are the same for an OpenAPI-described REST API and for a GraphQL
// endpoint, and keeping one copy of them is what stops a fix to, say,
// the token-fetch error scrubber from landing in one kind and not the
// other.
//
// The package deliberately knows nothing about tools, catalogs,
// operations or documents. It takes the slice of a connection's
// configuration that concerns credentials and transport, and it produces
// an Authenticator and an *http.Client. Each kind keeps its own exported
// Config with its own keys and maps the overlapping fields onto this
// one, so this package never appears in a kind's public API.
//
// Error text is owned by the caller. Config.ErrPrefix names the kind in
// every message this package produces ("apigateway: credential is
// required ..."), because an operator reading a refused connection save
// should see the surface they configured, not the seam behind it.
package upstreamauth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

const (
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

	// AuthModeOAuth is the canonical OAuth auth_mode shared across
	// every toolkit kind (see connoauth.AuthModeOAuth). The specific
	// flow is carried separately in OAuth2Config.Grant. Parse
	// normalizes the legacy api-only auth_mode values below to this
	// form, so a parsed Config always reports AuthModeOAuth for an
	// OAuth connection.
	AuthModeOAuth = connoauth.AuthModeOAuth

	// AuthModeOAuth2ClientCredentials is the legacy api-only auth_mode
	// that encoded the client_credentials grant in the mode string.
	// Retained so raw config authored before the schema unified (and
	// hand-built test Configs) still parse; Parse normalizes it to
	// AuthModeOAuth + Grant=client_credentials.
	//
	// client_credentials acquires a bearer token — server-to-server,
	// no human in the loop. The platform exchanges the configured
	// client_id + client_secret for a token at OAuth.TokenURL and
	// applies it as "Authorization: Bearer <token>" on outbound
	// calls. Tokens are cached + refreshed automatically by the
	// underlying golang.org/x/oauth2 library; no DB state is
	// required because every restart can re-acquire from credentials.
	AuthModeOAuth2ClientCredentials = "oauth2_client_credentials" // #nosec G101 -- mode name, not a credential

	// AuthModeOAuth2AuthorizationCode runs the user-driven OAuth 2.1
	// authorization-code grant: an admin completes a one-time browser
	// flow at connection setup; the resulting refresh token is
	// persisted (encrypted) so subsequent platform restarts and
	// background workloads keep working without further interaction.
	// Tokens are refreshed automatically before expiry. Requires the
	// platform's database (refresh-token state survives restarts).
	AuthModeOAuth2AuthorizationCode = "oauth2_authorization_code" // #nosec G101 -- mode name, not a credential

	// AuthModeMTLS authenticates by presenting an X.509 client
	// certificate during the TLS handshake per RFC 5246 / 8446. No
	// Authorization header is sent: the cert IS the credential.
	// Used by upstreams that map the cert's subject DN (or a SAN) to
	// a user identity in their authorizer, including service-mesh
	// peers, PKI-fronted internal APIs, healthcare integration
	// engines, financial messaging endpoints, and FedRAMP / DoD-
	// boundary services. Requires both mtls_client_cert_pem and
	// mtls_client_key_pem on the connection config; the mTLS material
	// can also be present alongside other auth modes (bearer + mTLS,
	// etc.), but auth_mode=mtls is the explicit "no header
	// credential" signal.
	AuthModeMTLS = "mtls"

	// CredentialPlacementHeader (default) sends the credential as an HTTP
	// header named by APIKeyHeader.
	CredentialPlacementHeader = "header"
	// CredentialPlacementQuery sends the credential as a URL query parameter
	// named by APIKeyParam.
	CredentialPlacementQuery = "query"

	// DefaultAPIKeyHeader is the conventional API-key header name when
	// the connection does not specify one.
	DefaultAPIKeyHeader = "X-API-Key" // #nosec G101 -- header name, not a credential

	// DefaultConnectTimeout caps the time spent establishing the
	// outbound connection (TCP + TLS handshake) on each invocation.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultCallTimeout caps the total per-call time including
	// upstream processing and response read.
	DefaultCallTimeout = 60 * time.Second

	// DefaultMaxResponseBytes is the upstream read cap: the most a kind
	// reads of any one response. It bounds transfer and buffering, not
	// what reaches the model; a kind's own inline budget does that.
	DefaultMaxResponseBytes = int64(10 * 1024 * 1024)
)

// EndpointAuthStyle values.
const (
	OAuth2AuthStyleHeader = "header"
	OAuth2AuthStyleParams = "params"
)

// cfgKey* constants name the keys this package reads from the
// map[string]any form a connection takes in the platform's
// connection_instances store. A kind that embeds this policy does not
// re-declare them: it hands the whole map to Parse and keeps only its
// own keys.
const (
	cfgKeyAuthMode        = "auth_mode"
	cfgKeyCredential      = "credential"     // #nosec G101 -- map key, not a secret
	cfgKeyAPIKeyHeader    = "api_key_header" // #nosec G101 -- map key, not a credential
	cfgKeyAPIKeyParam     = "api_key_param"  // #nosec G101 -- map key, not a credential
	cfgKeyAPIKeyPlacement = "api_key_placement"
	cfgKeyUsername        = "username"
	cfgKeyPassword        = "password" // #nosec G101 -- map key, not a credential; encryption handled by platform FieldEncryptor sensitive-keys list
	cfgKeyConnectTimeout  = "connect_timeout"
	cfgKeyCallTimeout     = "call_timeout"

	cfgKeyMaxResponseBytes = "max_response_bytes"

	// cfgKeyStaticHeaders holds operator-configured headers appended to
	// every outbound request. Required for upstreams that demand BOTH
	// an Authorization bearer AND a separate subscription/key header
	// (Google Cloud's x-goog-user-project quota-billing header, vendor
	// subscription keys, a GraphQL server's folder-routing header).
	// Stored as a map[string]any whose values are encrypted at rest by
	// platform.FieldEncryptor (see CfgKeyStaticHeaders in
	// pkg/platform/fieldcrypt.go).
	cfgKeyStaticHeaders = "static_headers"

	// OAuth config keys are owned by pkg/connoauth (the canonical
	// oauth_* vocabulary plus the legacy oauth2_* fallback). This
	// package delegates OAuth parsing to connoauth.ParseConfig rather
	// than declaring its own key constants. The keys remain top-level
	// (not nested) so the platform's FieldEncryptor, which walks only
	// the top level of the config map, encrypts oauth_client_secret at
	// rest without changes to the encryptor.

	// mTLS material config keys. Cert and CA bundle are public
	// material (plain text at rest); the private key is in the
	// platform's sensitive-keys list (see pkg/platform/fieldcrypt.go)
	// and encrypted via FieldEncryptor like every other secret on a
	// connection.
	cfgKeyMTLSClientCertPEM = "mtls_client_cert_pem" // #nosec G101 -- map key, not a credential
	cfgKeyMTLSClientKeyPEM  = "mtls_client_key_pem"  // #nosec G101 -- map key, not a credential
	cfgKeyTLSCABundlePEM    = "tls_ca_bundle_pem"

	cfgKeyIdentityPassthrough = "identity_passthrough"
)

// Config is the authentication and transport slice of a connection's
// configuration. A kind builds one from its own Config and hands it to
// NewAuthenticator and NewHTTPClient.
type Config struct {
	// Kind is the connoauth connection kind ("api", "graphql"). It
	// keys the persisted OAuth token row and identifies the connection
	// in connoauth's deduplicated configuration warnings, so two kinds
	// with a same-named connection do not share a token.
	Kind string
	// ErrPrefix names the calling kind in every error this package
	// produces. Empty falls back to the package name. Set it: an
	// operator whose connection save is refused should read
	// "apigateway: credential is required ...", not the name of an
	// internal seam they cannot see in any configuration file.
	ErrPrefix string
	// ConnectionName is the audit-visible connection identifier. Used
	// as the OAuth token-row key for the authorization_code grant.
	// Kinds populate it from the toolkit instance name after Parse.
	ConnectionName string

	// AuthMode selects the credential scheme: one of the AuthMode*
	// constants.
	AuthMode string
	// Credential is the bearer token or API key. Ignored when AuthMode
	// is "none". Encrypted at rest via the platform's FieldEncryptor.
	Credential string
	// CredentialPlacement is "header" (default) or "query" — only consulted
	// when AuthMode is "api_key".
	CredentialPlacement string
	// APIKeyHeader is the header name to set when CredentialPlacement is
	// "header". Defaults to DefaultAPIKeyHeader.
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
	// platform's FieldEncryptor.
	Password string
	// OAuth2 carries the OAuth 2.1 parameters used when AuthMode is
	// AuthModeOAuth. Empty for non-OAuth modes.
	OAuth2 OAuth2Config
	// SignedJWT carries the assertion parameters used when AuthMode is
	// AuthModeSignedJWT, or AuthModeOAuth with the jwt_bearer grant.
	// Empty otherwise.
	SignedJWT SignedJWTConfig

	// ConnectTimeout caps the dial step (TCP + TLS handshake) on each
	// invocation.
	ConnectTimeout time.Duration
	// CallTimeout caps the total per-invocation time.
	CallTimeout time.Duration
	// MaxResponseBytes is the upstream read cap: the most a kind reads
	// of any one response. Defaults to DefaultMaxResponseBytes.
	MaxResponseBytes int64
	// StaticHeaders are operator-configured headers attached to every
	// outbound request, in addition to whatever AuthMode contributes.
	// Operator-supplied; the model never sets or overrides these.
	// Values are encrypted at rest.
	StaticHeaders map[string]string

	// MTLSClientCertPEM is the PEM-encoded X.509 client certificate
	// chain (leaf first) presented during the TLS handshake. Public
	// material, stored in plain text. Required alongside
	// MTLSClientKeyPEM and optional otherwise; an ambiguous config
	// (one set, the other empty) is refused.
	MTLSClientCertPEM string
	// MTLSClientKeyPEM is the PEM-encoded private key matching
	// MTLSClientCertPEM. Encrypted at rest via the platform's
	// FieldEncryptor. Validation runs the cert + key through
	// tls.X509KeyPair so a key that does not match the cert is
	// rejected at write time, not on first outbound call.
	MTLSClientKeyPEM string
	// TLSCABundlePEM is an optional PEM bundle of root CA
	// certificates added to the TLS trust store for outbound
	// requests on this connection. Appended to the system root
	// pool, not substituted: public CAs remain trusted. Required
	// when the upstream's TLS certificate is signed by a private
	// CA (cluster-internal CA, mesh CA, corporate root) that the
	// host's default cert store does not carry.
	TLSCABundlePEM string

	// IdentityPassthrough forwards the acting caller's inbound bearer
	// token as the outbound Authorization header, instead of applying
	// this connection's shared credential. When set, AuthMode must be
	// "none": the shared-credential Authenticator is skipped, and two
	// sources for one header would be ambiguous. Reading the caller's
	// token off the request context is the kind's job, not this
	// package's — only the invariant lives here.
	IdentityPassthrough bool
}

// OAuth2Config describes the OAuth 2.1 grant parameters. For
// client_credentials the platform exchanges ClientID + ClientSecret at
// TokenURL for an access token (cached + refreshed by the
// golang.org/x/oauth2 library). For authorization_code an admin
// completes a one-time browser flow and the persisted refresh token is
// read through connoauth.Source on every call.
type OAuth2Config struct {
	// Grant is the OAuth flow, populated by Parse from the canonical
	// oauth_grant (or derived from a legacy auth_mode). One of
	// connoauth.GrantClientCredentials, connoauth.GrantAuthorizationCode
	// or connoauth.GrantJWTBearer. The authenticator and
	// validation dispatch on this rather than on the auth_mode string.
	Grant string
	// TokenURL is the upstream's token endpoint. Required.
	TokenURL string
	// ClientID is the platform's registered client id. Required.
	ClientID string
	// ClientSecret is the platform's registered client secret.
	// Required. Encrypted at rest via the platform's FieldEncryptor.
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

// Parse reads the authentication and transport keys out of a
// connection's config map and applies the defaults, leaving the kind to
// read its own keys from the same map. The returned Config is NOT
// validated: a kind interleaves these checks with its own so the first
// error an operator sees is the same one it was before this policy was
// shared (see Validate and the individual validators).
//
// kind is the connoauth connection kind, errPrefix names the kind in
// error text, and endpointURL identifies the connection in connoauth's
// deduplicated warnings about legacy keys and cleartext endpoints.
func Parse(kind, errPrefix, endpointURL string, cfg map[string]any) (Config, error) {
	c := Config{
		Kind:                kind,
		ErrPrefix:           errPrefix,
		AuthMode:            AuthModeNone,
		CredentialPlacement: CredentialPlacementHeader,
		APIKeyHeader:        DefaultAPIKeyHeader,
		ConnectTimeout:      DefaultConnectTimeout,
		CallTimeout:         DefaultCallTimeout,
		MaxResponseBytes:    DefaultMaxResponseBytes,
	}
	c.AuthMode = cfgmap.StringDefault(cfg, cfgKeyAuthMode, c.AuthMode)
	c.Credential = cfgmap.String(cfg, cfgKeyCredential)
	c.CredentialPlacement = cfgmap.StringDefault(cfg, cfgKeyAPIKeyPlacement, c.CredentialPlacement)
	c.APIKeyHeader = cfgmap.StringDefault(cfg, cfgKeyAPIKeyHeader, c.APIKeyHeader)
	c.APIKeyParam = cfgmap.String(cfg, cfgKeyAPIKeyParam)
	c.Username = cfgmap.String(cfg, cfgKeyUsername)
	c.Password = cfgmap.String(cfg, cfgKeyPassword)
	c.ConnectTimeout = cfgmap.Duration(cfg, cfgKeyConnectTimeout, c.ConnectTimeout)
	c.CallTimeout = cfgmap.Duration(cfg, cfgKeyCallTimeout, c.CallTimeout)
	c.MaxResponseBytes = cfgmap.Int64(cfg, cfgKeyMaxResponseBytes, c.MaxResponseBytes)
	if isOAuthAuthMode(c.AuthMode) {
		// Delegate OAuth parsing to the shared connoauth.ParseConfig
		// (canonical oauth_* keys, legacy oauth2_* fallback, grant
		// derivation) and normalize the auth_mode to the canonical
		// AuthModeOAuth so the authenticator and validation dispatch on
		// the grant rather than on three divergent mode strings.
		parsed, err := connoauth.ParseConfig(kind, endpointURL, cfg)
		if err != nil {
			return Config{}, errf(errPrefix, "%w", err)
		}
		c.AuthMode = AuthModeOAuth
		c.OAuth2 = oauth2ConfigFromConnoauth(parsed)
	}
	switch {
	case c.AuthMode == AuthModeSignedJWT:
		c.SignedJWT = parseSignedJWT(SignedJWTAlgHS256, endpointURL, cfg)
	case c.AuthMode == AuthModeOAuth && c.OAuth2.Grant == connoauth.GrantJWTBearer:
		c.SignedJWT = parseSignedJWT(SignedJWTAlgRS256, c.OAuth2.TokenURL, cfg)
	}
	c.StaticHeaders = cfgmap.StringMap(cfg, cfgKeyStaticHeaders)
	c.MTLSClientCertPEM = cfgmap.String(cfg, cfgKeyMTLSClientCertPEM)
	c.MTLSClientKeyPEM = cfgmap.String(cfg, cfgKeyMTLSClientKeyPEM)
	c.TLSCABundlePEM = cfgmap.String(cfg, cfgKeyTLSCABundlePEM)
	c.IdentityPassthrough = cfgmap.Bool(cfg, cfgKeyIdentityPassthrough)
	return c, nil
}

// Validate runs every check this package owns, in the order a kind with
// no additional keys would want them. A kind that interleaves its own
// checks calls the individual validators instead.
func (c Config) Validate() error {
	if err := c.ValidateAuth(); err != nil {
		return err
	}
	if err := c.ValidateTransport(); err != nil {
		return err
	}
	if err := c.ValidateStaticHeaders(); err != nil {
		return err
	}
	if err := c.ValidateIdentityPassthrough(); err != nil {
		return err
	}
	return c.ValidateTLSMaterial()
}

// ValidateTransport enforces that the timeouts and the read cap are
// positive. Zero would mean "no timeout" to net/http and "read nothing"
// to the response reader, neither of which any operator intends.
func (c Config) ValidateTransport() error {
	if c.ConnectTimeout <= 0 {
		return c.err("connect_timeout must be positive")
	}
	if c.CallTimeout <= 0 {
		return c.err("call_timeout must be positive")
	}
	if c.MaxResponseBytes <= 0 {
		return c.err("max_response_bytes must be positive")
	}
	return nil
}

// ValidateAuth enforces the per-mode credential requirements.
func (c Config) ValidateAuth() error {
	switch c.AuthMode {
	case AuthModeNone:
		return nil
	case AuthModeBearer:
		if c.Credential == "" {
			return c.err("credential is required when auth_mode is \"bearer\"")
		}
		return nil
	case AuthModeAPIKey:
		return c.validateAPIKeyAuth()
	case AuthModeBasic:
		return c.validateBasicAuth()
	case AuthModeOAuth, AuthModeOAuth2ClientCredentials, AuthModeOAuth2AuthorizationCode:
		return c.validateOAuthAuth()
	case AuthModeSignedJWT:
		return c.validateSignedJWTAuth()
	case AuthModeMTLS:
		// The mTLS material is validated centrally by
		// ValidateTLSMaterial so the same rules apply whether mTLS is
		// the credential or layered on top of
		// bearer/api_key/basic/oauth. The mode-specific requirement
		// (cert + key MUST be present) is enforced there via
		// Config.AuthMode inspection.
		return nil
	default:
		return c.errf("invalid auth_mode %q (want none, bearer, api_key, basic, signed_jwt, oauth, or mtls; an oauth connection carries its flow in %s)",
			c.AuthMode, connoauth.ConfigKeyGrant)
	}
}

// validateOAuthAuth dispatches to the grant-specific rules. A parsed
// Config always reports the canonical mode and carries the grant in
// OAuth2.Grant, so the grant decides. A hand-built Config that bypassed
// Parse still encodes the grant in the mode string, and there the mode
// decides — including when it contradicts the grant field, which is how
// the mode-per-grant validation behaved before the two collapsed.
func (c Config) validateOAuthAuth() error {
	switch c.AuthMode {
	case AuthModeOAuth2ClientCredentials:
		return c.validateOAuth2()
	case AuthModeOAuth2AuthorizationCode:
		return c.validateOAuth2AuthCode()
	}
	switch c.OAuth2.Grant {
	case connoauth.GrantAuthorizationCode:
		return c.validateOAuth2AuthCode()
	case connoauth.GrantJWTBearer:
		return c.validateOAuth2JWTBearer()
	default:
		return c.validateOAuth2()
	}
}

// validateBasicAuth enforces RFC 7617 + the platform's smuggling
// defenses for the "basic" auth mode. The userid (username) must be
// non-empty and contain no ":" (RFC 7617 §2 forbids it because the
// decoder splits on the first colon). Both fields must be free of
// CR/LF/NUL because neither RFC 7617 nor base64 stops an operator from
// pasting a "username\r\nX-Smuggled: 1" string that would inject
// extra headers after the Authorization line. The password may be
// empty: some legacy APIs accept a bearer token in the userid slot with
// an empty password (the "token:" pattern), so refusing empty here
// would block a real use case.
func (c Config) validateBasicAuth() error {
	if c.Username == "" {
		return c.err("username is required when auth_mode is \"basic\"")
	}
	// Smuggling defenses run before the colon check: a payload like
	// "alice\r\nX-Smuggled: 1" contains both CRLF and ":" and we want
	// the security-relevant error to surface, not the RFC compliance
	// one.
	if strings.ContainsAny(c.Username, "\r\n\x00") {
		return c.err("username contains CR/LF/NUL header smuggling vector")
	}
	if strings.ContainsAny(c.Password, "\r\n\x00") {
		return c.err("password contains CR/LF/NUL header smuggling vector")
	}
	if strings.Contains(c.Username, ":") {
		return c.err("username must not contain \":\" (RFC 7617 §2 forbids it in the userid)")
	}
	return nil
}

// errOAuthFieldRequired is the shape of every missing-OAuth-field
// refusal: the config key, then the mode and grant it is required under.
const errOAuthFieldRequired = "%s is required when %s"

// oauthRequirement names the mode and grant a missing-field refusal is
// speaking about, in the vocabulary the connection is actually stored
// in. A canonical connection reports auth_mode "oauth" and the grant it
// carries; the legacy mode values encoded the grant in the mode, and a
// Config built from one of those reports it that way. Naming the
// client_credentials mode unconditionally sent operators of an
// authorization_code connection looking for a misconfiguration that did
// not exist (#1681).
func (c Config) oauthRequirement() string {
	switch c.AuthMode {
	case AuthModeOAuth2ClientCredentials, AuthModeOAuth2AuthorizationCode:
		return fmt.Sprintf("auth_mode is %q", c.AuthMode)
	default:
		grant := c.OAuth2.Grant
		if grant == "" {
			grant = connoauth.GrantClientCredentials
		}
		return fmt.Sprintf("auth_mode is %q and %s is %q", AuthModeOAuth, connoauth.ConfigKeyGrant, grant)
	}
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
		return c.errf(errOAuthFieldRequired, connoauth.ConfigKeyAuthorizationURL, c.oauthRequirement())
	}
	return nil
}

func (c Config) validateOAuth2() error {
	if c.OAuth2.TokenURL == "" {
		return c.errf(errOAuthFieldRequired, connoauth.ConfigKeyTokenURL, c.oauthRequirement())
	}
	if c.OAuth2.ClientID == "" {
		return c.errf(errOAuthFieldRequired, connoauth.ConfigKeyClientID, c.oauthRequirement())
	}
	if c.OAuth2.ClientSecret == "" {
		return c.errf(errOAuthFieldRequired, connoauth.ConfigKeyClientSecret, c.oauthRequirement())
	}
	return c.validateEndpointAuthStyle()
}

// validateEndpointAuthStyle refuses a token-endpoint credential placement
// the authenticators do not know.
func (c Config) validateEndpointAuthStyle() error {
	switch c.OAuth2.EndpointAuthStyle {
	case OAuth2AuthStyleHeader, OAuth2AuthStyleParams:
		return nil
	default:
		return c.errf("invalid %s %q (want %q or %q)",
			connoauth.ConfigKeyEndpointAuthStyle, c.OAuth2.EndpointAuthStyle, OAuth2AuthStyleHeader, OAuth2AuthStyleParams)
	}
}

func (c Config) validateAPIKeyAuth() error {
	if c.Credential == "" {
		return c.err("credential is required when auth_mode is \"api_key\"")
	}
	switch c.CredentialPlacement {
	case CredentialPlacementHeader:
		if c.APIKeyHeader == "" {
			return c.err("api_key_header must not be empty")
		}
	case CredentialPlacementQuery:
		if c.APIKeyParam == "" {
			return c.err("api_key_param is required when api_key_placement is \"query\"")
		}
	default:
		return c.errf("invalid api_key_placement %q (want header or query)", c.CredentialPlacement)
	}
	return nil
}

// ValidateIdentityPassthrough enforces that a passthrough connection
// carries no shared credential. Passthrough forwards the caller's inbound
// token as the Authorization header, so a configured auth_mode would
// either be ignored (confusing) or fight for the same header. Requiring
// auth_mode=none keeps the single-credential-source invariant explicit.
func (c Config) ValidateIdentityPassthrough() error {
	if c.IdentityPassthrough && c.AuthMode != AuthModeNone {
		return c.errf("identity_passthrough requires auth_mode=none, got %q", c.AuthMode)
	}
	return nil
}

// IsOAuthAuthorizationCode reports whether the connection uses the
// OAuth authorization_code grant (canonical AuthModeOAuth plus that
// grant). The admin redirect handler and the kind handlers gate the
// one-time browser flow on this, so they do not depend on the raw
// auth_mode string shape.
func (c Config) IsOAuthAuthorizationCode() bool {
	return c.AuthMode == AuthModeOAuth && c.OAuth2.Grant == connoauth.GrantAuthorizationCode
}

// isOAuthAuthMode reports whether mode names an OAuth connection in any
// of the recognized input shapes: the canonical AuthModeOAuth or either
// legacy api-only mode that encoded the grant. Parse uses this to decide
// when to delegate to connoauth.ParseConfig and normalize.
func isOAuthAuthMode(mode string) bool {
	switch mode {
	case AuthModeOAuth, AuthModeOAuth2ClientCredentials, AuthModeOAuth2AuthorizationCode:
		return true
	default:
		return false
	}
}

// oauth2ConfigFromConnoauth projects the shared connoauth.Config onto
// this package's OAuth2Config. The endpoint auth style is mapped back to
// the operator-facing string form the authenticators and validation
// expect.
func oauth2ConfigFromConnoauth(c connoauth.Config) OAuth2Config {
	style := OAuth2AuthStyleHeader
	if c.EndpointAuthStyle == oauth2.AuthStyleInParams {
		style = OAuth2AuthStyleParams
	}
	return OAuth2Config{
		Grant:             c.Grant,
		TokenURL:          c.TokenURL,
		ClientID:          c.ClientID,
		ClientSecret:      c.ClientSecret,
		Scopes:            c.Scopes,
		EndpointAuthStyle: style,
		AuthorizationURL:  c.AuthorizationURL,
		Prompt:            c.Prompt,
	}
}

// ConnOAuthConfig maps this connection's OAuth settings to the unified
// connoauth.Config the Source consumes. The CA bundle travels with it
// so the token-exchange and refresh paths can verify an IdP behind a
// private CA without falling back to system trust.
//
// Exported because the initial authorization-code exchange (the admin
// OAuth kind handler) and the per-call silent refresh (the
// authenticator) must read every field through the same translator: a
// regression here would otherwise drop CABundlePEM, Prompt, or a future
// field from one path but not the other.
func (c Config) ConnOAuthConfig() connoauth.Config {
	authStyle := oauth2.AuthStyleInHeader
	if c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	return connoauth.Config{
		Grant:             c.OAuth2.Grant,
		AuthorizationURL:  c.OAuth2.AuthorizationURL,
		TokenURL:          c.OAuth2.TokenURL,
		ClientID:          c.OAuth2.ClientID,
		ClientSecret:      c.OAuth2.ClientSecret,
		Scopes:            c.OAuth2.Scopes,
		EndpointAuthStyle: authStyle,
		Prompt:            c.OAuth2.Prompt,
		CABundlePEM:       c.TLSCABundlePEM,
	}
}

// prefixOr returns the caller-supplied error prefix, or this package's
// name when a caller left it unset.
func prefixOr(p string) string {
	if p == "" {
		return "upstreamauth"
	}
	return p
}

// err builds an error in the calling kind's voice.
func (c Config) err(msg string) error {
	return errors.New(prefixOr(c.ErrPrefix) + ": " + msg)
}

// errf builds a formatted error in the calling kind's voice.
func (c Config) errf(format string, a ...any) error {
	return errf(c.ErrPrefix, format, a...)
}

// errf builds a formatted error in a kind's voice from the prefix
// alone, for the paths that have one before they have a Config. The
// prefix is joined to the format string rather than passed as an
// argument so the literal a reader (and the string-format linter) sees
// is the message itself.
func errf(prefix, format string, a ...any) error {
	return fmt.Errorf(prefixOr(prefix)+": "+format, a...)
}
