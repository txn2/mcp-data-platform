package apigateway

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// Authenticator applies a connection's authentication scheme to an
// outbound HTTP request before it is sent. Implementations must be
// safe for concurrent use — a single Authenticator is shared across
// all in-flight invocations of a connection.
//
// Implementations MUST NOT log credential material. The toolkit's
// audit pipeline expects no Authorization or X-API-Key value to ever
// appear in slog output, error messages, or audit rows; carelessly
// formatted error strings are the most common leak path.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NewAuthenticator returns the Authenticator implementation for a
// validated Config. ParseConfig has already rejected unknown auth
// modes, so the default branch only fires if a future mode is added
// without a matching case here.
func NewAuthenticator(c Config) (Authenticator, error) {
	switch c.AuthMode {
	case AuthModeNone:
		return noneAuth{}, nil
	case AuthModeBearer:
		return bearerAuth{credential: c.Credential}, nil
	case AuthModeAPIKey:
		return newAPIKeyAuth(c)
	case AuthModeBasic:
		return newBasicAuth(c)
	case AuthModeOAuth2ClientCredentials:
		return newOAuth2ClientCredentialsAuth(c), nil
	case AuthModeOAuth2AuthorizationCode:
		// authorization_code requires a TokenStore. NewAuthenticator
		// alone cannot supply one (it has no DB handle); the toolkit's
		// addParsedConnection wires the TokenStore at materialization
		// time. Returning the un-stored variant here keeps the call
		// site consistent: the toolkit immediately calls
		// SetTokenStore on it.
		return newOAuth2AuthorizationCodeAuth(c), nil
	case AuthModeMTLS:
		// The client certificate IS the credential. No header is
		// added; the TLS handshake at transport setup
		// (newHTTPTransport) attaches the cert. The no-op Apply
		// keeps the invocation path's "always call Apply" contract
		// without a nil check.
		return mtlsAuth{}, nil
	default:
		return nil, fmt.Errorf("apigateway: no authenticator for auth_mode %q", c.AuthMode)
	}
}

// mtlsAuth is a no-op Authenticator used when the connection presents
// a client certificate during the TLS handshake instead of an
// Authorization header. The cert + key are attached to the
// http.Transport's TLSClientConfig by newHTTPTransport; this struct
// exists only so the invocation path can call Apply unconditionally
// across every auth mode.
type mtlsAuth struct{}

// Apply is a no-op; auth_mode=mtls authenticates at the TLS layer.
func (mtlsAuth) Apply(_ *http.Request) error { return nil }

// noneAuth applies no credential. Distinct from a nil Authenticator so
// the toolkit's invocation path can call Apply unconditionally.
type noneAuth struct{}

// Apply is a no-op; the connection requested no outbound auth.
func (noneAuth) Apply(_ *http.Request) error { return nil }

// bearerAuth sets the Authorization header to "Bearer <credential>".
type bearerAuth struct {
	credential string
}

// Apply attaches the bearer token as the Authorization header.
func (b bearerAuth) Apply(req *http.Request) error {
	if b.credential == "" {
		return errors.New("apigateway: bearer credential is empty")
	}
	req.Header.Set(authorizationHeader, "Bearer "+b.credential)
	return nil
}

// apiKeyAuth attaches the credential as either a header or a query
// parameter, per the connection's APIKeyPlacement setting.
type apiKeyAuth struct {
	credential string
	placement  string
	header     string
	param      string
}

func newAPIKeyAuth(c Config) (apiKeyAuth, error) {
	a := apiKeyAuth{
		credential: c.Credential,
		placement:  c.APIKeyPlacement,
		header:     c.APIKeyHeader,
		param:      c.APIKeyParam,
	}
	if a.credential == "" {
		return apiKeyAuth{}, errors.New("apigateway: api_key credential is empty")
	}
	switch a.placement {
	case APIKeyPlacementHeader:
		if a.header == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_header is empty")
		}
	case APIKeyPlacementQuery:
		if a.param == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_param is empty")
		}
	default:
		return apiKeyAuth{}, fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return a, nil
}

// Apply attaches the API key as either a header or a query parameter,
// per the connection's APIKeyPlacement.
func (a apiKeyAuth) Apply(req *http.Request) error {
	switch a.placement {
	case APIKeyPlacementHeader:
		req.Header.Set(a.header, a.credential)
	case APIKeyPlacementQuery:
		q := req.URL.Query()
		q.Set(a.param, a.credential)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return nil
}

// basicAuth attaches HTTP Basic auth (RFC 7617) as
// "Authorization: Basic base64(username:password)". The pre-encoded
// header value is computed once at construction so each Apply call is
// just a Header.Set (matching the cost profile of bearer/api_key) and
// the plaintext password is not retained on the struct beyond
// construction.
type basicAuth struct {
	header string
}

// newBasicAuth constructs the authenticator with all validation
// re-checked against the parsed Config. Config.Validate() has already
// rejected ":" in the userid and CR/LF/NUL in either field, but
// authenticators construct from the (validated) Config without seeing
// the validator path, so the guards live here too as defense in depth
// against a future caller that bypasses Validate.
func newBasicAuth(c Config) (basicAuth, error) {
	if c.Username == "" {
		return basicAuth{}, errors.New("apigateway: basic auth requires a username")
	}
	if strings.Contains(c.Username, ":") {
		return basicAuth{}, errors.New("apigateway: basic auth username must not contain \":\"")
	}
	if strings.ContainsAny(c.Username, "\r\n\x00") || strings.ContainsAny(c.Password, "\r\n\x00") {
		return basicAuth{}, errors.New("apigateway: basic auth credentials contain CR/LF/NUL")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))
	return basicAuth{header: "Basic " + encoded}, nil
}

// Apply attaches the pre-encoded Basic credential as the Authorization
// header. newBasicAuth has already validated the inputs and computed
// the encoded value, so this hot path is just a Header.Set.
func (b basicAuth) Apply(req *http.Request) error {
	req.Header.Set(authorizationHeader, b.header)
	return nil
}

// oauth2ClientCredentialsAuth applies an OAuth 2.1 access token
// acquired via the client_credentials grant. Token caching and
// refresh-on-expiry are delegated to the standard
// golang.org/x/oauth2 library: the underlying TokenSource fetches
// once, caches in memory, and re-fetches transparently when the
// token expires. No DB state is required because the authoritative
// inputs (client_id + client_secret) survive process restarts as
// part of the connection's encrypted credentials blob.
//
// SECURITY: this struct intentionally does NOT carry the client
// secret in its log/error string output. tokenFetchError below
// scrubs the token URL's query string and userinfo before
// surfacing transport errors to the model — without this, a bad
// network path or DNS failure during a token fetch would expose
// any credential the operator embedded in the URL (an unfortunate
// pattern but one that exists in the wild).
type oauth2ClientCredentialsAuth struct {
	src oauth2.TokenSource
}

// oauth2TokenFetchTimeout caps how long a single token-endpoint
// request can take. Without this the default Go http.Client has no
// timeout and an unreachable IdP would hang every Apply call until
// the OS-level dial timeout fires (minutes). 30 seconds matches
// the order of magnitude of the upstream call_timeout while
// staying generous for IdPs with slow first-token issuance.
const oauth2TokenFetchTimeout = 30 * time.Second

// newTokenExchangeClient builds the http.Client used for the
// client_credentials token acquire. The initial authorization_code
// exchange and the refresh path use the same-named helper in
// pkg/connoauth/source.go via the unified Source; this one is only
// reached by newOAuth2ClientCredentialsAuth.
//
// The CheckRedirect hook refuses 3xx so a misconfigured or
// compromised IdP cannot redirect the form body (which carries
// client_secret) to an attacker URL.
//
// When cfg.TLSCABundlePEM is set, the bundle is appended to the
// system root pool and the resulting tls.Config is attached to a
// dedicated Transport. Public CAs remain trusted; the bundle never
// substitutes for the system roots. Client mTLS material is
// intentionally NOT plumbed here: presenting a client cert to the
// IdP is a deliberate decision that should not piggy-back on the
// upstream cert.
func newTokenExchangeClient(cfg Config) *http.Client {
	client := &http.Client{
		Timeout: oauth2TokenFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if cfg.TLSCABundlePEM == "" {
		return client
	}
	pool, err := rootPoolWithBundle(cfg.TLSCABundlePEM)
	if err != nil {
		return client
	}
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    pool,
		},
	}
	return client
}

func newOAuth2ClientCredentialsAuth(c Config) oauth2ClientCredentialsAuth {
	authStyle := oauth2.AuthStyleInHeader
	if c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	cfg := &clientcredentials.Config{
		ClientID:     c.OAuth2.ClientID,
		ClientSecret: c.OAuth2.ClientSecret,
		TokenURL:     c.OAuth2.TokenURL,
		Scopes:       c.OAuth2.Scopes,
		AuthStyle:    authStyle,
	}
	// Bound token-endpoint requests via a custom http.Client. The
	// oauth2 library reads ctx-value oauth2.HTTPClient and falls
	// back to http.DefaultClient (no timeout) otherwise. CheckRedirect
	// refuses to follow 3xx so a misconfigured or compromised IdP
	// cannot redirect this credential-bearing POST (carrying
	// client_secret in the form body or HTTP Basic header) to an
	// attacker URL. When the connection carries a TLS CA bundle, the
	// same bundle is honored here so IdPs behind a private CA work.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, newTokenExchangeClient(c))
	// The token source caches in-memory and re-fetches when expired.
	// Wrap with oauth2.ReuseTokenSource so the cache is reused
	// across Apply calls; clientcredentials.Config.TokenSource
	// already does this internally but the wrap is explicit
	// defense against future library changes.
	src := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))
	return oauth2ClientCredentialsAuth{src: src}
}

// Apply fetches (or returns the cached) access token and attaches
// it as the Authorization header. Errors from the token source —
// network failures, IdP rejections, expired credentials — are
// scrubbed via tokenFetchError before being returned, so the
// caller's error pipeline cannot leak the OAuth client secret or
// the token URL's userinfo.
func (a oauth2ClientCredentialsAuth) Apply(req *http.Request) error {
	tok, err := a.src.Token()
	if err != nil {
		return tokenFetchError(err)
	}
	if tok == nil || tok.AccessToken == "" {
		return errors.New("apigateway: oauth2 token source returned no access token")
	}
	req.Header.Set(authorizationHeader, "Bearer "+tok.AccessToken)
	return nil
}

// tokenFetchError sanitizes errors from oauth2.TokenSource.Token().
// The library's *oauth2.RetrieveError includes the IdP response
// body, which can contain sensitive material (e.g., a refresh
// token from a partial grant exchange). It also includes the
// token URL — if the operator embedded credentials in the URL
// (e.g., https://user:secret@idp.example/token), those would
// leak. We rebuild the message keeping only the non-sensitive
// pieces.
func tokenFetchError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return fmt.Errorf("apigateway: oauth2 token fetch failed: status=%d", re.Response.StatusCode)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		parsed, perr := url.Parse(ue.URL)
		if perr != nil {
			return fmt.Errorf("apigateway: oauth2 token fetch %s: %w", ue.Op, ue.Err)
		}
		parsed.RawQuery = ""
		parsed.User = nil
		return fmt.Errorf("apigateway: oauth2 token fetch %s %q: %w", ue.Op, parsed.String(), ue.Err)
	}
	// Fallback: redact anything that looks URL-shaped just in case
	// a future library version wraps in a different error type.
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New("apigateway: oauth2 token fetch failed (details redacted)")
	}
	return fmt.Errorf("apigateway: oauth2 token fetch failed: %s", msg)
}

// ErrNeedsReauth is the structured error api_invoke_endpoint surfaces
// when an authorization_code connection's stored refresh token is
// missing, expired beyond refresh_expires_at, or definitively rejected
// by the IdP (RFC 6749 §5.2 invalid_grant on the refresh_token grant).
// Transient failures (network, 5xx, request cancellation) DO NOT
// produce this error.
//
// The error message intentionally points the operator at the
// platform's reauth path rather than echoing the underlying IdP
// response (which can include sensitive material from a partial
// grant exchange).
var ErrNeedsReauth = errors.New("apigateway: oauth2 connection needs admin reconnect")

// oauth2AuthorizationCodeAuth applies an OAuth 2.1 access token
// acquired via the user-driven authorization_code grant. All token
// state lives in the unified connection_oauth_tokens row: the initial
// pair is written by the admin OAuth callback handler and every
// Apply round-trip reads through connoauth.Source, which refreshes
// transparently when the cached access token is near expiry and
// persists the rotated refresh token (RFC 6749 §6) back to the row.
//
// The Source is built per-call from the toolkit's connOAuthStore +
// authevents.Writer, so the authenticator carries no token state of
// its own — exactly the property that makes refresh coherent with the
// background refresher across replicas.
type oauth2AuthorizationCodeAuth struct {
	cfg Config
	// mu guards the store + events pair against concurrent
	// SetConnOAuthStore / SetAuthEvents (called from the toolkit's
	// platform-side wiring) while Apply reads them on every outbound
	// request. RWMutex so concurrent reads don't serialize.
	mu     sync.RWMutex
	store  connoauth.Store
	events *authevents.Writer
}

func newOAuth2AuthorizationCodeAuth(c Config) *oauth2AuthorizationCodeAuth {
	return &oauth2AuthorizationCodeAuth{cfg: c}
}

// SetConnOAuthStore wires the unified token store. Required before
// Apply can be called; the toolkit's SetConnOAuthStore method threads
// this through.
func (a *oauth2AuthorizationCodeAuth) SetConnOAuthStore(s connoauth.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store = s
}

// SetAuthEvents wires the audit-event writer (nil-safe). Wired from
// the platform alongside SetConnOAuthStore so every outbound refresh
// emits its lifecycle event.
func (a *oauth2AuthorizationCodeAuth) SetAuthEvents(w *authevents.Writer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = w
}

// snapshot returns the current store/events pair under read lock so
// Apply can issue the network call (which goes through connoauth.Source)
// without holding the mutex across it.
func (a *oauth2AuthorizationCodeAuth) snapshot() (connoauth.Store, *authevents.Writer) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.store, a.events
}

// Apply attaches a fresh OAuth bearer token. The Source's Token()
// always re-reads the persisted row before deciding whether to
// refresh, so background-refresher rotations land transparently —
// the authenticator never holds a stale refresh token.
func (a *oauth2AuthorizationCodeAuth) Apply(req *http.Request) error {
	store, events := a.snapshot()
	if store == nil {
		return errors.New("apigateway: oauth2 authorization_code: token store not wired")
	}
	src := connoauth.NewSource(store, connoauth.Key{
		Kind: connoauth.KindAPI,
		Name: a.cfg.ConnectionName,
	}, connoauthConfigFromOAuth2(a.cfg)).
		WithEvents(events).
		WithActor(authevents.SystemToolCall)
	token, err := src.Token(req.Context())
	if err != nil {
		if errors.Is(err, connoauth.ErrNeedsReauth) {
			return ErrNeedsReauth
		}
		return fmt.Errorf("apigateway: oauth token: %w", err)
	}
	req.Header.Set(authorizationHeader, "Bearer "+token)
	return nil
}

// connoauthConfigFromOAuth2 maps the toolkit's OAuth2 config slice to
// the unified connoauth.Config the Source consumes. The full Config
// (including the CA bundle for IdPs behind a private CA) is read so
// the token-exchange and refresh paths can verify the IdP's TLS cert
// against the operator's bundle without falling back to system trust.
func connoauthConfigFromOAuth2(c Config) connoauth.Config {
	authStyle := oauth2.AuthStyleInHeader
	if c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	return connoauth.Config{
		Grant:             "authorization_code",
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
