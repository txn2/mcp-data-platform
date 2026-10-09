package upstreamauth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth/googlekey"
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
// use, an issuer or a subject to identify the client by, a scope
// placement, and assertion timings that mint a token which is not already
// expired.
//
// The subject is optional, as it is for signed_jwt (#2061): Google reads
// sub as the Workspace user a service account impersonates under
// domain-wide delegation, and issues a token for the account itself only
// when the claim is absent.
//
// A connection whose Google key is a stored secret has no signing key or
// issuer in its config: both are read from the secret when the first
// assertion is minted.
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
	if err := c.validateScopePlacement(); err != nil {
		return err
	}
	if c.Google.Secret == "" {
		if _, err := c.signedJWTSigningKey(); err != nil {
			return err
		}
		if c.SignedJWT.Issuer == "" && c.SignedJWT.Subject == "" {
			return c.errf("one of %s or %s is required when %s", cfgKeyJWTIssuer, cfgKeyJWTSubject, c.oauthRequirement())
		}
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

	// mu guards events and keySecrets, which the kind's platform-side
	// wiring sets after construction while Apply may already be running,
	// and granted, which each exchange records.
	mu         sync.RWMutex
	events     *authevents.Writer
	keySecrets KeySecrets
	granted    Grant
}

// KeySecrets reads a stored secret's value for the connection it is bound to.
// A kind binds one per connection, so the secret's allow_connections is
// checked against that connection and no other.
type KeySecrets func(ctx context.Context, name string) (string, error)

// Grant is what the token endpoint issued on the last exchange: whose token
// it is and the scopes it carries, which the connection test reports.
type Grant struct {
	// Identity is the assertion's issuer, or its subject when it
	// impersonates one.
	Identity string
	// Scopes are the scopes the endpoint reported granting, or those
	// requested when it reported none.
	Scopes []string
}

// newJWTBearerAuth re-runs the grant's validation and resolves the signing
// material once, so a connection whose config cannot mint a usable
// assertion fails at construction rather than on every outbound call.
func newJWTBearerAuth(c Config) (*jwtBearerAuth, error) {
	if err := c.validateOAuth2JWTBearer(); err != nil {
		return nil, err
	}
	var signer signedJWTSigner
	if c.Google.Secret == "" {
		var err error
		if signer, err = c.signedJWTSigningKey(); err != nil {
			return nil, err
		}
	}
	a := &jwtBearerAuth{cfg: c}
	exchange := &jwtBearerExchange{
		cfg:    c,
		minter: assertionMinter{cfg: c, signer: signer, mode: jwtBearerMode, withJTI: true, scope: c.assertionScope()},
		keys:   a.keys,
		issued: a.issued,
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

// SetKeySecrets wires the stored-secret read a connection whose Google key is
// a stored secret mints its assertions through. See the package-level
// SetKeySecrets.
func (a *jwtBearerAuth) SetKeySecrets(k KeySecrets) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keySecrets = k
}

// Granted reports what the token endpoint issued on the last exchange, and
// false before the first.
func (a *jwtBearerAuth) Granted() (Grant, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.granted, a.granted.Identity != ""
}

func (a *jwtBearerAuth) issued(g Grant) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.granted = g
}

// keys returns the minter an assertion is signed with. A connection whose
// Google key is a stored secret reads the secret for every exchange, so a key
// rotated in the secret is the key the next token is minted with; every other
// connection signs with the key its config carries.
func (a *jwtBearerAuth) keys(ctx context.Context, static assertionMinter) (assertionMinter, error) {
	name := a.cfg.Google.Secret
	if name == "" {
		return static, nil
	}
	a.mu.RLock()
	read := a.keySecrets
	a.mu.RUnlock()
	if read == nil {
		return assertionMinter{}, a.cfg.errf("%s: the Google service account key is the stored secret %q, and stored secrets are not available here", jwtBearerMode, name)
	}
	raw, err := read(ctx, name)
	if err != nil {
		return assertionMinter{}, a.cfg.errf("%s: reading the Google service account key: %w", jwtBearerMode, err)
	}
	sa, err := googlekey.Parse(raw)
	if err != nil {
		return assertionMinter{}, a.cfg.errf("%s: the stored secret %q: %w", jwtBearerMode, name, err)
	}
	c := a.cfg
	c.SignedJWT.PrivateKeyPEM = sa.PrivateKey
	if c.SignedJWT.KeyID == "" {
		c.SignedJWT.KeyID = sa.PrivateKeyID
	}
	if c.SignedJWT.Issuer == "" {
		c.SignedJWT.Issuer = sa.ClientEmail
	}
	signer, err := c.signedJWTAsymmetricKey()
	if err != nil {
		return assertionMinter{}, err
	}
	m := static
	m.cfg, m.signer = c, signer
	return m, nil
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
		scrubbed := tokenFetchError(a.cfg, err)
		// Google answers an assertion whose scope claim names no access
		// scope with a 200 that carries no access token (#2061).
		if a.cfg.Google.Set && strings.Contains(err.Error(), "missing access_token") {
			return fmt.Errorf("oauth jwt_bearer: Google issued no access token for these scopes (%w); %s", scrubbed, googlekey.TokenHint("invalid_scope", ""))
		}
		return scrubbed
	}
	description := boundedDescription(re.ErrorDescription)
	if a.cfg.Google.Set {
		if hint := googlekey.TokenHint(re.ErrorCode, description); hint != "" {
			description = strings.TrimSuffix(cmp.Or(description, re.ErrorCode), ".") + ". " + hint
		}
	}
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
// (invalid_client), the client may not use this grant
// (unauthorized_client), or a scope it names is not one the endpoint
// issues (invalid_scope, which Google answers a misspelled scope URL
// with). Other codes describe a malformed request, which
// is the platform's fault rather than the connection's.
func isAssertionRefusal(code string) bool {
	switch code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_scope":
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
	// keys resolves the minter for one exchange (jwtBearerAuth.keys).
	// Nil signs with minter as built.
	keys func(context.Context, assertionMinter) (assertionMinter, error)
	// issued is told what each successful exchange granted. Nil-safe.
	issued func(Grant)
}

// Token signs an assertion and exchanges it for an access token.
func (e *jwtBearerExchange) Token() (*oauth2.Token, error) {
	now := e.now()
	minter := e.minter
	if e.keys != nil {
		var err error
		if minter, err = e.keys(e.ctx, minter); err != nil {
			return nil, assertionMintError{err: err}
		}
	}
	assertion, _, err := minter.mint(now)
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
	if e.issued != nil {
		e.issued(grantFrom(minter.cfg, tok))
	}
	return tok, nil
}

// assertionScope is the scope claim the assertion carries: the scopes, when
// the connection places them in the claim.
func (c Config) assertionScope() string {
	if c.OAuth2.ScopePlacement != ScopePlacementClaim && c.OAuth2.ScopePlacement != ScopePlacementBoth {
		return ""
	}
	return strings.Join(c.OAuth2.Scopes, " ")
}

// grantFrom reads what a token response granted: the scope field when the
// endpoint reports one (RFC 6749 section 5.1), the requested scopes when not.
func grantFrom(c Config, tok *oauth2.Token) Grant {
	g := Grant{Identity: cmp.Or(c.SignedJWT.Subject, c.SignedJWT.Issuer), Scopes: c.OAuth2.Scopes}
	if s, ok := tok.Extra("scope").(string); ok && strings.TrimSpace(s) != "" {
		g.Scopes = strings.Fields(s)
	}
	return g
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
	var scopes []string
	if e.cfg.OAuth2.ScopePlacement != ScopePlacementClaim {
		scopes = e.cfg.OAuth2.Scopes
	}
	return &clientcredentials.Config{
		ClientID:     e.cfg.OAuth2.ClientID,
		ClientSecret: e.cfg.OAuth2.ClientSecret,
		TokenURL:     e.cfg.OAuth2.TokenURL,
		Scopes:       scopes,
		AuthStyle:    style,
		EndpointParams: url.Values{
			"grant_type": {JWTBearerGrantType},
			"assertion":  {assertion},
		},
	}
}
