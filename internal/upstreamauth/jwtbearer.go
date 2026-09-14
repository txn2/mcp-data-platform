package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// JWTBearerGrantType is the grant_type value RFC 7523 section 2.1 defines
// for exchanging a JWT assertion at a token endpoint.
const JWTBearerGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer" // #nosec G101 -- grant type URN, not a credential

// jwtBearerMode names this grant in the errors the authenticator returns.
const jwtBearerMode = "oauth jwt_bearer" // #nosec G101 -- mode name, not a credential

// maxUpstreamDescriptionRunes bounds how much of an upstream's
// error_description the platform repeats in a tool error and an alert.
const maxUpstreamDescriptionRunes = 300

// ErrAssertionRejected is the error a kind's tool surfaces when the
// upstream token endpoint refused a jwt_bearer connection's signed
// assertion outright (RFC 6749 section 5.2 invalid_grant, invalid_client
// or unauthorized_client). The usual causes are an unapproved key, an
// unapproved integration user, or clock skew, all fixed at the upstream;
// nothing in the platform needs reconnecting, and the next call signs and
// exchanges a fresh assertion.
//
// The message carries no package prefix of its own: Apply wraps it in the
// calling kind's voice, followed by the upstream's error code and
// description, and errors.Is still matches.
var ErrAssertionRejected = errors.New("the upstream token endpoint rejected the signed assertion")

// validateOAuth2JWTBearer enforces the jwt_bearer grant's config rules:
// a token endpoint to exchange at, signing material the algorithm can
// use, the issuer and subject every upstream of this grant checks, and
// assertion timings that mint a token which is not already expired.
//
// The client credential is optional. Most upstreams authenticate the
// client by the assertion alone; one that also wants client
// authentication gets oauth_client_id and oauth_client_secret on the
// request, placed by oauth_endpoint_auth_style. A secret with no client
// id is refused, because there is no request that carries it correctly.
func (c Config) validateOAuth2JWTBearer() error {
	if c.OAuth2.TokenURL == "" {
		return c.errf(errOAuthFieldRequired, connoauth.ConfigKeyTokenURL, c.oauthRequirement())
	}
	if c.OAuth2.ClientSecret != "" && c.OAuth2.ClientID == "" {
		return c.errf("%s is required when %s is set and %s",
			connoauth.ConfigKeyClientID, connoauth.ConfigKeyClientSecret, c.oauthRequirement())
	}
	if err := c.validateEndpointAuthStyle(); err != nil {
		return err
	}
	if _, err := c.signedJWTSigningKey(); err != nil {
		return err
	}
	if c.SignedJWT.Issuer == "" {
		return c.errf(errOAuthFieldRequired, cfgKeyJWTIssuer, c.oauthRequirement())
	}
	if c.SignedJWT.Subject == "" {
		return c.errf(errOAuthFieldRequired, cfgKeyJWTSubject, c.oauthRequirement())
	}
	return c.validateAssertionTiming()
}

// jwtBearerAuth applies an access token obtained by exchanging a freshly
// signed assertion at the token endpoint. The access token is cached in
// memory by oauth2.ReuseTokenSource and re-obtained, with a new assertion,
// as it nears expiry. Like client_credentials it needs no database state:
// the key and the claims survive a restart as part of the connection.
//
// SECURITY: no field of this struct is ever formatted into a message. The
// errors Apply returns name the step and repeat only the upstream's error
// code and bounded description, never the key, the assertion or the
// access token.
type jwtBearerAuth struct {
	cfg Config
	src oauth2.TokenSource

	// mu guards events, which the kind's platform-side wiring sets after
	// construction while Apply may already be running.
	mu     sync.RWMutex
	events *authevents.Writer
}

// newJWTBearerAuth re-runs the grant's validation and resolves the signing
// material once, so a connection whose config cannot mint a usable
// assertion fails at construction rather than on every outbound call.
func newJWTBearerAuth(c Config) (*jwtBearerAuth, error) {
	if err := c.validateOAuth2JWTBearer(); err != nil {
		return nil, err
	}
	signer, err := c.signedJWTSigningKey()
	if err != nil {
		return nil, err
	}
	a := &jwtBearerAuth{cfg: c}
	exchange := &jwtBearerExchange{
		cfg:    c,
		minter: assertionMinter{cfg: c, signer: signer, mode: jwtBearerMode, withJTI: true},
		// The token request is bounded, refuses redirects and honors
		// the connection's CA bundle, exactly as client_credentials'.
		ctx:      context.WithValue(context.Background(), oauth2.HTTPClient, newTokenExchangeClient(c)),
		now:      time.Now,
		accepted: a.accepted,
	}
	a.src = oauth2.ReuseTokenSource(nil, withBoundedExpiry(exchange, exchange.now))
	return a, nil
}

// SetAuthEvents wires the writer the rejection and recovery of the
// exchange are announced through. Nil-safe; see the package-level
// SetAuthEvents for how a kind reaches it.
func (a *jwtBearerAuth) SetAuthEvents(w *authevents.Writer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = w
}

func (a *jwtBearerAuth) writer() *authevents.Writer {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.events
}

// Apply attaches the cached access token, exchanging a new assertion for
// one when there is none or it is near expiry.
func (a *jwtBearerAuth) Apply(req *http.Request) error {
	tok, err := a.src.Token()
	if err != nil {
		return a.fetchError(req.Context(), err)
	}
	if tok == nil || tok.AccessToken == "" {
		return a.cfg.errf("%s: the token endpoint returned no access token", jwtBearerMode)
	}
	req.Header.Set(AuthorizationHeader, "Bearer "+tok.AccessToken)
	return nil
}

// fetchError turns a failed exchange into the error the model is shown.
// A refusal the operator has to fix at the upstream is announced and
// returned with the upstream's code and description; everything else
// (network, a 5xx, a malformed response) is scrubbed exactly as the
// client_credentials path scrubs it.
func (a *jwtBearerAuth) fetchError(ctx context.Context, err error) error {
	var minted assertionMintError
	if errors.As(err, &minted) {
		// Already stated in the kind's voice, naming the step.
		return minted.err
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || !isAssertionRefusal(re.ErrorCode) {
		return tokenFetchError(a.cfg, err)
	}
	description := boundedDescription(re.ErrorDescription)
	a.writer().AssertionRejected(ctx, authevents.AssertionRefusal{
		Kind:        a.cfg.Kind,
		Name:        a.cfg.ConnectionName,
		TokenURL:    a.cfg.OAuth2.TokenURL,
		Code:        re.ErrorCode,
		Description: description,
	})
	if description == "" {
		return a.cfg.errf("%s: %w: %s", jwtBearerMode, ErrAssertionRejected, re.ErrorCode)
	}
	return a.cfg.errf("%s: %w: %s: %s", jwtBearerMode, ErrAssertionRejected, re.ErrorCode, description)
}

// accepted is called by the exchange each time the token endpoint issues
// an access token, which is what closes an alert an earlier refusal
// opened. It runs on every exchange, not only after a refusal this
// process saw: another replica may have seen the refusal, and the alert
// is one row for the connection.
func (a *jwtBearerAuth) accepted(ctx context.Context) {
	a.writer().AssertionAccepted(ctx, a.cfg.Kind, a.cfg.ConnectionName)
}

// isAssertionRefusal reports whether an RFC 6749 section 5.2 error code
// is a refusal an operator fixes at the upstream: the assertion or its
// user is not accepted (invalid_grant), the client is not recognized
// (invalid_client), or the client may not use this grant
// (unauthorized_client). Other codes describe a malformed request, which
// is the platform's fault rather than the connection's.
func isAssertionRefusal(code string) bool {
	switch code {
	case "invalid_grant", "invalid_client", "unauthorized_client":
		return true
	default:
		return false
	}
}

// boundedDescription makes an upstream's error_description safe to repeat
// in a tool error and an email: control characters become spaces, runs
// of whitespace collapse, and the text is cut at
// maxUpstreamDescriptionRunes.
func boundedDescription(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	fields := strings.Fields(cleaned)
	out := strings.Join(fields, " ")
	if runes := []rune(out); len(runes) > maxUpstreamDescriptionRunes {
		out = string(runes[:maxUpstreamDescriptionRunes]) + "..."
	}
	return out
}

// assertionMintError carries a failure to sign the assertion through the
// oauth2.TokenSource boundary, so Apply returns it as minted rather than
// scrubbing it as a token-endpoint failure it was not.
type assertionMintError struct{ err error }

func (e assertionMintError) Error() string { return e.err.Error() }

func (e assertionMintError) Unwrap() error { return e.err }

// jwtBearerExchange is the oauth2.TokenSource that signs an assertion and
// exchanges it. Every call mints a new assertion: the access token is what
// is cached, by the ReuseTokenSource wrapping this, and an assertion is
// good for one exchange.
type jwtBearerExchange struct {
	cfg    Config
	minter assertionMinter
	// ctx carries the bounded token-request client. oauth2.TokenSource
	// has no per-call context.
	ctx context.Context
	// now is time.Now in production and a stub in tests.
	now func() time.Time
	// accepted is told about every successful exchange.
	accepted func(context.Context)
}

// Token signs an assertion and exchanges it for an access token.
func (e *jwtBearerExchange) Token() (*oauth2.Token, error) {
	now := e.now()
	assertion, _, err := e.minter.mint(now)
	if err != nil {
		return nil, assertionMintError{err: err}
	}
	tok, err := e.request(assertion).Token(e.ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // Apply classifies and scrubs the library's error
	}
	// A refresh token is not used by this grant; it is not held.
	tok.RefreshToken = ""
	e.accepted(e.ctx)
	return tok, nil
}

// request builds the token request for one assertion. It reuses
// clientcredentials.Config for the request, its error parsing and its
// expiry handling, overriding grant_type as that package permits and
// adding the assertion.
//
// When the connection carries no client secret the client credential is
// sent as form parameters, which omits what is empty: the header style
// would send "Authorization: Basic Og==" for an empty client id and
// secret, which an upstream authenticating by the assertion alone can
// reject as a malformed client credential.
func (e *jwtBearerExchange) request(assertion string) *clientcredentials.Config {
	style := oauth2.AuthStyleInHeader
	if e.cfg.OAuth2.ClientSecret == "" || e.cfg.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		style = oauth2.AuthStyleInParams
	}
	return &clientcredentials.Config{
		ClientID:     e.cfg.OAuth2.ClientID,
		ClientSecret: e.cfg.OAuth2.ClientSecret,
		TokenURL:     e.cfg.OAuth2.TokenURL,
		Scopes:       e.cfg.OAuth2.Scopes,
		AuthStyle:    style,
		EndpointParams: url.Values{
			"grant_type": {JWTBearerGrantType},
			"assertion":  {assertion},
		},
	}
}
