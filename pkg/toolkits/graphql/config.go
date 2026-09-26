// Package graphql provides the platform's GraphQL connection kind: a
// toolkit that reaches a GraphQL endpoint through the platform's auth,
// persona, audit and export pipeline, the way pkg/toolkits/apigateway
// reaches an OpenAPI-described REST API and pkg/toolkits/gateway
// reaches an upstream MCP server.
//
// A GraphQL endpoint is one URL that everything is POSTed to, which is
// why it is a kind of its own rather than a shape of API connection:
// there is no route to authorize, no path to rank, and a failure is an
// errors array inside an HTTP 200. What the kind adds on top of the
// shared upstream transport is a schema it keeps by introspection, an
// index of the operations that schema exposes, validation of the
// document a model wrote before it is sent, and an operation-level
// policy that reduces a document to the fields it selects.
//
// The toolkit registers three tools however many connections it holds:
// graphql_discover finds an operation and renders a document that runs
// it, graphql_query executes one, and graphql_export streams one into
// an asset.
package graphql

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
)

const (
	// Kind is the connection-instance kind discriminator. Operators see
	// this in the admin UI's connection picker.
	Kind = "graphql"

	// upstreamErrPrefix names this toolkit in every message the shared
	// upstream auth and transport policy produces, so an operator whose
	// connection save is refused reads the surface they configured.
	upstreamErrPrefix = "graphql"

	// DefaultConnectTimeout caps the dial step on each call.
	DefaultConnectTimeout = upstreamauth.DefaultConnectTimeout
	// DefaultCallTimeout caps the total per-call time.
	DefaultCallTimeout = upstreamauth.DefaultCallTimeout
	// DefaultMaxResponseBytes is the upstream read cap: the most the
	// toolkit reads of any one response.
	DefaultMaxResponseBytes = upstreamauth.DefaultMaxResponseBytes

	// DefaultMaxQueryDepth caps the selection depth of a document the
	// toolkit will send. A deeply nested document is how a GraphQL
	// endpoint is made to do unbounded work from one small request, and
	// fifteen is deeper than any document graphql_discover renders.
	DefaultMaxQueryDepth = 15

	// DefaultMaxPages caps a paginated walk when the caller names no
	// limit of their own.
	DefaultMaxPages = 10

	// SchemaValidationStrict refuses a document that does not validate
	// against the stored schema. The default: a document naming a field
	// the connection's schema does not have is a caller working from
	// the wrong schema, and sending it spends a call to learn that.
	SchemaValidationStrict = "strict"
	// SchemaValidationWarn sends the document anyway and reports the
	// violations alongside the upstream's answer. For a deployment
	// whose stored schema is behind the endpoint it serves, or which
	// uses a directive the compatibility-floor introspection query does
	// not record.
	SchemaValidationWarn = "warn"
)

// The credential vocabulary an operator configures on a graphql
// connection. The values are defined by internal/upstreamauth, which
// owns the outbound authentication policy for every HTTP-based
// connection kind; they are aliased here because they are part of this
// toolkit's public API and of what an operator types into auth_mode.
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

	OAuth2AuthStyleHeader = upstreamauth.OAuth2AuthStyleHeader
	OAuth2AuthStyleParams = upstreamauth.OAuth2AuthStyleParams
)

// cfgKey* constants name this kind's own keys in the map[string]any a
// connection is stored as. The credential, timeout, response-cap,
// static-header and TLS keys are read by internal/upstreamauth from the
// same map (auth_mode, credential, api_key_header, api_key_param,
// api_key_placement, username, password, connect_timeout, call_timeout,
// max_response_bytes, static_headers, mtls_client_cert_pem,
// mtls_client_key_pem, tls_ca_bundle_pem, identity_passthrough) and are
// not re-declared here, so the two packages cannot come to disagree
// about a key's spelling.
const (
	cfgKeyEndpointURL      = "endpoint_url"
	cfgKeyCatalogID        = "catalog_id"
	cfgKeyDescription      = "description"
	cfgKeySchemaValidation = "schema_validation"
	cfgKeyMaxQueryDepth    = "max_query_depth"
	cfgKeyNamespaceDepth   = "namespace_depth"
	cfgKeyReadOnly         = "read_only"
)

// Config holds the configuration of one GraphQL connection.
type Config struct {
	// EndpointURL is the full URL documents are POSTed to (e.g.
	// "https://datahub.example.com/api/graphql"). Required. Unlike the
	// API gateway's base_url this is the whole address, not a root
	// paths are joined to: a GraphQL endpoint has one address.
	EndpointURL string
	// Description is an optional human-readable description of the
	// connection, surfaced by ListConnections and so by the admin UI
	// and the list_connections tool. Empty falls back to the endpoint.
	Description string
	// ConnectionName is the audit-visible connection identifier and the
	// value passed in a tool's `connection` argument. Populated from
	// the toolkit instance name.
	ConnectionName string

	// AuthMode and the credential fields below carry the shared
	// upstream authentication policy. See internal/upstreamauth.
	AuthMode            string
	Credential          string
	CredentialPlacement string
	APIKeyHeader        string
	APIKeyParam         string
	Username            string
	Password            string
	OAuth2              OAuth2Config
	// SignedJWT carries the assertion parameters used when AuthMode is
	// AuthModeSignedJWT, where the platform mints a short-lived JWT per call
	// from an identifier and a signing key issued out of band, and when
	// the OAuth grant is jwt_bearer, where that assertion is exchanged
	// at the token endpoint for an access token. Aliased
	// straight from the shared seam rather than mirrored, because
	// nothing in it is this kind's to define.
	SignedJWT SignedJWTConfig

	// ConnectTimeout caps the dial step on each call.
	ConnectTimeout time.Duration
	// CallTimeout caps the total per-call time.
	CallTimeout time.Duration
	// MaxResponseBytes is the upstream read cap.
	MaxResponseBytes int64
	// StaticHeaders are operator-configured headers attached to every
	// outbound request, in addition to whatever AuthMode contributes.
	// This is where an upstream's tenant or folder routing goes (Sage
	// X3's x-xtrem-endpoint, a vendor subscription key). The model
	// never sets or overrides these.
	StaticHeaders map[string]string
	// MTLSClientCertPEM is the PEM client certificate chain presented
	// during the TLS handshake.
	MTLSClientCertPEM string
	// MTLSClientKeyPEM is the PEM private key matching the certificate.
	// Encrypted at rest.
	MTLSClientKeyPEM string
	// TLSCABundlePEM is an optional PEM bundle of root CAs added to the
	// trust store for this connection's outbound requests.
	TLSCABundlePEM string
	// IdentityPassthrough forwards the acting caller's inbound bearer
	// token as the outbound Authorization header instead of applying
	// this connection's shared credential.
	IdentityPassthrough bool

	// CatalogID names the API catalog this connection takes its schema
	// from, instead of reading the endpoint (#1745). The catalog holds
	// one spec entry whose format is graphql and whose content is SDL,
	// refreshed from its source the way an OpenAPI document is, and
	// shared by every connection referencing the same catalog. Empty
	// means this connection reads its own endpoint, which is what every
	// connection written before the field did.
	CatalogID string

	// SchemaValidation is SchemaValidationStrict (default) or
	// SchemaValidationWarn.
	SchemaValidation string
	// MaxQueryDepth caps the selection depth of a document the toolkit
	// will send. Defaults to DefaultMaxQueryDepth.
	MaxQueryDepth int
	// NamespaceDepth caps how many segments a dotted operation id may
	// have when the schema is walked into operations. Defaults to
	// gqlschema.DefaultNamespaceDepth.
	NamespaceDepth int
	// ReadOnly refuses every mutation document on this connection, for
	// every persona. It is the connection-level counterpart to a
	// persona's `deny` rule on the MUTATION method: an operator who
	// mounts an ERP for reporting sets it once here rather than in
	// every persona.
	ReadOnly bool
}

// SignedJWTConfig describes the assertion the platform mints when
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

// OAuth2Config describes the OAuth parameters used when AuthMode is
// the canonical oauth mode. Mirrors the API gateway's, because both
// are projections of the same shared seam.
type OAuth2Config struct {
	// Grant is the OAuth flow: client_credentials, authorization_code
	// or jwt_bearer.
	Grant string
	// TokenURL is the upstream's token endpoint.
	TokenURL string
	// ClientID is the platform's registered client id.
	ClientID string
	// ClientSecret is the platform's registered client secret,
	// encrypted at rest.
	ClientSecret string
	// Scopes is an optional list of scopes to request.
	Scopes []string
	// EndpointAuthStyle is "header" (default) or "params".
	EndpointAuthStyle string
	// AuthorizationURL is the upstream's authorization endpoint,
	// required for the authorization_code grant.
	AuthorizationURL string
	// Prompt is an optional OIDC prompt parameter.
	Prompt string
}

// MultiConfig holds parsed per-connection configs plus the aggregate
// toolkit's default connection name.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig validates and returns the parsed config for every
// instance. Per-instance parse errors are logged and the bad instance
// is skipped so one misconfigured connection cannot block startup.
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

// ParseConfig parses a Config from the generic map a connection is
// stored as and applies defaults. The returned Config is fully
// validated.
func ParseConfig(cfg map[string]any) (Config, error) {
	endpoint := trimTrailingSlash(cfgmap.String(cfg, cfgKeyEndpointURL))
	up, err := upstreamauth.Parse(Kind, upstreamErrPrefix, endpoint, cfg)
	if err != nil {
		//nolint:wrapcheck // the seam builds its messages with this toolkit's prefix; wrapping would state it twice
		return Config{}, err
	}
	c := configFromUpstream(up)
	c.EndpointURL = endpoint
	c.Description = cfgmap.String(cfg, cfgKeyDescription)
	c.CatalogID = cfgmap.String(cfg, cfgKeyCatalogID)
	c.SchemaValidation = cfgmap.StringDefault(cfg, cfgKeySchemaValidation, SchemaValidationStrict)
	c.MaxQueryDepth = int(cfgmap.Int64(cfg, cfgKeyMaxQueryDepth, DefaultMaxQueryDepth))
	c.NamespaceDepth = int(cfgmap.Int64(cfg, cfgKeyNamespaceDepth, gqlschema.DefaultNamespaceDepth))
	c.ReadOnly = cfgmap.Bool(cfg, cfgKeyReadOnly)

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate returns an error when the configuration is missing required
// fields or holds invalid values. The auth, timeout, static-header,
// passthrough and TLS rules belong to internal/upstreamauth and are
// called individually so this kind's own checks stay interleaved in the
// order an operator reads them.
func (c Config) Validate() error {
	up := c.upstream()
	if c.EndpointURL == "" {
		return errors.New("graphql: endpoint_url is required")
	}
	if err := up.ValidateAuth(); err != nil {
		//nolint:wrapcheck // already an operator-facing "graphql: ..." message
		return err
	}
	if err := up.ValidateTransport(); err != nil {
		//nolint:wrapcheck // as above
		return err
	}
	return firstConfigError(
		c.validateSchemaValidation,
		c.validateLimits,
		up.ValidateStaticHeaders,
		up.ValidateIdentityPassthrough,
		up.ValidateTLSMaterial,
	)
}

// validateSchemaValidation refuses a validation mode that is neither of
// the two the toolkit implements, rather than silently treating a typo
// as the permissive one.
func (c Config) validateSchemaValidation() error {
	switch c.SchemaValidation {
	case SchemaValidationStrict, SchemaValidationWarn:
		return nil
	default:
		return fmt.Errorf("graphql: invalid schema_validation %q (want %q or %q)",
			c.SchemaValidation, SchemaValidationStrict, SchemaValidationWarn)
	}
}

// validateLimits refuses a non-positive bound. Zero on any of these
// would mean "refuse every document" or "return nothing", which no
// operator intends by typing it.
func (c Config) validateLimits() error {
	switch {
	case c.MaxQueryDepth <= 0:
		return errors.New("graphql: max_query_depth must be positive")
	case c.NamespaceDepth <= 0:
		return errors.New("graphql: namespace_depth must be positive")
	default:
		return nil
	}
}

// firstConfigError returns the first non-nil error from the checks, in
// order, so Validate stays under the cyclomatic-complexity ceiling as
// per-field validators are added.
func firstConfigError(checks ...func() error) error {
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// IsOAuthAuthorizationCode reports whether the connection uses the
// OAuth authorization_code grant. The admin redirect handler and the
// kind handler gate the one-time browser flow on this rather than on
// the raw auth_mode string shape.
func (c Config) IsOAuthAuthorizationCode() bool {
	return c.upstream().IsOAuthAuthorizationCode()
}

func trimTrailingSlash(s string) string {
	for s != "" && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
